package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alexedwards/scs/v2"
)

func TestCSRFTokenWithoutSessionManager(t *testing.T) {
	if _, err := CSRFToken(context.Background(), nil); !errors.Is(err, ErrCSRFUnavailable) {
		t.Fatalf("err = %v, want ErrCSRFUnavailable", err)
	}
	if ValidateCSRF(context.Background(), nil, "x") {
		t.Fatal("nil session manager must not validate a token")
	}
}

func TestCSRFTokenIsRandomAndStable(t *testing.T) {
	sm := scs.New()
	var tokens []string
	h := sm.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok, err := CSRFToken(r.Context(), sm)
		if err != nil {
			t.Fatalf("CSRFToken: %v", err)
		}
		again, _ := CSRFToken(r.Context(), sm)
		if again != tok {
			t.Errorf("token not stable within a session: %q != %q", again, tok)
		}
		tokens = append(tokens, tok)
	}))

	for i := 0; i < 2; i++ {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	}

	if len(tokens) != 2 {
		t.Fatalf("expected 2 tokens, got %d", len(tokens))
	}
	// 32 random bytes, base64url with no padding.
	if len(tokens[0]) != 43 {
		t.Errorf("token length = %d, want 43", len(tokens[0]))
	}
	if tokens[0] == tokens[1] {
		t.Error("tokens for different sessions are identical")
	}
}

// TestCSRFTokenPersistedBeforeConfirmation verifies that a token generated during
// a GET is actually persisted by the session middleware and can be validated on a
// later POST that carries the session cookie.
func TestCSRFTokenPersistedBeforeConfirmation(t *testing.T) {
	sm := scs.New()

	var generated string
	get := sm.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok, err := CSRFToken(r.Context(), sm)
		if err != nil {
			t.Fatalf("CSRFToken: %v", err)
		}
		generated = tok
	}))
	getRR := httptest.NewRecorder()
	get.ServeHTTP(getRR, httptest.NewRequest(http.MethodGet, "/", nil))
	cookies := getRR.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("session cookie was not set, so the CSRF token was not persisted")
	}

	var valid, wrong bool
	post := sm.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		valid = ValidateCSRF(r.Context(), sm, generated)
		wrong = ValidateCSRF(r.Context(), sm, generated+"x")
	}))
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	post.ServeHTTP(httptest.NewRecorder(), req)

	if !valid {
		t.Fatal("the persisted token did not validate in the same session")
	}
	if wrong {
		t.Fatal("a malformed token validated")
	}
}

func TestValidateCSRFRejections(t *testing.T) {
	sm := scs.New()
	h := sm.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok, _ := CSRFToken(r.Context(), sm)

		cases := map[string]bool{
			tok:       true,
			"":        false,
			"short":   false,
			tok + "x": false,
		}
		for submitted, want := range cases {
			if got := ValidateCSRF(r.Context(), sm, submitted); got != want {
				t.Errorf("ValidateCSRF(%q) = %v, want %v", submitted, got, want)
			}
		}
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
}

func TestRotateCSRFInvalidatesToken(t *testing.T) {
	sm := scs.New()

	var before, after string
	var oldValid, newValid bool
	h := sm.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		before, _ = CSRFToken(r.Context(), sm)
		RotateCSRF(r.Context(), sm)
		after, _ = CSRFToken(r.Context(), sm)
		oldValid = ValidateCSRF(r.Context(), sm, before)
		newValid = ValidateCSRF(r.Context(), sm, after)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	if before == after {
		t.Error("rotation did not change the token")
	}
	if oldValid {
		t.Error("the pre-rotation token still validates")
	}
	if !newValid {
		t.Error("the post-rotation token does not validate")
	}
}
