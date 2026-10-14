package handler

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/categories"
)

func TestPartsCSVImportPreservesExtendedProductFields(t *testing.T) {
	h, conn := setupPartTest(t)
	defer conn.Close()
	defer cleanupPartTest()
	csvContent := "Name,SKU,Brand,Model,Colour,Material,Finish,Condition,Length mm,Width mm,Height mm,Depth mm,Weight g,Notes,Purchased From,Supplier Product Code,Purchase Date,Purchase Price,Currency,Product URL,Purchase Notes\n" +
		"Imported bolt,BOLT-8-35,Acme,M8x35,Silver,Steel,Zinc,New,35,8,8,35,12.5,For fixtures,Local hardware shop,AC-835,2026-01-15,0.42,AUD,https://example.com/bolt,Receipt 42\n"
	request := createMultipartRequest(t, "/parts/import", http.MethodPost, map[string]string{"raw_text": csvContent}, map[string]string{})
	request = request.WithContext(auth.WithUser(request.Context(), auth.User{Role: "admin"}))
	response := httptest.NewRecorder()
	h.HandlePartsImport(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("import status=%d body=%s", response.Code, response.Body.String())
	}

	var partID int64
	if err := conn.QueryRowContext(context.Background(), `SELECT id FROM parts WHERE name = ?`, "Imported bolt").Scan(&partID); err != nil {
		t.Fatalf("import did not create expected product: %v; response: %s", err, response.Body.String())
	}
	var sku, brand, model, colour, material, finish, condition, notes string
	var length, weight, price sql.NullFloat64
	var currency, supplierCode, purchaseDate, productURL, purchaseNotes string
	err := conn.QueryRowContext(context.Background(), `SELECT sku,brand,model,colour,material,finish,condition,notes,length_mm,weight_g,purchase_price,currency,supplier_product_code,purchase_date,product_url,purchase_notes FROM part_details WHERE part_id = ?`, partID).Scan(
		&sku, &brand, &model, &colour, &material, &finish, &condition, &notes, &length, &weight, &price, &currency, &supplierCode, &purchaseDate, &productURL, &purchaseNotes)
	if err != nil {
		t.Fatal(err)
	}
	if sku != "BOLT-8-35" || brand != "Acme" || model != "M8x35" || colour != "Silver" || material != "Steel" || finish != "Zinc" || condition != "New" || notes != "For fixtures" {
		t.Fatalf("imported product descriptors are incomplete")
	}
	if !length.Valid || length.Float64 != 35 || !weight.Valid || weight.Float64 != 12.5 || !price.Valid || price.Float64 != 0.42 || currency != "AUD" || supplierCode != "AC-835" || purchaseDate != "2026-01-15" || productURL != "https://example.com/bolt" || purchaseNotes != "Receipt 42" {
		t.Fatalf("imported physical/purchasing fields are incomplete")
	}
}

func TestPartsCSVImportPreservesCategoryAndTypedSpecifications(t *testing.T) {
	h, conn := setupPartTest(t)
	defer conn.Close()
	defer cleanupPartTest()
	ctx := context.Background()
	service := categories.NewService(conn)
	categoryID, err := service.Save(ctx, categories.Category{Name: "Fasteners"})
	if err != nil {
		t.Fatal(err)
	}
	attributeID, err := service.SaveAttribute(ctx, categories.AttributeInput{
		CategoryID: categoryID, Name: "Length", DataType: categories.NumberType, Unit: "mm", Required: true,
		Minimum: sql.NullFloat64{Float64: 1, Valid: true}, Maximum: sql.NullFloat64{Float64: 100, Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	importCSV := func(content string) *httptest.ResponseRecorder {
		t.Helper()
		request := createMultipartRequest(t, "/parts/import", http.MethodPost, map[string]string{"raw_text": content}, map[string]string{})
		request = request.WithContext(auth.WithUser(request.Context(), auth.User{Role: "admin"}))
		response := httptest.NewRecorder()
		h.HandlePartsImport(response, request)
		return response
	}

	response := importCSV("Name,Category,Category Specifications\nImported fastener,Fasteners,\"{\"\"Length\"\":35}\"\n")
	if response.Code != http.StatusOK {
		t.Fatalf("valid category import status=%d body=%s", response.Code, response.Body.String())
	}
	var partID int64
	if err := conn.QueryRowContext(ctx, `SELECT p.id FROM parts p JOIN part_categories pc ON pc.part_id = p.id WHERE p.name = ? AND pc.category_id = ?`, "Imported fastener", categoryID).Scan(&partID); err != nil {
		t.Fatalf("imported category assignment missing: %v", err)
	}
	var got sql.NullFloat64
	if err := conn.QueryRowContext(ctx, `SELECT value_number FROM part_attribute_values WHERE part_id = ? AND attribute_id = ?`, partID, attributeID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Valid || got.Float64 != 35 {
		t.Fatalf("imported typed specification = %#v, want 35", got)
	}

	response = importCSV("Name,Category,Category Specifications\nMissing required value,Fasteners,{}\n")
	if response.Code != http.StatusOK {
		t.Fatalf("invalid category import status=%d body=%s", response.Code, response.Body.String())
	}
	var count int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM parts WHERE name = ?`, "Missing required value").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("invalid required specification import created a product")
	}
}
