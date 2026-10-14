package config

import (
	"os"
	"strings"
)

// PublicURL returns the externally reachable base URL of this instance,
// used to build absolute callback URLs (e.g. for DigiKey OAuth).
// Falls back to the empty string; callers should derive the URL from the
// request when unset.
func PublicURL() string {
	if v := os.Getenv("WLEDGER_PUBLIC_URL"); v != "" {
		return v
	}
	return ""
}

// PhysicalWLEDOutputEnabled is the explicit server-side capability for WLED
// state writes. It is deliberately opt-in so fresh and upgraded installations
// remain inventory-only unless an operator configures isolated/supported output.
func PhysicalWLEDOutputEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("WLEDGER_PHYSICAL_OUTPUT_ENABLED"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

const (
	// System Paths (Relative to project root)
	DirData          = "./data"
	DirDatabase      = "./data/wledger.db"
	DirLogs          = "./app/logs"
	DirUploads       = "./app/uploads"
	DirUploadsImages = "./app/uploads/images"
	DirUploadsDocs   = "./app/uploads/docs"
	DirStatic        = "./web/static"
	DirLocales       = "./locales"

	// Web URL Prefixes
	UrlPrefixStatic  = "/static/"
	UrlPrefixUploads = "/uploads/"
	UrlPrefixImages  = "/uploads/images/"
	UrlPrefixDocs    = "/uploads/docs/"

	// Upload Memory Buffer Limits
	MaxUploadSizeParts  = 100 << 20 // 100 MB
	MaxUploadSizeImport = 100 << 20 // 100 MB
	MaxUploadSizeBackup = 100 << 20 // 100 MB

	// Session Keys
	SessionKeyUserID = "user_id"
	SessionKeyRole   = "role"
)
