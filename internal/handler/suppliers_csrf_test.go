package handler

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alexedwards/scs/v2"
)

func TestRequestSchemeDoesNotTrustForwardedProto(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/suppliers/oauth/start", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	if scheme := requestScheme(req); scheme != "http" {
		t.Fatalf("scheme from untrusted forwarding header = %q, want transport scheme http", scheme)
	}

	req.TLS = &tls.ConnectionState{}
	req.Header.Set("X-Forwarded-Proto", "http")
	if scheme := requestScheme(req); scheme != "https" {
		t.Fatalf("scheme from TLS connection = %q, want https", scheme)
	}
}

func TestConsumeSupplierOAuthStateIsSessionBoundAndSingleUse(t *testing.T) {
	sm := scs.New()
	var seed bool
	var accepted bool
	h := sm.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seed {
			sm.Put(r.Context(), supplierOAuthStateSessionKey, "provider:random-state")
			return
		}
		accepted = consumeSupplierOAuthState(r.Context(), sm, r.URL.Query().Get("state"))
	}))

	seedRequest := httptest.NewRequest(http.MethodGet, "/seed", nil)
	seedResponse := httptest.NewRecorder()
	seed = true
	h.ServeHTTP(seedResponse, seedRequest)
	seed = false
	cookies := seedResponse.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("expected session cookie for the saved OAuth state")
	}

	requestWithState := func(state string) {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/callback?state="+state, nil)
		for _, cookie := range cookies {
			request.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
	}

	requestWithState("provider:attacker-state")
	if accepted {
		t.Fatal("state from a different OAuth flow was accepted")
	}
	requestWithState("provider:random-state")
	if !accepted {
		t.Fatal("matching state from the initiating session was rejected")
	}
	requestWithState("provider:random-state")
	if accepted {
		t.Fatal("consumed OAuth state was accepted a second time")
	}

	if consumeSupplierOAuthState(t.Context(), nil, "provider:random-state") {
		t.Fatal("OAuth state validated without a session manager")
	}
}
