package tags

import (
	"context"
	"database/sql"
	"strings"

	"github.com/tuxedocurly/wledger/internal/db"
)

type Service interface {
	SyncTags(ctx context.Context, qtx *db.Queries, partID int64, tagNames []string) error
	ListAllTags(ctx context.Context) ([]db.Tag, error)
}

type service struct {
	database *sql.DB
	queries  *db.Queries
}

func NewService(database *sql.DB, queries *db.Queries) Service {
	return &service{
		database: database,
		queries:  queries,
	}
}

func (s *service) ListAllTags(ctx context.Context) ([]db.Tag, error) {
	return s.queries.ListAllTags(ctx)
}

func (s *service) SyncTags(ctx context.Context, qtx *db.Queries, partID int64, tagNames []string) error {
	// Normalize and deduplicate tags
	uniqueTags := make(map[string]bool)
	for _, name := range tagNames {
		n := strings.TrimSpace(strings.ToLower(name))
		if n != "" {
			uniqueTags[n] = true
		}
	}

	// Remove existing tags for this part
	if err := qtx.RemoveTagsFromPart(ctx, partID); err != nil {
		return err
	}

	// Process each tag
	for name := range uniqueTags {
		// Find or Create
		tag, err := qtx.GetTagByName(ctx, name)
		if err != nil {
			if err == sql.ErrNoRows {
				tag, err = qtx.CreateTag(ctx, name)
				if err != nil {
					return err
				}
			} else {
				return err
			}
		}

		// Link
		if err := qtx.AddTagToPart(ctx, db.AddTagToPartParams{
			PartID: partID,
			TagID:  tag.ID,
		}); err != nil {
			return err
		}
	}

	// Cleanup unused tags
	if err := qtx.DeleteUnusedTags(ctx); err != nil {
		return err
	}

	return nil
}
