package suppliers

import "fmt"

type Capability int

const (
	CapBasic Capability = iota
	CapFootprint
	CapPicture
	CapDatasheet
	CapPrice
	CapDistributor
	CapManufacturer
)

func (c Capability) String() string {
	switch c {
	case CapBasic:
		return "basic"
	case CapFootprint:
		return "footprint"
	case CapPicture:
		return "picture"
	case CapDatasheet:
		return "datasheet"
	case CapPrice:
		return "price"
	case CapDistributor:
		return "distributor"
	case CapManufacturer:
		return "manufacturer"
	default:
		return "unknown"
	}
}

type ProviderInfo struct {
	Key            string
	Name           string
	BaseURL        string
	SupportsAuth   bool
	AuthType       string // "oauth2", "api_key", "hmac", "cookie", "none", "scraping"
	HasCredentials bool   // true if credentials are saved for this provider
}

type SearchResultDTO struct {
	ProviderKey         string `json:"provider_key"`
	ProviderID          string `json:"provider_id"`
	Name                string `json:"name"`
	Description         string `json:"description"`
	Category            string `json:"category"`
	Manufacturer        string `json:"manufacturer"`
	MPN                 string `json:"mpn"`
	PreviewImageURL     string `json:"preview_image_url"`
	ManufacturingStatus string `json:"manufacturing_status"`
	ProviderURL         string `json:"provider_url"`
	Footprint           string `json:"footprint"`
}

type PartDetailDTO struct {
	SearchResultDTO
	Notes                  string           `json:"notes"`
	Datasheets             []FileDTO        `json:"datasheets"`
	Images                 []FileDTO        `json:"images"`
	Parameters             []ParameterDTO   `json:"parameters"`
	VendorInfos            []PurchaseInfoDTO `json:"vendor_infos"`
	Mass                   float64          `json:"mass"`
	ManufacturerProductURL string           `json:"manufacturer_product_url"`
}

type PurchaseInfoDTO struct {
	DistributorName   string     `json:"distributor_name"`
	OrderNumber       string     `json:"order_number"`
	Prices            []PriceDTO `json:"prices"`
	ProductURL        string     `json:"product_url"`
	Price             string     `json:"price"`
	Currency          string     `json:"currency"`
	MinimumOrderQty   string     `json:"minimum_order_qty"`
	InStock           bool       `json:"in_stock"`
}

type PriceDTO struct {
	MinQuantity          int     `json:"min_quantity"`
	Price                string  `json:"price"`
	Currency             string  `json:"currency"`
	IncludesTax          bool    `json:"includes_tax"`
	PriceRelatedQuantity int     `json:"price_related_quantity"`
}

func (p PriceDTO) PriceAsFloat64() (float64, error) {
	var price float64
	_, err := fmt.Sscanf(p.Price, "%f", &price)
	if err != nil {
		return 0, fmt.Errorf("invalid price %q: %w", p.Price, err)
	}
	return price, nil
}

type FileDTO struct {
	URL  string `json:"url"`
	Name string `json:"name"`
}

type ParameterDTO struct {
	Name      string  `json:"name"`
	ValueText string  `json:"value_text"`
	ValueTyp  float64 `json:"value_typ"`
	ValueMin  float64 `json:"value_min"`
	ValueMax  float64 `json:"value_max"`
	Unit      string  `json:"unit"`
	Symbol    string  `json:"symbol"`
	Group     string  `json:"group"`
}

type ManufacturingStatus int

const (
	MfgStatusUnknown ManufacturingStatus = iota
	MfgStatusActive
	MfgStatusDiscontinued
	MfgStatusEOL
	MfgStatusNRND
	MfgStatusAnnounced
)

func ParseManufacturingStatus(s string) ManufacturingStatus {
	switch s {
	case "Active", "Production", "New", "Active/Production":
		return MfgStatusActive
	case "Obsolete", "Discontinued", "Invalid":
		return MfgStatusDiscontinued
	case "End of Life", "EOL", "Available While Stocks Last":
		return MfgStatusEOL
	case "Not Recommended for New Design", "NRND":
		return MfgStatusNRND
	case "New Product", "Announced":
		return MfgStatusAnnounced
	default:
		return MfgStatusUnknown
	}
}
