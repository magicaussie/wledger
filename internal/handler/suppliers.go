package handler

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/config"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/suppliers"
	"github.com/tuxedocurly/wledger/web/pages"
)

// GET /suppliers
func (h *Handler) HandleSupplierSearch(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)
	providers := h.Suppliers.GetActiveProviders()
	pages.SupplierSearch(user, providers, nil, nil, "").Render(r.Context(), w)
}

// GET /suppliers/search?q=keyword&providers=mouser,digikey
func (h *Handler) HandleSupplierSearchAPI(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)
	keyword := strings.TrimSpace(r.URL.Query().Get("q"))
	providerFilter := r.URL.Query()["providers"]

	var providerKeys []string
	if len(providerFilter) > 0 {
		providerKeys = providerFilter
	}

	if keyword == "" {
		pages.SupplierResults(user, nil, nil, "").Render(r.Context(), w)
		return
	}

	results, diagnostics, _ := h.Suppliers.Search(r.Context(), keyword, providerKeys)
	h.Logger.Info("Search returned", "results_count", len(results), "failures", len(diagnostics))
	pages.SupplierResults(user, results, diagnostics, "").Render(r.Context(), w)
}

// GET /suppliers/{key}/{id}
func (h *Handler) HandleSupplierDetail(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)
	providerKey := chi.URLParam(r, "key")
	providerID := chi.URLParam(r, "id")

	detail, err := h.Suppliers.GetDetails(r.Context(), providerKey, providerID)
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to fetch part details", http.StatusInternalServerError)
		return
	}

	providers := h.Suppliers.GetActiveProviders()

	var bins []db.Bin
	if user.CanWrite() {
		bins, _ = h.Queries.GetAllBins(r.Context())
	}

	pages.SupplierDetail(user, detail, providers, bins).Render(r.Context(), w)
}

// POST /suppliers/import
func (h *Handler) HandleSupplierImport(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)
	if !user.CanWrite() {
		h.UIError.Respond(w, r, nil, "Unauthorized", http.StatusForbidden)
		return
	}

	providerKey := r.FormValue("provider_key")
	providerID := r.FormValue("provider_id")
	quantity := 0
	binID := int64(0)

	if v := r.FormValue("quantity"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			quantity = parsed
		}
	}
	if v := r.FormValue("bin_id"); v != "" {
		if parsed, err := strconv.ParseInt(v, 10, 64); err == nil && parsed > 0 {
			binID = parsed
		}
	}

	if providerKey == "" || providerID == "" {
		h.UIError.Respond(w, r, nil, "Missing provider key or ID", http.StatusBadRequest)
		return
	}

	req := suppliers.ImportRequest{
		ProviderKey: providerKey,
		ProviderID:  providerID,
	}
	if quantity > 0 && binID > 0 {
		req.BinID = &binID
		req.Quantity = quantity
	}

	partID, err := h.Suppliers.ImportFromProvider(r.Context(), req)
	if err != nil {
		var alreadyImported *suppliers.ErrPartAlreadyImported
		if errors.As(err, &alreadyImported) {
			http.Redirect(w, r, "/parts/"+strconv.FormatInt(alreadyImported.PartID, 10)+"?already_imported=1", http.StatusSeeOther)
			return
		}
		h.UIError.Respond(w, r, err, "Failed to import part", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/parts/"+strconv.FormatInt(partID, 10), http.StatusSeeOther)
}

// POST /suppliers/url/parse
func (h *Handler) HandleSupplierURLParse(w http.ResponseWriter, r *http.Request) {
	rawURL := r.FormValue("url")
	if rawURL == "" {
		http.Error(w, "No URL provided", http.StatusBadRequest)
		return
	}

	result, err := h.Suppliers.ParseSupplierURL(r.Context(), rawURL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	http.Redirect(w, r, "/suppliers/"+result.ProviderKey+"/"+result.ProviderID, http.StatusSeeOther)
}

// GET /suppliers/credentials
func (h *Handler) HandleSupplierCredentialsPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("X-WLEDger-Build", "2026-07-11-v2")
	user := auth.GetUserFromRequest(r)
	providers := h.Suppliers.GetAllProviders()
	pages.SupplierCredentials(user, providers, r.URL.Query().Get("saved")).Render(r.Context(), w)
}

// POST /suppliers/credentials
func (h *Handler) HandleSupplierCredentialsSave(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)
	if !user.IsAdmin() {
		h.UIError.Respond(w, r, nil, "Unauthorized", http.StatusForbidden)
		return
	}

	providerKey := r.FormValue("provider_key")
	apiKey := r.FormValue("api_key")
	apiSecret := r.FormValue("api_secret")

	if providerKey == "" {
		h.UIError.Respond(w, r, nil, "Missing provider key", http.StatusBadRequest)
		return
	}

	if err := h.Suppliers.SaveCredentials(r.Context(), providerKey, apiKey, apiSecret); err != nil {
		h.UIError.Respond(w, r, err, "Failed to save credentials", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/suppliers/credentials?saved="+providerKey, http.StatusSeeOther)
}

// GET /suppliers/oauth/callback?code=...&state=...
func (h *Handler) HandleSupplierOAuthCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")

	if code == "" {
		http.Error(w, "Missing authorization code", http.StatusBadRequest)
		return
	}

	if err := h.Suppliers.HandleOAuthCallback(r.Context(), state, code); err != nil {
		h.Logger.Error("OAuth callback failed", "error", err, "state", state)
		http.Error(w, "OAuth authentication failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/suppliers/credentials", http.StatusSeeOther)
}

// GET /suppliers/oauth/start?provider=digikey
func (h *Handler) HandleSupplierOAuthStart(w http.ResponseWriter, r *http.Request) {
	providerKey := r.URL.Query().Get("provider")
	if providerKey == "" {
		http.Error(w, "Missing provider", http.StatusBadRequest)
		return
	}

	stateBytes := make([]byte, 16)
	if _, err := rand.Read(stateBytes); err != nil {
		http.Error(w, "Failed to generate state", http.StatusInternalServerError)
		return
	}
	state := providerKey + ":" + hex.EncodeToString(stateBytes)

	scheme := "http"
	if r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	base := strings.TrimRight(config.PublicURL(), "/")
	if base == "" {
		base = scheme + "://" + r.Host
	}
	redirectURI := base + "/suppliers/oauth/callback"

	authURL, err := h.Suppliers.GetOAuthURL(r.Context(), providerKey, redirectURI, state)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	http.Redirect(w, r, authURL, http.StatusSeeOther)
}

// GET /suppliers/recent
func (h *Handler) HandleSupplierRecent(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)
	parts, err := h.Suppliers.GetRecentlyImported(r.Context(), 20)
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to fetch recent imports", http.StatusInternalServerError)
		return
	}
	pages.SupplierRecent(user, parts).Render(r.Context(), w)
}

// GET /suppliers/compare?part_id=123
func (h *Handler) HandleSupplierCompare(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)
	partIDStr := r.URL.Query().Get("part_id")
	if partIDStr == "" {
		http.Error(w, "Missing part_id", http.StatusBadRequest)
		return
	}
	partID, err := strconv.ParseInt(partIDStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid part_id", http.StatusBadRequest)
		return
	}

	pricing, err := h.Suppliers.GetPriceComparison(r.Context(), partID)
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to fetch price comparison", http.StatusInternalServerError)
		return
	}

	part, err := h.Parts.GetPart(r.Context(), partID)
	if err != nil {
		h.UIError.Respond(w, r, err, "Part not found", http.StatusNotFound)
		return
	}

	pages.SupplierCompare(user, part, pricing).Render(r.Context(), w)
}

// GET /suppliers/price-history?part_id=123
func (h *Handler) HandleSupplierPriceHistory(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)
	partIDStr := r.URL.Query().Get("part_id")
	if partIDStr == "" {
		http.Error(w, "Missing part_id", http.StatusBadRequest)
		return
	}
	partID, err := strconv.ParseInt(partIDStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid part_id", http.StatusBadRequest)
		return
	}

	history, err := h.Suppliers.GetPriceHistory(r.Context(), partID)
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to fetch price history", http.StatusInternalServerError)
		return
	}

	part, err := h.Parts.GetPart(r.Context(), partID)
	if err != nil {
		h.UIError.Respond(w, r, err, "Part not found", http.StatusNotFound)
		return
	}

	pages.SupplierPriceHistory(user, part, history).Render(r.Context(), w)
}

// POST /suppliers/price-snapshot
func (h *Handler) HandleSupplierRecordSnapshot(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)
	if !user.CanWrite() {
		h.UIError.Respond(w, r, nil, "Unauthorized", http.StatusForbidden)
		return
	}

	partIDStr := r.FormValue("part_id")
	partID, err := strconv.ParseInt(partIDStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid part_id", http.StatusBadRequest)
		return
	}

	if err := h.Suppliers.RecordPriceSnapshot(r.Context(), partID); err != nil {
		h.UIError.Respond(w, r, err, "Failed to record price snapshot", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/suppliers/price-history?part_id="+strconv.FormatInt(partID, 10), http.StatusSeeOther)
}
