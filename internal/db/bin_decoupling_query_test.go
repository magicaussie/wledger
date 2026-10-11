package db_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
)

// Regression guard for stable bin identity decoupling: bins.container_id is
// nullable so an unassigned bin exists without an electrical container. These
// tests assert that a NULL container_id stays NULL (never coerced to 0) through
// both the schema and the generated queries, that the legacy container-scoped
// queries still return only the requested container's bins, and that a part
// assignment on an unassigned bin keeps its bin_id and quantity.
//
// They require the migration that drops NOT NULL on bins.container_id; until it
// lands the unassigned-bin insert is rejected. The legacy graph is seeded with
// raw SQL, matching the physical-storage migration tests, so the fixtures do not
// depend on the generated CreateBin parameter type. Locate is not exercised.

func openBinDecouplingFixture(t *testing.T) (*sql.DB, *db.Queries) {
	t.Helper()

	conn, err := db.Open("file:" + filepath.Join(t.TempDir(), "bin-decoupling.db"))
	if err != nil {
		t.Fatalf("open fixture database: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	if err := db.Migrate(conn); err != nil {
		t.Fatalf("migrate fixture database: %v", err)
	}

	return conn, db.New(conn)
}

func binFixtureExec(t *testing.T, conn *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := conn.ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

func binFixtureContainerID(t *testing.T, conn *sql.DB, binID int64) sql.NullInt64 {
	t.Helper()
	var containerID sql.NullInt64
	if err := conn.QueryRowContext(context.Background(),
		`SELECT container_id FROM bins WHERE id = ?`, binID).Scan(&containerID); err != nil {
		t.Fatalf("read bins.container_id for bin %d: %v", binID, err)
	}
	return containerID
}

func seedBinDecouplingGraph(t *testing.T, conn *sql.DB) {
	t.Helper()
	binFixtureExec(t, conn, `INSERT INTO controllers (id, name, ip_address) VALUES (1, 'C1', '10.0.0.1')`)
	binFixtureExec(t, conn, `INSERT INTO containers (id, name, controller_id) VALUES (1, 'String A', 1)`)
	binFixtureExec(t, conn, `INSERT INTO containers (id, name, controller_id) VALUES (2, 'String B', 1)`)
	binFixtureExec(t, conn, `INSERT INTO bins (id, name, container_id, led_index, width, grid_x, grid_y) VALUES (1, 'A1', 1, 0, 4, 0, 0)`)
	binFixtureExec(t, conn, `INSERT INTO bins (id, name, container_id, led_index, width, grid_x, grid_y) VALUES (2, 'A2', 1, 10, 2, 1, 0)`)
	binFixtureExec(t, conn, `INSERT INTO bins (id, name, container_id, led_index, width, grid_x, grid_y) VALUES (3, 'B1', 2, 0, 4, 0, 0)`)
	binFixtureExec(t, conn, `INSERT INTO bins (id, name, container_id, led_index, width, grid_x, grid_y) VALUES (4, 'Unassigned', NULL, 0, 1, NULL, NULL)`)
}

func binIDs(bins []db.Bin) []int64 {
	ids := make([]int64, 0, len(bins))
	for _, b := range bins {
		ids = append(ids, b.ID)
	}
	return ids
}

func equalInt64s(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestBinDecouplingSchemaStoresNullContainer(t *testing.T) {
	conn, q := openBinDecouplingFixture(t)
	ctx := context.Background()

	seedBinDecouplingGraph(t, conn)

	if got := binFixtureContainerID(t, conn, 4); got.Valid {
		t.Fatalf("unassigned bin container_id = %d, want NULL (never coerced to 0)", got.Int64)
	}
	if got := binFixtureContainerID(t, conn, 1); !got.Valid || got.Int64 != 1 {
		t.Fatalf("assigned bin container_id = %+v, want 1", got)
	}

	unassigned, err := q.GetBin(ctx, 4)
	if err != nil {
		t.Fatalf("GetBin on unassigned bin: %v (generated model must treat container_id as nullable)", err)
	}
	if unassigned.ID != 4 {
		t.Fatalf("GetBin returned bin id %d, want 4", unassigned.ID)
	}
	if unassigned.ContainerID.Valid {
		t.Fatalf("GetBin container_id = %+v, want NULL (never coerced to 0)", unassigned.ContainerID)
	}

	bins, err := q.GetAllBins(ctx)
	if err != nil {
		t.Fatalf("GetAllBins: %v", err)
	}
	if len(bins) != 4 {
		t.Fatalf("GetAllBins returned %d bins, want 4", len(bins))
	}
}

func TestBinDecouplingLegacyContainerScopedQueries(t *testing.T) {
	conn, q := openBinDecouplingFixture(t)
	ctx := context.Background()

	seedBinDecouplingGraph(t, conn)

	gotA, err := q.GetBinsByContainer(ctx, sql.NullInt64{Int64: 1, Valid: true})
	if err != nil {
		t.Fatalf("GetBinsByContainer(1): %v", err)
	}
	if ids := binIDs(gotA); !equalInt64s(ids, []int64{1, 2}) {
		t.Fatalf("GetBinsByContainer(1) ids = %v, want [1 2]", ids)
	}

	gotB, err := q.GetBinsByContainer(ctx, sql.NullInt64{Int64: 2, Valid: true})
	if err != nil {
		t.Fatalf("GetBinsByContainer(2): %v", err)
	}
	if ids := binIDs(gotB); !equalInt64s(ids, []int64{3}) {
		t.Fatalf("GetBinsByContainer(2) ids = %v, want [3]", ids)
	}

	ledZero := sql.NullInt64{Int64: 0, Valid: true}
	if id, err := q.GetBinByContainerAndLed(ctx, db.GetBinByContainerAndLedParams{
		ContainerID: sql.NullInt64{Int64: 1, Valid: true}, LedIndex: ledZero,
	}); err != nil || id != 1 {
		t.Fatalf("GetBinByContainerAndLed(A,0) = %d, %v; want 1", id, err)
	}
	if id, err := q.GetBinByContainerAndLed(ctx, db.GetBinByContainerAndLedParams{
		ContainerID: sql.NullInt64{Int64: 2, Valid: true}, LedIndex: ledZero,
	}); err != nil || id != 3 {
		t.Fatalf("GetBinByContainerAndLed(B,0) = %d, %v; want 3", id, err)
	}
}

func TestBinDecouplingAssignmentOnUnassignedBin(t *testing.T) {
	conn, q := openBinDecouplingFixture(t)
	ctx := context.Background()

	seedBinDecouplingGraph(t, conn)
	binFixtureExec(t, conn, `INSERT INTO parts (id, name) VALUES (1, 'Part One')`)

	if err := q.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{
		PartID:   1,
		BinID:    sql.NullInt64{Int64: 4, Valid: true},
		Quantity: 7,
	}); err != nil {
		t.Fatalf("CreatePartAssignment on unassigned bin: %v", err)
	}

	assignments, err := q.GetPartAssignments(ctx, 1)
	if err != nil {
		t.Fatalf("GetPartAssignments: %v", err)
	}
	if len(assignments) != 1 {
		t.Fatalf("GetPartAssignments returned %d rows, want 1", len(assignments))
	}
	got := assignments[0]
	if !got.BinID.Valid || got.BinID.Int64 != 4 {
		t.Fatalf("assignment bin_id = %+v, want 4", got.BinID)
	}
	if got.Quantity != 7 {
		t.Fatalf("assignment quantity = %d, want 7", got.Quantity)
	}
	if got.ContainerID.Valid {
		t.Fatalf("assignment container_id = %d, want NULL for an unassigned bin", got.ContainerID.Int64)
	}

	contents, err := q.GetBinContents(ctx, sql.NullInt64{Int64: 4, Valid: true})
	if err != nil {
		t.Fatalf("GetBinContents on unassigned bin: %v", err)
	}
	if len(contents) != 1 || contents[0].ID != 1 || contents[0].Quantity != 7 {
		t.Fatalf("GetBinContents on unassigned bin = %+v, want one row part 1 quantity 7", contents)
	}
}
