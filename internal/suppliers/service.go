package suppliers

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/tuxedocurly/wledger/internal/db"
)

// Service provides orchestration for supplier search, detail retrieval, and import.
type Service interface {
	Search(ctx context.Context, keyword string, providerKeys []string) ([]SearchResultDTO, []ProviderDiagnostic, error)
	GetDetails(ctx context.Context, providerKey, providerID string) (*PartDetailDTO, error)
	ImportFromProvider(ctx context.Context, req ImportRequest) (int64, error)
	ParseSupplierURL(ctx context.Context, rawURL string) (*URLParseResult, error)
	GetActiveProviders() []ProviderInfo
	GetAllProviders() []ProviderInfo
	SaveCredentials(ctx context.Context, providerKey, apiKey, apiSecret string) error
	LoadCredentials(ctx context.Context) error
	GetOAuthURL(ctx context.Context, providerKey, redirectURI, state string) (string, error)
	HandleOAuthCallback(ctx context.Context, state, code string) error
	GetRecentlyImported(ctx context.Context, limit int) ([]RecentlyImportedPart, error)
	GetPriceComparison(ctx context.Context, partID int64) ([]PriceComparisonRow, error)
	RecordPriceSnapshot(ctx context.Context, partID int64) error
	GetPriceHistory(ctx context.Context, partID int64) ([]PriceHistoryRow, error)
}

type ImportRequest struct {
	ProviderKey string
	ProviderID  string
	BinID       *int64
	Quantity    int
}

// ErrPartAlreadyImported is returned when a part with the same provider reference already exists.
type ErrPartAlreadyImported struct {
	PartID int64
}

func (e *ErrPartAlreadyImported) Error() string {
	return fmt.Sprintf("part already imported as part #%d", e.PartID)
}

// ProviderDiagnostic reports a provider that failed during a search so the UI
// can explain why no results appeared for it (e.g. a blocked bot-protection
// page) instead of silently omitting it.
type ProviderDiagnostic struct {
	ProviderKey  string `json:"provider_key"`
	ProviderName string `json:"provider_name"`
	Error        string `json:"error"`
}

type service struct {
	store   db.Store
	cache   *Cache
	logger  *slog.Logger
}

// NewService creates a new supplier service.
func NewService(store db.Store, cache *Cache, logger *slog.Logger) Service {
	return &service{
		store:  store,
		cache:  cache,
		logger: logger,
	}
}

func (s *service) getCacheTTL(ctx context.Context) time.Duration {
	settings, err := s.store.GetSettings(ctx)
	if err == nil && settings.SupplierCacheTtlHours.Int64 > 0 {
		return time.Duration(settings.SupplierCacheTtlHours.Int64) * time.Hour
	}
	return 96 * time.Hour // 4 days default
}

// searchTTLFor returns the effective search cache TTL for a provider,
// honouring a provider-specific override when present.
func (s *service) searchTTLFor(ctx context.Context, providerKey string) time.Duration {
	if p, err := Get(providerKey); err == nil {
		if ctp, ok := p.(CacheTTLProvider); ok {
			if ttl := ctp.SearchCacheTTL(); ttl > 0 {
				return ttl
			}
		}
	}
	return s.getCacheTTL(ctx)
}

// detailTTLFor returns the effective detail cache TTL for a provider,
// honouring a provider-specific override when present.
func (s *service) detailTTLFor(ctx context.Context, providerKey string) time.Duration {
	if p, err := Get(providerKey); err == nil {
		if ctp, ok := p.(CacheTTLProvider); ok {
			if ttl := ctp.DetailCacheTTL(); ttl > 0 {
				return ttl
			}
		}
	}
	return s.getCacheTTL(ctx)
}

func (s *service) GetActiveProviders() []ProviderInfo {
	providers := GetAllActive()
	infos := make([]ProviderInfo, len(providers))
	for i, p := range providers {
		infos[i] = p.GetProviderInfo()
	}
	return infos
}

func (s *service) GetAllProviders() []ProviderInfo {
	infos := GetAllInfos()
	// Mark which providers already have saved credentials
	creds, err := s.store.GetAllSupplierCredentials(context.Background())
	if err == nil {
		saved := make(map[string]bool)
		for _, c := range creds {
			if c.IsActive.Bool {
				saved[c.ProviderKey] = true
			}
		}
		for i := range infos {
			if saved[infos[i].Key] {
				infos[i].HasCredentials = true
			}
		}
	}
	return infos
}

func (s *service) Search(ctx context.Context, keyword string, providerKeys []string) ([]SearchResultDTO, []ProviderDiagnostic, error) {
	if len(providerKeys) == 0 {
		providerKeys = Keys()
	}

	keywords := splitKeywords(keyword)

	var allResults []SearchResultDTO
	var diagnostics []ProviderDiagnostic
	seen := make(map[string]bool)

	for _, key := range providerKeys {
		provider, err := Get(key)
		if err != nil {
			s.logger.Warn("skipping unknown provider", "key", key, "error", err)
			continue
		}

		if !provider.IsActive() {
			s.logger.Debug("skipping inactive provider", "key", key)
			continue
		}

		// Check cache for the full keyword
		if cached, err := s.cache.GetSearchResults(ctx, key, keyword); err == nil && len(cached) > 0 {
			s.logger.Debug("serving search from cache", "key", key, "count", len(cached))
			for _, r := range cached {
				cacheKey := r.ProviderKey + ":" + r.ProviderID
				if !seen[cacheKey] {
					seen[cacheKey] = true
					allResults = append(allResults, r)
				}
			}
			continue
		}

		// Search with each keyword, deduplicating by provider+ID
		for _, kw := range keywords {
			results, err := provider.SearchByKeyword(ctx, kw)
			if err != nil {
				s.logger.Error("provider search failed", "key", key, "keyword", kw, "error", err)
				diagnostics = append(diagnostics, ProviderDiagnostic{
					ProviderKey:  key,
					ProviderName: provider.GetProviderInfo().Name,
					Error:        err.Error(),
				})
				continue
			}

			for _, r := range results {
				cacheKey := r.ProviderKey + ":" + r.ProviderID
				if !seen[cacheKey] {
					seen[cacheKey] = true
					allResults = append(allResults, r)
				}
			}

			// Cache results for each individual keyword
			s.cache.SetSearchResults(ctx, key, kw, results, s.searchTTLFor(ctx, key))
		}

		// Cache the combined results too
		var combined []SearchResultDTO
		for _, r := range allResults {
			if r.ProviderKey == key {
				combined = append(combined, r)
			}
		}
		if len(combined) > 0 {
			s.cache.SetSearchResults(ctx, key, keyword, combined, s.searchTTLFor(ctx, key))
		}
	}

	return allResults, diagnostics, nil
}

// splitKeywords splits a search string into individual keywords.
// Supports comma separation and space separation.
func splitKeywords(keyword string) []string {
	// First try comma separation
	if strings.Contains(keyword, ",") {
		parts := strings.Split(keyword, ",")
		var result []string
		for _, p := range parts {
			if trimmed := strings.TrimSpace(p); trimmed != "" {
				result = append(result, trimmed)
			}
		}
		if len(result) > 0 {
			return result
		}
	}
	// Fall back to the whole keyword
	return []string{strings.TrimSpace(keyword)}
}

func (s *service) GetDetails(ctx context.Context, providerKey, providerID string) (*PartDetailDTO, error) {
	// Check cache first
	if cached, err := s.cache.GetPartDetail(ctx, providerKey, providerID); err == nil && cached != nil {
		s.logger.Debug("serving detail from cache", "key", providerKey, "id", providerID)
		return cached, nil
	}

	provider, err := Get(providerKey)
	if err != nil {
		return nil, fmt.Errorf("unknown provider: %w", err)
	}

	if !provider.IsActive() {
		return nil, fmt.Errorf("provider %s is not active", providerKey)
	}

	detail, err := provider.GetDetails(ctx, providerID)
	if err != nil {
		return nil, fmt.Errorf("failed to get details from %s: %w", providerKey, err)
	}

	// Cache with configured TTL
	s.cache.SetPartDetail(ctx, providerKey, providerID, detail, s.detailTTLFor(ctx, providerKey))

	return detail, nil
}

func (s *service) ImportFromProvider(ctx context.Context, req ImportRequest) (int64, error) {
	// Check if part already exists with this provider reference
	existing, err := s.store.FindExistingPartByProviderRef(ctx, db.FindExistingPartByProviderRefParams{
		ProviderKey: req.ProviderKey,
		ProviderID:  req.ProviderID,
	})
	if err == nil {
		return 0, &ErrPartAlreadyImported{PartID: existing.ID}
	}

	// Get full details from provider
	detail, err := s.GetDetails(ctx, req.ProviderKey, req.ProviderID)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch part details: %w", err)
	}

	// Create the part
	var partID int64
	err = s.store.ExecTx(ctx, func(q db.Querier) error {
		var err error
		partID, err = q.CreatePart(ctx, db.CreatePartParams{
			Name:         detail.Name,
			Description:  nullString(detail.Description),
			PartNumber:   nullString(detail.MPN),
			Manufacturer: nullString(detail.Manufacturer),
			Supplier:     nullString(supplierName(detail)),
			Footprint:    nullString(detail.Footprint),
		})
		if err != nil {
			return fmt.Errorf("failed to create part: %w", err)
		}

		// Create supplier reference
		srID, err := q.CreateSupplierReference(ctx, db.CreateSupplierReferenceParams{
			PartID:      partID,
			ProviderKey: req.ProviderKey,
			ProviderID:  req.ProviderID,
			ProviderUrl: nullString(detail.ProviderURL),
		})
		if err != nil {
			return fmt.Errorf("failed to create supplier reference: %w", err)
		}

		// Import pricing
		for _, vi := range detail.VendorInfos {
			for _, price := range vi.Prices {
				priceFloat, err := price.PriceAsFloat64()
				if err != nil {
					s.logger.Warn("failed to parse price", "price", price.Price, "error", err)
					continue
				}
				_, err = q.CreatePartPricing(ctx, db.CreatePartPricingParams{
					PartID:         partID,
					SupplierRefID: srID,
					MinQuantity:    int64(price.MinQuantity),
					Price:          priceFloat,
					Currency:       price.Currency,
					IncludesTax:    sql.NullBool{Bool: price.IncludesTax, Valid: true},
				})
				if err != nil {
					s.logger.Warn("failed to create pricing", "error", err)
				}
			}
		}

		// Import parameters
		for _, param := range detail.Parameters {
			_, err = q.CreatePartParameter(ctx, db.CreatePartParameterParams{
				PartID:     partID,
				Name:       param.Name,
				ValueText:  nullString(param.ValueText),
				ValueTyp:   nullFloat64(param.ValueTyp),
				ValueMin:   nullFloat64(param.ValueMin),
				ValueMax:   nullFloat64(param.ValueMax),
				Unit:       nullString(param.Unit),
				Symbol:     nullString(param.Symbol),
				ParamGroup: nullString(param.Group),
			})
			if err != nil {
				s.logger.Warn("failed to create parameter", "name", param.Name, "error", err)
			}
		}

		// Create stock assignment if requested
		if req.Quantity > 0 && req.BinID != nil {
			err = q.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{
				PartID:   partID,
				BinID:    sql.NullInt64{Int64: *req.BinID, Valid: true},
				Quantity: int64(req.Quantity),
			})
			if err != nil {
				return fmt.Errorf("failed to create stock assignment: %w", err)
			}
		}

		return nil
	})

	if err != nil {
		return 0, err
	}

	// Record initial price snapshot for history tracking
	if snapErr := s.RecordPriceSnapshot(ctx, partID); snapErr != nil {
		s.logger.Warn("failed to record initial price snapshot", "part_id", partID, "error", snapErr)
	}

	return partID, nil
}

func (s *service) ParseSupplierURL(ctx context.Context, rawURL string) (*URLParseResult, error) {
	return ParseSupplierURL(rawURL)
}

func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func nullFloat64(f float64) sql.NullFloat64 {
	if f == 0 {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: f, Valid: true}
}

func supplierName(detail *PartDetailDTO) string {
	if len(detail.VendorInfos) > 0 {
		return detail.VendorInfos[0].DistributorName
	}
	return ""
}

func (s *service) SaveCredentials(ctx context.Context, providerKey, apiKey, apiSecret string) error {
	err := s.store.UpsertSupplierCredential(ctx, db.UpsertSupplierCredentialParams{
		ProviderKey:    providerKey,
		ApiKey:         nullString(apiKey),
		ApiSecret:      nullString(apiSecret),
		AccessToken:    sql.NullString{},
		RefreshToken:   sql.NullString{},
		TokenExpiresAt: sql.NullTime{},
		IsActive:       sql.NullBool{Bool: true, Valid: true},
	})
	if err != nil {
		return fmt.Errorf("failed to save credentials for %s: %w", providerKey, err)
	}

	// Also update the in-memory provider
	provider, err := Get(providerKey)
	if err != nil {
		return nil
	}
	if akp, ok := provider.(APIKeyProvider); ok {
		akp.SetAPIKey(apiKey)
	}
	if apiSecret != "" {
		type secretSetter interface {
			SetSecret(string)
		}
		if ss, ok := provider.(secretSetter); ok {
			ss.SetSecret(apiSecret)
		}
	}

	return nil
}

type oauthCallbackSetter interface {
	OnTokenSaved(fn func())
}

func (s *service) LoadCredentials(ctx context.Context) error {
	creds, err := s.store.GetAllSupplierCredentials(ctx)
	if err != nil {
		return fmt.Errorf("failed to load supplier credentials: %w", err)
	}

	for _, cred := range creds {
		provider, err := Get(cred.ProviderKey)
		if err != nil {
			s.logger.Debug("skipping credential for unknown provider", "key", cred.ProviderKey)
			continue
		}

		if !cred.IsActive.Bool {
			s.logger.Debug("skipping inactive credentials", "key", cred.ProviderKey)
			continue
		}

		if akp, ok := provider.(APIKeyProvider); ok {
			if cred.ApiKey.Valid && cred.ApiKey.String != "" {
				akp.SetAPIKey(cred.ApiKey.String)
				s.logger.Info("loaded API credentials for provider", "key", cred.ProviderKey)
			}
		}

		// Handle providers that need both key and secret (like TME)
		if cred.ApiSecret.Valid && cred.ApiSecret.String != "" {
			type secretSetter interface {
				SetSecret(string)
			}
			if ss, ok := provider.(secretSetter); ok {
				ss.SetSecret(cred.ApiSecret.String)
			}
		}

// Load and refresh OAuth tokens
	if cred.AccessToken.Valid && cred.AccessToken.String != "" {
		type oauthSetter interface {
			SetCredentials(accessToken, refreshToken string, expiresAt interface{}) error
		}
		if os, ok := provider.(oauthSetter); ok {
			if err := os.SetCredentials(cred.AccessToken.String, cred.RefreshToken.String, cred.TokenExpiresAt.Time); err != nil {
				s.logger.Warn("failed to set OAuth credentials", "key", cred.ProviderKey, "error", err)
				continue
			}

			// Register a persistence callback so any runtime token refresh
			// (which rotates the refresh token) is written back to the DB.
			if oc, ok := provider.(oauthCallbackSetter); ok {
				oc.OnTokenSaved(func() {
					type credentialGetter interface {
						GetCredentials() (accessToken, refreshToken string, expiresAt interface{}, err error)
					}
					cg, ok := provider.(credentialGetter)
					if !ok {
						return
					}
					at, rt, exp, err := cg.GetCredentials()
					if err != nil {
						s.logger.Warn("failed to get credentials for persistence", "key", cred.ProviderKey, "error", err)
						return
					}
					expiresAtTime := sql.NullTime{}
					if t, ok := exp.(time.Time); ok {
						expiresAtTime = sql.NullTime{Time: t, Valid: true}
					}
					_ = s.store.UpsertSupplierCredential(ctx, db.UpsertSupplierCredentialParams{
						ProviderKey:    cred.ProviderKey,
						ApiKey:         cred.ApiKey,
						ApiSecret:      cred.ApiSecret,
						AccessToken:    sql.NullString{String: at, Valid: at != ""},
						RefreshToken:   sql.NullString{String: rt, Valid: rt != ""},
						TokenExpiresAt: expiresAtTime,
						IsActive:       sql.NullBool{Bool: true, Valid: true},
					})
					s.logger.Info("OAuth token persisted", "key", cred.ProviderKey)
				})
			}

			// Check if token needs refresh
			type tokenChecker interface {
				IsTokenExpired() bool
			}
			type tokenRefresher interface {
				RefreshAccessToken(ctx context.Context) error
			}
			if tc, ok := provider.(tokenChecker); ok {
				if tc.IsTokenExpired() {
					if rf, ok := provider.(tokenRefresher); ok {
						s.logger.Info("refreshing expired OAuth token", "key", cred.ProviderKey)
						if err := rf.RefreshAccessToken(ctx); err != nil {
							s.logger.Error("failed to refresh OAuth token", "key", cred.ProviderKey, "error", err)
							continue
						}

						// Save refreshed tokens
						type credentialGetter interface {
							GetCredentials() (accessToken, refreshToken string, expiresAt interface{}, err error)
						}
						if cg, ok := provider.(credentialGetter); ok {
							accessToken, refreshToken, expiresAt, err := cg.GetCredentials()
							if err == nil {
								expiresAtTime := sql.NullTime{}
								if t, ok := expiresAt.(time.Time); ok {
									expiresAtTime = sql.NullTime{Time: t, Valid: true}
								}
								_ = s.store.UpsertSupplierCredential(ctx, db.UpsertSupplierCredentialParams{
									ProviderKey:    cred.ProviderKey,
									ApiKey:         cred.ApiKey,
									ApiSecret:      cred.ApiSecret,
									AccessToken:    sql.NullString{String: accessToken, Valid: accessToken != ""},
									RefreshToken:   sql.NullString{String: refreshToken, Valid: refreshToken != ""},
									TokenExpiresAt: expiresAtTime,
									IsActive:       sql.NullBool{Bool: true, Valid: true},
								})
								s.logger.Info("OAuth token refreshed and saved", "key", cred.ProviderKey)
							}
						}
					}
				}
			}
		}
	}
	}

	return nil
}

func (s *service) GetOAuthURL(ctx context.Context, providerKey, redirectURI, state string) (string, error) {
	provider, err := Get(providerKey)
	if err != nil {
		return "", fmt.Errorf("unknown provider: %w", err)
	}

	type oauthProvider interface {
		GetAuthorizationURL(redirectURI, state string) string
	}
	if op, ok := provider.(oauthProvider); ok {
		return op.GetAuthorizationURL(redirectURI, state), nil
	}

	return "", fmt.Errorf("provider %s does not support OAuth", providerKey)
}

func (s *service) HandleOAuthCallback(ctx context.Context, state, code string) error {
	if state == "" {
		return fmt.Errorf("missing OAuth state parameter")
	}

	// Resolve provider from state (format: "providerKey:random")
	parts := strings.SplitN(state, ":", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid OAuth state format")
	}
	providerKey := parts[0]

	provider, err := Get(providerKey)
	if err != nil {
		return fmt.Errorf("unknown provider: %w", err)
	}

	type codeExchanger interface {
		ExchangeCode(ctx context.Context, code, redirectURI string) error
	}
	ce, ok := provider.(codeExchanger)
	if !ok {
		return fmt.Errorf("provider %s does not support code exchange", providerKey)
	}

	if err := ce.ExchangeCode(ctx, code, ""); err != nil {
		return fmt.Errorf("failed to exchange code for %s: %w", providerKey, err)
	}

	// Save tokens to database
	type credentialGetter interface {
		GetCredentials() (accessToken, refreshToken string, expiresAt interface{}, err error)
	}
	if cg, ok := provider.(credentialGetter); ok {
		accessToken, refreshToken, expiresAt, err := cg.GetCredentials()
		if err != nil {
			s.logger.Warn("failed to get credentials after exchange", "key", providerKey, "error", err)
			return nil
		}

		expiresAtTime := sql.NullTime{}
		if t, ok := expiresAt.(time.Time); ok {
			expiresAtTime = sql.NullTime{Time: t, Valid: true}
		}

		err = s.store.UpsertSupplierCredential(ctx, db.UpsertSupplierCredentialParams{
			ProviderKey:    providerKey,
			ApiKey:         nullString(""),
			ApiSecret:      nullString(""),
			AccessToken:    sql.NullString{String: accessToken, Valid: accessToken != ""},
			RefreshToken:   sql.NullString{String: refreshToken, Valid: refreshToken != ""},
			TokenExpiresAt: expiresAtTime,
			IsActive:       sql.NullBool{Bool: true, Valid: true},
		})
		if err != nil {
			return fmt.Errorf("failed to save OAuth credentials for %s: %w", providerKey, err)
		}

		s.logger.Info("OAuth credentials saved", "key", providerKey)
	}

	return nil
}

type RecentlyImportedPart struct {
	ID          int64
	Name        string
	MPN         string
	Supplier    string
	ProviderKey string
	ImportedAt  time.Time
}

type PriceComparisonRow struct {
	ProviderKey  string
	MinQuantity  int64
	Price        float64
	Currency     string
	IncludesTax  bool
}

func (s *service) GetRecentlyImported(ctx context.Context, limit int) ([]RecentlyImportedPart, error) {
	if limit <= 0 {
		limit = 10
	}

	rows, err := s.store.GetRecentlyImportedParts(ctx, int64(limit))
	if err != nil {
		return nil, fmt.Errorf("failed to fetch recently imported parts: %w", err)
	}

	result := make([]RecentlyImportedPart, len(rows))
	for i, row := range rows {
		mpn := ""
		if row.PartNumber.Valid {
			mpn = row.PartNumber.String
		}
		supplier := ""
		if row.Supplier.Valid {
			supplier = row.Supplier.String
		}
		result[i] = RecentlyImportedPart{
			ID:          row.ID,
			Name:        row.Name,
			MPN:         mpn,
			Supplier:    supplier,
			ProviderKey: row.ProviderKey,
			ImportedAt:  row.ImportedAt.Time,
		}
	}

	return result, nil
}

func (s *service) GetPriceComparison(ctx context.Context, partID int64) ([]PriceComparisonRow, error) {
	rows, err := s.store.GetPriceComparisonByPart(ctx, partID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch price comparison: %w", err)
	}

	result := make([]PriceComparisonRow, len(rows))
	for i, row := range rows {
		tax := false
		if row.IncludesTax.Valid {
			tax = row.IncludesTax.Bool
		}
		result[i] = PriceComparisonRow{
			ProviderKey: row.ProviderKey,
			MinQuantity: row.MinQuantity,
			Price:       row.Price,
			Currency:    row.Currency,
			IncludesTax: tax,
		}
	}

	return result, nil
}

type PriceHistoryRow struct {
	ID            int64
	PartID        int64
	SupplierRefID int64
	ProviderKey   string
	MinQuantity   int64
	Price         float64
	Currency      string
	IncludesTax   bool
	RecordedAt    time.Time
}

func (s *service) RecordPriceSnapshot(ctx context.Context, partID int64) error {
	refs, err := s.store.GetSupplierReferencesByPart(ctx, partID)
	if err != nil {
		return fmt.Errorf("failed to get supplier references: %w", err)
	}

	for _, ref := range refs {
		pricing, err := s.store.GetPartPricingBySupplierRef(ctx, ref.ID)
		if err != nil {
			s.logger.Warn("failed to get pricing for snapshot", "ref_id", ref.ID, "error", err)
			continue
		}

		for _, p := range pricing {
			_, err := s.store.CreatePriceHistory(ctx, db.CreatePriceHistoryParams{
				PartID:        partID,
				SupplierRefID: ref.ID,
				MinQuantity:   p.MinQuantity,
				Price:         p.Price,
				Currency:      p.Currency,
				IncludesTax:   p.IncludesTax,
			})
			if err != nil {
				s.logger.Warn("failed to create price history entry", "error", err)
			}
		}
	}

	return nil
}

func (s *service) GetPriceHistory(ctx context.Context, partID int64) ([]PriceHistoryRow, error) {
	rows, err := s.store.GetPriceHistoryByPart(ctx, partID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch price history: %w", err)
	}

	result := make([]PriceHistoryRow, len(rows))
	for i, row := range rows {
		tax := false
		if row.IncludesTax.Valid {
			tax = row.IncludesTax.Bool
		}
		recorded := time.Time{}
		if row.RecordedAt.Valid {
			recorded = row.RecordedAt.Time
		}
		result[i] = PriceHistoryRow{
			ID:            row.ID,
			PartID:        row.PartID,
			SupplierRefID: row.SupplierRefID,
			ProviderKey:   row.ProviderKey,
			MinQuantity:   row.MinQuantity,
			Price:         row.Price,
			Currency:      row.Currency,
			IncludesTax:   tax,
			RecordedAt:    recorded,
		}
	}

	return result, nil
}
