package jaycar

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tuxedocurly/wledger/internal/suppliers"
)

// JaycarIndexEntry is a single record in the persistent catalogue cache. It is
// populated lazily from live search results so keyword searches still return
// real products when the BFF API is unreachable or rate limited.
type JaycarIndexEntry struct {
	SKU       string    `json:"sku"`
	Name      string    `json:"name"`
	Brand     string    `json:"brand"`
	Price     string    `json:"price"`
	URL       string    `json:"url"`
	ImageURL  string    `json:"image_url"`
	Available bool      `json:"available"`
	UpdatedAt time.Time `json:"updated_at"`
}

// catalogue is a concurrency-safe, best-effort persistent cache of search
// results. Writes never fail searches: a read-only or unwritable data dir
// simply means the cache is skipped.
type catalogue struct {
	mu      sync.Mutex
	path    string
	log     *slog.Logger
	entries map[string]JaycarIndexEntry
}

func newCatalogue(path string, log *slog.Logger) *catalogue {
	if path == "" {
		path = filepath.Join("data", "jaycar_catalogue.json")
	}
	c := &catalogue{path: path, log: log, entries: make(map[string]JaycarIndexEntry)}
	c.load()
	return c
}

func (c *catalogue) load() {
	data, err := os.ReadFile(c.path)
	if err != nil {
		return // first run or unwritable dir; cache starts empty
	}
	var entries []JaycarIndexEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		c.log.Warn("[JAYCAR] ignoring corrupt catalogue cache", "path", c.path, "error", err)
		return
	}
	for _, e := range entries {
		if e.SKU != "" {
			c.entries[e.SKU] = e
		}
	}
}

// merge adds fresh products to the cache and persists it.
func (c *catalogue) merge(products []jaycarListing) {
	if len(products) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now().UTC()
	for _, p := range products {
		if p.Sku == "" {
			continue
		}
		c.entries[p.Sku] = JaycarIndexEntry{
			SKU:       p.Sku,
			Name:      p.Title,
			Brand:     p.BrandName,
			Price:     p.FinalPrice.String(),
			URL:       listingURL(p),
			ImageURL:  p.Thumbnail.Src,
			Available: p.InStock,
			UpdatedAt: now,
		}
	}
	c.saveLocked()
}

// search ranks cached entries by how well they match keyword, using the same
// precedence as live search: exact SKU, SKU prefix, name contains all words,
// then partial matches.
func (c *catalogue) search(keyword string) []JaycarIndexEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	kw := strings.ToUpper(strings.TrimSpace(keyword))
	var hits []JaycarIndexEntry
	for _, e := range c.entries {
		if entryScore(e, kw) >= 0 {
			hits = append(hits, e)
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		return entryScore(hits[i], kw) < entryScore(hits[j], kw)
	})
	if len(hits) > 12 {
		hits = hits[:12]
	}
	return hits
}

func (c *catalogue) saveLocked() {
	data, err := json.MarshalIndent(c.sortedLocked(), "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		c.log.Debug("[JAYCAR] cannot create catalogue dir", "path", c.path, "error", err)
		return
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		c.log.Debug("[JAYCAR] cannot write catalogue cache", "path", c.path, "error", err)
		return
	}
	_ = os.Rename(tmp, c.path)
}

func (c *catalogue) sortedLocked() []JaycarIndexEntry {
	entries := make([]JaycarIndexEntry, 0, len(c.entries))
	for _, e := range c.entries {
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].SKU < entries[j].SKU })
	return entries
}

// catalogueToSearchResults maps cached catalogue entries onto the search
// result DTOs used by the rest of the application.
func catalogueToSearchResults(entries []JaycarIndexEntry) []suppliers.SearchResultDTO {
	results := make([]suppliers.SearchResultDTO, 0, len(entries))
	for _, e := range entries {
		name := e.Name
		if name == "" {
			name = e.SKU
		}
		results = append(results, suppliers.SearchResultDTO{
			ProviderKey:     "jaycar",
			ProviderID:      e.SKU,
			Name:            name,
			Manufacturer:    e.Brand,
			MPN:             e.SKU,
			PreviewImageURL: e.ImageURL,
			ProviderURL:     e.URL,
		})
	}
	return results
}

// entryScore returns a lower-is-better rank for a cached entry against an
// uppercase keyword, or -1 when the entry does not match at all. Name
// matching is case- and punctuation-insensitive, so "micro:bit", "micro bit"
// and "microbit" all match the micro:bit product.
func entryScore(e JaycarIndexEntry, kw string) int {
	if kw == "" {
		return -1
	}
	sku := strings.ToUpper(e.SKU)
	kwC := compact(kw)
	nameC := compact(e.Name)

	if sku == kw {
		return 0
	}
	if strings.HasPrefix(sku, kw) || strings.HasPrefix(kw, sku) {
		return 1
	}
	if nameC == kwC {
		return 2
	}
	if strings.Contains(nameC, kwC) {
		return 3
	}
	words := compactWords(kw)
	all := true
	for _, w := range words {
		if !strings.Contains(nameC, w) {
			all = false
			break
		}
	}
	if all {
		return 4
	}
	for _, w := range words {
		if strings.Contains(nameC, w) {
			return 5
		}
	}
	return -1
}

// compact lowercases a string and strips every non-alphanumeric character, so
// "micro:bit", "micro bit" and "microbit" all become "microbit".
func compact(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// compactWords splits a keyword on whitespace and compacts each word, keeping
// the original word boundaries for multi-word queries like "micro bit".
func compactWords(s string) []string {
	raw := strings.Fields(strings.ToLower(s))
	words := make([]string, 0, len(raw))
	for _, w := range raw {
		if c := compact(w); c != "" {
			words = append(words, c)
		}
	}
	return words
}

// rankListing orders live search results with the same precedence, applied on
// top of the API's own ranking to guarantee exact SKU matches surface first.
func rankListing(keyword string, products []jaycarListing) {
	kw := strings.ToUpper(strings.TrimSpace(keyword))
	sort.SliceStable(products, func(i, j int) bool {
		return listingScore(products[i], kw) < listingScore(products[j], kw)
	})
}

// listingScore is entryScore for live listing rows. Entries that do not match
// the keyword at all are pushed to the back rather than ahead of real matches.
func listingScore(p jaycarListing, kw string) int {
	if s := entryScore(JaycarIndexEntry{SKU: p.Sku, Name: p.Title}, kw); s >= 0 {
		return s
	}
	return 100
}
