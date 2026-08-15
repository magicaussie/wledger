package jaycar

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// bffBaseURL is the Storefront Cloud (Vue Storefront/commercetools) BFF that
// backs www.jaycar.com.au. Unlike the marketing pages on jaycar.com.au it is
// served without a DataDome challenge, so it is the reliable acquisition path
// for the Jaycar catalogue. All endpoints require an anonymous access token
// obtained from /bff/auth/accessToken.
const bffBaseURL = "https://jaycar-prod.australia-southeast1.gcp.storefrontcloud.io/api"

const (
	bffPathAccessToken = "/bff/auth/accessToken"
	bffPathSearch      = "/bff/products/list"
	bffPathPage        = "/bff/page"
)

// apiClient talks to the Jaycar Storefront Cloud BFF API. It obtains and
// caches the anonymous access token, applies conservative rate limiting and
// validates every response so a DataDome challenge can never be mistaken for
// real product data.
type apiClient struct {
	httpClient  *http.Client
	userAgent   string
	baseURL     string
	log         *slog.Logger
	minInterval time.Duration // minimum gap between API calls

	mu        sync.Mutex
	token     string
	tokenExp  time.Time
	lastReq   time.Time
}

// newAPIClient creates a client for the production BFF.
func newAPIClient() *apiClient {
	return &apiClient{
		httpClient:  &http.Client{Timeout: 30 * time.Second},
		userAgent:   "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0 Safari/537.36",
		baseURL:     bffBaseURL,
		log:         slog.Default(),
		minInterval: 300 * time.Millisecond,
	}
}

// newAPIClientForTest points the client at a test server.
func newAPIClientForTest(client *http.Client, baseURL string) *apiClient {
	return &apiClient{
		httpClient:  client,
		userAgent:   "test",
		baseURL:     baseURL,
		log:         slog.Default(),
		minInterval: 0,
	}
}

// jwtExpiry decodes the exp claim from a signed JWT without verifying it.
func jwtExpiry(token string) time.Time {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return time.Time{}
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(raw, &claims); err != nil || claims.Exp == 0 {
		return time.Time{}
	}
	return time.Unix(claims.Exp, 0)
}

// do performs a GET against the BFF API, attaching the Bearer token and
// enforcing the request rate limit. Non-200 responses are turned into errors,
// and a DataDome challenge is reported explicitly so callers never receive
// challenge HTML as if it were product data.
func (c *apiClient) do(ctx context.Context, path string, query url.Values) ([]byte, error) {
	status, body, err := c.doRaw(ctx, path, query)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		return body, nil
	}
	snippet := strings.TrimSpace(string(body))
	if isBotChallenge(snippet) {
		return nil, fmt.Errorf("[JAYCAR] BFF API blocked by bot protection (DataDome)")
	}
	if len(snippet) > 160 {
		snippet = snippet[:160]
	}
	return nil, fmt.Errorf("[JAYCAR] BFF API HTTP %d: %s", status, snippet)
}

// doRaw performs the GET and returns the raw status and body so redirects can
// be inspected by callers (e.g. /bff/page issues a 301 with the canonical slug
// in the body).
func (c *apiClient) doRaw(ctx context.Context, path string, query url.Values) (int, []byte, error) {
	c.mu.Lock()
	if c.minInterval > 0 {
		if wait := c.minInterval - time.Since(c.lastReq); wait > 0 {
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				c.mu.Unlock()
				return 0, nil, ctx.Err()
			}
		}
		c.lastReq = time.Now()
	}
	token := ""
	if path != bffPathAccessToken {
		var err error
		token, err = c.ensureTokenLocked(ctx)
		if err != nil {
			c.mu.Unlock()
			return 0, nil, err
		}
	}
	c.mu.Unlock()
	return c.request(ctx, path, query, token)
}

// request builds and performs a single GET, attaching the Bearer token when
// non-empty. It never locks c.mu so it can be used while the lock is held.
func (c *apiClient) request(ctx context.Context, path string, query url.Values, token string) (int, []byte, error) {
	u, err := url.Parse(c.baseURL + path)
	if err != nil {
		return 0, nil, fmt.Errorf("[JAYCAR] build API URL: %w", err)
	}
	if query != nil {
		u.RawQuery = query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return 0, nil, fmt.Errorf("[JAYCAR] build request: %w", err)
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("[JAYCAR] BFF API request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return 0, nil, fmt.Errorf("[JAYCAR] read BFF API response: %w", err)
	}
	return resp.StatusCode, body, nil
}

// ensureTokenLocked returns a cached, unexpired anonymous access token,
// fetching a new one when the cached copy is missing or about to expire.
// Callers hold c.mu; the refresh performs its own request without locking.
func (c *apiClient) ensureTokenLocked(ctx context.Context) (string, error) {
	if c.token != "" && time.Now().Before(c.tokenExp) {
		return c.token, nil
	}
	_, body, err := c.request(ctx, bffPathAccessToken, nil, "")
	if err != nil {
		return "", fmt.Errorf("[JAYCAR] fetch access token: %w", err)
	}
	var resp struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("[JAYCAR] decode access token: %w", err)
	}
	if resp.AccessToken == "" {
		return "", fmt.Errorf("[JAYCAR] empty access token response")
	}
	c.token = resp.AccessToken
	c.tokenExp = time.Now().Add(time.Hour)
	if exp := jwtExpiry(resp.AccessToken); !exp.IsZero() {
		if ttl := time.Until(exp.Add(-time.Minute)); ttl > time.Minute {
			c.tokenExp = time.Now().Add(ttl)
		}
	}
	return c.token, nil
}

// searchProducts searches the catalogue by keyword using the same ranked
// search the website uses; exact SKU matches come back first.
func (c *apiClient) searchProducts(ctx context.Context, keyword string) ([]jaycarListing, error) {
	body, err := c.do(ctx, bffPathSearch, url.Values{
		"q":     {keyword},
		"limit": {"12"},
	})
	if err != nil {
		return nil, err
	}
	var resp struct {
		Products []jaycarListing `json:"products"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("[JAYCAR] decode search response: %w", err)
	}
	return resp.Products, nil
}

// errProductNotFound signals that the BFF API answered authoritatively that a
// CAT.NO does not exist, so callers must not fall back to scraping.
var errProductNotFound = errors.New("[JAYCAR] no product found")

// getProductBySku fetches a single product listing by its catalogue number.
// It returns an error when the SKU does not exist so callers can distinguish
// "no such product" from "acquired nothing".
func (c *apiClient) getProductBySku(ctx context.Context, sku string) (*jaycarListing, error) {
	body, err := c.do(ctx, bffPathSearch, url.Values{"sku": {sku}})
	if err != nil {
		return nil, err
	}
	var resp struct {
		TotalProducts int             `json:"totalProducts"`
		Products      []jaycarListing `json:"products"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("[JAYCAR] decode SKU response: %w", err)
	}
	if len(resp.Products) == 0 {
		return nil, fmt.Errorf("%w for CAT.NO %s", errProductNotFound, sku)
	}
	return &resp.Products[0], nil
}

// getPage fetches the structured page data for a canonical product path (e.g.
// "/bbc-micro-bit-v2-go-development-board-bundle/p/XC4324"). A 301 with the
// canonical slug in its body is followed once, matching the website's redirect
// handling for short /p/<SKU> paths.
func (c *apiClient) getPage(ctx context.Context, path string) ([]byte, error) {
	status, body, err := c.doRaw(ctx, bffPathPage, url.Values{"slug": {path}})
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		return body, nil
	}
	if status == http.StatusMovedPermanently {
		var redir struct {
			Data struct {
				Redirect struct {
					URL string `json:"url"`
				} `json:"redirect"`
			} `json:"data"`
		}
		if json.Unmarshal(body, &redir) == nil && redir.Data.Redirect.URL != "" && redir.Data.Redirect.URL != path {
			c.log.Debug("[JAYCAR] following page redirect", "from", path, "to", redir.Data.Redirect.URL)
			return c.getPage(ctx, redir.Data.Redirect.URL)
		}
	}
	snippet := strings.TrimSpace(string(body))
	if isBotChallenge(snippet) {
		return nil, fmt.Errorf("[JAYCAR] BFF API blocked by bot protection (DataDome)")
	}
	return nil, fmt.Errorf("[JAYCAR] BFF API page HTTP %d: %s", status, truncate(snippet, 120))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
