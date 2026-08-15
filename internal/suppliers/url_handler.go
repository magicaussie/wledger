package suppliers

import (
	"fmt"
	"net/url"
	"strings"
)

// URLParseResult holds the result of parsing a supplier URL.
type URLParseResult struct {
	ProviderKey string
	ProviderID  string
	ProviderURL string
}

// ParseSupplierURL attempts to extract a provider and part ID from a URL.
// It checks all registered URLHandlerProvider implementations.
func ParseSupplierURL(rawURL string) (*URLParseResult, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	domain := strings.ToLower(parsed.Hostname())
	if domain == "" {
		return nil, fmt.Errorf("no hostname found in URL")
	}

	// Check with www. prefix stripped
	domain = strings.TrimPrefix(domain, "www.")

	uhp, err := GetByDomain(domain)
	if err != nil {
		return nil, fmt.Errorf("no provider handles URL from domain %s: %w", domain, err)
	}

	partID, ok := uhp.ExtractPartIDFromURL(rawURL)
	if !ok {
		return nil, fmt.Errorf("provider %s could not extract part ID from URL: %s",
			uhp.GetProviderInfo().Key, rawURL)
	}

	return &URLParseResult{
		ProviderKey: uhp.GetProviderInfo().Key,
		ProviderID:  partID,
		ProviderURL: rawURL,
	}, nil
}

// NormalizeURL cleans up a supplier URL for consistent storage.
func NormalizeURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}

	// Force HTTPS
	if parsed.Scheme == "http" {
		parsed.Scheme = "https"
	}

	// Strip tracking params
	q := parsed.Query()
	trackingParams := []string{"utm_source", "utm_medium", "utm_campaign", "utm_content", "utm_term", "ref"}
	for _, p := range trackingParams {
		q.Del(p)
	}
	parsed.RawQuery = q.Encode()

	// Strip trailing slash
	path := strings.TrimRight(parsed.Path, "/")
	parsed.Path = path

	return parsed.String()
}
