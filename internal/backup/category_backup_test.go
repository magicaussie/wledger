package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/tuxedocurly/wledger/internal/categories"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/productdetails"
)

func TestCategoryAndProductSpecificationsBackupRoundTrip(t *testing.T) {
	ctx := context.Background()
	sourceDB, sourceStore, sourceBackup, sourceUploads, sourceCleanup := newBackupService(t)
	defer sourceCleanup()
	if err := sourceStore.InitSettings(ctx); err != nil {
		t.Fatal(err)
	}

	categoryService := categories.NewServiceWithUploadsDir(sourceDB, sourceUploads)
	categoryImage := filepath.Join(sourceUploads, "images", "category-backup.jpg")
	if err := os.MkdirAll(filepath.Dir(categoryImage), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(categoryImage, []byte("temporary image payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	rootID, err := categoryService.Save(ctx, categories.Category{Name: "Hardware", Description: "Reusable stock"})
	if err != nil {
		t.Fatal(err)
	}
	childID, err := categoryService.Save(ctx, categories.Category{Name: "Fasteners", ImagePath: "/uploads/images/category-backup.jpg", ParentID: sql.NullInt64{Int64: rootID, Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	attributeID, err := categoryService.SaveAttribute(ctx, categories.AttributeInput{CategoryID: childID, Name: "Length", DataType: categories.NumberType, Unit: "mm", Options: []string{"35"}, Minimum: sql.NullFloat64{Float64: 1, Valid: true}, Maximum: sql.NullFloat64{Float64: 100, Valid: true}, Required: true})
	if err != nil {
		t.Fatal(err)
	}
	partID, err := sourceStore.CreatePart(ctx, db.CreatePartParams{Name: "M8 bolt"})
	if err != nil {
		t.Fatal(err)
	}
	length := 35.0
	if err := categoryService.SetProductCategory(ctx, partID, &childID, []categories.Specification{{AttributeID: attributeID, Number: &length}}); err != nil {
		t.Fatal(err)
	}
	extended := productdetails.ExtendedDetails{PartID: partID, SKU: "BOLT-001", Brand: "Acme", Model: "M8 x 35", Colour: "Silver", Material: "Steel", LengthMM: sql.NullFloat64{Float64: 35, Valid: true}, DimensionUnit: "in", WeightG: sql.NullFloat64{Float64: 12.5, Valid: true}, WeightUnit: "lb", PurchasedFrom: "Local Hardware", SupplierProductCode: "ACME-8035", PurchaseDate: sql.NullString{String: "2026-01-15", Valid: true}, PurchasePrice: sql.NullFloat64{Float64: 0.4, Valid: true}, Currency: "AUD", ProductURL: "https://example.com/bolt", PurchaseNotes: "Reference receipt 44"}
	if err := productdetails.SaveExtendedDetails(ctx, sourceDB, extended); err != nil {
		t.Fatalf("save extended details: %v", err)
	}

	var archive bytes.Buffer
	if err := sourceBackup.Export(ctx, &archive); err != nil {
		t.Fatalf("export: %v", err)
	}
	zipArchive, err := zip.NewReader(bytes.NewReader(archive.Bytes()), int64(archive.Len()))
	if err != nil {
		t.Fatal(err)
	}
	var csvFile io.ReadCloser
	for _, file := range zipArchive.File {
		if file.Name == "human_readable_parts.csv" {
			csvFile, err = file.Open()
			break
		}
	}
	if err != nil || csvFile == nil {
		t.Fatalf("open human-readable product export: %v", err)
	}
	reader := csv.NewReader(csvFile)
	header, err := reader.Read()
	if err != nil {
		t.Fatal(err)
	}
	row, err := reader.Read()
	if err != nil {
		t.Fatal(err)
	}
	column := make(map[string]int, len(header))
	for i, field := range header {
		column[field] = i
	}
	for field, want := range map[string]string{"SKU": "BOLT-001", "Brand": "Acme", "Length": "35", "Dimension Unit": "in", "Weight": "12.5", "Weight Unit": "lb", "Purchase Price": "0.4", "Currency": "AUD", "Category": "Fasteners", "Category Specifications": `{"Length":35}`} {
		index, ok := column[field]
		if !ok || index >= len(row) || row[index] != want {
			t.Errorf("CSV %s = %q, want %q", field, row[index], want)
		}
	}
	_ = csvFile.Close()

	destinationDB, destinationStore, destinationBackup, destinationUploads, destinationCleanup := newBackupService(t)
	defer destinationCleanup()
	if err := destinationStore.InitSettings(ctx); err != nil {
		t.Fatal(err)
	}
	if err := destinationBackup.Restore(ctx, bytes.NewReader(archive.Bytes()), int64(archive.Len())); err != nil {
		t.Fatalf("restore: %v", err)
	}

	var gotName, gotImage string
	var gotParent sql.NullInt64
	if err := destinationDB.QueryRowContext(ctx, `SELECT name, parent_id, image_path FROM categories WHERE id = ?`, childID).Scan(&gotName, &gotParent, &gotImage); err != nil {
		t.Fatal(err)
	}
	if gotName != "Fasteners" || !gotParent.Valid || gotParent.Int64 != rootID {
		t.Fatalf("restored category = %q parent=%#v", gotName, gotParent)
	}
	if gotImage != "/uploads/images/category-backup.jpg" {
		t.Fatalf("restored category image path = %q", gotImage)
	}
	if _, err := os.Stat(filepath.Join(destinationUploads, "images", "category-backup.jpg")); err != nil {
		t.Fatalf("restored category image file missing: %v", err)
	}
	var count int
	if err := destinationDB.QueryRowContext(ctx, `SELECT count(*) FROM part_categories WHERE part_id = ? AND category_id = ?`, partID, childID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("restored product category count=%d err=%v", count, err)
	}
	var gotLength sql.NullFloat64
	if err := destinationDB.QueryRowContext(ctx, `SELECT value_number FROM part_attribute_values WHERE part_id = ? AND attribute_id = ?`, partID, attributeID).Scan(&gotLength); err != nil {
		t.Fatal(err)
	}
	if !gotLength.Valid || gotLength.Float64 != length {
		t.Fatalf("restored category specification length = %#v, want %v", gotLength, length)
	}
	restoredAttributes, err := categories.NewService(destinationDB).ListAttributes(ctx, childID)
	if err != nil || len(restoredAttributes) != 1 || len(restoredAttributes[0].Options) != 1 || restoredAttributes[0].Options[0] != "35" || !restoredAttributes[0].Minimum.Valid || restoredAttributes[0].Minimum.Float64 != 1 || !restoredAttributes[0].Maximum.Valid || restoredAttributes[0].Maximum.Float64 != 100 {
		t.Fatalf("restored category attribute constraints=%#v err=%v", restoredAttributes, err)
	}
	var gotSKU, gotBrand, gotProductURL, gotDimensionUnit, gotWeightUnit string
	var gotProductLength, gotProductWeight sql.NullFloat64
	if err := destinationDB.QueryRowContext(ctx, `SELECT sku, brand, length_mm, dimension_unit, weight_g, weight_unit, product_url FROM part_details WHERE part_id = ?`, partID).Scan(&gotSKU, &gotBrand, &gotProductLength, &gotDimensionUnit, &gotProductWeight, &gotWeightUnit, &gotProductURL); err != nil {
		t.Fatal(err)
	}
	if gotSKU != "BOLT-001" || gotBrand != "Acme" || !gotProductLength.Valid || gotProductLength.Float64 != 35 || gotDimensionUnit != "in" || !gotProductWeight.Valid || gotProductWeight.Float64 != 12.5 || gotWeightUnit != "lb" || gotProductURL != "https://example.com/bolt" {
		t.Fatalf("restored extended details = sku %q brand %q length %#v %q weight %#v %q url %q", gotSKU, gotBrand, gotProductLength, gotDimensionUnit, gotProductWeight, gotWeightUnit, gotProductURL)
	}
}

func TestCurrentV21BackupWithoutPartDetailsRemainsReadable(t *testing.T) {
	manifest := physBaseStorageManifest()
	manifest.Version = supportedCategoryManifestVersion
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	archive := buildEnvelopeZip(t, [][2]string{{currentStorageManifestName, string(encoded)}})
	zipReader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := readRestoreEnvelope(zipReader)
	if err != nil {
		t.Fatalf("read version 2.1 current envelope: %v", err)
	}
	if envelope.Current == nil || envelope.Current.Version != supportedCategoryManifestVersion {
		t.Fatalf("decoded current manifest = %#v", envelope.Current)
	}
	if envelope.Legacy.PartDetails != nil {
		t.Fatal("2.1 envelope unexpectedly contains part details")
	}
}

func TestCurrentV20BackupWithoutCategoryFieldsRemainsReadable(t *testing.T) {
	manifest := physBaseStorageManifest()
	manifest.Version = supportedPreviousManifestVersion
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	archive := buildEnvelopeZip(t, [][2]string{{currentStorageManifestName, string(encoded)}})
	zipReader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := readRestoreEnvelope(zipReader)
	if err != nil {
		t.Fatalf("read legacy current envelope: %v", err)
	}
	if envelope.Current == nil || envelope.Current.Version != supportedPreviousManifestVersion {
		t.Fatalf("decoded current manifest = %#v", envelope.Current)
	}
	if len(envelope.Legacy.Categories) != 0 || len(envelope.Legacy.PartCategories) != 0 {
		t.Fatal("2.0 envelope unexpectedly gained category records")
	}
}
