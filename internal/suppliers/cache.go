package suppliers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/tuxedocurly/wledger/internal/db"
)

// Cache provides caching for supplier search results and part details.
type Cache struct {
	store  db.Store
	logger *slog.Logger
}

// NewCache creates a new supplier cache.
func NewCache(store db.Store, logger *slog.Logger) *Cache {
	return &Cache{
		store:  store,
		logger: logger,
	}
}

func searchCacheKey(providerKey, keyword string) string {
	return fmt.Sprintf("search:%s:%s", providerKey, keyword)
}

func detailCacheKey(providerKey, providerID string) string {
	return fmt.Sprintf("detail:%s:%s", providerKey, providerID)
}

// GetSearchResults returns cached search results if available and not expired.
func (c *Cache) GetSearchResults(ctx context.Context, providerKey, keyword string) ([]SearchResultDTO, error) {
	key := searchCacheKey(providerKey, keyword)
	raw, err := c.getRaw(ctx, key)
	if err != nil || raw == nil {
		return nil, err
	}
	var results []SearchResultDTO
	if err := json.Unmarshal(raw, &results); err != nil {
		c.logger.Error("failed to unmarshal cached search results", "key", key, "error", err)
		return nil, nil
	}
	return results, nil
}

// SetSearchResults caches search results with the given TTL.
func (c *Cache) SetSearchResults(ctx context.Context, providerKey, keyword string, results []SearchResultDTO, ttl time.Duration) error {
	key := searchCacheKey(providerKey, keyword)
	return c.set(ctx, key, providerKey, results, ttl)
}

// GetPartDetail returns cached part detail if available and not expired.
func (c *Cache) GetPartDetail(ctx context.Context, providerKey, providerID string) (*PartDetailDTO, error) {
	key := detailCacheKey(providerKey, providerID)
	raw, err := c.getRaw(ctx, key)
	if err != nil || raw == nil {
		return nil, err
	}
	var detail PartDetailDTO
	if err := json.Unmarshal(raw, &detail); err != nil {
		c.logger.Error("failed to unmarshal cached part detail", "key", key, "error", err)
		return nil, nil
	}
	return &detail, nil
}

// SetPartDetail caches a part detail with the given TTL.
func (c *Cache) SetPartDetail(ctx context.Context, providerKey, providerID string, detail *PartDetailDTO, ttl time.Duration) error {
	key := detailCacheKey(providerKey, providerID)
	return c.set(ctx, key, providerKey, detail, ttl)
}

// InvalidateProvider removes all cached entries for a provider.
func (c *Cache) InvalidateProvider(ctx context.Context, providerKey string) error {
	c.logger.Info("invalidating cache for provider", "provider", providerKey)
	return c.store.DeleteSupplierCacheByProvider(ctx, providerKey)
}

// PurgeExpired removes all expired cache entries.
func (c *Cache) PurgeExpired(ctx context.Context) error {
	c.logger.Info("purging expired supplier cache entries")
	return c.store.DeleteExpiredSupplierCache(ctx)
}

func (c *Cache) getRaw(ctx context.Context, key string) ([]byte, error) {
	row, err := c.store.GetSupplierCache(ctx, key)
	if err != nil {
		return nil, nil
	}

	if time.Now().After(row.ExpiresAt) {
		c.store.DeleteSupplierCache(ctx, key)
		return nil, nil
	}

	var data []byte
	switch d := row.Data.(type) {
	case []byte:
		data = d
	case string:
		data = []byte(d)
	default:
		data = []byte(fmt.Sprintf("%v", d))
	}

	return data, nil
}

func (c *Cache) set(ctx context.Context, key, providerKey string, data any, ttl time.Duration) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal data for cache: %w", err)
	}

	expiresAt := time.Now().Add(ttl)
	err = c.store.UpsertSupplierCache(ctx, db.UpsertSupplierCacheParams{
		CacheKey:    key,
		ProviderKey: providerKey,
		Data:        string(jsonData),
		ExpiresAt:   expiresAt,
	})
	if err != nil {
		return fmt.Errorf("failed to upsert cache: %w", err)
	}

	return nil
}
