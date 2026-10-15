package parts

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/documents"
	"github.com/tuxedocurly/wledger/internal/middleware"
	"github.com/tuxedocurly/wledger/internal/tags"
)

func newDeleteTestService(t *testing.T) (*service, db.Store, context.Context) {
	t.Helper()
	t.Chdir(t.TempDir())
	database, s, cleanup := setupTestDB(t)
	t.Cleanup(cleanup)
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if _, err := s.CreateUser(context.Background(), db.CreateUserParams{Email: "admin@test.com", PasswordHash: "h", Role: "admin"}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	svc := NewService(database, s, logger, tags.NewService(database, s), documents.NewService(s, logger)).(*service)
	ctx := context.WithValue(context.Background(), middleware.UserContextKey, int64(1))
	return svc, s, ctx
}

// TestDeletePartsRollbackPreservesFiles verifies that a bulk delete which fails
// partway (here because of a missing id) rolls back the database rows AND leaves
// the surviving parts' attachment files on disk. Previously files were deleted
// inside the loop before commit, so a later failure permanently erased the
// earlier parts' attachments while their rows survived.
func TestDeletePartsRollbackPreservesFiles(t *testing.T) {
	svc, s, ctx := newDeleteTestService(t)
	docSvc := documents.NewService(s, slog.New(slog.NewTextHandler(os.Stdout, nil)))

	partID, err := svc.CreatePart(ctx, CreatePartRequest{Name: "Keep me"})
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	docPath, err := docSvc.UploadDocument(ctx, s, partID, strings.NewReader("payload"), "keep.pdf")
	if err != nil {
		t.Fatalf("upload document: %v", err)
	}
	disk := filepath.Join("app", "uploads", strings.TrimPrefix(docPath, "/uploads/"))

	err = svc.DeleteParts(ctx, []int64{partID, 999999})
	if err == nil {
		t.Fatal("bulk delete including a missing id succeeded, want error")
	}

	if _, err := s.GetPart(ctx, partID); err != nil {
		t.Fatalf("part row removed by rolled-back delete: %v", err)
	}
	if _, err := os.Stat(disk); err != nil {
		t.Fatalf("document file removed by rolled-back delete: %v", err)
	}
}

// TestUpdatePartCannotRepointAnotherPartsLink verifies that submitting another
// part's link id in an update is rejected and leaves that link untouched. The
// underlying UPDATE previously matched on link id alone, so a crafted request
// could rewrite a different part's link.
func TestUpdatePartCannotRepointAnotherPartsLink(t *testing.T) {
	svc, s, ctx := newDeleteTestService(t)

	partA, err := svc.CreatePart(ctx, CreatePartRequest{Name: "Part A"})
	if err != nil {
		t.Fatalf("create part A: %v", err)
	}
	partB, err := svc.CreatePart(ctx, CreatePartRequest{
		Name:  "Part B",
		Links: []LinkDTO{{URL: "https://b.example", Label: "b"}},
	})
	if err != nil {
		t.Fatalf("create part B: %v", err)
	}

	bLinks, err := s.GetPartLinks(ctx, partB)
	if err != nil || len(bLinks) != 1 {
		t.Fatalf("part B links = %d err=%v, want 1", len(bLinks), err)
	}
	bLink := bLinks[0]

	err = svc.UpdatePart(ctx, UpdatePartRequest{
		ID:            partA,
		Name:          "Part A renamed",
		ExistingLinks: []LinkDTO{{ID: bLink.ID, URL: "https://attacker.example", Label: "hijacked"}},
	})
	if err == nil {
		t.Fatal("cross-part link update succeeded, want rejection")
	}

	after, err := s.GetPartLinks(ctx, partB)
	if err != nil || len(after) != 1 {
		t.Fatalf("part B links after attack = %d err=%v, want 1", len(after), err)
	}
	if after[0].Url != "https://b.example" {
		t.Fatalf("part B link url = %q, want unchanged %q", after[0].Url, "https://b.example")
	}
	// The whole update transaction must roll back, leaving part A unchanged too.
	a, err := s.GetPart(ctx, partA)
	if err != nil {
		t.Fatalf("get part A: %v", err)
	}
	if a.Name != "Part A" {
		t.Fatalf("part A name = %q, want rollback to %q", a.Name, "Part A")
	}
}
