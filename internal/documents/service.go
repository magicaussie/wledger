package documents

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tuxedocurly/wledger/internal/config"
	"github.com/tuxedocurly/wledger/internal/db"
)

type Service interface {
	AddLink(ctx context.Context, q db.Querier, partID int64, url, label string) error
	DeleteLink(ctx context.Context, id int64) error
	UploadDocument(ctx context.Context, q db.Querier, partID int64, file io.Reader, filename string) (string, error)
	DeleteDocument(ctx context.Context, id int64) error
}

type service struct {
	store  db.Store
	logger *slog.Logger
}

func NewService(store db.Store, logger *slog.Logger) Service {
	return &service{
		store:  store,
		logger: logger,
	}
}

func (s *service) AddLink(ctx context.Context, q db.Querier, partID int64, url, label string) error {
	if url == "" {
		return nil
	}
	s.logger.Debug("adding part link", "part_id", partID, "url", url)
	return q.CreatePartLink(ctx, db.CreatePartLinkParams{
		PartID: partID,
		Url:    url,
		Label:  sql.NullString{String: label, Valid: label != ""},
	})
}

func (s *service) DeleteLink(ctx context.Context, id int64) error {
	return s.store.DeletePartLink(ctx, id)
}

func (s *service) UploadDocument(ctx context.Context, q db.Querier, partID int64, file io.Reader, filename string) (string, error) {
	s.logger.Debug("saving part document", "part_id", partID, "filename", filename)
	savedWebPath, err := s.saveDocument(file, filename)
	if err != nil {
		return "", fmt.Errorf("failed to save document %s: %w", filename, err)
	}

	err = q.CreatePartDoc(ctx, db.CreatePartDocParams{
		PartID:   partID,
		FilePath: savedWebPath,
		FileName: filename,
	})
	if err != nil {
		s.deleteFile(savedWebPath)
		return "", fmt.Errorf("failed to create doc record: %w", err)
	}

	return savedWebPath, nil
}

func (s *service) DeleteDocument(ctx context.Context, id int64) error {
	doc, err := s.store.GetPartDoc(ctx, id)
	if err == nil {
		s.deleteFile(doc.FilePath)
	}
	return s.store.DeletePartDoc(ctx, id)
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

func (s *service) deleteFile(webPath string) {
	if strings.HasPrefix(webPath, config.UrlPrefixUploads) {
		relPath := strings.TrimPrefix(webPath, config.UrlPrefixUploads)
		diskPath := filepath.Join(config.DirUploads, relPath)
		err := os.Remove(diskPath)
		if err != nil {
			s.logger.Warn("failed to delete doc file", "path", diskPath, "err", err)
		}
	}
}
