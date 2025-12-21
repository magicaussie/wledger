package backup

import (
	"time"

	"github.com/tuxedocurly/wledger/internal/db"
)

type Manifest struct {
	Version         string              `json:"version"`
	ExportedAt      time.Time           `json:"exported_at"`
	Settings        db.Setting          `json:"settings"`
	Users           []db.User           `json:"users"`
	Controllers     []db.Controller     `json:"controllers"`
	Bins            []db.Bin            `json:"bins"`
	Parts           []db.Part           `json:"parts"`
	PartAssignments []db.PartAssignment `json:"part_assignments"`
	PartLinks       []db.PartLink       `json:"part_links"`
	PartDocs        []db.PartDoc        `json:"part_docs"`
	PartAiPrompts   []db.PartAiPrompt   `json:"part_ai_prompts"`
	Tags            []db.Tag            `json:"tags"`
	PartTags        []db.PartTag        `json:"part_tags"`
	AuditLogs       []db.AuditLog       `json:"audit_logs"`
}
