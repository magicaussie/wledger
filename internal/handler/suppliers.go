package handler

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"time"

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
	h.Logger.Info("supplier_search", "keyword", keyword, "results_count", len(results), "failures", len(diagnostics))
	for _, d := range diagnostics {
		h.Logger.Warn("supplier_search_failure", "provider", d.ProviderKey, "provider_name", d.ProviderName, "keyword", keyword, "error", d.Error)
	}
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

// GET /suppliers/image?url=<encoded>&provider=<key>
func (h *Handler) HandleSupplierImageProxy(w http.ResponseWriter, r *http.Request) {
	imageURL := r.URL.Query().Get("url")

	if imageURL == "" {
		http.Error(w, "Missing url parameter", http.StatusBadRequest)
		return
	}

	// Validate URL
	parsedURL, err := url.Parse(imageURL)
	if err != nil || parsedURL.Scheme != "https" {
		http.Error(w, "Invalid URL", http.StatusBadRequest)
		return
	}

	// Only allow known supplier domains for security
	allowedDomains := map[string]bool{
		"spotlightstores.com":                             true,
		"www.spotlightstores.com":                         true,
		"officeworks.com.au":                              true,
		"www.officeworks.com.au":                          true,
		"images-officeworks-com-australia.netdna-ssl.com": true,
		"s3-ap-southeast-2.amazonaws.com":                 true,
		"amazon.com.au":                                   true,
		"www.amazon.com.au":                               true,
		"bunnings.com.au":                                 true,
		"www.bunnings.com.au":                             true,
		"altronics.com.au":                                true,
		"www.altronics.com.au":                            true,
		"core-electronics.com.au":                         true,
		"www.core-electronics.com.au":                     true,
		"littlebirdelectronics.com.au":                    true,
		"www.littlebirdelectronics.com.au":                true,
		"supercheapauto.com.au":                           true,
		"www.supercheapauto.com.au":                       true,
		"autobarn.com.au":                                 true,
		"www.autobarn.com.au":                             true,
		"medias.autobarn.com.au":                          true,
"media.rs-online.com":              true,
		"docs.rs-online.com":               true,
		"ae-pic-a1.aliexpress-media.com":   true,
		"ae01.alicdn.com":                  true,
		"img.alicdn.com":                   true,
		"ae.alicdn.com":                    true,
	}
	if !allowedDomains[parsedURL.Host] {
		http.Error(w, "Domain not allowed", http.StatusBadRequest)
		return
	}

	// For Spotlight images (Waf-protected), use Selenium helper to fetch
	if parsedURL.Host == "spotlightstores.com" || parsedURL.Host == "www.spotlightstores.com" {
		imageURL := r.URL.Query().Get("url")
		h.Logger.Debug("image proxy: fetching spotlight image via selenium", "url", imageURL)
		cmd := exec.CommandContext(r.Context(), "python3", "/wledger/scripts/spotlight_helper.py", "image", imageURL)
		cmd.Dir = "/wledger"
		output, err := cmd.Output()
		if err != nil {
			h.Logger.Error("image proxy: helper failed", "error", err)
			http.Error(w, "Failed to fetch image via helper", http.StatusBadGateway)
			return
		}
		var result struct {
			OK          bool   `json:"ok"`
			Error       string `json:"error"`
			ContentType string `json:"content_type"`
			Base64      string `json:"base64"`
		}
		if err := json.Unmarshal(output, &result); err != nil || !result.OK {
			h.Logger.Error("image proxy: failed to parse image data", "output", string(output[:minInt(len(output), 500)]))
			http.Error(w, "Failed to parse image data", http.StatusBadGateway)
			return
		}
		imgData, err := base64.StdEncoding.DecodeString(result.Base64)
		if err != nil {
			h.Logger.Error("image proxy: failed to decode base64", "error", err)
			http.Error(w, "Failed to decode image", http.StatusInternalServerError)
			return
		}
		h.Logger.Debug("image proxy: successfully fetched image", "content_type", result.ContentType, "size", len(imgData))
		if result.ContentType == "" {
			result.ContentType = "image/jpeg"
		}
		w.Header().Set("Content-Type", result.ContentType)
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Write(imgData)
		return
	}

	// For other providers, use direct HTTP fetch
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", imageURL, nil)
	if err != nil {
		http.Error(w, "Failed to create request", http.StatusInternalServerError)
		return
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "image/webp,image/apng,image/*,*/*;q=0.8")

	client := &http.Client{Timeout: 25 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "Failed to fetch image", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		http.Error(w, "Image not found", resp.StatusCode)
		return
	}

	// Copy headers
	contentType := resp.Header.Get("Content-Type")
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	} else {
		w.Header().Set("Content-Type", "image/jpeg")
	}
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	// Stream image to response
	_, _ = io.Copy(w, io.LimitReader(resp.Body, 10*1024*1024)) // 10MB limit
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
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
