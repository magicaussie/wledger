package backup

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"

	"github.com/tuxedocurly/wledger/internal/db"
)

// Restore envelope framing. A restore archive carries exactly one manifest.
//
// Historical (legacy) backups use the restore_data.json entry holding a manifest
// whose version is 1.0; that is the only format this build can restore, and it
// must keep restoring unchanged. storage_data.json is reserved for a future,
// complete additive storage format that does not exist yet: its presence is
// rejected rather than being misread as a legacy manifest, and a mixture of the
// two is rejected as ambiguous instead of being resolved by ZIP entry order.
const (
	legacyManifestName             = "restore_data.json"
	currentStorageManifestName     = "storage_data.json"
	supportedLegacyManifestVersion = "1.0"
)

// BackupFormatCode is a machine-readable classification of an unsupported
// restore archive envelope. It lets callers and tests distinguish the failure
// classes without parsing the human-readable reason.
type BackupFormatCode string

const (
	// FormatMissingManifest: the archive carries no manifest entry.
	FormatMissingManifest BackupFormatCode = "missing_manifest"
	// FormatDuplicateLegacyManifest: the archive carries more than one legacy
	// restore_data.json entry.
	FormatDuplicateLegacyManifest BackupFormatCode = "duplicate_legacy_manifest"
	// FormatAmbiguousMixedManifests: the archive carries both a legacy and a
	// reserved current manifest entry.
	FormatAmbiguousMixedManifests BackupFormatCode = "ambiguous_mixed_manifests"
	// FormatUnsupportedCurrentManifest: the archive carries the reserved
	// storage_data.json entry, which no supported format produces yet.
	FormatUnsupportedCurrentManifest BackupFormatCode = "unsupported_current_manifest"
	// FormatUnsupportedManifestVersion: the manifest version is absent or is not
	// a supported legacy version.
	FormatUnsupportedManifestVersion BackupFormatCode = "unsupported_manifest_version"
	// FormatMalformedManifestJSON: the manifest JSON is malformed or has trailing
	// content after the manifest object.
	FormatMalformedManifestJSON BackupFormatCode = "malformed_manifest_json"
	// FormatDuplicateManifestField: the manifest object repeats a top-level
	// member, so which value applies would be ambiguous.
	FormatDuplicateManifestField BackupFormatCode = "duplicate_manifest_field"
	// FormatUnknownManifestField: the manifest object carries a top-level member
	// that no supported manifest defines.
	FormatUnknownManifestField BackupFormatCode = "unknown_manifest_field"
)

// BackupFormatError reports that a restore archive's envelope (its manifest
// framing and version) is not a supported backup format. It is returned before
// any database mutation, uploads/staging cleanup, audit write or filesystem
// staging, so a rejected restore leaves the installation untouched.
//
// It mirrors the package's RestoreCommittedError convention: an exported,
// type-asserted error carrying structured detail (the machine-readable Code) so
// callers can branch on the failure class rather than the message.
type BackupFormatError struct {
	Code   BackupFormatCode
	Reason string
}

func (e *BackupFormatError) Error() string {
	return "invalid backup format (" + string(e.Code) + "): " + e.Reason
}

// readRestoreEnvelope selects and decodes the single manifest a restore archive
// is allowed to carry, rejecting every unsupported or ambiguous framing. It
// inspects every ZIP entry rather than stopping at the first manifest-shaped
// name, so duplicate legacy manifests, a legacy/current mixture, and an
// unexpected current-format manifest are rejected instead of being silently
// resolved by entry order.
func readRestoreEnvelope(zr *zip.Reader) (Manifest, error) {
	var legacy, current []*zip.File
	for _, f := range zr.File {
		switch f.Name {
		case legacyManifestName:
			legacy = append(legacy, f)
		case currentStorageManifestName:
			current = append(current, f)
		}
	}

	if len(current) > 0 {
		if len(legacy) > 0 {
			return Manifest{}, &BackupFormatError{
				Code:   FormatAmbiguousMixedManifests,
				Reason: "archive contains both " + currentStorageManifestName + " and " + legacyManifestName,
			}
		}
		return Manifest{}, &BackupFormatError{
			Code:   FormatUnsupportedCurrentManifest,
			Reason: currentStorageManifestName + " is a reserved, not-yet-supported backup format",
		}
	}
	if len(legacy) > 1 {
		return Manifest{}, &BackupFormatError{
			Code:   FormatDuplicateLegacyManifest,
			Reason: "archive contains multiple " + legacyManifestName + " entries",
		}
	}
	if len(legacy) == 0 {
		return Manifest{}, &BackupFormatError{
			Code:   FormatMissingManifest,
			Reason: legacyManifestName + " is missing",
		}
	}

	return decodeLegacyManifest(legacy[0])
}

// decodeLegacyManifest decodes and validates the single legacy manifest. It
// rejects malformed JSON, trailing content after the manifest object, duplicate
// or unknown top-level members, and any manifest version other than the
// supported legacy version (including an absent version), so an unrecognized
// archive is never decoded as if it were 1.0.
func decodeLegacyManifest(f *zip.File) (Manifest, error) {
	rc, err := f.Open()
	if err != nil {
		return Manifest{}, malformedManifest("failed to open " + legacyManifestName + ": " + err.Error())
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		return Manifest{}, malformedManifest("failed to read " + legacyManifestName + ": " + err.Error())
	}

	// Boundary-decode the top-level object first so a repeated member or an
	// unknown future field is rejected rather than silently collapsed or ignored
	// by the struct decoder, which would let an ambiguous envelope be accepted.
	if err := scanManifestObject(data); err != nil {
		return Manifest{}, err
	}

	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, malformedManifest("failed to parse backup JSON: " + err.Error())
	}

	if manifest.Version != supportedLegacyManifestVersion {
		return Manifest{}, &BackupFormatError{
			Code:   FormatUnsupportedManifestVersion,
			Reason: fmt.Sprintf("unsupported backup manifest version %q (supported: %q)", manifest.Version, supportedLegacyManifestVersion),
		}
	}
	return manifest, nil
}

// scanManifestObject walks the top-level JSON object exactly once, rejecting a
// repeated member, a member no supported manifest defines, malformed JSON, and
// trailing content after the object. It reads each value as raw JSON, so nested
// values (including raw audit JSON) reach the struct decode byte-for-byte
// untouched.
func scanManifestObject(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return malformedManifest("failed to parse backup JSON: " + err.Error())
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return malformedManifest("backup manifest is not a JSON object")
	}

	allowed := manifestTopLevelFields()
	seen := make(map[string]struct{})
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return malformedManifest("failed to parse backup JSON: " + err.Error())
		}
		key, ok := keyTok.(string)
		if !ok {
			return malformedManifest("backup manifest contains a non-string member name")
		}
		if _, dup := seen[key]; dup {
			return &BackupFormatError{
				Code:   FormatDuplicateManifestField,
				Reason: fmt.Sprintf("backup manifest repeats top-level member %q", key),
			}
		}
		seen[key] = struct{}{}
		if _, ok := allowed[key]; !ok {
			return &BackupFormatError{
				Code:   FormatUnknownManifestField,
				Reason: fmt.Sprintf("backup manifest has unknown top-level member %q", key),
			}
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return malformedManifest("failed to parse backup JSON: " + err.Error())
		}
	}
	if _, err := dec.Token(); err != nil {
		return malformedManifest("failed to parse backup JSON: " + err.Error())
	}
	// A valid manifest is exactly one JSON value. Any trailing token or garbage
	// means the archive is not a well-formed legacy backup.
	if _, err := dec.Token(); err != io.EOF {
		if err == nil {
			return malformedManifest("unexpected content after the backup manifest object")
		}
		return malformedManifest("failed to parse trailing backup JSON: " + err.Error())
	}
	return nil
}

func malformedManifest(reason string) *BackupFormatError {
	return &BackupFormatError{Code: FormatMalformedManifestJSON, Reason: reason}
}

// manifestTopLevelFields returns the set of top-level JSON member names the
// current Manifest defines, derived from its struct tags so every supported
// legacy field (and any field later added to the struct) is accepted
// automatically while genuinely unknown members are rejected.
func manifestTopLevelFields() map[string]struct{} {
	t := reflect.TypeOf(Manifest{})
	fields := make(map[string]struct{}, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		name := strings.Split(tag, ",")[0]
		if name == "" {
			continue
		}
		fields[name] = struct{}{}
	}
	return fields
}

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
