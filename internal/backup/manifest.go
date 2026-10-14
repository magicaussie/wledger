package backup

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/tuxedocurly/wledger/internal/db"
)

// Restore envelope framing. A restore archive carries exactly one manifest.
//
// Historical (legacy) backups use the restore_data.json entry holding a manifest
// whose version is 1.0, and they keep restoring unchanged. Current backups use
// the storage_data.json entry holding a complete version 2.2 storage manifest
// that embeds the historical manifest plus the physical storage rows and every
// system flag. Exactly one of the two entries must be present: a mixture is
// rejected as ambiguous rather than resolved by ZIP entry order, a duplicate is
// rejected rather than silently resolved, and a missing manifest is rejected
// rather than read as an empty backup.
const (
	legacyManifestName               = "restore_data.json"
	currentStorageManifestName       = "storage_data.json"
	supportedLegacyManifestVersion   = "1.0"
	supportedPreviousManifestVersion = "2.0"
	supportedCategoryManifestVersion = "2.1"
	supportedCurrentManifestVersion  = "2.2"
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
	// FormatDuplicateCurrentManifest: the archive carries more than one current
	// storage_data.json entry.
	FormatDuplicateCurrentManifest BackupFormatCode = "duplicate_current_manifest"
	// FormatUnsupportedCurrentManifest is retained so callers that classify every
	// envelope code keep a stable set. A storage_data.json that cannot be read is
	// reported as its specific decode, version or graph failure instead.
	FormatUnsupportedCurrentManifest BackupFormatCode = "unsupported_current_manifest"
	// FormatInvalidPhysicalGraph: a current manifest declares physical storage
	// rows that are not internally consistent: a duplicate id, a dangling
	// reference, invalid geometry, a controller claimed by more than one cabinet,
	// a duplicate bin attachment or an incomplete flag key.
	FormatInvalidPhysicalGraph BackupFormatCode = "invalid_physical_graph"
	// FormatMissingManifestField: a manifest object omits a member its format
	// requires, so the archive is incomplete rather than empty.
	FormatMissingManifestField BackupFormatCode = "missing_manifest_field"
	// FormatInvalidManifestField: a manifest member is present but has the wrong
	// shape, for example JSON null where the format requires an object or an
	// array.
	FormatInvalidManifestField BackupFormatCode = "invalid_manifest_field"
	// FormatUnsupportedCoordinateSpace: a declared LED coordinate space (the
	// embedded legacy marker or the captured coordinate flag) is not a value this
	// build can interpret.
	FormatUnsupportedCoordinateSpace BackupFormatCode = "unsupported_coordinate_space"
	// FormatInconsistentCoordinateSpace: the embedded legacy coordinate marker and
	// the captured coordinate flag describe different spaces, so the archive's bin
	// index space is ambiguous.
	FormatInconsistentCoordinateSpace BackupFormatCode = "inconsistent_coordinate_space"
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

// restoreEnvelope is the manifest a restore archive carries, discriminated by
// format. Legacy always holds the complete historical manifest: the whole
// archive for a 1.0 backup, or the embedded legacy member of a current envelope.
// Current is non-nil only for a version 2.0, 2.1 or 2.2 archive.
type restoreEnvelope struct {
	Legacy  Manifest
	Current *StorageManifest
}

// readRestoreEnvelope selects and decodes the single manifest a restore archive
// is allowed to carry, rejecting every unsupported or ambiguous framing. It
// inspects every ZIP entry rather than stopping at the first manifest-shaped
// name, so duplicate manifests, a legacy/current mixture and a missing manifest
// are rejected instead of being silently resolved by entry order.
func readRestoreEnvelope(zr *zip.Reader) (restoreEnvelope, error) {
	var legacy, current []*zip.File
	for _, f := range zr.File {
		switch f.Name {
		case legacyManifestName:
			legacy = append(legacy, f)
		case currentStorageManifestName:
			current = append(current, f)
		}
	}

	if len(current) > 0 && len(legacy) > 0 {
		return restoreEnvelope{}, &BackupFormatError{
			Code:   FormatAmbiguousMixedManifests,
			Reason: "archive contains both " + currentStorageManifestName + " and " + legacyManifestName,
		}
	}
	if len(current) > 1 {
		return restoreEnvelope{}, &BackupFormatError{
			Code:   FormatDuplicateCurrentManifest,
			Reason: "archive contains multiple " + currentStorageManifestName + " entries",
		}
	}
	if len(current) == 1 {
		env, err := decodeCurrentStorageManifest(current[0])
		if err != nil {
			return restoreEnvelope{}, err
		}
		return restoreEnvelope{Legacy: env.Legacy, Current: &env}, nil
	}
	if len(legacy) > 1 {
		return restoreEnvelope{}, &BackupFormatError{
			Code:   FormatDuplicateLegacyManifest,
			Reason: "archive contains multiple " + legacyManifestName + " entries",
		}
	}
	if len(legacy) == 0 {
		return restoreEnvelope{}, &BackupFormatError{
			Code:   FormatMissingManifest,
			Reason: legacyManifestName + " is missing",
		}
	}

	manifest, err := decodeLegacyManifest(legacy[0])
	if err != nil {
		return restoreEnvelope{}, err
	}
	return restoreEnvelope{Legacy: manifest}, nil
}

// decodeLegacyManifest decodes and validates the single legacy manifest. It
// rejects malformed JSON, trailing content after the manifest object, duplicate
// or unknown top-level members, and any manifest version other than the
// supported legacy version (including an absent version), so an unrecognized
// archive is never decoded as if it were 1.0.
func decodeLegacyManifest(f *zip.File) (Manifest, error) {
	data, err := readManifestEntry(f)
	if err != nil {
		return Manifest{}, err
	}

	// Boundary-decode the top-level object first so a repeated member or an
	// unknown future field is rejected rather than silently collapsed or ignored
	// by the struct decoder, which would let an ambiguous envelope be accepted.
	if _, err := scanJSONObject(data, manifestTopLevelFields()); err != nil {
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

// readManifestEntry opens and reads one manifest ZIP entry. An entry that cannot
// be opened or read is reported as a malformed manifest, so an unreadable
// archive is rejected with the same classification as unparseable JSON.
func readManifestEntry(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, malformedManifest("failed to open " + f.Name + ": " + err.Error())
	}
	defer rc.Close()

	data, err := io.ReadAll(io.LimitReader(rc, maxRestoreManifestBytes+1))
	if err != nil {
		return nil, malformedManifest("failed to read " + f.Name + ": " + err.Error())
	}
	if len(data) > maxRestoreManifestBytes {
		return nil, malformedManifest(fmt.Sprintf("%s exceeds %d bytes", f.Name, maxRestoreManifestBytes))
	}
	return data, nil
}

// scanJSONObject walks one top-level JSON object exactly once, rejecting a
// repeated member, a member the allowed set does not define, malformed JSON, and
// trailing content after the object. It returns every member's raw JSON so a
// caller can apply the same boundary checks to a nested object (the embedded
// legacy manifest of a current envelope), and so nested values (including raw
// audit JSON) reach the struct decode byte-for-byte untouched.
func scanJSONObject(data []byte, allowed map[string]struct{}) (map[string]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return nil, malformedManifest("failed to parse backup JSON: " + err.Error())
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return nil, malformedManifest("backup manifest is not a JSON object")
	}

	members := make(map[string]json.RawMessage)
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, malformedManifest("failed to parse backup JSON: " + err.Error())
		}
		key, ok := keyTok.(string)
		if !ok {
			return nil, malformedManifest("backup manifest contains a non-string member name")
		}
		if _, dup := members[key]; dup {
			return nil, &BackupFormatError{
				Code:   FormatDuplicateManifestField,
				Reason: fmt.Sprintf("backup manifest repeats top-level member %q", key),
			}
		}
		if _, ok := allowed[key]; !ok {
			return nil, &BackupFormatError{
				Code:   FormatUnknownManifestField,
				Reason: fmt.Sprintf("backup manifest has unknown top-level member %q", key),
			}
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, malformedManifest("failed to parse backup JSON: " + err.Error())
		}
		members[key] = raw
	}
	if _, err := dec.Token(); err != nil {
		return nil, malformedManifest("failed to parse backup JSON: " + err.Error())
	}
	// A valid manifest is exactly one JSON value. Any trailing token or garbage
	// means the archive is not a well-formed backup.
	if _, err := dec.Token(); err != io.EOF {
		if err == nil {
			return nil, malformedManifest("unexpected content after the backup manifest object")
		}
		return nil, malformedManifest("failed to parse trailing backup JSON: " + err.Error())
	}
	return members, nil
}

func malformedManifest(reason string) *BackupFormatError {
	return &BackupFormatError{Code: FormatMalformedManifestJSON, Reason: reason}
}

// manifestTopLevelFields returns the set of top-level JSON member names the
// historical Manifest defines, derived from its struct tags so every supported
// legacy field (and any field later added to the struct) is accepted
// automatically while genuinely unknown members are rejected.
func manifestTopLevelFields() map[string]struct{} {
	return topLevelJSONFields(reflect.TypeOf(Manifest{}))
}

// manifestRequiredFields returns the member names the historical Manifest
// requires: every json-tagged field whose tag does not carry omitempty. Fields
// tagged omitempty (the coordinate marker and the later supplier sections) are
// optional, so an archive that predates them stays readable.
func manifestRequiredFields() map[string]struct{} {
	fields := requiredTopLevelJSONFields(reflect.TypeOf(Manifest{}))
	// Category and extended product data are additive versioned fields. Keep them
	// optional when decoding historical 1.0 and earlier current envelopes.
	for _, name := range []string{"categories", "part_categories", "category_attributes", "part_attribute_values", "part_details"} {
		delete(fields, name)
	}
	return fields
}

// topLevelJSONFields derives the accepted top-level JSON member names from a
// struct's json tags, so an envelope's allowed member set always matches the
// struct it decodes into.
func topLevelJSONFields(t reflect.Type) map[string]struct{} {
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

// requiredTopLevelJSONFields derives the member names a struct requires: every
// json-tagged field whose tag does not carry omitempty.
func requiredTopLevelJSONFields(t reflect.Type) map[string]struct{} {
	fields := make(map[string]struct{}, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		parts := strings.Split(t.Field(i).Tag.Get("json"), ",")
		if len(parts) == 0 || parts[0] == "" || parts[0] == "-" {
			continue
		}
		optional := false
		for _, option := range parts[1:] {
			if option == "omitempty" {
				optional = true
			}
		}
		if optional {
			continue
		}
		fields[parts[0]] = struct{}{}
	}
	return fields
}

// requireMembers rejects an object that omits a member its format requires. The
// missing names are reported in a stable order so the reason is deterministic.
func requireMembers(members map[string]json.RawMessage, required map[string]struct{}, where string) error {
	missing := make([]string, 0, len(required))
	for name := range required {
		if _, ok := members[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return &BackupFormatError{
		Code:   FormatMissingManifestField,
		Reason: where + " is missing required member(s) " + strings.Join(missing, ", "),
	}
}

// sortedFieldNames returns a field-name set in a stable order, so iterating a
// derived set produces deterministic validation order and messages.
func sortedFieldNames(fields map[string]struct{}) []string {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
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
	Categories          []db.Category           `json:"categories,omitempty"`
	PartCategories      []db.PartCategory       `json:"part_categories,omitempty"`
	CategoryAttributes  []db.CategoryAttribute  `json:"category_attributes,omitempty"`
	PartAttributeValues []db.PartAttributeValue `json:"part_attribute_values,omitempty"`
	PartDetails         []db.PartDetail         `json:"part_details,omitempty"`
	SupplierRefs        []db.SupplierReference  `json:"supplier_references,omitempty"`
	PartParameters      []db.PartParameter      `json:"part_parameters,omitempty"`
	PartPricing         []db.PartPricing        `json:"part_pricing,omitempty"`
	SupplierCredentials []db.SupplierCredential `json:"supplier_credentials,omitempty"`
	PriceHistory        []db.PriceHistory       `json:"price_history,omitempty"`
}

func (m Manifest) MarshalJSON() ([]byte, error) {
	type manifestAlias Manifest
	encoded, err := json.Marshal(manifestAlias(m))
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		return nil, err
	}
	if err := addOptionalList(fields, "categories", m.Categories); err != nil {
		return nil, err
	}
	if err := addOptionalList(fields, "part_categories", m.PartCategories); err != nil {
		return nil, err
	}
	if err := addOptionalList(fields, "category_attributes", m.CategoryAttributes); err != nil {
		return nil, err
	}
	if err := addOptionalList(fields, "part_attribute_values", m.PartAttributeValues); err != nil {
		return nil, err
	}
	if err := addOptionalList(fields, "part_details", m.PartDetails); err != nil {
		return nil, err
	}
	return json.Marshal(fields)
}

func addOptionalList[T any](fields map[string]json.RawMessage, name string, value []T) error {
	if value == nil {
		delete(fields, name)
		return nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	fields[name] = encoded
	return nil
}
