package backup

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/tuxedocurly/wledger/internal/db"
)

func (s *service) validateCandidateDatabase(ctx context.Context, manifest Manifest, envelope restoreEnvelope, space, generation string) error {
	dir, err := os.MkdirTemp("", "wledger-restore-candidate-db-")
	if err != nil {
		return fmt.Errorf("create candidate database directory: %w", err)
	}
	defer os.RemoveAll(dir)

	candidate, err := db.Open("file:" + filepath.Join(dir, "candidate.db"))
	if err != nil {
		return fmt.Errorf("open candidate database: %w", err)
	}
	defer candidate.Close()
	if err := db.Migrate(candidate); err != nil {
		return fmt.Errorf("candidate database is not migration-compatible: %w", err)
	}
	store := db.NewStore(candidate)
	if err := store.ExecImmediateTx(ctx, func(qtx db.Querier) error {
		return s.applyRestoredDatabase(ctx, qtx, manifest, envelope, space, generation)
	}); err != nil {
		return fmt.Errorf("candidate database validation failed: %w", err)
	}
	if err := validateSQLite(candidate); err != nil {
		return fmt.Errorf("candidate database integrity check failed: %w", err)
	}
	return nil
}

func validateReferencedAssets(ctx context.Context, store db.Store, uploadsDir string) error {
	parts, err := store.GetAllParts(ctx)
	if err != nil {
		return fmt.Errorf("read part asset references: %w", err)
	}
	docs, err := store.GetAllPartDocs(ctx)
	if err != nil {
		return fmt.Errorf("read document asset references: %w", err)
	}
	categories, err := store.GetAllCategories(ctx)
	if err != nil {
		return fmt.Errorf("read category asset references: %w", err)
	}
	return validateManifestAssetRefs(uploadsDir, parts, docs, categories)
}

func validateManifestAssetRefs(uploadsDir string, parts []db.Part, docs []db.PartDoc, categories []db.Category) error {
	for _, part := range parts {
		if part.ImagePath.Valid && part.ImagePath.String != "" {
			if err := requireReferencedAsset(uploadsDir, part.ImagePath.String); err != nil {
				return fmt.Errorf("part %d image: %w", part.ID, err)
			}
		}
	}
	for _, doc := range docs {
		if err := requireReferencedAsset(uploadsDir, doc.FilePath); err != nil {
			return fmt.Errorf("part document %d: %w", doc.ID, err)
		}
	}
	for _, category := range categories {
		if category.ImagePath != "" {
			if err := requireReferencedAsset(uploadsDir, category.ImagePath); err != nil {
				return fmt.Errorf("category %d image: %w", category.ID, err)
			}
		}
	}
	return nil
}

func requireReferencedAsset(uploadsDir, webPath string) error {
	u, err := url.Parse(webPath)
	if err != nil || u.IsAbs() || u.Host != "" || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("unsupported uploaded-asset reference %q", webPath)
	}
	const prefix = "/uploads/"
	if !strings.HasPrefix(u.Path, prefix) {
		return fmt.Errorf("asset reference is outside %s: %q", prefix, webPath)
	}
	rel := strings.TrimPrefix(u.Path, prefix)
	if rel == "" || strings.Contains(rel, "\\") || path.Clean(rel) != rel || path.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, "../") {
		return fmt.Errorf("unsafe uploaded-asset reference %q", webPath)
	}
	target := filepath.Join(uploadsDir, filepath.FromSlash(rel))
	if !withinDir(uploadsDir, target) {
		return fmt.Errorf("uploaded-asset reference escapes storage root: %q", webPath)
	}
	current := uploadsDir
	components := strings.Split(filepath.FromSlash(rel), string(filepath.Separator))
	for i, component := range components {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			return fmt.Errorf("referenced upload %q is missing: %w", webPath, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("referenced upload %q traverses a symlink", webPath)
		}
		if i == len(components)-1 {
			if !info.Mode().IsRegular() {
				return fmt.Errorf("referenced upload %q is not a regular file", webPath)
			}
		} else if !info.IsDir() {
			return fmt.Errorf("referenced upload %q has a non-directory parent", webPath)
		}
	}
	return nil
}
