package digikey

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tuxedocurly/wledger/internal/suppliers"
)

const (
	baseURL        = "https://api.digikey.com"
	searchURL      = baseURL + "/products/v4/search/keyword"
	detailURL      = baseURL + "/products/v4/search/"
	authorizeURL   = baseURL + "/v1/oauth2/authorize"
	tokenURL       = baseURL + "/v1/oauth2/token"
	sandboxURL     = "https://sandbox-api.digikey.com" // for sandbox testing
)

// Provider implements the suppliers.Provider interface for DigiKey.
type Provider struct {
	httpClient    *http.Client
	clientID      string
	clientSecret  string
	accessToken   string
	refreshToken  string
	tokenExpiry   time.Time
	redirectURI   string
	onTokenSaved  func()
}

// OnTokenSaved registers a callback invoked after tokens change (exchange/refresh),
// allowing the service to persist the latest refresh token to the database.
func (p *Provider) OnTokenSaved(fn func()) {
	p.onTokenSaved = fn
}

func init() {
	suppliers.Register(NewProvider("", ""))
}

func NewProvider(clientID, clientSecret string) *Provider {
	return &Provider{
		httpClient:   &http.Client{Timeout: 15 * time.Second},
		clientID:     clientID,
		clientSecret: clientSecret,
	}
}

// SetAPIKey implements the suppliers.APIKeyProvider interface.
// For DigiKey, this sets the client ID.
func (p *Provider) SetAPIKey(apiKey string) {
	p.clientID = apiKey
}

// SetSecret sets the client secret.
func (p *Provider) SetSecret(secret string) {
	p.clientSecret = secret
}

// SetCredentials implements the suppliers.OAuthProvider interface.
func (p *Provider) SetCredentials(accessToken, refreshToken string, expiresAt interface{}) error {
	p.accessToken = accessToken
	p.refreshToken = refreshToken
	if t, ok := expiresAt.(time.Time); ok {
		p.tokenExpiry = t
	}
	return nil
}

// GetCredentials implements the suppliers.OAuthProvider interface.
func (p *Provider) GetCredentials() (accessToken, refreshToken string, expiresAt interface{}, err error) {
	return p.accessToken, p.refreshToken, p.tokenExpiry, nil
}

func (p *Provider) GetProviderInfo() suppliers.ProviderInfo {
	return suppliers.ProviderInfo{
		Key:          "digikey",
		Name:         "DigiKey",
		BaseURL:      "https://www.digikey.com",
		SupportsAuth: true,
		AuthType:     "oauth2",
	}
}

func (p *Provider) IsActive() bool {
	return p.clientID != ""
}

func (p *Provider) GetCapabilities() []suppliers.Capability {
	return []suppliers.Capability{
		suppliers.CapBasic,
		suppliers.CapPicture,
		suppliers.CapDatasheet,
		suppliers.CapPrice,
		suppliers.CapFootprint,
		suppliers.CapDistributor,
		suppliers.CapManufacturer,
	}
}

func (p *Provider) HandlesDomain(domain string) bool {
	return domain == "digikey.com" || domain == "www.digikey.com"
}

func (p *Provider) ExtractPartIDFromURL(rawURL string) (string, bool) {
	// URL format: https://www.digikey.com/en/products/detail/XXXX/XXXX/XXXX
	// or: https://www.digikey.com/product-detail/en/XXXX/XXXX/XXXX
	parts := strings.Split(rawURL, "/")
	for i, part := range parts {
		if (part == "detail" || part == "en") && i+1 < len(parts) {
			// DigiKey product IDs are typically numeric
			id := parts[i+1]
			if id != "" && id != "products" {
				return id, true
			}
		}
	}
	return "", false
}

func (p *Provider) ensureValidToken(ctx context.Context) error {
	if p.IsTokenExpired() {
		if p.refreshToken == "" {
			return fmt.Errorf("no refresh token available")
		}
		return p.RefreshAccessToken(ctx)
	}
	return nil
}

func (p *Provider) SearchByKeyword(ctx context.Context, keyword string) ([]suppliers.SearchResultDTO, error) {
	if p.clientID == "" {
		return nil, fmt.Errorf("DigiKey client ID not configured")
	}

	// Ensure we have a valid access token
	if err := p.ensureValidToken(ctx); err != nil {
		return nil, fmt.Errorf("DigiKey authorization not completed - click \"Connect with DigiKey\" to authorize: %w", err)
	}

	body := digikeySearchRequest{
		Keywords: keyword,
		RecordCount: 50,
		RecordStartPosition: 0,
		ExcludeMarketPlaceProducts: true,
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", searchURL, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	// DigiKey v4 API requires the client ID in this header.
	req.Header.Set("X-DIGIKEY-Client-Id", p.clientID)
	// DigiKey v4 search uses OAuth2 Bearer tokens.
	req.Header.Set("Authorization", "Bearer "+p.accessToken)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("DigiKey search request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("DigiKey API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var apiResp digikeySearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("failed to decode DigiKey search response: %w", err)
	}

	log.Printf("DigiKey search response: product_count=%d exact_matches=%d total=%d", len(apiResp.Products), len(apiResp.ExactMatches), apiResp.ProductsCount)

	return p.partsToSearchResults(apiResp.Products), nil
}

func (p *Provider) GetDetails(ctx context.Context, providerID string) (*suppliers.PartDetailDTO, error) {
	if p.clientID == "" {
		return nil, fmt.Errorf("DigiKey client ID not configured")
	}

	// Ensure we have a valid access token
	if err := p.ensureValidToken(ctx); err != nil {
		return nil, fmt.Errorf("DigiKey authorization not completed - click \"Connect with DigiKey\" to authorize: %w", err)
	}

	url := fmt.Sprintf("%s%s/productdetails", detailURL, providerID)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/json")
	// DigiKey v4 API requires X-DIGIKEY-Client-Id header
	req.Header.Set("X-DIGIKEY-Client-Id", p.clientID)
	// DigiKey v4 detail uses OAuth2 Bearer tokens.
	req.Header.Set("Authorization", "Bearer "+p.accessToken)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("DigiKey detail request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("DigiKey API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var apiResp digikeyDetailResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("failed to decode DigiKey detail response: %w", err)
	}

	if apiResp.Product == nil {
		return nil, fmt.Errorf("no product found with DigiKey PN %s", providerID)
	}

	return p.productToDetail(apiResp.Product), nil
}

// GetAuthorizationURL returns the URL for the OAuth2 authorization flow.
func (p *Provider) GetAuthorizationURL(redirectURI, state string) string {
	p.redirectURI = redirectURI
	params := url.Values{}
	params.Set("response_type", "code")
	params.Set("client_id", p.clientID)
	params.Set("redirect_uri", redirectURI)
	params.Set("state", state)
	return authorizeURL + "?" + params.Encode()
}

// ExchangeCode exchanges an authorization code for tokens.
func (p *Provider) ExchangeCode(ctx context.Context, code, redirectURI string) error {
	if redirectURI == "" {
		redirectURI = p.redirectURI
	}
	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("code", code)
	data.Set("redirect_uri", redirectURI)
	data.Set("client_id", p.clientID)
	data.Set("client_secret", p.clientSecret)

	req, err := http.NewRequestWithContext(ctx, "POST", tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("DigiKey token exchange failed: %w", err)
	}
	defer resp.Body.Close()

	var tokenResp digikeyTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return fmt.Errorf("failed to decode token response: %w", err)
	}

	p.accessToken = tokenResp.AccessToken
	p.refreshToken = tokenResp.RefreshToken
	p.tokenExpiry = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	if p.onTokenSaved != nil {
		p.onTokenSaved()
	}

	return nil
}

// RefreshAccessToken refreshes the OAuth2 token using the refresh token.
func (p *Provider) RefreshAccessToken(ctx context.Context) error {
	if p.refreshToken == "" {
		return fmt.Errorf("no refresh token available")
	}

	data := url.Values{}
	data.Set("grant_type", "refresh_token")
	data.Set("refresh_token", p.refreshToken)
	data.Set("client_id", p.clientID)
	data.Set("client_secret", p.clientSecret)

	req, err := http.NewRequestWithContext(ctx, "POST", tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("DigiKey token refresh failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("DigiKey refresh returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var tokenResp digikeyTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return fmt.Errorf("failed to decode token refresh response: %w", err)
	}

	p.accessToken = tokenResp.AccessToken
	if tokenResp.RefreshToken != "" {
		p.refreshToken = tokenResp.RefreshToken
	}
	p.tokenExpiry = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	if p.onTokenSaved != nil {
		p.onTokenSaved()
	}

	return nil
}

// IsTokenExpired returns true if the token is expired or about to expire (within 5 minutes).
func (p *Provider) IsTokenExpired() bool {
	return time.Now().Add(5 * time.Minute).After(p.tokenExpiry)
}

// digikeyProductNumber returns the primary DigiKey product number using the
// first variation, or falls back to the MPN.
func digikeyProductNumber(p digikeyProduct) string {
	if len(p.ProductVariations) > 0 && p.ProductVariations[0].DigiKeyProductNumber != "" {
		return p.ProductVariations[0].DigiKeyProductNumber
	}
	return p.ManufacturerProductNumber
}

func (p *Provider) partsToSearchResults(parts []digikeyProduct) []suppliers.SearchResultDTO {
	results := make([]suppliers.SearchResultDTO, 0, len(parts))
	for _, part := range parts {
		results = append(results, suppliers.SearchResultDTO{
			ProviderKey:         "digikey",
			ProviderID:          digikeyProductNumber(part),
			Name:                part.Description.ProductDescription,
			Description:         part.Description.DetailedDescription,
			Category:            part.Category.Name,
			Manufacturer:        part.Manufacturer.Name,
			MPN:                 part.ManufacturerProductNumber,
			PreviewImageURL:     part.PhotoURL,
			ManufacturingStatus: part.ProductStatus.Status,
			ProviderURL:         part.ProductURL,
			Footprint:           "",
		})
	}
	return results
}

func (p *Provider) productToDetail(product *digikeyProduct) *suppliers.PartDetailDTO {
	imgURL := product.PhotoURL
	digiKeyPN := digikeyProductNumber(*product)

	detail := &suppliers.PartDetailDTO{
		SearchResultDTO: suppliers.SearchResultDTO{
			ProviderKey:     "digikey",
			ProviderID:      digiKeyPN,
			Name:            product.Description.ProductDescription,
			Description:     product.Description.DetailedDescription,
			Category:        product.Category.Name,
			Manufacturer:    product.Manufacturer.Name,
			MPN:             product.ManufacturerProductNumber,
			PreviewImageURL: imgURL,
			ProviderURL:     product.ProductURL,
			Footprint:       "",
		},
	}

	if imgURL != "" {
		detail.Images = append(detail.Images, suppliers.FileDTO{
			URL:  imgURL,
			Name: digiKeyPN + ".jpg",
		})
	}

	// Datasheets
	if product.DatasheetURL != "" {
		detail.Datasheets = append(detail.Datasheets, suppliers.FileDTO{
			URL:  product.DatasheetURL,
			Name: "datasheet.pdf",
		})
	}

	// Pricing: v4 returns pricing per variation; use the first variation
	// that has pricing, preferring the smallest minimum order quantity.
	var bestVariation *digikeyVariation
	for i := range product.ProductVariations {
		variation := &product.ProductVariations[i]
		if len(variation.StandardPricing) == 0 {
			continue
		}
		if bestVariation == nil || variation.MinimumOrderQuantity < bestVariation.MinimumOrderQuantity {
			bestVariation = variation
		}
	}
	if bestVariation != nil {
		vi := suppliers.PurchaseInfoDTO{
			DistributorName: "DigiKey",
			OrderNumber:     bestVariation.DigiKeyProductNumber,
			ProductURL:      product.ProductURL,
		}
		for _, price := range bestVariation.StandardPricing {
			vi.Prices = append(vi.Prices, suppliers.PriceDTO{
				MinQuantity:          price.BreakQuantity,
				Price:                strconv.FormatFloat(price.UnitPrice, 'f', 6, 64),
				Currency:             "USD",
				IncludesTax:          false,
				PriceRelatedQuantity: 1,
			})
		}
		detail.VendorInfos = append(detail.VendorInfos, vi)
	}

	// Parameters
	if len(product.ProductVariations) > 0 && product.ProductVariations[0].PackageType.Name != "" {
		detail.Parameters = append(detail.Parameters, suppliers.ParameterDTO{
			Name:      "Package",
			ValueText: product.ProductVariations[0].PackageType.Name,
			Group:     "Physical",
		})
	}
	if product.Classifications.RohsStatus != "" {
		detail.Parameters = append(detail.Parameters, suppliers.ParameterDTO{
			Name:      "RoHS Status",
			ValueText: product.Classifications.RohsStatus,
			Group:     "Compliance",
		})
	}
	if product.ProductStatus.Status != "" {
		detail.Parameters = append(detail.Parameters, suppliers.ParameterDTO{
			Name:      "Product Status",
			ValueText: product.ProductStatus.Status,
			Group:     "General",
		})
	}
	for _, param := range product.Parameters {
		if param.ParameterText == "" {
			continue
		}
		detail.Parameters = append(detail.Parameters, suppliers.ParameterDTO{
			Name:      param.ParameterText,
			ValueText: param.ValueText,
			Group:     "Technical",
		})
	}

	return detail
}

// DigiKey API types

type digikeySearchRequest struct {
	Keywords                  string `json:"Keywords"`
	RecordCount               int    `json:"RecordCount"`
	RecordStartPosition       int    `json:"RecordStartPosition"`
	ExcludeMarketPlaceProducts bool  `json:"ExcludeMarketPlaceProducts"`
}

type digikeySearchResponse struct {
	Products      []digikeyProduct `json:"Products"`
	ProductsCount int              `json:"ProductsCount"`
	ExactMatches  []string         `json:"ExactMatches"`
}

type digikeyDetailResponse struct {
	Product *digikeyProduct `json:"Product"`
}

type digikeyProduct struct {
	Description               digikeyDescription  `json:"Description"`
	Manufacturer              digikeyRef          `json:"Manufacturer"`
	ManufacturerProductNumber string              `json:"ManufacturerProductNumber"`
	ProductURL                string              `json:"ProductUrl"`
	DatasheetURL              string              `json:"DatasheetUrl"`
	PhotoURL                  string              `json:"PhotoUrl"`
	ProductVariations         []digikeyVariation  `json:"ProductVariations"`
	QuantityAvailable         int                 `json:"QuantityAvailable"`
	ProductStatus             digikeyStatus       `json:"ProductStatus"`
	Parameters                []digikeyParameter  `json:"Parameters"`
	Category                  digikeyCategory     `json:"Category"`
	Series                    digikeyRef          `json:"Series"`
	Classifications           digikeyClassifications `json:"Classifications"`
}

type digikeyDescription struct {
	ProductDescription string `json:"ProductDescription"`
	DetailedDescription string `json:"DetailedDescription"`
}

type digikeyVariation struct {
	DigiKeyProductNumber string         `json:"DigiKeyProductNumber"`
	PackageType          digikeyRef     `json:"PackageType"`
	StandardPricing      []digikeyPrice `json:"StandardPricing"`
	MinimumOrderQuantity int            `json:"MinimumOrderQuantity"`
}

type digikeyRef struct {
	ID   int    `json:"ID"`
	Name string `json:"Name"`
}

type digikeyCategory struct {
	CategoryID int    `json:"CategoryId"`
	Name       string `json:"Name"`
}

type digikeyParameter struct {
	ParameterText string `json:"ParameterText"`
	ValueText     string `json:"ValueText"`
}

type digikeyClassifications struct {
	ReachStatus string `json:"ReachStatus"`
	RohsStatus  string `json:"RohsStatus"`
}

type digikeyStatus struct {
	ID     int    `json:"ID"`
	Status string `json:"Status"`
}

type digikeyPrice struct {
	BreakQuantity int     `json:"BreakQuantity"`
	Quantity      int     `json:"Quantity"`
	UnitPrice     float64 `json:"UnitPrice"`
	TotalPrice    float64 `json:"TotalPrice"`
}

type digikeyTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}
