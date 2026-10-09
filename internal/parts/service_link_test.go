package parts

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/documents"
	"github.com/tuxedocurly/wledger/internal/middleware"
	"github.com/tuxedocurly/wledger/internal/tags"
)

func setupLinkTestService(t *testing.T) (Service, db.Store, context.Context) {
	t.Helper()

	database, s, dbCleanup := setupTestDB(t)
	t.Cleanup(dbCleanup)

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	tagSvc := tags.NewService(database, s)
	docSvc := documents.NewService(s, logger)
	svc := NewService(database, s, logger, tagSvc, docSvc)

	if _, err := s.CreateUser(context.Background(), db.CreateUserParams{
		Email:        "admin@test.com",
		PasswordHash: "hash",
		Role:         "admin",
	}); err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	ctx := context.WithValue(context.Background(), middleware.UserContextKey, int64(1))
	return svc, s, ctx
}

// unsafeLinkURLs are values that must never be persisted as active links.
var unsafeLinkURLs = []string{
	"javascript:alert(1)",
	"data:text/html,<script>alert(1)</script>",
	"JaVaScRiPt:alert(1)",
	"vbscript:msgbox(1)",
	"file:///etc/passwd",
	"//example.com",
	"not-a-url",
	"https://exa mple.com",
}

func TestCreatePart_RejectsInvalidLinks(t *testing.T) {
	svc, s, ctx := setupLinkTestService(t)

	for _, u := range unsafeLinkURLs {
		_, err := svc.CreatePart(ctx, CreatePartRequest{
			Name:  "Bad Link Part",
			Links: []LinkDTO{{URL: u, Label: "bad"}},
		})
		if !errors.Is(err, ErrInvalidLinkURL) {
			t.Errorf("CreatePart with %q: expected ErrInvalidLinkURL, got %v", u, err)
		}
	}

	// No part may be persisted when link validation fails.
	parts, err := s.GetAllParts(ctx)
	if err != nil {
		t.Fatalf("GetAllParts failed: %v", err)
	}
	if len(parts) != 0 {
		t.Errorf("expected no parts persisted after invalid link attempts, got %d", len(parts))
	}
}

func TestCreatePart_PersistsValidLinks(t *testing.T) {
	svc, s, ctx := setupLinkTestService(t)

	id, err := svc.CreatePart(ctx, CreatePartRequest{
		Name: "Good Part",
		Links: []LinkDTO{
			{URL: "https://example.com/a", Label: "A"},
			{URL: "http://example.com/b?x=1", Label: "B"},
		},
	})
	if err != nil {
		t.Fatalf("CreatePart failed: %v", err)
	}

	links, err := s.GetPartLinks(ctx, id)
	if err != nil {
		t.Fatalf("GetPartLinks failed: %v", err)
	}
	if len(links) != 2 {
		t.Fatalf("expected 2 links, got %d", len(links))
	}
}

func TestUpdatePart_RejectsInvalidNewLink(t *testing.T) {
	svc, s, ctx := setupLinkTestService(t)

	id, err := svc.CreatePart(ctx, CreatePartRequest{Name: "Original"})
	if err != nil {
		t.Fatalf("CreatePart failed: %v", err)
	}

	// A pre-existing attachment that must survive the failed update.
	if err := s.CreatePartDoc(ctx, db.CreatePartDocParams{
		PartID:   id,
		FilePath: "/uploads/docs/keep.pdf",
		FileName: "keep.pdf",
	}); err != nil {
		t.Fatalf("CreatePartDoc failed: %v", err)
	}

	err = svc.UpdatePart(ctx, UpdatePartRequest{
		ID:       id,
		Name:     "Renamed",
		NewLinks: []LinkDTO{{URL: "javascript:alert(1)", Label: "bad"}},
	})
	if !errors.Is(err, ErrInvalidLinkURL) {
		t.Fatalf("expected ErrInvalidLinkURL, got %v", err)
	}

	// The part record must be untouched.
	p, err := svc.GetPart(ctx, id)
	if err != nil {
		t.Fatalf("GetPart failed: %v", err)
	}
	if p.Name != "Original" {
		t.Errorf("expected part name to be unchanged, got %q", p.Name)
	}

	// No link may be persisted.
	links, _ := s.GetPartLinks(ctx, id)
	if len(links) != 0 {
		t.Errorf("expected no links persisted, got %d", len(links))
	}

	// The existing attachment must survive.
	docs, _ := s.GetPartDocs(ctx, id)
	if len(docs) != 1 {
		t.Errorf("expected existing document to survive, got %d", len(docs))
	}
}

func TestUpdatePart_RejectsInvalidExistingLink(t *testing.T) {
	svc, s, ctx := setupLinkTestService(t)

	id, err := svc.CreatePart(ctx, CreatePartRequest{
		Name:  "Original",
		Links: []LinkDTO{{URL: "https://example.com/ok", Label: "ok"}},
	})
	if err != nil {
		t.Fatalf("CreatePart failed: %v", err)
	}

	links, _ := s.GetPartLinks(ctx, id)
	if len(links) != 1 {
		t.Fatalf("expected 1 link, got %d", len(links))
	}

	err = svc.UpdatePart(ctx, UpdatePartRequest{
		ID:            id,
		Name:          "Renamed",
		ExistingLinks: []LinkDTO{{ID: links[0].ID, URL: "javascript:alert(1)", Label: "ok"}},
	})
	if !errors.Is(err, ErrInvalidLinkURL) {
		t.Fatalf("expected ErrInvalidLinkURL, got %v", err)
	}

	// The stored link must be unchanged.
	after, _ := s.GetPartLinks(ctx, id)
	if len(after) != 1 || after[0].Url != "https://example.com/ok" {
		t.Errorf("expected existing link to be preserved, got %+v", after)
	}
}

func TestUpdatePart_PersistsValidLinks(t *testing.T) {
	svc, s, ctx := setupLinkTestService(t)

	id, err := svc.CreatePart(ctx, CreatePartRequest{
		Name:  "Original",
		Links: []LinkDTO{{URL: "https://example.com/old", Label: "old"}},
	})
	if err != nil {
		t.Fatalf("CreatePart failed: %v", err)
	}
	links, _ := s.GetPartLinks(ctx, id)

	err = svc.UpdatePart(ctx, UpdatePartRequest{
		ID:            id,
		Name:          "Renamed",
		ExistingLinks: []LinkDTO{{ID: links[0].ID, URL: "https://example.com/updated", Label: "updated"}},
		NewLinks:      []LinkDTO{{URL: "http://example.com/new", Label: "new"}},
	})
	if err != nil {
		t.Fatalf("UpdatePart failed: %v", err)
	}

	after, _ := s.GetPartLinks(ctx, id)
	if len(after) != 2 {
		t.Fatalf("expected 2 links, got %d", len(after))
	}
}
