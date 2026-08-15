package suppliers

import (
	"context"
	"time"
)

// Provider is the core interface that all supplier integrations must implement.
type Provider interface {
	GetProviderInfo() ProviderInfo
	IsActive() bool
	SearchByKeyword(ctx context.Context, keyword string) ([]SearchResultDTO, error)
	GetDetails(ctx context.Context, providerID string) (*PartDetailDTO, error)
	GetCapabilities() []Capability
}

// CacheTTLProvider is optionally implemented by providers that need shorter
// cache lifetimes than the global default (e.g. scrapers that must avoid
// hammering a site, or providers with fast-changing prices). Returning a zero
// TTL falls back to the configured default.
type CacheTTLProvider interface {
	SearchCacheTTL() time.Duration
	DetailCacheTTL() time.Duration
}

// APIKeyProvider is optionally implemented by providers that use a simple API key.
type APIKeyProvider interface {
	Provider
	SetAPIKey(apiKey string)
}

// OAuthProvider is optionally implemented by providers that use OAuth2.
type OAuthProvider interface {
	Provider
	SetCredentials(accessToken, refreshToken string, expiresAt interface{}) error
	GetCredentials() (accessToken, refreshToken string, expiresAt interface{}, err error)
}

// URLHandlerProvider is optionally implemented by providers that can extract
// part identifiers directly from supplier URLs.
type URLHandlerProvider interface {
	Provider
	ExtractPartIDFromURL(rawURL string) (string, bool)
	HandlesDomain(domain string) bool
}

// BatchProvider is optionally implemented by providers that support
// batch keyword searching for efficiency.
type BatchProvider interface {
	Provider
	SearchByKeywordsBatch(ctx context.Context, keywords []string) (map[string][]SearchResultDTO, error)
}
