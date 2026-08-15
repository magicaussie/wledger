package jaycar

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// PageFetcher acquires raw HTML for a Jaycar page. Implementations isolate the
// acquisition problem (DataDome bot protection, proxies, browser-supplied
// pages) completely from parsing. The provider only ever parses whatever a
// PageFetcher returns.
type PageFetcher interface {
	Fetch(ctx context.Context, pageURL string) ([]byte, error)
}

// HTTPFetcher fetches pages over plain HTTP with a browser-like user agent.
// It will typically be rejected by DataDome from a blocked IP, but remains
// the default fallback when no better acquisition path is configured.
type HTTPFetcher struct {
	client    *http.Client
	userAgent string
}

// NewHTTPFetcher creates an HTTPFetcher. A nil client uses a default
// 20s-timeout client; a nil userAgent uses a desktop Chrome UA.
func NewHTTPFetcher(client *http.Client, userAgent string) *HTTPFetcher {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	if userAgent == "" {
		userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0 Safari/537.36"
	}
	return &HTTPFetcher{client: client, userAgent: userAgent}
}

func (f *HTTPFetcher) Fetch(ctx context.Context, pageURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", pageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", f.userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-AU,en;q=0.9")

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		snippet := strings.TrimSpace(string(body))
		if isBotChallenge(snippet) {
			return nil, fmt.Errorf("jaycar blocked by bot protection (DataDome); use a real browser acquisition path (see suppliers docs)")
		}
		return nil, fmt.Errorf("jaycar HTTP %d: %s", resp.StatusCode, snippet)
	}

	return io.ReadAll(resp.Body)
}

// FileFetcher reads saved page HTML from a directory, for offline development
// and testing without contacting Jaycar at all. It resolves the page URL's
// SKU (e.g. "RR0548" from /p/RR0548) to <dir>/<SKU>.html.
type FileFetcher struct {
	Dir string
}

// NewFileFetcher creates a FileFetcher rooted at dir.
func NewFileFetcher(dir string) *FileFetcher {
	return &FileFetcher{Dir: dir}
}

func (f *FileFetcher) Fetch(_ context.Context, pageURL string) ([]byte, error) {
	name := skuFromURL(pageURL)
	if name == "" {
		name = "page"
	}
	path := filepath.Join(f.Dir, name+".html")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("jaycar file fetcher: %w", err)
	}
	return data, nil
}

// PageStore keeps product pages pushed in from a real browser (e.g. via a
// browser extension). Acquisition then happens entirely in the user's normal
// Chrome session, which Jaycar allows, rather than trying to defeat DataDome
// from the server.
type PageStore struct {
	mu    sync.RWMutex
	pages map[string]string
}

// NewPageStore creates an empty page store.
func NewPageStore() *PageStore {
	return &PageStore{pages: make(map[string]string)}
}

// Save stores a page's HTML keyed by its SKU so lookups are tolerant of the
// canonical product URL (with slug) vs. the short /p/<SKU> form.
func (s *PageStore) Save(pageURL, html string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sku := skuFromURL(pageURL); sku != "" {
		s.pages[sku] = html
	}
	s.pages[pageURL] = html
}

func (s *PageStore) load(pageURL string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if html, ok := s.pages[pageURL]; ok {
		return html, true
	}
	if sku := skuFromURL(pageURL); sku != "" {
		if html, ok := s.pages[sku]; ok {
			return html, true
		}
	}
	return "", false
}

// StoreFetcher is a PageFetcher backed by a PageStore. Pages are captured by
// the user's real browser and pushed in; the provider parses them as normal.
type StoreFetcher struct {
	Store *PageStore
}

// NewStoreFetcher creates a StoreFetcher around store.
func NewStoreFetcher(store *PageStore) *StoreFetcher {
	return &StoreFetcher{Store: store}
}

func (f *StoreFetcher) Fetch(_ context.Context, pageURL string) ([]byte, error) {
	html, ok := f.Store.load(pageURL)
	if !ok {
		return nil, fmt.Errorf("jaycar: no saved page for %s", pageURL)
	}
	return []byte(html), nil
}

// skuFromURL extracts the Jaycar SKU from the /p/<SKU> segment of a URL path.
func skuFromURL(pageURL string) string {
	u, err := url.Parse(pageURL)
	if err != nil {
		return ""
	}
	m := skuRE.FindStringSubmatch(u.Path)
	if m == nil {
		return ""
	}
	return m[1]
}

// isBotChallenge reports whether a short response snippet looks like a
// DataDome challenge page rather than a real Jaycar page.
func isBotChallenge(snippet string) bool {
	lower := strings.ToLower(snippet)
	for _, marker := range []string{
		"geo.captcha-delivery.com",
		"enable js and disable any ad blocker",
		"please enable javascript",
		"challenge-platform",
		"cdn-cgi/challenge",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}