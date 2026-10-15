// Package uploads detects and removes orphaned upload files: files that exist
// under the uploads directory but that no database row references.
//
// Orphaned files are harmless but they are carried into every backup and are
// restored into the target installation, so an administrator may want to remove
// them. Detection is read-only and cleanup is deliberately conservative: it
// re-scans immediately before deleting and refuses to remove any file that has
// become referenced in the meantime, so a concurrent upload, edit or restore can
// never cause a referenced asset to be deleted.
package uploads

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tuxedocurly/wledger/internal/config"
)

// Orphan describes a single unreferenced upload file.
type Orphan struct {
	// RelPath is the path relative to the uploads directory, using forward
	// slashes (for example "images/part_123.jpg").
	RelPath string `json:"rel_path"`
	Size    int64  `json:"size"`
}

// Report is the result of a scan.
type Report struct {
	// Referenced is the number of distinct upload files the database references.
	Referenced int `json:"referenced"`
	// Orphans lists the files present on disk that nothing references.
	Orphans []Orphan `json:"orphans"`
}

// Service scans and cleans the uploads directory.
type Service struct {
	db         *sql.DB
	uploadsDir string
}

// NewService returns a Service for the given database and uploads directory.
func NewService(database *sql.DB, uploadsDir string) *Service {
	return &Service{db: database, uploadsDir: uploadsDir}
}

// Scan lists every upload file that no part image, category image or part
// document references. It never modifies the filesystem or the database.
func (s *Service) Scan(ctx context.Context) (Report, error) {
	referenced, err := s.referencedPaths(ctx)
	if err != nil {
		return Report{}, err
	}
	report := Report{Referenced: len(referenced)}

	err = filepath.WalkDir(s.uploadsDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, fs.ErrNotExist) {
				return nil
			}
			return walkErr
		}
		// Hidden entries are the restore control plane and operator recovery
		// directories; they are never part of the served uploads namespace and
		// must never be reported or removed here.
		if path != s.uploadsDir && strings.HasPrefix(entry.Name(), ".") {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		rel, err := filepath.Rel(s.uploadsDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if _, ok := referenced[rel]; ok {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		report.Orphans = append(report.Orphans, Orphan{RelPath: rel, Size: info.Size()})
		return nil
	})
	if err != nil {
		return Report{}, fmt.Errorf("scan uploads directory: %w", err)
	}
	sort.Slice(report.Orphans, func(i, j int) bool { return report.Orphans[i].RelPath < report.Orphans[j].RelPath })
	return report, nil
}

// Delete removes the named orphaned files and returns the paths it actually
// deleted. It re-scans first and silently skips any requested path that is now
// referenced or no longer present, so it can never delete a live asset. A
// requested path that escapes the uploads directory is ignored.
func (s *Service) Delete(ctx context.Context, relPaths []string) ([]string, error) {
	report, err := s.Scan(ctx)
	if err != nil {
		return nil, err
	}
	orphans := make(map[string]struct{}, len(report.Orphans))
	for _, orphan := range report.Orphans {
		orphans[orphan.RelPath] = struct{}{}
	}

	var deleted []string
	for _, rel := range relPaths {
		clean := filepath.ToSlash(filepath.Clean(strings.TrimSpace(rel)))
		if clean == "." || clean == "" {
			continue
		}
		if _, ok := orphans[clean]; !ok {
			// No longer an orphan (or never was): leave it untouched.
			continue
		}
		full := filepath.Join(s.uploadsDir, filepath.FromSlash(clean))
		if !withinDir(s.uploadsDir, full) {
			continue
		}
		if err := os.Remove(full); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return deleted, fmt.Errorf("remove orphaned upload %q: %w", clean, err)
		}
		deleted = append(deleted, clean)
	}
	return deleted, nil
}

// referencedPaths collects the upload files the database references, including
// the derived thumbnail of every referenced image.
func (s *Service) referencedPaths(ctx context.Context) (map[string]struct{}, error) {
	referenced := map[string]struct{}{}
	add := func(webPath string) {
		rel, ok := webPathToRel(webPath)
		if !ok {
			return
		}
		referenced[rel] = struct{}{}
		// Every stored image also has a derived thumbnail beside it.
		if strings.HasPrefix(rel, "images/") {
			ext := filepath.Ext(rel)
			referenced[strings.TrimSuffix(rel, ext)+"_thumb.jpg"] = struct{}{}
		}
	}

	queries := []struct {
		label string
		sql   string
	}{
		{"part images", `SELECT image_path FROM parts WHERE image_path IS NOT NULL AND image_path <> ''`},
		{"category images", `SELECT image_path FROM categories WHERE image_path IS NOT NULL AND image_path <> ''`},
		{"part documents", `SELECT file_path FROM part_docs WHERE file_path IS NOT NULL AND file_path <> ''`},
	}
	for _, query := range queries {
		rows, err := s.db.QueryContext(ctx, query.sql)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", query.label, err)
		}
		for rows.Next() {
			var webPath string
			if err := rows.Scan(&webPath); err != nil {
				rows.Close()
				return nil, fmt.Errorf("read %s: %w", query.label, err)
			}
			add(webPath)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("read %s: %w", query.label, err)
		}
		rows.Close()
	}
	return referenced, nil
}

// webPathToRel converts a stored web path such as "/uploads/images/part_1.jpg"
// into the uploads-relative path "images/part_1.jpg". It returns false for a
// path that is not inside the uploads namespace.
func webPathToRel(webPath string) (string, bool) {
	if !strings.HasPrefix(webPath, config.UrlPrefixUploads) {
		return "", false
	}
	rel := strings.TrimPrefix(webPath, config.UrlPrefixUploads)
	rel = strings.TrimPrefix(rel, "/")
	if rel == "" {
		return "", false
	}
	return filepath.ToSlash(filepath.Clean(rel)), true
}

// withinDir reports whether target resolves to base itself or a descendant of
// base, using a relative-path test so a sibling sharing a string prefix is
// rejected.
func withinDir(base, target string) bool {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
