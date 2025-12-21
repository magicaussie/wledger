package inspiration

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/tuxedocurly/wledger/internal/db"
)

type Service interface {
	ConstructPrompt(ctx context.Context, templateID int64, tagFilters []string) (string, error)
	GetAllTemplates(ctx context.Context) ([]db.InspirationTemplate, error)
	GetTemplate(ctx context.Context, id int64) (db.InspirationTemplate, error)
	CreateTemplate(ctx context.Context, title, content string) (db.InspirationTemplate, error)
	UpdateTemplate(ctx context.Context, id int64, title, content string) error
	DeleteTemplate(ctx context.Context, id int64) error
}

type service struct {
	queries *db.Queries
}

func NewService(queries *db.Queries) Service {
	return &service{
		queries: queries,
	}
}

func (s *service) GetAllTemplates(ctx context.Context) ([]db.InspirationTemplate, error) {
	return s.queries.GetAllInspirationTemplates(ctx)
}

func (s *service) GetTemplate(ctx context.Context, id int64) (db.InspirationTemplate, error) {
	return s.queries.GetInspirationTemplate(ctx, id)
}

func (s *service) CreateTemplate(ctx context.Context, title, content string) (db.InspirationTemplate, error) {
	return s.queries.CreateInspirationTemplate(ctx, db.CreateInspirationTemplateParams{
		Title:           title,
		TemplateContent: content,
	})
}

func (s *service) UpdateTemplate(ctx context.Context, id int64, title, content string) error {
	return s.queries.UpdateInspirationTemplate(ctx, db.UpdateInspirationTemplateParams{
		Title:           title,
		TemplateContent: content,
		ID:              id,
	})
}

func (s *service) DeleteTemplate(ctx context.Context, id int64) error {
	return s.queries.DeleteInspirationTemplate(ctx, id)
}

func (s *service) ConstructPrompt(ctx context.Context, templateID int64, tagFilters []string) (string, error) {
	// Fetch template
	tmpl, err := s.queries.GetInspirationTemplate(ctx, templateID)
	if err != nil {
		return "", fmt.Errorf("failed to get template: %w", err)
	}

	// Fetch Parts based on filters
	var parts []struct {
		Name          string
		PartNumber    sql.NullString
		TotalQuantity int64
	}

	if len(tagFilters) > 0 {
		rows, err := s.queries.GetPartsForInspirationFiltered(ctx, tagFilters)
		if err != nil {
			return "", fmt.Errorf("failed to fetch filtered parts: %w", err)
		}
		parts = make([]struct {
			Name          string
			PartNumber    sql.NullString
			TotalQuantity int64
		}, len(rows))
		for i, r := range rows {
			parts[i] = struct {
				Name          string
				PartNumber    sql.NullString
				TotalQuantity int64
			}{r.Name, r.PartNumber, r.TotalQuantity}
		}
	} else {
		rows, err := s.queries.GetPartsForInspirationAll(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to fetch all parts: %w", err)
		}
		parts = make([]struct {
			Name          string
			PartNumber    sql.NullString
			TotalQuantity int64
		}, len(rows))
		for i, r := range rows {
			parts[i] = struct {
				Name          string
				PartNumber    sql.NullString
				TotalQuantity int64
			}{r.Name, r.PartNumber, r.TotalQuantity}
		}
	}

	// Format into a markdown table
	var sb strings.Builder
	sb.WriteString(tmpl.TemplateContent)
	sb.WriteString("\n\n| Qty | Name | Part # |\n")
	sb.WriteString("| --- | --- | --- |\n")

	for _, p := range parts {
		pn := ""
		if p.PartNumber.Valid {
			pn = p.PartNumber.String
		}
		// Escape pipes in name or part number to avoid breaking table
		cleanName := strings.ReplaceAll(p.Name, "|", "\\|")
		cleanPN := strings.ReplaceAll(pn, "|", "\\|")

		sb.WriteString(fmt.Sprintf("| %d | %s | %s |\n", p.TotalQuantity, cleanName, cleanPN))
	}

	return sb.String(), nil
}
