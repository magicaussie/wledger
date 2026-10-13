package backup

import (
	"bytes"
	"context"
	"database/sql"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
)

func TestExportRestorePreservesAllSettings(t *testing.T) {
	ctx := context.Background()
	_, sourceStore, sourceService, _, cleanupSource := newBackupService(t)
	defer cleanupSource()
	if err := sourceStore.InitSettings(ctx); err != nil {
		t.Fatalf("initialize source settings: %v", err)
	}
	if err := sourceStore.UpdateGeneralSettings(ctx, db.UpdateGeneralSettingsParams{
		RequireAuthForRead:   sql.NullBool{Bool: true, Valid: true},
		LocateTimeoutSeconds: sql.NullInt64{Int64: 37, Valid: true},
		EnableLocateTimeout:  sql.NullBool{Bool: true, Valid: true},
		EnableDebugLogs:      sql.NullBool{Bool: true, Valid: true},
	}); err != nil {
		t.Fatalf("set source general settings: %v", err)
	}
	if err := sourceStore.UpdateColors(ctx, db.UpdateColorsParams{
		ColorLocate:        sql.NullString{String: "#123456", Valid: true},
		ColorStockOk:       sql.NullString{String: "#234567", Valid: true},
		ColorStockLow:      sql.NullString{String: "#345678", Valid: true},
		ColorStockCritical: sql.NullString{String: "#456789", Valid: true},
		ColorError:         sql.NullString{String: "#56789A", Valid: true},
	}); err != nil {
		t.Fatalf("set source color settings: %v", err)
	}
	if err := sourceStore.UpdateSupplierSettings(ctx, db.UpdateSupplierSettingsParams{
		SupplierCacheTtlHours: sql.NullInt64{Int64: 41, Valid: true},
		DefaultCurrency:       sql.NullString{String: "AUD", Valid: true},
	}); err != nil {
		t.Fatalf("set source supplier settings: %v", err)
	}
	if err := sourceStore.MarkInspirationSeedsApplied(ctx); err != nil {
		t.Fatalf("mark source seed state: %v", err)
	}
	want, err := sourceStore.GetSettings(ctx)
	if err != nil {
		t.Fatalf("read source settings: %v", err)
	}

	var archive bytes.Buffer
	if err := sourceService.Export(ctx, &archive); err != nil {
		t.Fatalf("export source settings: %v", err)
	}

	_, destinationStore, destinationService, _, cleanupDestination := newBackupService(t)
	defer cleanupDestination()
	if err := destinationStore.InitSettings(ctx); err != nil {
		t.Fatalf("initialize fresh destination settings: %v", err)
	}
	if err := destinationService.Restore(ctx, bytes.NewReader(archive.Bytes()), int64(archive.Len())); err != nil {
		t.Fatalf("restore settings into fresh destination: %v", err)
	}
	got, err := destinationStore.GetSettings(ctx)
	if err != nil {
		t.Fatalf("read restored settings: %v", err)
	}

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"require_auth_for_read", got.RequireAuthForRead, want.RequireAuthForRead},
		{"locate_timeout_seconds", got.LocateTimeoutSeconds, want.LocateTimeoutSeconds},
		{"enable_locate_timeout", got.EnableLocateTimeout, want.EnableLocateTimeout},
		{"enable_debug_logs", got.EnableDebugLogs, want.EnableDebugLogs},
		{"color_locate", got.ColorLocate, want.ColorLocate},
		{"color_stock_ok", got.ColorStockOk, want.ColorStockOk},
		{"color_stock_low", got.ColorStockLow, want.ColorStockLow},
		{"color_stock_critical", got.ColorStockCritical, want.ColorStockCritical},
		{"color_error", got.ColorError, want.ColorError},
		{"inspiration_seeds_applied", got.InspirationSeedsApplied, want.InspirationSeedsApplied},
		{"supplier_cache_ttl_hours", got.SupplierCacheTtlHours, want.SupplierCacheTtlHours},
		{"default_currency", got.DefaultCurrency, want.DefaultCurrency},
	}
	for _, check := range checks {
		if check.got != check.want {
			t.Errorf("restored setting %s = %#v, want %#v", check.name, check.got, check.want)
		}
	}
	if !got.CreatedAt.Valid || !want.CreatedAt.Valid || !got.CreatedAt.Time.Equal(want.CreatedAt.Time) {
		t.Errorf("restored created_at = %+v, want %+v", got.CreatedAt, want.CreatedAt)
	}
	if !got.UpdatedAt.Valid || !want.UpdatedAt.Valid || !got.UpdatedAt.Time.Equal(want.UpdatedAt.Time) {
		t.Errorf("restored updated_at = %+v, want %+v", got.UpdatedAt, want.UpdatedAt)
	}
}
