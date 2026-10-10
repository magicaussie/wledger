package backup

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/tuxedocurly/wledger/internal/db"
)

// AuditLogEntry is the backup representation of an audit log. old_value and
// new_value are kept as raw JSON (rather than a decoded interface{}) so the exact
// stored JSON is preserved across a backup/restore round trip, every JSON value
// type is supported, and malformed JSON is rejected when the manifest is decoded.
// The JSON field names match the historical manifest, so existing archives remain
// compatible.
type AuditLogEntry struct {
	ID         int64           `json:"id"`
	UserID     sql.NullInt64   `json:"user_id"`
	ActionType string          `json:"action_type"`
	EntityType string          `json:"entity_type"`
	EntityID   int64           `json:"entity_id"`
	Details    sql.NullString  `json:"details"`
	OldValue   json.RawMessage `json:"old_value,omitempty"`
	NewValue   json.RawMessage `json:"new_value,omitempty"`
	CreatedAt  sql.NullTime    `json:"created_at"`
}

type Manifest struct {
	Version    string    `json:"version"`
	ExportedAt time.Time `json:"exported_at"`
	// BinIndexSpace records the coordinate system of the bins' LED indices.
	// "segment" means segment-relative and "drawer" means relative to the owning
	// drawer's allocation. It is omitted by backups that predate this field, in
	// which case the coordinate system must not be assumed.
	BinIndexSpace       string                  `json:"bin_index_space,omitempty"`
	Settings            db.Setting              `json:"settings"`
	Users               []db.User               `json:"users"`
	Controllers         []db.Controller         `json:"controllers"`
	Containers          []db.Container          `json:"containers"`
	Walls               []db.Wall               `json:"walls"`
	WallCards           []db.WallCard           `json:"wall_cards"`
	Bins                []db.Bin                `json:"bins"`
	Parts               []db.Part               `json:"parts"`
	PartAssignments     []db.PartAssignment     `json:"part_assignments"`
	PartLinks           []db.PartLink           `json:"part_links"`
	PartDocs            []db.PartDoc            `json:"part_docs"`
	PartAiPrompts       []db.PartAiPrompt       `json:"part_ai_prompts"`
	Tags                []db.Tag                `json:"tags"`
	PartTags            []db.PartTag            `json:"part_tags"`
	AuditLogs           []AuditLogEntry         `json:"audit_logs"`
	SupplierRefs        []db.SupplierReference  `json:"supplier_references,omitempty"`
	PartParameters      []db.PartParameter      `json:"part_parameters,omitempty"`
	PartPricing         []db.PartPricing        `json:"part_pricing,omitempty"`
	SupplierCredentials []db.SupplierCredential `json:"supplier_credentials,omitempty"`
	PriceHistory        []db.PriceHistory       `json:"price_history,omitempty"`
}
