package config

import "os"

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
