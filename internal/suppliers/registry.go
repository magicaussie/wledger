package suppliers

import (
	"fmt"
	"sort"
	"sync"
)

var (
	globalRegistry = &Registry{
		providers: make(map[string]Provider),
	}
	once sync.Once
)

// Registry manages all registered supplier providers.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

// Register adds a provider to the global registry.
func Register(p Provider) {
	once.Do(func() {})
	globalRegistry.mu.Lock()
	defer globalRegistry.mu.Unlock()
	info := p.GetProviderInfo()
	globalRegistry.providers[info.Key] = p
}

// Get returns a provider by its key.
func Get(key string) (Provider, error) {
	globalRegistry.mu.RLock()
	defer globalRegistry.mu.RUnlock()
	p, ok := globalRegistry.providers[key]
	if !ok {
		return nil, fmt.Errorf("unknown provider: %s", key)
	}
	return p, nil
}

// GetAll returns all registered providers sorted by name.
func GetAll() []Provider {
	globalRegistry.mu.RLock()
	defer globalRegistry.mu.RUnlock()
	result := make([]Provider, 0, len(globalRegistry.providers))
	for _, p := range globalRegistry.providers {
		result = append(result, p)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].GetProviderInfo().Name < result[j].GetProviderInfo().Name
	})
	return result
}

// GetAllActive returns only active providers sorted by name.
func GetAllActive() []Provider {
	globalRegistry.mu.RLock()
	defer globalRegistry.mu.RUnlock()
	result := make([]Provider, 0)
	for _, p := range globalRegistry.providers {
		if p.IsActive() {
			result = append(result, p)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].GetProviderInfo().Name < result[j].GetProviderInfo().Name
	})
	return result
}

// GetAllInfos returns ProviderInfo for all registered providers sorted by name.
func GetAllInfos() []ProviderInfo {
	globalRegistry.mu.RLock()
	defer globalRegistry.mu.RUnlock()
	result := make([]ProviderInfo, 0, len(globalRegistry.providers))
	for _, p := range globalRegistry.providers {
		result = append(result, p.GetProviderInfo())
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// GetByDomain finds a provider that handles the given domain via URLHandlerProvider.
func GetByDomain(domain string) (URLHandlerProvider, error) {
	globalRegistry.mu.RLock()
	defer globalRegistry.mu.RUnlock()
	for _, p := range globalRegistry.providers {
		if uhp, ok := p.(URLHandlerProvider); ok {
			if uhp.HandlesDomain(domain) {
				return uhp, nil
			}
		}
	}
	return nil, fmt.Errorf("no provider handles domain: %s", domain)
}

// Keys returns all registered provider keys sorted alphabetically.
func Keys() []string {
	globalRegistry.mu.RLock()
	defer globalRegistry.mu.RUnlock()
	keys := make([]string, 0, len(globalRegistry.providers))
	for k := range globalRegistry.providers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
