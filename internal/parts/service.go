package parts

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tuxedocurly/wledger/internal/audit"
	"github.com/tuxedocurly/wledger/internal/config"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/images"
	"github.com/tuxedocurly/wledger/internal/tags"
)

type Service interface {
	CreatePart(ctx context.Context, req CreatePartRequest) (int64, error)
	UpdatePart(ctx context.Context, req UpdatePartRequest) error
	DeletePart(ctx context.Context, id int64) error

	AssignStock(ctx context.Context, req AssignStockRequest) error
	MoveStock(ctx context.Context, req MoveStockRequest) error
	RemoveStock(ctx context.Context, req RemoveStockRequest) error
	AdjustStock(ctx context.Context, assignmentID int64, delta int) error

	DeleteLink(ctx context.Context, id int64) error
	DeleteDoc(ctx context.Context, id int64) error
}

type LinkDTO struct {
	ID    int64
	Label string
	URL   string
}

type DocUpload struct {
	File   io.Reader
	Header *multipart.FileHeader
}

type CreatePartRequest struct {
	Name              string
	Description       string
	PartNumber        string
	Manufacturer      string
	Supplier          string
	BarcodeData       string
	UnitCost          float64
	ReorderLevel      int
	MinStockThreshold int
	Image             *DocUpload
	Links             []LinkDTO
	Documents         []DocUpload
	Tags              []string
}

type UpdatePartRequest struct {
	ID                int64
	Name              string
	Description       string
	PartNumber        string
	Manufacturer      string
	Supplier          string
	BarcodeData       string
	UnitCost          float64
	ReorderLevel      int
	MinStockThreshold int
	Image             *DocUpload
	ExistingLinks     []LinkDTO
	NewLinks          []LinkDTO
	NewDocuments      []DocUpload
	Tags              []string
}

type AssignStockRequest struct {
	PartID   int64
	BinID    int64
	Quantity int
}

type MoveStockRequest struct {
	PartID       int64
	AssignmentID int64
	TargetBinID  int64
}

type RemoveStockRequest struct {
	PartID       int64
	AssignmentID int64
}

type service struct {
	database *sql.DB
	queries  *db.Queries
	logger   *slog.Logger
	tags     tags.Service
}

func NewService(database *sql.DB, queries *db.Queries, logger *slog.Logger, tags tags.Service) Service {
	return &service{
		database: database,
		queries:  queries,
		logger:   logger,
		tags:     tags,
	}
}

func (s *service) CreatePart(ctx context.Context, req CreatePartRequest) (int64, error) {
	s.logger.Debug("starting part creation", "name", req.Name, "barcode", req.BarcodeData)
	var imagePath string
	if req.Image != nil && req.Image.File != nil {
		s.logger.Debug("processing image upload", "name", req.Name)
		if mf, ok := req.Image.File.(multipart.File); ok {
			fileName, err := images.ProcessUpload(mf, req.Image.Header)
			mf.Close() // Close after processing
			if err == nil {
				imagePath = config.UrlPrefixImages + fileName
				s.logger.Debug("image uploaded successfully", "path", imagePath)
			} else {
				s.logger.Warn("failed to process image upload", "err", err)
			}
		}
	}

	tx, err := s.database.Begin()
	if err != nil {
		if imagePath != "" {
			images.DeleteByWebPath(imagePath)
		}
		s.logger.Error("failed to begin transaction for part creation", "err", err)
		return 0, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	qtx := s.queries.WithTx(tx)

	newID, err := qtx.CreatePart(ctx, db.CreatePartParams{
		Name:              req.Name,
		Description:       sql.NullString{String: req.Description, Valid: req.Description != ""},
		PartNumber:        sql.NullString{String: req.PartNumber, Valid: req.PartNumber != ""},
		Manufacturer:      sql.NullString{String: req.Manufacturer, Valid: req.Manufacturer != ""},
		Supplier:          sql.NullString{String: req.Supplier, Valid: req.Supplier != ""},
		BarcodeData:       sql.NullString{String: req.BarcodeData, Valid: req.BarcodeData != ""},
		UnitCost:          sql.NullFloat64{Float64: req.UnitCost, Valid: true},
		ReorderLevel:      sql.NullInt64{Int64: int64(req.ReorderLevel), Valid: true},
		MinStockThreshold: sql.NullInt64{Int64: int64(req.MinStockThreshold), Valid: true},
		ImagePath:         sql.NullString{String: imagePath, Valid: imagePath != ""},
	})

	if err != nil {
		if imagePath != "" {
			images.DeleteByWebPath(imagePath)
		}
		return 0, err
	}
	s.logger.Debug("base part record created", "id", newID)

	for _, l := range req.Links {
		if l.URL == "" {
			continue
		}
		s.logger.Debug("adding part link", "id", newID, "url", l.URL)
		err = qtx.CreatePartLink(ctx, db.CreatePartLinkParams{
			PartID: newID,
			Url:    l.URL,
			Label:  sql.NullString{String: l.Label, Valid: l.Label != ""},
		})
		if err != nil {
			if imagePath != "" {
				images.DeleteByWebPath(imagePath)
			}
			s.logger.Error("failed to create part link during creation", "err", err, "part_name", req.Name)
			return 0, fmt.Errorf("failed to create link: %w", err)
		}
	}

	var uploadedDocs []string
	for _, du := range req.Documents {
		s.logger.Debug("saving part document", "id", newID, "filename", du.Header.Filename)
		savedWebPath, err := s.saveDocument(du.File, du.Header.Filename)
		// Close if it's a closer (it usually is if it came from multipart)
		if closer, ok := du.File.(io.Closer); ok {
			closer.Close()
		}

		if err != nil {
			s.cleanupFiles(imagePath, uploadedDocs)
			return 0, fmt.Errorf("failed to save document %s: %w", du.Header.Filename, err)
		}

		uploadedDocs = append(uploadedDocs, savedWebPath)
		err = qtx.CreatePartDoc(ctx, db.CreatePartDocParams{
			PartID:   newID,
			FilePath: savedWebPath,
			FileName: du.Header.Filename,
		})
		if err != nil {
			s.cleanupFiles(imagePath, uploadedDocs)
			return 0, fmt.Errorf("failed to create doc record: %w", err)
		}
	}

	s.logger.Debug("syncing tags", "id", newID, "tags", req.Tags)
	if err := s.tags.SyncTags(ctx, qtx, newID, req.Tags); err != nil {
		s.cleanupFiles(imagePath, uploadedDocs)
		return 0, fmt.Errorf("failed to sync tags: %w", err)
	}

	audit.Log(ctx, qtx, "CREATE", "PART", newID, "Created part "+req.Name, nil, nil)

	if err := tx.Commit(); err != nil {
		s.cleanupFiles(imagePath, uploadedDocs)
		return 0, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return newID, nil
}

func (s *service) UpdatePart(ctx context.Context, req UpdatePartRequest) error {
	s.logger.Debug("starting part update", "id", req.ID, "name", req.Name)
	oldPart, err := s.queries.GetPart(ctx, req.ID)
	if err != nil {
		return fmt.Errorf("part not found: %w", err)
	}

	newImagePath := oldPart.ImagePath.String
	uploadedNewImage := false
	if req.Image != nil && req.Image.File != nil {
		s.logger.Debug("processing new image upload", "id", req.ID)
		if mf, ok := req.Image.File.(multipart.File); ok {
			fileName, err := images.ProcessUpload(mf, req.Image.Header)
			mf.Close() // Close after processing
			if err == nil {
				newImagePath = config.UrlPrefixImages + fileName
				uploadedNewImage = true
				s.logger.Debug("new image uploaded", "id", req.ID, "path", newImagePath)
			} else {
				s.logger.Warn("failed to process new image upload", "err", err)
			}
		}
	}

	tx, err := s.database.Begin()
	if err != nil {
		if uploadedNewImage {
			images.DeleteByWebPath(newImagePath)
		}
		s.logger.Error("failed to begin transaction for part update", "err", err, "part_id", req.ID)
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	qtx := s.queries.WithTx(tx)

	err = qtx.UpdatePart(ctx, db.UpdatePartParams{
		Name:              req.Name,
		Description:       sql.NullString{String: req.Description, Valid: req.Description != ""},
		PartNumber:        sql.NullString{String: req.PartNumber, Valid: req.PartNumber != ""},
		Manufacturer:      sql.NullString{String: req.Manufacturer, Valid: req.Manufacturer != ""},
		Supplier:          sql.NullString{String: req.Supplier, Valid: req.Supplier != ""},
		BarcodeData:       sql.NullString{String: req.BarcodeData, Valid: req.BarcodeData != ""},
		UnitCost:          sql.NullFloat64{Float64: req.UnitCost, Valid: true},
		ReorderLevel:      sql.NullInt64{Int64: int64(req.ReorderLevel), Valid: true},
		MinStockThreshold: sql.NullInt64{Int64: int64(req.MinStockThreshold), Valid: true},
		ImagePath:         sql.NullString{String: newImagePath, Valid: newImagePath != ""},
		ID:                req.ID,
	})

	if err != nil {
		if uploadedNewImage {
			images.DeleteByWebPath(newImagePath)
		}
		return err
	}

	for _, l := range req.ExistingLinks {
		if l.ID == 0 || l.URL == "" {
			continue
		}
		s.logger.Debug("updating existing link", "id", req.ID, "link_id", l.ID, "url", l.URL)
		err = qtx.UpdatePartLink(ctx, db.UpdatePartLinkParams{
			Url:   l.URL,
			Label: sql.NullString{String: l.Label, Valid: l.Label != ""},
			ID:    l.ID,
		})
		if err != nil {
			if uploadedNewImage {
				images.DeleteByWebPath(newImagePath)
			}
			return fmt.Errorf("failed to update link: %w", err)
		}
	}

	for _, l := range req.NewLinks {
		if l.URL == "" {
			continue
		}
		s.logger.Debug("creating new link", "id", req.ID, "url", l.URL)
		err = qtx.CreatePartLink(ctx, db.CreatePartLinkParams{
			PartID: req.ID,
			Url:    l.URL,
			Label:  sql.NullString{String: l.Label, Valid: l.Label != ""},
		})
		if err != nil {
			if uploadedNewImage {
				images.DeleteByWebPath(newImagePath)
			}
			return fmt.Errorf("failed to create link: %w", err)
		}
	}

	var uploadedDocs []string
	for _, du := range req.NewDocuments {
		s.logger.Debug("saving new part document", "id", req.ID, "filename", du.Header.Filename)
		savedWebPath, err := s.saveDocument(du.File, du.Header.Filename)
		// Close if it's a closer
		if closer, ok := du.File.(io.Closer); ok {
			closer.Close()
		}

		if err != nil {
			s.cleanupFiles(uploadedNewImage, newImagePath, uploadedDocs)
			return fmt.Errorf("failed to save document %s: %w", du.Header.Filename, err)
		}

		uploadedDocs = append(uploadedDocs, savedWebPath)
				err = qtx.CreatePartDoc(ctx, db.CreatePartDocParams{
					PartID:   req.ID,
					FilePath: savedWebPath,
					FileName: du.Header.Filename,
				})
						if err != nil {
							s.cleanupFiles(uploadedNewImage, newImagePath, uploadedDocs)
							return fmt.Errorf("failed to create doc record: %w", err)
						}
					}
				
					s.logger.Debug("syncing tags", "id", req.ID, "tags", req.Tags)
					if err := s.tags.SyncTags(ctx, qtx, req.ID, req.Tags); err != nil {
						s.cleanupFiles(uploadedNewImage, newImagePath, uploadedDocs)
						return fmt.Errorf("failed to sync tags: %w", err)
					}
				
					audit.Log(ctx, qtx, "UPDATE", "PART", req.ID, "Updated details", nil, nil)
				
			if err := tx.Commit(); err != nil {
		s.cleanupFiles(uploadedNewImage, newImagePath, uploadedDocs)
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	if uploadedNewImage && oldPart.ImagePath.Valid {
		images.DeleteByWebPath(oldPart.ImagePath.String)
	}

	return nil
}

func (s *service) DeletePart(ctx context.Context, id int64) error {
	p, err := s.queries.GetPart(ctx, id)
	if err == nil && p.ImagePath.Valid {
		images.DeleteByWebPath(p.ImagePath.String)
	}

	docs, err := s.queries.GetPartDocs(ctx, id)
	if err == nil {
		for _, doc := range docs {
			if strings.HasPrefix(doc.FilePath, config.UrlPrefixUploads) {
				relPath := strings.TrimPrefix(doc.FilePath, config.UrlPrefixUploads)
				diskPath := filepath.Join(config.DirUploads, relPath)
				os.Remove(diskPath)
			}
		}
	}

	audit.Log(ctx, s.queries, "DELETE", "PART", id, "Deleted part", nil, nil)
	return s.queries.DeletePart(ctx, id)
}

func (s *service) AssignStock(ctx context.Context, req AssignStockRequest) error {
	s.logger.Debug("assigning stock", "part_id", req.PartID, "bin_id", req.BinID, "qty", req.Quantity)
	nullBinID := sql.NullInt64{Int64: int64(req.BinID), Valid: true}

	existingID, err := s.queries.GetAssignmentID(ctx, db.GetAssignmentIDParams{
		PartID: req.PartID,
		BinID:  nullBinID,
	})

	if err == nil {
		// Assignment exists, fetch current quantity
		s.logger.Debug("updating existing assignment", "id", existingID)
		existing, err := s.queries.GetAssignment(ctx, existingID)
		if err != nil {
			s.logger.Error("failed to fetch existing assignment for stock addition", "err", err, "part_id", req.PartID, "bin_id", req.BinID)
			return fmt.Errorf("failed to fetch existing assignment: %w", err)
		}

		newQty := existing.Quantity + int64(req.Quantity)
		err = s.queries.UpdatePartAssignmentQuantity(ctx, db.UpdatePartAssignmentQuantityParams{
			Quantity: newQty,
			PartID:   req.PartID,
			BinID:    nullBinID,
		})
	} else {
		// New assignment
		s.logger.Debug("creating new assignment", "part_id", req.PartID, "bin_id", req.BinID)
		err = s.queries.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{
			PartID:   req.PartID,
			BinID:    nullBinID,
			Quantity: int64(req.Quantity),
		})
	}

	if err != nil {
		s.logger.Error("failed to update or create part assignment", "err", err, "part_id", req.PartID, "bin_id", req.BinID)
		return err
	}

	audit.Log(ctx, s.queries, "STOCK_ADD", "PART", req.PartID, "Added stock", nil, nil)
	return nil
}

func (s *service) AdjustStock(ctx context.Context, assignmentID int64, delta int) error {
	s.logger.Debug("adjusting stock", "id", assignmentID, "delta", delta)
	assignment, err := s.queries.GetAssignment(ctx, assignmentID)
	if err != nil {
		s.logger.Error("assignment not found for adjustment", "err", err, "assignment_id", assignmentID)
		return fmt.Errorf("assignment not found: %w", err)
	}

	newQty := assignment.Quantity + int64(delta)
	if newQty < 0 {
		newQty = 0
	}

	err = s.queries.UpdatePartAssignmentQuantity(ctx, db.UpdatePartAssignmentQuantityParams{
		Quantity: newQty,
		PartID:   assignment.PartID,
		BinID:    assignment.BinID,
	})

	if err != nil {
		return err
	}

	action := "STOCK_INC"
	if delta < 0 {
		action = "STOCK_DEC"
	}
	audit.Log(ctx, s.queries, action, "PART", assignment.PartID, fmt.Sprintf("Adjusted stock by %d", delta), nil, nil)

	return nil
}

func (s *service) MoveStock(ctx context.Context, req MoveStockRequest) error {
	s.logger.Debug("moving stock", "part_id", req.PartID, "assignment_id", req.AssignmentID, "target_bin_id", req.TargetBinID)
	tx, err := s.database.Begin()
	if err != nil {
		s.logger.Error("failed to begin transaction for stock move", "err", err, "part_id", req.PartID)
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	qtx := s.queries.WithTx(tx)

	source, err := qtx.GetAssignment(ctx, req.AssignmentID)
	if err != nil {
		s.logger.Error("source assignment not found for stock move", "err", err, "assignment_id", req.AssignmentID)
		return fmt.Errorf("source assignment not found: %w", err)
	}

	s.logger.Debug("checking if target bin already has an assignment for this part", "part_id", req.PartID, "bin_id", req.TargetBinID)
	targetID, err := qtx.GetAssignmentID(ctx, db.GetAssignmentIDParams{
		PartID: req.PartID,
		BinID:  sql.NullInt64{Int64: req.TargetBinID, Valid: true},
	})

	if err == nil {
		s.logger.Debug("merging stock into existing target assignment", "target_id", targetID)
		target, err := qtx.GetAssignment(ctx, targetID)
		if err != nil {
			return fmt.Errorf("failed to fetch target for merge: %w", err)
		}

		newQty := target.Quantity + source.Quantity

		err = qtx.UpdatePartAssignmentQuantity(ctx, db.UpdatePartAssignmentQuantityParams{
			Quantity: newQty,
			PartID:   req.PartID,
			BinID:    sql.NullInt64{Int64: req.TargetBinID, Valid: true},
		})
		if err != nil {
			return fmt.Errorf("failed to update target stock: %w", err)
		}

		err = qtx.DeleteAssignment(ctx, req.AssignmentID)
		if err != nil {
			return fmt.Errorf("failed to delete source stock after merge: %w", err)
		}

		audit.Log(ctx, qtx, "STOCK_MERGE", "PART", req.PartID, fmt.Sprintf("Merged stock into bin %d", req.TargetBinID), nil, nil)
	} else {
		s.logger.Debug("reassigning assignment to new bin")
		err = qtx.ReassignPartAssignment(ctx, db.ReassignPartAssignmentParams{
			BinID: sql.NullInt64{Int64: req.TargetBinID, Valid: true},
			ID:    req.AssignmentID,
		})
		if err != nil {
			return fmt.Errorf("failed to move stock: %w", err)
		}

		audit.Log(ctx, qtx, "STOCK_MOVE", "PART", req.PartID, fmt.Sprintf("Moved stock to bin %d", req.TargetBinID), nil, nil)
	}

	return tx.Commit()
}

func (s *service) RemoveStock(ctx context.Context, req RemoveStockRequest) error {
	err := s.queries.DeleteAssignment(ctx, req.AssignmentID)
	if err != nil {
		return err
	}

	audit.Log(ctx, s.queries, "STOCK_REMOVE", "PART", req.PartID, "Removed stock", nil, nil)
	return nil
}

func (s *service) DeleteLink(ctx context.Context, id int64) error {
	return s.queries.DeletePartLink(ctx, id)
}

func (s *service) DeleteDoc(ctx context.Context, id int64) error {
	doc, err := s.queries.GetPartDoc(ctx, id)
	if err == nil {
		relPath := strings.TrimPrefix(doc.FilePath, config.UrlPrefixUploads)
		diskPath := filepath.Join(config.DirUploads, relPath)
		err := os.Remove(diskPath)
		if err != nil {
			s.logger.Warn("failed to delete doc file", "path", diskPath, "err", err)
		}
	}

	return s.queries.DeletePartDoc(ctx, id)
}

func (s *service) saveDocument(src io.Reader, filename string) (string, error) {
	if err := os.MkdirAll(config.DirUploadsDocs, 0755); err != nil {
		return "", err
	}

	ext := filepath.Ext(filename)
	base := strings.TrimSuffix(filename, ext)
	base = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, base)

	cleanName := fmt.Sprintf("%s_%d%s", base, time.Now().Unix(), ext)
	destPath := filepath.Join(config.DirUploadsDocs, cleanName)

	dst, err := os.Create(destPath)
	if err != nil {
		return "", err
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return "", err
	}

	return config.UrlPrefixDocs + cleanName, nil
}

func (s *service) cleanupFiles(args ...interface{}) {
	var imagePath string
	var docs []string
	var uploadedNewImage bool

	for _, arg := range args {
		switch v := arg.(type) {
		case bool:
			uploadedNewImage = v
		case string:
			imagePath = v
		case []string:
			docs = v
		}
	}

	if (uploadedNewImage || imagePath != "") && imagePath != "" {
		images.DeleteByWebPath(imagePath)
	}
	for _, p := range docs {
		rel := strings.TrimPrefix(p, config.UrlPrefixUploads)
		relPath := filepath.Join(config.DirUploads, rel)
		os.Remove(relPath)
	}
}
