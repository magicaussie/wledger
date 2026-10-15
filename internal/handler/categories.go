package handler

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/categories"
	"github.com/tuxedocurly/wledger/internal/config"
	"github.com/tuxedocurly/wledger/internal/images"
	"github.com/tuxedocurly/wledger/internal/maintenance"
	"github.com/tuxedocurly/wledger/internal/qrcode"
	"github.com/tuxedocurly/wledger/web/pages"
)

const categoryScanPrefix = "wledger:category:"

func CategoryScanCode(id int64) string { return fmt.Sprintf("%s%d", categoryScanPrefix, id) }

func categoryID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, categories.ErrNotFound
	}
	return id, nil
}

func (h *Handler) HandleCategories(w http.ResponseWriter, r *http.Request) {
	items, err := categories.NewService(h.Database).List(r.Context(), strings.TrimSpace(r.URL.Query().Get("q")))
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to load categories", http.StatusInternalServerError)
		return
	}
	pages.CategoryList(auth.GetUserFromRequest(r), items, r.URL.Query().Get("q")).Render(r.Context(), w)
}

func (h *Handler) HandleCategoryDetail(w http.ResponseWriter, r *http.Request) {
	id, err := categoryID(r)
	if err != nil {
		http.Error(w, "Category not found", http.StatusNotFound)
		return
	}
	svc := categories.NewService(h.Database)
	item, err := svc.Get(r.Context(), id)
	if err != nil {
		categoryError(h, w, r, err, "Failed to load category")
		return
	}
	attributes, err := svc.ListAttributes(r.Context(), id)
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to load category specifications", http.StatusInternalServerError)
		return
	}
	products, err := svc.Products(r.Context(), id, strings.TrimSpace(r.URL.Query().Get("q")))
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to load category products", http.StatusInternalServerError)
		return
	}
	pages.CategoryDetail(auth.GetUserFromRequest(r), item, attributes, products, r.URL.Query().Get("q")).Render(r.Context(), w)
}

func (h *Handler) HandleCategoryNew(w http.ResponseWriter, r *http.Request) {
	items, err := categories.NewService(h.Database).List(r.Context(), "")
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to load categories", http.StatusInternalServerError)
		return
	}
	pages.CategoryForm(auth.GetUserFromRequest(r), categories.Category{}, items).Render(r.Context(), w)
}

func (h *Handler) HandleCategoryEdit(w http.ResponseWriter, r *http.Request) {
	id, err := categoryID(r)
	if err != nil {
		http.Error(w, "Category not found", http.StatusNotFound)
		return
	}
	svc := categories.NewService(h.Database)
	item, err := svc.Get(r.Context(), id)
	if err != nil {
		categoryError(h, w, r, err, "Failed to load category")
		return
	}
	items, err := svc.List(r.Context(), "")
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to load categories", http.StatusInternalServerError)
		return
	}
	pages.CategoryForm(auth.GetUserFromRequest(r), item, items).Render(r.Context(), w)
}

func (h *Handler) HandleCategoryCreate(w http.ResponseWriter, r *http.Request) {
	ctx, releaseUploads, claimErr := maintenance.UploadReferences(r.Context(), config.DirUploads)
	if claimErr != nil {
		h.UIError.Respond(w, r, claimErr, "Upload lock unavailable", http.StatusServiceUnavailable)
		return
	}
	defer releaseUploads()
	r = r.WithContext(ctx)
	if !limitRequestBody(w, r, config.MaxUploadSizeParts) {
		return
	}
	if err := r.ParseMultipartForm(config.MaxUploadSizeParts); err != nil {
		http.Error(w, "Invalid category form", http.StatusBadRequest)
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	imagePath, uploaded, err := processCategoryImage(r, "", false)
	if err != nil {
		http.Error(w, "Invalid category image", http.StatusBadRequest)
		return
	}
	item, err := categoryFromForm(r, 0)
	if err != nil {
		if uploaded {
			images.DeleteByWebPath(imagePath)
		}
		http.Error(w, "Invalid category fields", http.StatusBadRequest)
		return
	}
	item.ImagePath = imagePath
	id, err := categories.NewService(h.Database).Save(r.Context(), item)
	if err != nil {
		if uploaded {
			images.DeleteByWebPath(imagePath)
		}
		categoryError(h, w, r, err, "Failed to save category")
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/categories/%d", id), http.StatusSeeOther)
}

func (h *Handler) HandleCategoryUpdate(w http.ResponseWriter, r *http.Request) {
	ctx, releaseUploads, claimErr := maintenance.UploadReferences(r.Context(), config.DirUploads)
	if claimErr != nil {
		h.UIError.Respond(w, r, claimErr, "Upload lock unavailable", http.StatusServiceUnavailable)
		return
	}
	defer releaseUploads()
	r = r.WithContext(ctx)
	id, err := categoryID(r)
	if err != nil {
		http.Error(w, "Category not found", http.StatusNotFound)
		return
	}
	if !limitRequestBody(w, r, config.MaxUploadSizeParts) {
		return
	}
	if err := r.ParseMultipartForm(config.MaxUploadSizeParts); err != nil {
		http.Error(w, "Invalid category form", http.StatusBadRequest)
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	svc := categories.NewService(h.Database)
	oldCategory, err := svc.Get(r.Context(), id)
	if err != nil {
		categoryError(h, w, r, err, "Failed to load category")
		return
	}
	imagePath, uploaded, err := processCategoryImage(r, oldCategory.ImagePath, r.FormValue("remove_image") == "true")
	if err != nil {
		http.Error(w, "Invalid category image", http.StatusBadRequest)
		return
	}
	item, err := categoryFromForm(r, id)
	if err != nil {
		if uploaded {
			images.DeleteByWebPath(imagePath)
		}
		http.Error(w, "Invalid category fields", http.StatusBadRequest)
		return
	}
	item.ImagePath = imagePath
	if _, err := svc.Save(r.Context(), item); err != nil {
		if uploaded {
			images.DeleteByWebPath(imagePath)
		}
		categoryError(h, w, r, err, "Failed to update category")
		return
	}
	if oldCategory.ImagePath != "" && oldCategory.ImagePath != imagePath {
		images.DeleteByWebPath(oldCategory.ImagePath)
	}
	http.Redirect(w, r, fmt.Sprintf("/categories/%d", id), http.StatusSeeOther)
}

func (h *Handler) HandleCategoryDelete(w http.ResponseWriter, r *http.Request) {
	id, err := categoryID(r)
	if err != nil {
		http.Error(w, "Category not found", http.StatusNotFound)
		return
	}
	svc := categories.NewService(h.Database)
	item, err := svc.Get(r.Context(), id)
	if err != nil {
		categoryError(h, w, r, err, "Failed to load category")
		return
	}
	if err := svc.Delete(r.Context(), id); err != nil {
		categoryError(h, w, r, err, "Failed to delete category")
		return
	}
	if item.ImagePath != "" {
		images.DeleteByWebPath(item.ImagePath)
	}
	http.Redirect(w, r, "/categories", http.StatusSeeOther)
}

func (h *Handler) HandleCategoryAttributeSave(w http.ResponseWriter, r *http.Request) {
	categoryID, err := categoryID(r)
	if err != nil {
		http.Error(w, "Category not found", http.StatusNotFound)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid specification form", http.StatusBadRequest)
		return
	}
	id := int64(0)
	if raw := chi.URLParam(r, "attributeID"); raw != "" {
		id, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			http.Error(w, "Invalid specification identity", http.StatusBadRequest)
			return
		}
	}
	order, err := strconv.ParseInt(r.FormValue("display_order"), 10, 64)
	if err != nil && r.FormValue("display_order") != "" {
		http.Error(w, "Invalid display order", http.StatusBadRequest)
		return
	}
	minimum, err := optionalAttributeNumber(r.FormValue("min_value"))
	if err != nil {
		http.Error(w, "Invalid minimum value", http.StatusBadRequest)
		return
	}
	maximum, err := optionalAttributeNumber(r.FormValue("max_value"))
	if err != nil {
		http.Error(w, "Invalid maximum value", http.StatusBadRequest)
		return
	}
	options := strings.Split(r.FormValue("options"), "\n")
	_, err = categories.NewService(h.Database).SaveAttribute(r.Context(), categories.AttributeInput{
		ID: id, CategoryID: categoryID, Name: r.FormValue("name"), DataType: categories.DataType(r.FormValue("data_type")),
		Unit: r.FormValue("unit"), Options: options, Minimum: minimum, Maximum: maximum,
		Required: r.FormValue("required") == "on", DisplayOrder: order,
	})
	if err != nil {
		categoryError(h, w, r, err, "Failed to save category specification")
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/categories/%d", categoryID), http.StatusSeeOther)
}

func (h *Handler) HandleCategoryAttributeDelete(w http.ResponseWriter, r *http.Request) {
	categoryID, err := categoryID(r)
	if err != nil {
		http.Error(w, "Category not found", http.StatusNotFound)
		return
	}
	attributeID, err := strconv.ParseInt(chi.URLParam(r, "attributeID"), 10, 64)
	if err != nil || attributeID <= 0 {
		http.Error(w, "Specification not found", http.StatusNotFound)
		return
	}
	if err := categories.NewService(h.Database).DeleteAttribute(r.Context(), categoryID, attributeID); err != nil {
		categoryError(h, w, r, err, "Failed to delete category specification")
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/categories/%d", categoryID), http.StatusSeeOther)
}

func (h *Handler) HandleCategoryQR(w http.ResponseWriter, r *http.Request) {
	id, err := categoryID(r)
	if err != nil {
		http.Error(w, "Category not found", http.StatusNotFound)
		return
	}
	if _, err := categories.NewService(h.Database).Get(r.Context(), id); err != nil {
		categoryError(h, w, r, err, "Failed to load category")
		return
	}
	png, err := qrcode.PNG(CategoryScanCode(id), qrScaleFromQuery(r))
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to generate category QR code", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(png)
}

func optionalAttributeNumber(raw string) (sql.NullFloat64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return sql.NullFloat64{}, nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return sql.NullFloat64{}, categories.ErrInvalid
	}
	return sql.NullFloat64{Float64: value, Valid: true}, nil
}

func processCategoryImage(r *http.Request, current string, remove bool) (string, bool, error) {
	if remove {
		current = ""
	}
	file, header, err := r.FormFile("image")
	if errors.Is(err, http.ErrMissingFile) {
		return current, false, nil
	}
	if err != nil {
		return current, false, err
	}
	defer file.Close()
	name, err := images.ProcessUploadWithPrefix(file, header, "category")
	if err != nil {
		return current, false, err
	}
	return config.UrlPrefixImages + name, true, nil
}

func categoryFromForm(r *http.Request, id int64) (categories.Category, error) {
	item := categories.Category{ID: id, Name: r.FormValue("name"), Description: r.FormValue("description")}
	if parent := strings.TrimSpace(r.FormValue("parent_id")); parent != "" {
		parentID, err := strconv.ParseInt(parent, 10, 64)
		if err != nil || parentID <= 0 {
			return categories.Category{}, errors.Join(categories.ErrInvalid, err)
		}
		item.ParentID = sql.NullInt64{Int64: parentID, Valid: true}
	}
	return item, nil
}

func categoryError(h *Handler, w http.ResponseWriter, r *http.Request, err error, message string) {
	switch {
	case errors.Is(err, categories.ErrNotFound):
		http.Error(w, "Category not found", http.StatusNotFound)
	case errors.Is(err, categories.ErrInvalid), errors.Is(err, categories.ErrCategoryCycle), errors.Is(err, categories.ErrInvalidValue), errors.Is(err, categories.ErrAttributeScope):
		h.UIError.Respond(w, r, err, err.Error(), http.StatusBadRequest)
	default:
		h.UIError.Respond(w, r, err, message, http.StatusInternalServerError)
	}
}
