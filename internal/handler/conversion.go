package handler

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/hardware"
	"github.com/tuxedocurly/wledger/internal/middleware"
	"github.com/tuxedocurly/wledger/web/pages"
)

// GET /hardware/conversion — read-only conversion preview. This never modifies
// data and never triggers a conversion.
func (h *Handler) HandleConversionPreview(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)
	if !user.CanConfigure() {
		h.UIError.Respond(w, r, nil, "Forbidden: Insufficient Permissions", http.StatusForbidden)
		return
	}

	report, err := hardware.PreflightConversion(r.Context(), h.Queries)
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to build conversion preview", http.StatusInternalServerError)
		return
	}

	token, err := middleware.CSRFToken(r.Context(), h.Session)
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to prepare conversion form", http.StatusInternalServerError)
		return
	}

	pages.ConversionPreview(user, newConversionView(report, token, r.URL.Query().Get("result"))).Render(r.Context(), w)
}

// newConversionView maps a conversion report to the pages render model.
func newConversionView(report hardware.ConversionReport, csrfToken, result string) pages.ConversionView {
	view := pages.ConversionView{
		Space:        report.Space,
		TotalDrawers: report.TotalDrawers,
		Convertible:  report.Convertible,
		Blocked:      report.Blocked,
		AffectedBins: report.AffectedBins,
		Fingerprint:  report.Fingerprint(),
		CSRF:         csrfToken,
		Result:       result,
	}
	for _, f := range report.Findings {
		fv := pages.ConversionFindingView{
			Name:        f.ContainerName,
			SegmentID:   f.SegmentID,
			LedStart:    f.LedStart,
			LedCount:    f.LedCount,
			Convertible: f.Class == hardware.ConversionConvertible,
			Reason:      f.Reason,
		}
		for _, b := range f.Bins {
			fv.Bins = append(fv.Bins, pages.ConversionBinView{BinID: b.BinID, From: b.FromIndex, To: b.ToIndex})
		}
		view.Findings = append(view.Findings, fv)
	}
	return view
}

// POST /hardware/conversion — administrator-confirmed conversion. Requires an
// authenticated administrator, a valid CSRF token, an explicit confirmation
// field and a fresh, matching preview fingerprint. The conversion itself
// revalidates the whole preflight inside its write transaction and is
// all-or-nothing.
func (h *Handler) HandleConversionConfirm(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)
	if !user.CanConfigure() {
		h.UIError.Respond(w, r, nil, "Forbidden: Insufficient Permissions", http.StatusForbidden)
		return
	}

	if err := r.ParseForm(); err != nil {
		h.UIError.Respond(w, r, err, "Invalid conversion request", http.StatusBadRequest)
		return
	}

	if !middleware.ValidateCSRF(r.Context(), h.Session, r.FormValue("csrf_token")) {
		h.UIError.Respond(w, r, nil, "Invalid or missing CSRF token", http.StatusForbidden)
		return
	}

	if r.FormValue("confirm") != "confirm" {
		h.UIError.Respond(w, r, nil, "Conversion requires explicit confirmation", http.StatusBadRequest)
		return
	}

	// A confirmation must be bound to a reviewed preflight. An absent fingerprint
	// would otherwise disable the stale-state guard, so it is required here even
	// though the engine can operate without one.
	fingerprint := r.FormValue("fingerprint")
	if fingerprint == "" {
		h.UIError.Respond(w, r, nil, "A fresh conversion preview is required before confirming", http.StatusBadRequest)
		return
	}

	result, err := hardware.ConvertToDrawerRelativeConfirmed(r.Context(), h.Queries, fingerprint, h.Logger)
	if err != nil {
		if errors.Is(err, hardware.ErrConversionUnresolved) {
			h.redirectConversion(w, r, "unresolved")
			return
		}
		h.UIError.Respond(w, r, err, "Conversion failed", http.StatusInternalServerError)
		return
	}

	h.redirectConversion(w, r, string(result.Outcome))
}

func (h *Handler) redirectConversion(w http.ResponseWriter, r *http.Request, result string) {
	http.Redirect(w, r, "/hardware/conversion?result="+url.QueryEscape(result), http.StatusSeeOther)
}
