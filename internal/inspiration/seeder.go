package inspiration

import (
	"context"
	"embed"
	"fmt"
	"log/slog"
	"strings"
	"unicode"

	"github.com/tuxedocurly/wledger/internal/db"
)

//go:embed templates/*.md
var templateFS embed.FS

// SeedTemplates checks if templates have been seeded and if not, inserts them.
func (s *service) SeedTemplates(ctx context.Context) error {
	// Check if initial inspiration templates are seeded
	settings, err := s.queries.GetSettings(ctx)
	if err != nil {
		return fmt.Errorf("failed to get settings: %w", err)
	}

	if settings.InspirationSeedsApplied.Valid && settings.InspirationSeedsApplied.Bool {
		slog.Info("Inspiration templates already seeded, skipping.")
		return nil
	}

	slog.Info("Seeding inspiration templates...")

	// Read templates from embedded FS
	entries, err := templateFS.ReadDir("templates")
	if err != nil {
		return fmt.Errorf("failed to read templates dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		content, err := templateFS.ReadFile("templates/" + entry.Name())
		if err != nil {
			return fmt.Errorf("failed to read template file %s: %w", entry.Name(), err)
		}

		// Convert filename to Title (e.g., project_ideas.md -> Project Ideas)
		title := formatTitle(entry.Name())

		// Insert
		_, err = s.queries.CreateInspirationTemplate(ctx, db.CreateInspirationTemplateParams{
			Title:           title,
			TemplateContent: string(content),
		})
		if err != nil {
			return fmt.Errorf("failed to insert template %s: %w", title, err)
		}
		slog.Info("Seeded template", "title", title)
	}

	// Mark as seeded
	err = s.queries.MarkInspirationSeedsApplied(ctx)
	if err != nil {
		return fmt.Errorf("failed to mark seeds applied: %w", err)
	}

	slog.Info("Inspiration templates seeding complete.")
	return nil
}

func formatTitle(filename string) string {
	name := strings.TrimSuffix(filename, ".md")
	words := strings.Split(name, "_")
	for i, w := range words {
		if len(w) > 0 {
			runes := []rune(w)
			runes[0] = unicode.ToUpper(runes[0])
			words[i] = string(runes)
		}
	}
	return strings.Join(words, " ")
}
