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
)

type Service interface {
	CreatePart(ctx context.Context, req CreatePartRequest) (int64, error)
	UpdatePart(ctx context.Context, req UpdatePartRequest) error
	DeletePart(ctx context.Context, id int64) error

	AssignStock(ctx context.Context, req AssignStockRequest) error
	MoveStock(ctx context.Context, req MoveStockRequest) error
	RemoveStock(ctx context.Context, req RemoveStockRequest) error

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
}

func NewService(database *sql.DB, queries *db.Queries, logger *slog.Logger) Service {
	return &service{
		database: database,
		queries:  queries,
		logger:   logger,
	}
}

func (s *service) CreatePart(ctx context.Context, req CreatePartRequest) (int64, error) {
	var imagePath string
	if req.Image != nil && req.Image.File != nil {
		if mf, ok := req.Image.File.(multipart.File); ok {
			fileName, err := images.ProcessUpload(mf, req.Image.Header)
			if err == nil {
				imagePath = config.UrlPrefixImages + fileName
			}
		}
	}

	tx, err := s.database.Begin()
	if err != nil {
		if imagePath != "" {
			images.DeleteByWebPath(imagePath)
		}
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

	for _, l := range req.Links {
		if l.URL == "" {
			continue
		}
		err = qtx.CreatePartLink(ctx, db.CreatePartLinkParams{
			PartID: newID,
			Url:    l.URL,
			Label:  sql.NullString{String: l.Label, Valid: l.Label != ""},
		})
		if err != nil {
			if imagePath != "" {
				images.DeleteByWebPath(imagePath)
			}
			return 0, fmt.Errorf("failed to create link: %w", err)
		}
	}

	var uploadedDocs []string
	for _, du := range req.Documents {
		savedWebPath, err := s.saveDocument(du.File, du.Header.Filename)
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

	audit.Log(ctx, qtx, "CREATE", "PART", newID, "Created part "+req.Name, nil, nil)

	if err := tx.Commit(); err != nil {
		s.cleanupFiles(imagePath, uploadedDocs)
		return 0, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return newID, nil
}

func (s *service) UpdatePart(ctx context.Context, req UpdatePartRequest) error {
	oldPart, err := s.queries.GetPart(ctx, req.ID)
	if err != nil {
		return fmt.Errorf("part not found: %w", err)
	}

	newImagePath := oldPart.ImagePath.String
	uploadedNewImage := false
	if req.Image != nil && req.Image.File != nil {
		if mf, ok := req.Image.File.(multipart.File); ok {
			fileName, err := images.ProcessUpload(mf, req.Image.Header)
			if err == nil {
				newImagePath = config.UrlPrefixImages + fileName
				uploadedNewImage = true
			}
		}
	}

	tx, err := s.database.Begin()
	if err != nil {
		if uploadedNewImage {
			images.DeleteByWebPath(newImagePath)
		}
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
		savedWebPath, err := s.saveDocument(du.File, du.Header.Filename)
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
	nullBinID := sql.NullInt64{Int64: int64(req.BinID), Valid: true}

	_, err := s.queries.GetAssignmentID(ctx, db.GetAssignmentIDParams{
		PartID: req.PartID,
		BinID:  nullBinID,
	})

	if err == nil {
		err = s.queries.UpdatePartAssignmentQuantity(ctx, db.UpdatePartAssignmentQuantityParams{
			Quantity: int64(req.Quantity),
			PartID:   req.PartID,
			BinID:    nullBinID,
		})
	} else {
		err = s.queries.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{
			PartID:   req.PartID,
			BinID:    nullBinID,
			Quantity: int64(req.Quantity),
		})
	}

	if err != nil {
		return err
	}

	audit.Log(ctx, s.queries, "STOCK_ADD", "PART", req.PartID, "Added stock", nil, nil)
	return nil
}

func (s *service) MoveStock(ctx context.Context, req MoveStockRequest) error {
	tx, err := s.database.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	qtx := s.queries.WithTx(tx)

	source, err := qtx.GetAssignment(ctx, req.AssignmentID)
	if err != nil {
		return fmt.Errorf("source assignment not found: %w", err)
	}

	targetID, err := qtx.GetAssignmentID(ctx, db.GetAssignmentIDParams{
		PartID: req.PartID,
		BinID:  sql.NullInt64{Int64: req.TargetBinID, Valid: true},
	})

	if err == nil {
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
			s.logger.Warn("failed to delete doc file", "path", diskPath, "error", err)
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
