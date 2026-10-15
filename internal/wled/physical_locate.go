package wled

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tuxedocurly/wledger/internal/cabinetpixels"
	"github.com/tuxedocurly/wledger/internal/db"
)

var (
	ErrLocateMappingUnverified = errors.New("physical drawer Locate is disabled until an administrator verifies the controller's linear WLED output mapping")
	ErrPhysicalBinMissing      = errors.New("inventory bin has no physical drawer association")
	ErrNoDrawerLEDAllocation   = errors.New("physical drawer has no saved LED ranges")
	ErrLocateSessionOwned      = errors.New("active Locate session belongs to another user")
	ErrLocateSessionActive     = db.ErrPhysicalLocateActive
	ErrLocateTopology          = errors.New("WLED segment or output topology is not safe for physical Locate")
	ErrLocateSessionStale      = errors.New("WLED configuration changed during the Locate session; cleanup was withheld to avoid writing to a different mapping")
	ErrHardwareBusy            = errors.New("controller is reserved by an active physical Locate session")
)

const locateBrightness = 128

const (
	physicalLocateMaximumControllers        = 5
	physicalLocateRetryPersistenceAllowance = 10 * time.Second
)

var (
	locateSessionTimeout            = 2 * time.Minute
	physicalLocateControllerTimeout = 5 * time.Second
	// Five targets get independent verification and cleanup budgets plus persistence headroom.
	physicalLocateCleanupRetryTimeout = time.Duration(physicalLocateMaximumControllers)*2*physicalLocateControllerTimeout + physicalLocateRetryPersistenceAllowance
	physicalLocateCleanupRetryDelay   = 15 * time.Second
	physicalLocateCoordinator         sync.Mutex
)

// ControllerRange carries a 1-based logical pixel span. Public session Ranges
// use cabinet-wide coordinates; durable controller-target ranges are local to the
// target controller and are recorded separately.
type ControllerRange struct {
	Start int64 `json:"start"`
	Count int64 `json:"count"`
}

type PhysicalLocateSession struct {
	ID                      string
	ControllerID            int64
	ControllerAddress       string
	ControllerPort          int64
	DrawerID                int64
	BinID                   int64
	CabinetName             string
	DrawerName              string
	OwnerID                 int64
	State                   string
	Ranges                  []ControllerRange
	Color                   string
	Brightness              int
	LastOperation           string
	LastOperationAt         time.Time
	LastSuccessfulOperation string
	LastSuccessfulAt        string
	LastError               string
	AffectedControllerIDs   []int64
}

type physicalControllerTarget struct {
	ControllerID      int64
	ControllerAddress string
	ControllerPort    int64
	Ranges            []ControllerRange // controller-local, 1-based
	Commands          []physicalPixelCommand
	Fingerprint       string
	MayHaveReceived   bool
	LastError         string
}

type resolvedControllerTarget struct {
	controller db.Controller
	ranges     []ControllerRange
}

type resolvedPhysicalLocation struct {
	controller db.Controller // primary target, retained for legacy session presentation
	drawer     db.Drawer
	cabinet    db.Cabinet
	ranges     []ControllerRange // cabinet-wide, 1-based
	targets    []resolvedControllerTarget
	color      string
}

type physicalPixelCommand struct {
	SegmentID int   `json:"segment_id"`
	Start     int64 `json:"start"`
	Stop      int64 `json:"stop"`
}

type physicalSessionState struct {
	PhysicalLocateSession
	fingerprint string // primary-target fingerprint for legacy session columns
	commands    []physicalPixelCommand
	targets     []physicalControllerTarget
	timer       *time.Timer
}

type PhysicalLocateService struct {
	store  db.Store
	client *Client

	loadMu   sync.Mutex
	loadErr  error
	mu       sync.Mutex
	retryMu  sync.Mutex
	locks    map[int64]*sync.Mutex
	sessions map[int64]*physicalSessionState
}

func NewPhysicalLocateService(store db.Store, client *Client) *PhysicalLocateService {
	client.SetOperationStore(store)
	s := &PhysicalLocateService{
		store:    store,
		client:   client,
		locks:    make(map[int64]*sync.Mutex),
		sessions: make(map[int64]*physicalSessionState),
	}
	_ = s.ensureLoaded(context.Background())
	return s
}

// LocateInventoryBin confirms that binID is one of this part's current stock
// locations, then resolves physical placement and persisted drawer allocations
// from one database snapshot before issuing any WLED request.
func (s *PhysicalLocateService) LocateInventoryBin(ctx context.Context, partID, binID, ownerID int64) (PhysicalLocateSession, error) {
	if err := requirePhysicalOutput(); err != nil {
		return PhysicalLocateSession{}, err
	}
	if partID <= 0 || binID <= 0 || ownerID <= 0 {
		return PhysicalLocateSession{}, errors.New("part, bin, and authenticated user are required")
	}
	var found bool
	if err := s.store.ExecTx(ctx, func(q db.Querier) error {
		_, err := q.GetAssignmentID(ctx, db.GetAssignmentIDParams{PartID: partID, BinID: sql.NullInt64{Int64: binID, Valid: true}})
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("bin %d is not a current inventory location for part %d", binID, partID)
		}
		if err != nil {
			return fmt.Errorf("resolve inventory location: %w", err)
		}
		found = true
		return nil
	}); err != nil {
		return PhysicalLocateSession{}, err
	}
	if !found {
		return PhysicalLocateSession{}, fmt.Errorf("bin %d is not a current inventory location for part %d", binID, partID)
	}
	return s.locate(ctx, 0, binID, ownerID)
}

// LocateDrawerBin starts (or switches) the application-owned Locate session for
// an attached bin in a physical drawer. Drawer IDs are never interpreted as
// legacy electrical container IDs.
func (s *PhysicalLocateService) LocateDrawerBin(ctx context.Context, drawerID, binID, ownerID int64) (PhysicalLocateSession, error) {
	if err := requirePhysicalOutput(); err != nil {
		return PhysicalLocateSession{}, err
	}
	if drawerID <= 0 || binID <= 0 || ownerID <= 0 {
		return PhysicalLocateSession{}, errors.New("drawer, bin, and authenticated user are required")
	}
	return s.locate(ctx, drawerID, binID, ownerID)
}

func (s *PhysicalLocateService) locate(ctx context.Context, expectedDrawerID, binID, ownerID int64) (PhysicalLocateSession, error) {
	if err := requirePhysicalOutput(); err != nil {
		return PhysicalLocateSession{}, err
	}
	physicalLocateCoordinator.Lock()
	defer physicalLocateCoordinator.Unlock()
	if err := s.ensureLoaded(ctx); err != nil {
		return PhysicalLocateSession{}, fmt.Errorf("load prior Locate cleanup state: %w", err)
	}
	if _, old := s.activeSession(); old != nil {
		if old.OwnerID != ownerID {
			return PhysicalLocateSession{}, ErrLocateSessionOwned
		}
		unlock := s.lockSessionTargets(old)
		current := s.getSession(old.ControllerID)
		if current != nil && current.ID == old.ID && isActivePhysicalSession(current.State) {
			err := s.restoreSession(ctx, current)
			unlock()
			if err != nil {
				return PhysicalLocateSession{}, fmt.Errorf("could not safely stop the previous drawer Locate: %w", err)
			}
		} else {
			unlock()
		}
	}

	var location resolvedPhysicalLocation
	var session *physicalSessionState
	controllersByID := make(map[int64]db.Controller)
	err := db.WithOperationClaim(ctx, s.store, func(q db.Querier) error {
		owned, err := db.HasPhysicalLocateOwnership(ctx, q)
		if err != nil {
			return fmt.Errorf("check durable Locate ownership before reservation: %w", err)
		}
		if owned {
			return ErrLocateSessionActive
		}
		location, err = s.resolveWithQuerier(ctx, q, expectedDrawerID, binID)
		if err != nil {
			return err
		}
		if len(location.targets) == 0 || len(location.ranges) == 0 {
			return ErrNoDrawerLEDAllocation
		}
		prepared := make([]physicalControllerTarget, 0, len(location.targets))
		for _, resolved := range location.targets {
			controller := resolved.controller
			if !controller.LocateMappingVerified || strings.TrimSpace(controller.LocateMappingFingerprint) == "" {
				return fmt.Errorf("controller #%d: %w", controller.ID, ErrLocateMappingUnverified)
			}
			info, cfg, state, mappingFingerprint, err := s.client.inspectPhysicalController(ctx, controller, controller.LedCount)
			if err != nil {
				return fmt.Errorf("validate controller #%d before multi-controller Locate: %w", controller.ID, err)
			}
			if mappingFingerprint != controller.LocateMappingFingerprint {
				return fmt.Errorf("controller #%d: %w", controller.ID, ErrLocateMappingUnverified)
			}
			commands, fingerprint, err := mapPhysicalRanges(resolved.ranges, controller, controller.LedCount, info, cfg, state, location.color, locateBrightness)
			if err != nil {
				return fmt.Errorf("map controller #%d pixels before activation: %w", controller.ID, err)
			}
			prepared = append(prepared, physicalControllerTarget{
				ControllerID: controller.ID, ControllerAddress: controller.IpAddress, ControllerPort: controllerPort(controller),
				Ranges: append([]ControllerRange(nil), resolved.ranges...), Commands: commands, Fingerprint: fingerprint,
			})
			controllersByID[controller.ID] = controller
		}
		id, err := newLocateSessionID()
		if err != nil {
			return fmt.Errorf("create Locate session: %w", err)
		}
		now := time.Now().UTC()
		primary := prepared[0]
		affected := make([]int64, 0, len(prepared))
		for _, target := range prepared {
			affected = append(affected, target.ControllerID)
		}
		session = &physicalSessionState{
			PhysicalLocateSession: PhysicalLocateSession{
				ID: id, ControllerID: primary.ControllerID, ControllerAddress: primary.ControllerAddress,
				ControllerPort: primary.ControllerPort, DrawerID: location.drawer.ID, BinID: binID,
				CabinetName: location.cabinet.Name, DrawerName: location.drawer.Label, OwnerID: ownerID,
				State: "activating", Ranges: append([]ControllerRange(nil), location.ranges...),
				AffectedControllerIDs: affected, Color: location.color, Brightness: locateBrightness,
				LastOperation: "multi-controller Locate requested", LastOperationAt: now,
			},
			fingerprint: primary.Fingerprint, commands: append([]physicalPixelCommand(nil), primary.Commands...), targets: prepared,
		}
		return s.insertSessionWithQuerier(ctx, q, session)
	})
	if err != nil {
		return PhysicalLocateSession{}, fmt.Errorf("reserve multi-controller Locate session: %w", err)
	}
	if session == nil {
		return PhysicalLocateSession{}, errors.New("Locate reservation completed without a session")
	}
	s.setSession(session.ControllerID, session)
	setSessionReservations(session, true)

	var activationErr error
	for targetIndex := range session.targets {
		target := &session.targets[targetIndex]
		target.MayHaveReceived = true
		if err := s.persistSession(ctx, session); err != nil {
			target.MayHaveReceived = false
			activationErr = fmt.Errorf("record controller #%d activation attempt before request: %w", target.ControllerID, err)
			break
		}
		controller := controllersByID[target.ControllerID]
		commandCtx, cancel := context.WithTimeout(ctx, physicalLocateControllerTimeout)
		_, commandErr := s.withPhysicalCommandClaim(commandCtx, session.ID, target, []string{"activating"}, controller, scaleRGB(colorRGB(location.color), locateBrightness), false)
		cancel()
		if commandErr != nil {
			target.LastError = commandErr.Error()
			activationErr = fmt.Errorf("controller #%d activation failed: %w", target.ControllerID, commandErr)
			break
		}
		session.LastOperation = fmt.Sprintf("drawer pixel ranges acknowledged by controller #%d", target.ControllerID)
		session.LastOperationAt = time.Now().UTC()
		session.LastSuccessfulOperation = session.LastOperation
		session.LastSuccessfulAt = session.LastOperationAt.Format(time.RFC3339Nano)
		if err := s.persistSession(ctx, session); err != nil {
			activationErr = fmt.Errorf("controller #%d activation was acknowledged but durable session update failed: %w", target.ControllerID, err)
		}
		if activationErr != nil {
			break
		}
	}
	if activationErr != nil {
		session.State = "partial"
		session.LastError = activationErr.Error()
		session.LastOperation = "multi-controller Locate partially failed; targeted cleanup requested"
		session.LastOperationAt = time.Now().UTC()
		_ = s.persistSession(context.Background(), session)
		cleanupErr := s.restoreSession(context.Background(), session)
		if cleanupErr != nil {
			session.State = "partial"
			session.LastError = fmt.Sprintf("%v; cleanup not confirmed on every affected controller: %v", activationErr, cleanupErr)
			_ = s.persistSession(context.Background(), session)
			return session.PhysicalLocateSession, fmt.Errorf("multi-controller Locate partially failed and cleanup remains pending: %w", cleanupErr)
		}
		session.State = "failed"
		session.LastError = activationErr.Error()
		session.LastOperation = "multi-controller Locate failed; targeted cleanup acknowledged"
		session.LastOperationAt = time.Now().UTC()
		_ = s.persistSession(context.Background(), session)
		return PhysicalLocateSession{}, activationErr
	}

	session.State = "active"
	session.LastError = ""
	session.LastOperation = "multi-controller drawer LED ranges activated"
	session.LastOperationAt = time.Now().UTC()
	if err := s.persistSession(ctx, session); err != nil {
		session.State = "partial"
		session.LastError = fmt.Sprintf("all activations may have completed but the final session update failed: %v", err)
		_ = s.persistSession(context.Background(), session)
		cleanupErr := s.restoreSession(context.Background(), session)
		if cleanupErr != nil {
			return session.PhysicalLocateSession, fmt.Errorf("Locate state could not be persisted and cleanup was not confirmed: %w", cleanupErr)
		}
		return PhysicalLocateSession{}, fmt.Errorf("activation was cleaned up because the final session update failed: %w", err)
	}
	s.retryMu.Lock()
	session.timer = time.AfterFunc(locateSessionTimeout, func() {
		_ = s.runScheduledCleanupRetry(session.ControllerID, ownerID, session.ID)
	})
	s.retryMu.Unlock()
	return session.PhysicalLocateSession, nil
}

// VerifyControllerMapping snapshots the read-only WLED output configuration an
// administrator attests. The returned fingerprint must remain unchanged for
// every later activation; this method never writes WLED state.
func (s *PhysicalLocateService) VerifyControllerMapping(ctx context.Context, controllerID int64) (string, error) {
	controller, err := s.store.GetController(ctx, controllerID)
	if err != nil {
		return "", fmt.Errorf("controller not found: %w", err)
	}
	if controller.LedCount <= 0 {
		return "", errors.New("controller capacity must be configured before mapping verification")
	}
	var info physicalInfo
	var cfg physicalConfig
	if err := s.client.getControllerJSON(ctx, controller, "/json/info", &info); err != nil {
		return "", fmt.Errorf("read WLED info: %w", err)
	}
	if err := s.client.getControllerJSON(ctx, controller, "/json/cfg", &cfg); err != nil {
		return "", fmt.Errorf("read WLED LED output configuration: %w", err)
	}
	return physicalMappingFingerprint(controller, info, cfg, controller.LedCount)
}

func (s *PhysicalLocateService) SaveControllerMappingVerification(ctx context.Context, controllerID int64, verified bool) error {
	physicalLocateCoordinator.Lock()
	defer physicalLocateCoordinator.Unlock()
	if err := s.ensureLoaded(ctx); err != nil {
		return fmt.Errorf("load prior Locate cleanup state: %w", err)
	}
	if _, session := s.activeSession(); session != nil {
		return ErrLocateSessionActive
	}
	if _, err := s.store.GetController(ctx, controllerID); err != nil {
		return fmt.Errorf("controller not found: %w", err)
	}
	fingerprint := ""
	if verified {
		verifiedFingerprint, err := s.VerifyControllerMapping(ctx, controllerID)
		if err != nil {
			return err
		}
		fingerprint = verifiedFingerprint
	}
	return db.WithOperationClaim(ctx, s.store, func(q db.Querier) error {
		owned, err := db.HasPhysicalLocateOwnership(ctx, q)
		if err != nil {
			return err
		}
		if owned {
			return ErrLocateSessionActive
		}
		rows, err := q.UpdateControllerLocateMappingVerification(ctx, db.UpdateControllerLocateMappingVerificationParams{
			LocateMappingVerified: verified, LocateMappingFingerprint: fingerprint, ID: controllerID,
		})
		if err != nil {
			return err
		}
		if rows != 1 {
			return sql.ErrNoRows
		}
		return nil
	})
}

// PhysicalOutput describes one explicitly configured WLED hardware output as
// reported by read-only discovery. It never represents a WLED segment.
type PhysicalOutput struct {
	// Order is the zero-based position of the output in the controller's explicit
	// output list; it is the physical order in which the controller drives LEDs.
	Order int64 `json:"order"`
	// Start is the zero-based controller-wide index of the output's first LED,
	// taken from explicit WLED geometry and never inferred.
	Start int64 `json:"start"`
	// Length is the number of LEDs driven by this output.
	Length int64 `json:"length"`
	// Pins are the active GPIO pins assigned to this output, in WLED order.
	Pins []int64 `json:"pins"`
	// FirstIndex is the 1-based inclusive controller-wide number of the first LED.
	FirstIndex int64 `json:"first_index"`
	// LastIndex is the 1-based inclusive controller-wide number of the last LED.
	LastIndex int64 `json:"last_index"`
}

// PhysicalOutputLayout is the verified, read-only view of a controller's
// explicit hardware output geometry. Discovery reads only /json/info and
// /json/cfg; it never reads /json/state and never writes to the controller.
type PhysicalOutputLayout struct {
	ControllerID int64            `json:"controller_id"`
	Capacity     int64            `json:"capacity"`
	MAC          string           `json:"mac"`
	Fingerprint  string           `json:"fingerprint"`
	Outputs      []PhysicalOutput `json:"outputs"`
}

// DiscoverPhysicalOutputs reads a controller's explicit WLED output geometry
// using GET /json/info and GET /json/cfg only. It validates controller
// capacity and identity together with a complete, contiguous, non-reversed and
// non-skipped output layout using the same safety logic as mapping
// verification, then reports each output's order, zero-based controller-wide
// start, length, active GPIO pins and the corresponding 1-based inclusive
// controller-wide numbering. Missing starts are rejected rather than inferred,
// and outputs are never conflated with WLED segments.
func (s *PhysicalLocateService) DiscoverPhysicalOutputs(ctx context.Context, controllerID int64) (PhysicalOutputLayout, error) {
	if controllerID <= 0 {
		return PhysicalOutputLayout{}, errors.New("controller id is required for physical output discovery")
	}
	controller, err := s.store.GetController(ctx, controllerID)
	if err != nil {
		return PhysicalOutputLayout{}, fmt.Errorf("controller not found: %w", err)
	}
	if controller.LedCount <= 0 {
		return PhysicalOutputLayout{}, errors.New("controller capacity must be configured before physical output discovery")
	}
	var info physicalInfo
	var cfg physicalConfig
	if err := s.client.getControllerJSON(ctx, controller, "/json/info", &info); err != nil {
		return PhysicalOutputLayout{}, fmt.Errorf("read WLED info: %w", err)
	}
	if err := s.client.getControllerJSON(ctx, controller, "/json/cfg", &cfg); err != nil {
		return PhysicalOutputLayout{}, fmt.Errorf("read WLED LED output configuration: %w", err)
	}
	fingerprint, err := physicalMappingFingerprint(controller, info, cfg, controller.LedCount)
	if err != nil {
		return PhysicalOutputLayout{}, err
	}
	outputs := make([]PhysicalOutput, 0, len(cfg.Hardware.LED.Inputs))
	for order, output := range cfg.Hardware.LED.Inputs {
		pins, err := activeOutputPins(output)
		if err != nil {
			return PhysicalOutputLayout{}, err
		}
		// physicalMappingFingerprint has already rejected outputs without an
		// explicit start, so the dereference below cannot infer a missing start.
		start := *output.Start
		outputs = append(outputs, PhysicalOutput{
			Order:      int64(order),
			Start:      start,
			Length:     output.Length,
			Pins:       pins,
			FirstIndex: start + 1,
			LastIndex:  start + output.Length,
		})
	}
	return PhysicalOutputLayout{
		ControllerID: controller.ID,
		Capacity:     controller.LedCount,
		MAC:          info.MAC,
		Fingerprint:  fingerprint,
		Outputs:      outputs,
	}, nil
}

// activeOutputPins returns the active (non-negative) GPIO pins of one explicit
// WLED output. It fails closed when the pin configuration is missing or has no
// active pin, matching the safety rules used for mapping verification.
func activeOutputPins(output physicalOutput) ([]int64, error) {
	var pins []int64
	if len(output.Pins) == 0 || json.Unmarshal(output.Pins, &pins) != nil {
		return nil, fmt.Errorf("%w: output pin configuration is missing or unsupported", ErrLocateTopology)
	}
	active := make([]int64, 0, len(pins))
	for _, pin := range pins {
		if pin >= 0 {
			active = append(active, pin)
		}
	}
	if len(active) == 0 {
		return nil, fmt.Errorf("%w: output has no active pin", ErrLocateTopology)
	}
	return active, nil
}

func (s *PhysicalLocateService) resolve(ctx context.Context, expectedDrawerID, binID int64) (resolvedPhysicalLocation, error) {
	var result resolvedPhysicalLocation
	err := s.store.ExecTx(ctx, func(q db.Querier) error {
		var err error
		result, err = s.resolveWithQuerier(ctx, q, expectedDrawerID, binID)
		return err
	})
	return result, err
}

func (s *PhysicalLocateService) resolveWithQuerier(ctx context.Context, q db.Querier, expectedDrawerID, binID int64) (resolvedPhysicalLocation, error) {
	attachment, err := q.GetBinDrawerAttachment(ctx, binID)
	if errors.Is(err, sql.ErrNoRows) {
		return resolvedPhysicalLocation{}, fmt.Errorf("%w: bin %d", ErrPhysicalBinMissing, binID)
	}
	if err != nil {
		return resolvedPhysicalLocation{}, fmt.Errorf("resolve physical bin attachment: %w", err)
	}
	if expectedDrawerID != 0 && attachment.DrawerID != expectedDrawerID {
		return resolvedPhysicalLocation{}, fmt.Errorf("bin %d is not physically attached to drawer %d", binID, expectedDrawerID)
	}
	drawer, err := q.GetDrawer(ctx, attachment.DrawerID)
	if err != nil {
		return resolvedPhysicalLocation{}, fmt.Errorf("physical drawer not found: %w", err)
	}
	if drawer.Archived != 0 {
		return resolvedPhysicalLocation{}, fmt.Errorf("physical drawer %d is archived", drawer.ID)
	}
	cabinet, err := q.GetCabinet(ctx, drawer.CabinetID)
	if err != nil {
		return resolvedPhysicalLocation{}, fmt.Errorf("physical cabinet not found: %w", err)
	}
	if cabinet.Archived != 0 {
		return resolvedPhysicalLocation{}, fmt.Errorf("physical cabinet %d is archived", cabinet.ID)
	}
	allocations, err := q.ListDrawerLEDAllocationsByDrawer(ctx, drawer.ID)
	if err != nil {
		return resolvedPhysicalLocation{}, fmt.Errorf("read saved drawer LED ranges: %w", err)
	}
	if len(allocations) == 0 {
		return resolvedPhysicalLocation{}, ErrNoDrawerLEDAllocation
	}
	topology, err := cabinetpixels.Load(ctx, q, cabinet.ID)
	if err != nil {
		return resolvedPhysicalLocation{}, fmt.Errorf("resolve cabinet-wide LED numbering: %w", err)
	}
	globalRanges := make([]ControllerRange, 0, len(allocations))
	localRanges := make(map[int64][]ControllerRange)
	for _, allocation := range allocations {
		if allocation.CabinetID != cabinet.ID || allocation.DrawerID != drawer.ID {
			return resolvedPhysicalLocation{}, fmt.Errorf("saved LED range %d has stale cabinet/drawer ownership", allocation.ID)
		}
		pieces, err := cabinetpixels.SplitTopologyRange(topology, allocation.StartPixel, allocation.PixelCount)
		if err != nil {
			return resolvedPhysicalLocation{}, fmt.Errorf("saved cabinet pixel range %d–%d is outside the configured topology: %w", allocation.StartPixel, allocation.StartPixel+allocation.PixelCount-1, err)
		}
		globalRanges = append(globalRanges, ControllerRange{Start: allocation.StartPixel, Count: allocation.PixelCount})
		for _, piece := range pieces {
			localRanges[piece.ControllerID] = append(localRanges[piece.ControllerID], ControllerRange{Start: piece.LocalStart, Count: piece.Count})
		}
	}
	targets := make([]resolvedControllerTarget, 0, len(localRanges))
	for _, controller := range topology.Controllers {
		ranges, ok := localRanges[controller.Record.ID]
		if ok {
			targets = append(targets, resolvedControllerTarget{controller: controller.Record, ranges: mergeControllerRanges(ranges)})
		}
	}
	if len(targets) == 0 {
		return resolvedPhysicalLocation{}, ErrNoDrawerLEDAllocation
	}
	settings, err := q.GetSettings(ctx)
	if err != nil {
		return resolvedPhysicalLocation{}, fmt.Errorf("read Locate color setting: %w", err)
	}
	color := strings.TrimSpace(settings.ColorLocate.String)
	if color == "" {
		color = "#0000FF"
	}
	if _, err := HexToRGB(color); err != nil {
		return resolvedPhysicalLocation{}, fmt.Errorf("invalid configured Locate color: %w", err)
	}
	return resolvedPhysicalLocation{controller: targets[0].controller, drawer: drawer, cabinet: cabinet, ranges: globalRanges, targets: targets, color: color}, nil
}

func mergeControllerRanges(ranges []ControllerRange) []ControllerRange {
	if len(ranges) == 0 {
		return nil
	}
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].Start < ranges[j].Start })
	merged := make([]ControllerRange, 0, len(ranges))
	for _, current := range ranges {
		if len(merged) == 0 {
			merged = append(merged, current)
			continue
		}
		last := &merged[len(merged)-1]
		lastEnd := last.Start + last.Count - 1
		if current.Start <= lastEnd+1 {
			currentEnd := current.Start + current.Count - 1
			if currentEnd > lastEnd {
				last.Count = currentEnd - last.Start + 1
			}
			continue
		}
		merged = append(merged, current)
	}
	return merged
}

func (s *PhysicalLocateService) StopDrawer(ctx context.Context, drawerID, ownerID int64) error {
	if drawerID <= 0 || ownerID <= 0 {
		return errors.New("drawer and authenticated user are required")
	}
	physicalLocateCoordinator.Lock()
	defer physicalLocateCoordinator.Unlock()
	if err := s.ensureLoaded(ctx); err != nil {
		return fmt.Errorf("load prior Locate cleanup state: %w", err)
	}
	controllerID, session := s.activeSession()
	if session == nil || session.DrawerID != drawerID {
		return nil
	}
	return s.stopControllerSessionLocked(ctx, controllerID, ownerID, "", drawerID)
}

func (s *PhysicalLocateService) stopControllerSession(ctx context.Context, controllerID, ownerID int64, expectedSessionID string, expectedDrawerID int64) error {
	if err := lockPhysicalLocateCoordinator(ctx); err != nil {
		if ctx.Err() != nil && expectedSessionID != "" {
			if session := s.getSession(controllerID); session != nil && session.ID == expectedSessionID {
				s.scheduleCleanupRetry(session)
			}
		}
		return fmt.Errorf("wait for physical Locate cleanup lock: %w", err)
	}
	defer physicalLocateCoordinator.Unlock()
	if err := s.ensureLoaded(ctx); err != nil {
		if ctx.Err() != nil {
			if session := s.getSession(controllerID); session != nil && session.ID == expectedSessionID && isActivePhysicalSession(session.State) {
				s.scheduleCleanupRetry(session)
			}
		}
		return fmt.Errorf("load prior Locate cleanup state: %w", err)
	}
	return s.stopControllerSessionLocked(ctx, controllerID, ownerID, expectedSessionID, expectedDrawerID)
}

func lockPhysicalLocateCoordinator(ctx context.Context) error {
	poll := time.NewTicker(5 * time.Millisecond)
	defer poll.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if physicalLocateCoordinator.TryLock() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-poll.C:
		}
	}
}

func (s *PhysicalLocateService) stopControllerSessionLocked(ctx context.Context, controllerID, ownerID int64, expectedSessionID string, expectedDrawerID int64) error {
	session := s.getSession(controllerID)
	if session == nil || !isActivePhysicalSession(session.State) {
		return nil
	}
	if expectedSessionID != "" && session.ID != expectedSessionID {
		return nil
	}
	if expectedDrawerID != 0 && session.DrawerID != expectedDrawerID {
		return nil
	}
	if session.OwnerID != ownerID {
		return ErrLocateSessionOwned
	}
	unlock := s.lockSessionTargets(session)
	defer unlock()
	return s.restoreSession(ctx, session)
}

func (s *PhysicalLocateService) restoreSession(ctx context.Context, session *physicalSessionState) error {
	s.retryMu.Lock()
	if session.timer != nil {
		session.timer.Stop()
		session.timer = nil
	}
	s.retryMu.Unlock()
	if len(session.targets) == 0 { // compatibility with a pre-target in-memory session
		session.targets = []physicalControllerTarget{{
			ControllerID: session.ControllerID, ControllerAddress: session.ControllerAddress,
			ControllerPort: session.ControllerPort, Ranges: append([]ControllerRange(nil), session.Ranges...),
			Commands: append([]physicalPixelCommand(nil), session.commands...), Fingerprint: session.fingerprint, MayHaveReceived: true,
		}}
	}

	controllers := make([]db.Controller, len(session.targets))
	verified := make([]bool, len(session.targets))
	var cleanupErrors []error
	recordTargetError := func(target *physicalControllerTarget, err error) {
		if target.LastError == "" {
			target.LastError = err.Error()
		} else {
			target.LastError += "; " + err.Error()
		}
		cleanupErrors = append(cleanupErrors, err)
	}
	for i := range session.targets {
		target := &session.targets[i]
		if !target.MayHaveReceived {
			target.LastError = ""
			continue
		}
		target.LastError = ""
		verifyCtx, cancel := context.WithTimeout(ctx, physicalLocateControllerTimeout)
		controller, err := s.store.GetController(verifyCtx, target.ControllerID)
		if err != nil {
			recordTargetError(target, fmt.Errorf("controller #%d is unavailable: %w", target.ControllerID, err))
			cancel()
			continue
		}
		if controller.IpAddress != target.ControllerAddress || controllerPort(controller) != target.ControllerPort {
			recordTargetError(target, fmt.Errorf("%w: controller #%d address or port changed", ErrLocateSessionStale, target.ControllerID))
			cancel()
			continue
		}
		if !controller.LocateMappingVerified || strings.TrimSpace(controller.LocateMappingFingerprint) == "" {
			recordTargetError(target, fmt.Errorf("controller #%d mapping verification is no longer valid", target.ControllerID))
			cancel()
			continue
		}
		if len(target.Commands) == 0 {
			recordTargetError(target, fmt.Errorf("%w: controller #%d has no recorded targeted cleanup commands", ErrLocateSessionStale, target.ControllerID))
			cancel()
			continue
		}
		info, cfg, state, mappingFingerprint, err := s.client.inspectPhysicalController(verifyCtx, controller, controller.LedCount)
		if err != nil {
			recordTargetError(target, fmt.Errorf("cannot verify controller #%d before cleanup: %w", target.ControllerID, err))
			cancel()
			continue
		}
		if mappingFingerprint != controller.LocateMappingFingerprint {
			recordTargetError(target, fmt.Errorf("%w: controller #%d output configuration changed", ErrLocateSessionStale, target.ControllerID))
			cancel()
			continue
		}
		expectedCommands, fingerprint, err := mapPhysicalRanges(target.Ranges, controller, controller.LedCount, info, cfg, state, session.Color, session.Brightness)
		if err != nil {
			recordTargetError(target, fmt.Errorf("cannot verify controller #%d cleanup mapping: %w", target.ControllerID, err))
			cancel()
			continue
		}
		if fingerprint != target.Fingerprint || !reflect.DeepEqual(expectedCommands, target.Commands) {
			recordTargetError(target, fmt.Errorf("%w: controller #%d WLED target state changed", ErrLocateSessionStale, target.ControllerID))
			cancel()
			continue
		}
		cancel()
		controllers[i] = controller
		verified[i] = true
	}

	session.State = "stopping"
	session.LastError = joinCleanupErrors(cleanupErrors)
	session.LastOperation = "targeted cleanup requested independently for verified controllers; unresolved targets retained"
	session.LastOperationAt = time.Now().UTC()
	if err := s.persistSession(ctx, session); err != nil {
		session.State = "partial"
		session.LastError = fmt.Sprintf("cleanup ownership could not be saved: %v", err)
		session.LastOperation = "cleanup withheld because session ownership was not confirmed"
		session.LastOperationAt = time.Now().UTC()
		s.scheduleCleanupRetry(session)
		return fmt.Errorf("cleanup ownership could not be saved: %w", err)
	}

	for targetIndex := range session.targets {
		target := &session.targets[targetIndex]
		if !target.MayHaveReceived || !verified[targetIndex] {
			continue
		}
		if err := ctx.Err(); err != nil {
			recordTargetError(target, fmt.Errorf("controller #%d cleanup canceled: %w", target.ControllerID, err))
			continue
		}
		cleanupCtx, cancel := context.WithTimeout(ctx, physicalLocateControllerTimeout)
		targetCleanupConfirmed := true
		cleared, commandErr := s.withPhysicalCommandClaim(cleanupCtx, session.ID, target, []string{"stopping", "partial"}, controllers[targetIndex], []int{0, 0, 0}, true)
		if commandErr != nil {
			targetCleanupConfirmed = false
			recordTargetError(target, fmt.Errorf("controller #%d targeted cleanup failed: %w", target.ControllerID, commandErr))
		}
		if !cleared && commandErr == nil {
			target.MayHaveReceived = false
		}
		if targetCleanupConfirmed && cleared {
			session.LastSuccessfulOperation = fmt.Sprintf("controller #%d acknowledged targeted range-off", target.ControllerID)
			session.LastSuccessfulAt = time.Now().UTC().Format(time.RFC3339Nano)
			session.LastOperation = session.LastSuccessfulOperation
			session.LastOperationAt = time.Now().UTC()
		}
		if !targetCleanupConfirmed {
			cancel()
			continue
		}

		// Clear ownership only after all targeted writes were acknowledged and
		// the controller's resolved state was durably recorded.
		target.MayHaveReceived = false
		target.LastError = ""
		if err := s.persistSession(cleanupCtx, session); err != nil {
			target.MayHaveReceived = true
			recordTargetError(target, fmt.Errorf("controller #%d cleanup was acknowledged but its resolution could not be persisted: %w", target.ControllerID, err))
		}
		cancel()
	}

	if len(cleanupErrors) != 0 {
		session.State = "partial"
		session.LastError = joinCleanupErrors(cleanupErrors)
		session.LastOperation = "cleanup attempted on every safely verified target; unresolved cleanup targets retained for retry"
		session.LastOperationAt = time.Now().UTC()
		_ = s.persistSession(ctx, session)
		s.scheduleCleanupRetry(session)
		return fmt.Errorf("targeted cleanup not confirmed for every affected controller: %w", errors.Join(cleanupErrors...))
	}
	session.State = "stopped"
	session.LastError = ""
	session.LastOperation = "WLED acknowledged targeted cleanup on all affected controllers"
	session.LastOperationAt = time.Now().UTC()
	session.LastSuccessfulOperation = session.LastOperation
	session.LastSuccessfulAt = session.LastOperationAt.Format(time.RFC3339Nano)
	if err := s.persistSession(ctx, session); err != nil {
		session.State = "partial"
		session.LastError = fmt.Sprintf("all cleanup acknowledgements arrived, but durable session finalization failed: %v", err)
		session.LastOperation = "cleanup acknowledged; durable session finalization pending"
		session.LastOperationAt = time.Now().UTC()
		s.scheduleCleanupRetry(session)
		return fmt.Errorf("WLED acknowledged cleanup but session state could not be saved: %w", err)
	}
	return nil
}

// withPhysicalCommandClaim fences each bounded physical command batch with a
// fresh durable session/target read. The target attempt marker is committed
// before activation; cleanup clears it in the same transaction as the last
// acknowledged targeted range-off command.
func (s *PhysicalLocateService) withPhysicalCommandClaim(ctx context.Context, sessionID string, target *physicalControllerTarget, allowedStates []string, controller db.Controller, rgb []int, resolveOwnership bool) (bool, error) {
	if !resolveOwnership {
		if err := requirePhysicalOutput(); err != nil {
			return false, err
		}
	}
	if target == nil || sessionID == "" {
		return false, errors.New("physical Locate session and target are required")
	}
	cleared := false
	err := db.WithOperationClaim(ctx, s.store, func(q db.Querier) error {
		sessions, err := q.ListPhysicalLocateSessions(ctx)
		if err != nil {
			return fmt.Errorf("reload durable Locate session before WLED command: %w", err)
		}
		var current *db.PhysicalLocateSession
		for i := range sessions {
			if sessions[i].SessionID == sessionID {
				current = &sessions[i]
				break
			}
		}
		if current == nil {
			return errors.New("physical Locate session was superseded before WLED command")
		}
		stateAllowed := false
		for _, state := range allowedStates {
			if current.State == state {
				stateAllowed = true
				break
			}
		}
		if !stateAllowed {
			if resolveOwnership && (current.State == "stopped" || current.State == "failed") {
				return nil
			}
			return fmt.Errorf("physical Locate session state %q does not authorize WLED command", current.State)
		}
		targets, err := q.ListPhysicalLocateSessionTargets(ctx, sessionID)
		if err != nil {
			return fmt.Errorf("reload durable Locate target before WLED command: %w", err)
		}
		var saved *db.PhysicalLocateSessionTarget
		for i := range targets {
			if targets[i].ControllerID == target.ControllerID {
				saved = &targets[i]
				break
			}
		}
		if saved == nil {
			return errors.New("physical Locate target was superseded before WLED command")
		}
		if saved.ControllerAddress != target.ControllerAddress || saved.ControllerPort != target.ControllerPort || saved.Fingerprint != target.Fingerprint {
			return errors.New("durable physical Locate target no longer matches the command snapshot")
		}
		var savedRanges []ControllerRange
		if err := json.Unmarshal([]byte(saved.RangesJson), &savedRanges); err != nil {
			return fmt.Errorf("decode durable physical Locate ranges: %w", err)
		}
		if !reflect.DeepEqual(savedRanges, target.Ranges) {
			return errors.New("durable physical Locate ranges changed before WLED command")
		}
		var savedCommands []physicalPixelCommand
		if err := json.Unmarshal([]byte(saved.CommandsJson), &savedCommands); err != nil {
			return fmt.Errorf("decode durable physical Locate commands: %w", err)
		}
		if !reflect.DeepEqual(savedCommands, target.Commands) {
			return errors.New("durable physical Locate commands changed before WLED command")
		}
		if saved.MayHaveReceived == 0 {
			if resolveOwnership {
				return nil
			}
			return errors.New("activation target has no durable may-have-received marker")
		}
		if controller.IpAddress != saved.ControllerAddress || controllerPort(controller) != saved.ControllerPort {
			return fmt.Errorf("controller #%d endpoint changed before WLED command", target.ControllerID)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		for _, command := range savedCommands {
			if err := s.client.setPhysicalPixels(ctx, controller, command.SegmentID, command.Start, command.Stop, rgb); err != nil {
				return fmt.Errorf("controller #%d segment %d pixels %d-%d: %w", target.ControllerID, command.SegmentID, command.Start, command.Stop, err)
			}
		}
		if resolveOwnership {
			rows, err := q.UpdatePhysicalLocateSessionTargetOwnership(ctx, db.UpdatePhysicalLocateSessionTargetOwnershipParams{
				MayHaveReceived: 0, LastError: "", SessionID: sessionID,
				ControllerID: target.ControllerID, MayHaveReceived_2: 1,
			})
			if err != nil {
				return fmt.Errorf("persist targeted cleanup resolution: %w", err)
			}
			if rows != 1 {
				return errors.New("physical Locate target ownership changed while cleanup claim was held")
			}
			cleared = true
		}
		return nil
	})
	return cleared, err
}

func joinCleanupErrors(errs []error) string {
	if len(errs) == 0 {
		return ""
	}
	messages := make([]string, 0, len(errs))
	for _, err := range errs {
		messages = append(messages, err.Error())
	}
	return strings.Join(messages, "; ")
}

func (s *PhysicalLocateService) scheduleCleanupRetry(session *physicalSessionState) {
	s.retryMu.Lock()
	defer s.retryMu.Unlock()
	if session.timer != nil {
		session.timer.Stop()
	}
	controllerID, ownerID, sessionID := session.ControllerID, session.OwnerID, session.ID
	session.timer = time.AfterFunc(physicalLocateCleanupRetryDelay, func() {
		_ = s.runScheduledCleanupRetry(controllerID, ownerID, sessionID)
	})
}

func (s *PhysicalLocateService) runScheduledCleanupRetry(controllerID, ownerID int64, sessionID string) error {
	retryCtx, cancel := context.WithTimeout(context.Background(), physicalLocateCleanupRetryTimeout)
	defer cancel()
	return s.stopControllerSession(retryCtx, controllerID, ownerID, sessionID, 0)
}

func (s *PhysicalLocateService) SessionForDrawer(drawerID int64) (PhysicalLocateSession, bool) {
	session, found, _ := s.SessionStatusForDrawer(context.Background(), drawerID)
	return session, found
}

func (s *PhysicalLocateService) SessionStatusForDrawer(ctx context.Context, drawerID int64) (PhysicalLocateSession, bool, error) {
	physicalLocateCoordinator.Lock()
	defer physicalLocateCoordinator.Unlock()
	if err := s.ensureLoaded(ctx); err != nil {
		return PhysicalLocateSession{}, false, err
	}
	s.mu.Lock()
	var controllerIDs []int64
	for id, session := range s.sessions {
		if session.DrawerID == drawerID {
			controllerIDs = append(controllerIDs, id)
		}
	}
	s.mu.Unlock()
	for _, id := range controllerIDs {
		lock := s.controllerLock(id)
		lock.Lock()
		session := s.getSession(id)
		if session != nil && session.DrawerID == drawerID {
			copy := session.PhysicalLocateSession
			copy.Ranges = append([]ControllerRange(nil), session.Ranges...)
			lock.Unlock()
			return copy, true, nil
		}
		lock.Unlock()
	}
	return PhysicalLocateSession{}, false, nil
}

// WithoutActiveSession is a process-local fast rejection around a configuration
// callback; it is not a cross-process safety boundary. Every callback must itself
// perform its complete mutation through a SQLite operation claim and re-check
// durable ownership in that transaction. Callers that cannot meet that contract
// must not use this helper.
func (s *PhysicalLocateService) WithoutActiveSession(ctx context.Context, write func() error) error {
	if write == nil {
		return errors.New("configuration write callback is required")
	}
	physicalLocateCoordinator.Lock()
	defer physicalLocateCoordinator.Unlock()
	if err := s.ensureLoaded(ctx); err != nil {
		return fmt.Errorf("load prior Locate cleanup state: %w", err)
	}
	if _, session := s.activeSession(); session != nil {
		return ErrLocateSessionActive
	}
	return write()
}

func (s *PhysicalLocateService) activeSession() (int64, *physicalSessionState) {
	s.mu.Lock()
	ids := make([]int64, 0, len(s.sessions))
	for id := range s.sessions {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	for _, id := range ids {
		lock := s.controllerLock(id)
		lock.Lock()
		session := s.getSession(id)
		if session != nil && (session.State == "active" || session.State == "partial" || session.State == "activating" || session.State == "stopping") {
			lock.Unlock()
			return id, session
		}
		lock.Unlock()
	}
	return 0, nil
}

func (s *PhysicalLocateService) controllerLock(controllerID int64) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	lock := s.locks[controllerID]
	if lock == nil {
		lock = &sync.Mutex{}
		s.locks[controllerID] = lock
	}
	return lock
}

// lockControllers acquires per-controller locks in stable ascending ID order
// and releases them in reverse order. Callers may pass duplicates or unsorted IDs.
func (s *PhysicalLocateService) lockControllers(controllerIDs []int64) func() {
	ids := append([]int64(nil), controllerIDs...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	unique := ids[:0]
	for _, id := range ids {
		if id > 0 && (len(unique) == 0 || unique[len(unique)-1] != id) {
			unique = append(unique, id)
		}
	}
	locks := make([]*sync.Mutex, 0, len(unique))
	for _, id := range unique {
		lock := s.controllerLock(id)
		lock.Lock()
		locks = append(locks, lock)
	}
	return func() {
		for i := len(locks) - 1; i >= 0; i-- {
			locks[i].Unlock()
		}
	}
}

func (s *PhysicalLocateService) lockSessionTargets(session *physicalSessionState) func() {
	ids := make([]int64, 0, len(session.targets)+1)
	for _, target := range session.targets {
		ids = append(ids, target.ControllerID)
	}
	if len(ids) == 0 {
		ids = append(ids, session.ControllerID)
	}
	return s.lockControllers(ids)
}

func (s *PhysicalLocateService) ensureLoaded(ctx context.Context) error {
	s.loadMu.Lock()
	defer s.loadMu.Unlock()
	rows, err := s.store.ListPhysicalLocateSessions(ctx)
	if err != nil {
		return err
	}
	loaded := make(map[int64]*physicalSessionState, len(rows))
	for _, row := range rows {
		var ranges []ControllerRange
		var commands []physicalPixelCommand
		if err := json.Unmarshal([]byte(row.RangesJson), &ranges); err != nil {
			s.loadErr = fmt.Errorf("decode saved session ranges for controller %d: %w", row.ControllerID, err)
			return s.loadErr
		}
		if err := json.Unmarshal([]byte(row.CommandsJson), &commands); err != nil {
			s.loadErr = fmt.Errorf("decode saved cleanup commands for controller %d: %w", row.ControllerID, err)
			return s.loadErr
		}
		targetRows, err := s.store.ListPhysicalLocateSessionTargets(ctx, row.SessionID)
		if err != nil {
			s.loadErr = fmt.Errorf("read durable controller targets for session %s: %w", row.SessionID, err)
			return s.loadErr
		}
		targets := make([]physicalControllerTarget, 0, len(targetRows))
		for _, targetRow := range targetRows {
			var localRanges []ControllerRange
			var targetCommands []physicalPixelCommand
			if err := json.Unmarshal([]byte(targetRow.RangesJson), &localRanges); err != nil {
				s.loadErr = fmt.Errorf("decode saved local ranges for controller %d: %w", targetRow.ControllerID, err)
				return s.loadErr
			}
			if err := json.Unmarshal([]byte(targetRow.CommandsJson), &targetCommands); err != nil {
				s.loadErr = fmt.Errorf("decode saved target commands for controller %d: %w", targetRow.ControllerID, err)
				return s.loadErr
			}
			targets = append(targets, physicalControllerTarget{
				ControllerID: targetRow.ControllerID, ControllerAddress: targetRow.ControllerAddress,
				ControllerPort: targetRow.ControllerPort, Ranges: localRanges, Commands: targetCommands,
				Fingerprint: targetRow.Fingerprint, MayHaveReceived: targetRow.MayHaveReceived != 0, LastError: targetRow.LastError,
			})
		}
		if len(targets) == 0 { // safety fallback for a database migrated without target rows
			targets = append(targets, physicalControllerTarget{
				ControllerID: row.ControllerID, ControllerAddress: row.ControllerAddress, ControllerPort: row.ControllerPort,
				Ranges: append([]ControllerRange(nil), ranges...), Commands: append([]physicalPixelCommand(nil), commands...),
				Fingerprint: row.Fingerprint, MayHaveReceived: isActivePhysicalSession(row.State), LastError: row.LastError,
			})
		}
		affected := make([]int64, 0, len(targets))
		for _, target := range targets {
			affected = append(affected, target.ControllerID)
		}
		loaded[row.ControllerID] = &physicalSessionState{
			PhysicalLocateSession: PhysicalLocateSession{
				ID: row.SessionID, ControllerID: row.ControllerID, ControllerAddress: row.ControllerAddress,
				ControllerPort: row.ControllerPort, DrawerID: row.DrawerID, BinID: row.BinID,
				OwnerID: row.OwnerID, State: row.State, Ranges: ranges, Color: row.Color,
				Brightness: int(row.Brightness), LastOperation: row.LastOperation,
				LastOperationAt: row.LastOperationAt, LastSuccessfulOperation: row.LastSuccessfulOperation,
				LastSuccessfulAt: row.LastSuccessfulAt, LastError: row.LastError, AffectedControllerIDs: affected,
			},
			fingerprint: row.Fingerprint, commands: commands, targets: targets,
		}
	}
	s.loadErr = nil
	s.mu.Lock()
	previous := s.sessions
	s.sessions = loaded
	s.mu.Unlock()
	for id, old := range previous {
		fresh := loaded[id]
		s.retryMu.Lock()
		if old != nil && old.timer != nil && (fresh == nil || fresh.ID != old.ID) {
			old.timer.Stop()
		}
		s.retryMu.Unlock()
		if fresh == nil && old != nil {
			setSessionReservations(old, false)
		}
	}
	for id, session := range loaded {
		old := previous[id]
		if old != nil && old.ID == session.ID {
			if old.State == "partial" && strings.Contains(old.LastOperation, "finalization pending") && session.State == "stopping" && session.LastError == "" {
				session.State = "partial"
				session.LastError = old.LastError
				session.LastOperation = old.LastOperation
				session.LastOperationAt = old.LastOperationAt
			}
		}
		s.retryMu.Lock()
		if old != nil && old.ID == session.ID {
			session.timer = old.timer
		}
		if !isActivePhysicalSession(session.State) {
			if session.timer != nil {
				session.timer.Stop()
				session.timer = nil
			}
			s.retryMu.Unlock()
			setSessionReservations(session, false)
			continue
		}
		if session.timer == nil {
			remaining := time.Until(session.LastOperationAt.Add(locateSessionTimeout))
			if remaining < 0 {
				remaining = 0
			}
			controllerID, ownerID, sessionID := id, session.OwnerID, session.ID
			session.timer = time.AfterFunc(remaining, func() {
				_ = s.runScheduledCleanupRetry(controllerID, ownerID, sessionID)
			})
		}
		s.retryMu.Unlock()
		setSessionReservations(session, true)
	}
	return nil
}

func (s *PhysicalLocateService) insertSessionWithQuerier(ctx context.Context, q db.Querier, session *physicalSessionState) error {
	if len(session.targets) == 0 {
		return errors.New("a durable Locate reservation requires at least one target")
	}
	primary := &session.targets[0]
	for i := range session.targets {
		if session.targets[i].ControllerID == session.ControllerID {
			primary = &session.targets[i]
			break
		}
	}
	ranges, err := json.Marshal(session.Ranges)
	if err != nil {
		return err
	}
	commands, err := json.Marshal(primary.Commands)
	if err != nil {
		return err
	}
	if err := q.ClearPhysicalLocateSessionTargetsForPrimaryController(ctx, session.ControllerID); err != nil {
		return err
	}
	rows, err := q.SavePhysicalLocateSession(ctx, db.SavePhysicalLocateSessionParams{
		ControllerID: session.ControllerID, ControllerAddress: session.ControllerAddress, ControllerPort: session.ControllerPort,
		SessionID: session.ID, DrawerID: session.DrawerID, BinID: session.BinID, OwnerID: session.OwnerID,
		State: session.State, RangesJson: string(ranges), CommandsJson: string(commands), Fingerprint: primary.Fingerprint,
		Color: session.Color, Brightness: int64(session.Brightness), LastOperation: session.LastOperation,
		LastOperationAt: session.LastOperationAt, LastSuccessfulOperation: session.LastSuccessfulOperation,
		LastSuccessfulAt: session.LastSuccessfulAt, LastError: session.LastError,
	})
	if err != nil {
		return err
	}
	if rows != 1 {
		return errors.New("another durable Locate session owns the controller")
	}
	for _, target := range session.targets {
		targetRanges, err := json.Marshal(target.Ranges)
		if err != nil {
			return err
		}
		targetCommands, err := json.Marshal(target.Commands)
		if err != nil {
			return err
		}
		if err := q.SavePhysicalLocateSessionTarget(ctx, db.SavePhysicalLocateSessionTargetParams{
			SessionID: session.ID, ControllerID: target.ControllerID, ControllerAddress: target.ControllerAddress,
			ControllerPort: target.ControllerPort, RangesJson: string(targetRanges), CommandsJson: string(targetCommands),
			Fingerprint: target.Fingerprint, MayHaveReceived: boolToInt64(target.MayHaveReceived), LastError: target.LastError,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *PhysicalLocateService) persistSession(ctx context.Context, session *physicalSessionState) error {
	if len(session.targets) == 0 {
		session.targets = []physicalControllerTarget{{
			ControllerID: session.ControllerID, ControllerAddress: session.ControllerAddress, ControllerPort: session.ControllerPort,
			Ranges: append([]ControllerRange(nil), session.Ranges...), Commands: append([]physicalPixelCommand(nil), session.commands...),
			Fingerprint: session.fingerprint, MayHaveReceived: isActivePhysicalSession(session.State), LastError: session.LastError,
		}}
	}
	var primary *physicalControllerTarget
	for i := range session.targets {
		if session.targets[i].ControllerID == session.ControllerID {
			primary = &session.targets[i]
			break
		}
	}
	if primary == nil {
		primary = &session.targets[0]
		session.ControllerID = primary.ControllerID
		session.ControllerAddress = primary.ControllerAddress
		session.ControllerPort = primary.ControllerPort
	}
	session.fingerprint = primary.Fingerprint
	session.commands = append(session.commands[:0], primary.Commands...)
	session.AffectedControllerIDs = session.AffectedControllerIDs[:0]
	for _, target := range session.targets {
		session.AffectedControllerIDs = append(session.AffectedControllerIDs, target.ControllerID)
	}
	ranges, err := json.Marshal(session.Ranges)
	if err != nil {
		return err
	}
	commands, err := json.Marshal(primary.Commands)
	if err != nil {
		return err
	}
	args := db.SavePhysicalLocateSessionParams{
		ControllerID: session.ControllerID, ControllerAddress: session.ControllerAddress,
		ControllerPort: session.ControllerPort, SessionID: session.ID, DrawerID: session.DrawerID,
		BinID: session.BinID, OwnerID: session.OwnerID, State: session.State,
		RangesJson: string(ranges), CommandsJson: string(commands), Fingerprint: primary.Fingerprint,
		Color: session.Color, Brightness: int64(session.Brightness), LastOperation: session.LastOperation,
		LastOperationAt: session.LastOperationAt, LastSuccessfulOperation: session.LastSuccessfulOperation,
		LastSuccessfulAt: session.LastSuccessfulAt, LastError: session.LastError,
	}
	err = db.WithOperationClaim(ctx, s.store, func(q db.Querier) error {
		rows, err := q.ListPhysicalLocateSessions(ctx)
		if err != nil {
			return err
		}
		var current *db.PhysicalLocateSession
		for i := range rows {
			if rows[i].ControllerID == session.ControllerID {
				current = &rows[i]
				break
			}
		}
		newSession := current == nil || current.SessionID != session.ID
		if newSession {
			if current != nil && current.State != "stopped" && current.State != "failed" {
				return errors.New("another Locate session owns this controller")
			}
			if err := q.ClearPhysicalLocateSessionTargetsForPrimaryController(ctx, session.ControllerID); err != nil {
				return err
			}
			rowsAffected, err := q.SavePhysicalLocateSession(ctx, args)
			if err != nil {
				return err
			}
			if rowsAffected != 1 {
				return errors.New("another Locate session owns this controller")
			}
			if err := q.ClearPhysicalLocateSessionTargets(ctx, session.ID); err != nil {
				return err
			}
			for _, target := range session.targets {
				targetRanges, err := json.Marshal(target.Ranges)
				if err != nil {
					return err
				}
				targetCommands, err := json.Marshal(target.Commands)
				if err != nil {
					return err
				}
				if err := q.SavePhysicalLocateSessionTarget(ctx, db.SavePhysicalLocateSessionTargetParams{
					SessionID: session.ID, ControllerID: target.ControllerID, ControllerAddress: target.ControllerAddress,
					ControllerPort: target.ControllerPort, RangesJson: string(targetRanges), CommandsJson: string(targetCommands),
					Fingerprint: target.Fingerprint, MayHaveReceived: boolToInt64(target.MayHaveReceived), LastError: target.LastError,
				}); err != nil {
					return err
				}
			}
			return nil
		}

		if !physicalLocateTransitionAllowed(current.State, session.State) {
			return fmt.Errorf("refusing stale physical Locate transition %q to %q", current.State, session.State)
		}
		rowsJson, err := json.Marshal(session.Ranges)
		if err != nil {
			return err
		}
		commandsJson, err := json.Marshal(primary.Commands)
		if err != nil {
			return err
		}
		updated, err := q.UpdatePhysicalLocateSessionCAS(ctx, db.UpdatePhysicalLocateSessionCASParams{
			State: session.State, RangesJson: string(rowsJson), CommandsJson: string(commandsJson), Fingerprint: primary.Fingerprint,
			Color: session.Color, Brightness: int64(session.Brightness), LastOperation: session.LastOperation,
			LastOperationAt: session.LastOperationAt, LastSuccessfulOperation: session.LastSuccessfulOperation,
			LastSuccessfulAt: session.LastSuccessfulAt, LastError: session.LastError,
			ControllerID: session.ControllerID, SessionID: session.ID, State_2: current.State,
		})
		if err != nil {
			return err
		}
		if updated != 1 {
			return errors.New("physical Locate session changed before compare-and-set update")
		}
		persistedTargets, err := q.ListPhysicalLocateSessionTargets(ctx, session.ID)
		if err != nil {
			return err
		}
		persistedByID := make(map[int64]db.PhysicalLocateSessionTarget, len(persistedTargets))
		for _, target := range persistedTargets {
			persistedByID[target.ControllerID] = target
		}
		for _, target := range session.targets {
			persisted, ok := persistedByID[target.ControllerID]
			if !ok || persisted.ControllerAddress != target.ControllerAddress || persisted.ControllerPort != target.ControllerPort || persisted.Fingerprint != target.Fingerprint {
				return errors.New("physical Locate target snapshot changed during compare-and-set update")
			}
			var savedRanges []ControllerRange
			if err := json.Unmarshal([]byte(persisted.RangesJson), &savedRanges); err != nil || !reflect.DeepEqual(savedRanges, target.Ranges) {
				return errors.New("physical Locate target ranges changed during compare-and-set update")
			}
			var savedCommands []physicalPixelCommand
			if err := json.Unmarshal([]byte(persisted.CommandsJson), &savedCommands); err != nil || !reflect.DeepEqual(savedCommands, target.Commands) {
				return errors.New("physical Locate command snapshot changed during compare-and-set update")
			}
			desired := boolToInt64(target.MayHaveReceived)
			if persisted.MayHaveReceived != desired {
				if persisted.MayHaveReceived == 1 && desired == 0 {
					return errors.New("target ownership can only be cleared by acknowledged targeted cleanup")
				}
				if current.State != "activating" || session.State != "activating" {
					return errors.New("refusing to reacquire resolved physical Locate target ownership")
				}
				changed, err := q.UpdatePhysicalLocateSessionTargetOwnership(ctx, db.UpdatePhysicalLocateSessionTargetOwnershipParams{
					MayHaveReceived: desired, LastError: target.LastError, SessionID: session.ID,
					ControllerID: target.ControllerID, MayHaveReceived_2: persisted.MayHaveReceived,
				})
				if err != nil {
					return err
				}
				if changed != 1 {
					return errors.New("physical Locate target ownership changed before compare-and-set update")
				}
			} else if persisted.LastError != target.LastError {
				changed, err := q.UpdatePhysicalLocateSessionTargetOwnership(ctx, db.UpdatePhysicalLocateSessionTargetOwnershipParams{
					MayHaveReceived: desired, LastError: target.LastError, SessionID: session.ID,
					ControllerID: target.ControllerID, MayHaveReceived_2: persisted.MayHaveReceived,
				})
				if err != nil {
					return err
				}
				if changed != 1 {
					return errors.New("physical Locate target changed before compare-and-set update")
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	setSessionReservations(session, isActivePhysicalSession(session.State))
	return nil
}

func physicalLocateTransitionAllowed(from, to string) bool {
	if from == to {
		return true
	}
	switch from {
	case "activating":
		return to == "active" || to == "stopping" || to == "partial" || to == "failed"
	case "active":
		return to == "stopping" || to == "partial"
	case "stopping":
		return to == "partial" || to == "stopped" || to == "failed"
	case "partial":
		return to == "stopping" || to == "stopped"
	case "stopped":
		return to == "failed"
	default:
		return false
	}
}

func setSessionReservations(session *physicalSessionState, reserved bool) {
	if len(session.targets) == 0 {
		setPhysicalReservation(session.ID, session.ControllerID, session.ControllerAddress, session.ControllerPort, reserved)
		return
	}
	for _, target := range session.targets {
		setPhysicalReservation(session.ID, target.ControllerID, target.ControllerAddress, target.ControllerPort, reserved)
	}
}

func isActivePhysicalSession(state string) bool {
	return state == "active" || state == "partial" || state == "activating" || state == "stopping"
}

func boolToInt64(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

func (s *PhysicalLocateService) getSession(controllerID int64) *physicalSessionState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[controllerID]
}

func (s *PhysicalLocateService) setSession(controllerID int64, session *physicalSessionState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[controllerID] = session
}

func newLocateSessionID() (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(data[:]), nil
}

type physicalInfo struct {
	MAC  string `json:"mac"`
	LEDs struct {
		Count int64 `json:"count"`
		RGBW  bool  `json:"rgbw"`
	} `json:"leds"`
}

type physicalState struct {
	On  bool              `json:"on"`
	Seg []physicalSegment `json:"seg"`
}

type physicalSegment struct {
	ID     int   `json:"id"`
	Start  int64 `json:"start"`
	Stop   int64 `json:"stop"`
	Offset int64 `json:"of"`
	On     bool  `json:"on"`
	FX     int   `json:"fx"`
	Group  int64 `json:"grp"`
	Space  int64 `json:"spc"`
	Rev    bool  `json:"rev"`
	Mirror bool  `json:"mi"`
}

type physicalOutput struct {
	Start   *int64          `json:"start"`
	Length  int64           `json:"len"`
	Reverse bool            `json:"rev"`
	Skip    int64           `json:"skip"`
	Pins    json.RawMessage `json:"pin"`
	Order   int64           `json:"order"`
	Type    int64           `json:"type"`
}

type physicalConfig struct {
	Hardware struct {
		LED struct {
			Inputs []physicalOutput `json:"ins"`
			Matrix json.RawMessage  `json:"matrix"`
			Map    json.RawMessage  `json:"map"`
			LEDMap json.RawMessage  `json:"ledmap"`
		} `json:"led"`
	} `json:"hw"`
}

func (c *Client) inspectPhysicalController(ctx context.Context, controller db.Controller, capacity int64) (physicalInfo, physicalConfig, physicalState, string, error) {
	var info physicalInfo
	var cfg physicalConfig
	var state physicalState
	if err := c.getControllerJSON(ctx, controller, "/json/info", &info); err != nil {
		return info, cfg, state, "", fmt.Errorf("read WLED info: %w", err)
	}
	if err := c.getControllerJSON(ctx, controller, "/json/cfg", &cfg); err != nil {
		return info, cfg, state, "", fmt.Errorf("read WLED LED output configuration: %w", err)
	}
	mappingFingerprint, err := physicalMappingFingerprint(controller, info, cfg, capacity)
	if err != nil {
		return info, cfg, state, "", err
	}
	if err := c.getControllerJSON(ctx, controller, "/json/state", &state); err != nil {
		return info, cfg, state, "", fmt.Errorf("read WLED state and segments: %w", err)
	}
	if !state.On {
		return info, cfg, state, "", fmt.Errorf("%w: controller is off; Locate will not power on unrelated segments", ErrLocateTopology)
	}
	return info, cfg, state, mappingFingerprint, nil
}

func physicalMappingFingerprint(controller db.Controller, info physicalInfo, cfg physicalConfig, capacity int64) (string, error) {
	if capacity <= 0 || info.LEDs.Count != capacity {
		return "", fmt.Errorf("%w: configured capacity %d does not match WLED-reported count %d", ErrLocateTopology, capacity, info.LEDs.Count)
	}
	if info.LEDs.RGBW {
		return "", fmt.Errorf("%w: RGBW output color semantics are not supported for safe Locate restoration", ErrLocateTopology)
	}
	if controller.MacAddress.Valid && strings.TrimSpace(controller.MacAddress.String) != "" &&
		!strings.EqualFold(strings.TrimSpace(info.MAC), strings.TrimSpace(controller.MacAddress.String)) {
		return "", fmt.Errorf("%w: WLED-reported MAC does not match the saved controller identity", ErrLocateTopology)
	}
	if len(cfg.Hardware.LED.Inputs) == 0 {
		return "", fmt.Errorf("%w: WLED output configuration is missing or unsupported", ErrLocateTopology)
	}
	var cursor int64
	seenPins := make(map[int64]struct{})
	for _, output := range cfg.Hardware.LED.Inputs {
		if output.Start == nil || *output.Start != cursor || output.Length <= 0 || output.Type <= 0 || output.Reverse || output.Skip != 0 {
			return "", fmt.Errorf("%w: output starts, lengths, reversal, or skipped LEDs are unsupported", ErrLocateTopology)
		}
		if output.Length > capacity-cursor {
			return "", fmt.Errorf("%w: output configuration exceeds controller capacity", ErrLocateTopology)
		}
		var pins []int64
		if len(output.Pins) == 0 || json.Unmarshal(output.Pins, &pins) != nil || len(pins) == 0 {
			return "", fmt.Errorf("%w: output pin configuration is missing or unsupported", ErrLocateTopology)
		}
		activePin := false
		for _, pin := range pins {
			if pin < 0 {
				continue
			}
			activePin = true
			if _, exists := seenPins[pin]; exists {
				return "", fmt.Errorf("%w: output pin assignment is duplicated", ErrLocateTopology)
			}
			seenPins[pin] = struct{}{}
		}
		if !activePin {
			return "", fmt.Errorf("%w: output has no active pin", ErrLocateTopology)
		}
		cursor += output.Length
	}
	if cursor != capacity {
		return "", fmt.Errorf("%w: WLED output lengths total %d; expected %d", ErrLocateTopology, cursor, capacity)
	}
	if hasNonDefaultConfig(cfg.Hardware.LED.Matrix) {
		return "", fmt.Errorf("%w: matrix LED configuration is not supported", ErrLocateTopology)
	}
	if hasNonDefaultConfig(cfg.Hardware.LED.Map) || hasNonDefaultConfig(cfg.Hardware.LED.LEDMap) {
		return "", fmt.Errorf("%w: custom LED mapping is not supported", ErrLocateTopology)
	}
	encoded, err := json.Marshal(struct {
		MAC     string
		Count   int64
		Outputs []physicalOutput
	}{strings.ToLower(strings.TrimSpace(info.MAC)), capacity, cfg.Hardware.LED.Inputs})
	if err != nil {
		return "", fmt.Errorf("fingerprint WLED outputs: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func hasNonDefaultConfig(raw json.RawMessage) bool {
	value := strings.TrimSpace(string(raw))
	if value == "" || value == "null" || value == "false" || value == "0" || value == "{}" || value == "[]" {
		return false
	}
	var integer int64
	if json.Unmarshal(raw, &integer) == nil && integer == 0 {
		return false
	}
	return true
}

func (c *Client) getControllerJSON(ctx context.Context, controller db.Controller, path string, target any) error {
	bounded, cancel := controllerRequestContext(ctx, controller)
	defer cancel()
	url := controllerURL(controller, path)
	req, err := http.NewRequestWithContext(bounded, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if readErr != nil {
			return fmt.Errorf("GET %s returned %s (could not read error response: %v)", path, resp.Status, readErr)
		}
		if message := strings.TrimSpace(string(body)); message != "" {
			return fmt.Errorf("GET %s returned %s: %s", path, resp.Status, message)
		}
		return fmt.Errorf("GET %s returned %s", path, resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		return fmt.Errorf("decode WLED response: %w", err)
	}
	return nil
}

func controllerURL(controller db.Controller, path string) string {
	return controllerURLForAddress(controller.IpAddress, controllerPort(controller), path)
}

func mapPhysicalRanges(ranges []ControllerRange, controller db.Controller, capacity int64, info physicalInfo, cfg physicalConfig, state physicalState, color string, brightness int) ([]physicalPixelCommand, string, error) {
	if capacity <= 0 || !state.On {
		return nil, "", ErrLocateTopology
	}
	mappingFingerprint, err := physicalMappingFingerprint(controller, info, cfg, capacity)
	if err != nil {
		return nil, "", err
	}
	segments := append([]physicalSegment(nil), state.Seg...)
	sort.Slice(segments, func(i, j int) bool { return segments[i].Start < segments[j].Start })
	if len(segments) == 0 {
		return nil, "", fmt.Errorf("%w: WLED reported no segments", ErrLocateTopology)
	}
	var cursor int64
	seenIDs := make(map[int]struct{}, len(segments))
	for _, seg := range segments {
		if seg.ID < 0 {
			return nil, "", fmt.Errorf("%w: WLED reported a negative segment id", ErrLocateTopology)
		}
		if _, exists := seenIDs[seg.ID]; exists {
			return nil, "", fmt.Errorf("%w: WLED reported duplicate segment id %d", ErrLocateTopology, seg.ID)
		}
		seenIDs[seg.ID] = struct{}{}
		if seg.Start != cursor || seg.Stop <= seg.Start || seg.Stop > capacity {
			return nil, "", fmt.Errorf("%w: segments do not form a gap-free controller-wide partition", ErrLocateTopology)
		}
		cursor = seg.Stop
	}
	if cursor != capacity {
		return nil, "", fmt.Errorf("%w: WLED segments do not cover the full controller", ErrLocateTopology)
	}
	fingerprintData := struct {
		Mapping  string
		Segments []physicalSegment
	}{mappingFingerprint, segments}
	encoded, err := json.Marshal(fingerprintData)
	if err != nil {
		return nil, "", fmt.Errorf("fingerprint WLED segments: %w", err)
	}
	digest := sha256.Sum256(encoded)
	fingerprint := hex.EncodeToString(digest[:])
	if _, err := HexToRGB(color); err != nil {
		return nil, "", err
	}
	var commands []physicalPixelCommand
	for _, allocated := range ranges {
		if allocated.Start < 1 || allocated.Count < 1 || allocated.Start-1 > capacity-allocated.Count {
			return nil, "", fmt.Errorf("saved controller-wide range %d + %d is outside capacity %d", allocated.Start, allocated.Count, capacity)
		}
		start, stop := allocated.Start-1, allocated.Start-1+allocated.Count
		for _, seg := range segments {
			left, right := max(start, seg.Start), min(stop, seg.Stop)
			if left >= right {
				continue
			}
			if !seg.On || seg.FX != 0 || seg.Group != 1 || seg.Space != 0 || seg.Rev || seg.Mirror || seg.Offset != 0 {
				return nil, "", fmt.Errorf("%w: target segment %d must be on, solid, ungrouped, unspaced, unreversed, and have compatible solid segment state", ErrLocateTopology, seg.ID)
			}
			commands = append(commands, physicalPixelCommand{
				SegmentID: seg.ID, Start: left - seg.Start, Stop: right - seg.Start,
			})
		}
	}
	if len(commands) == 0 {
		return nil, "", ErrNoDrawerLEDAllocation
	}
	return commands, fingerprint, nil
}

func (c *Client) setPhysicalPixels(ctx context.Context, controller db.Controller, segmentID int, start, stop int64, rgb []int) error {
	bounded, cancel := controllerRequestContext(ctx, controller)
	defer cancel()
	if segmentID < 0 || start < 0 || stop <= start || len(rgb) < 3 {
		return errors.New("invalid WLED pixel command")
	}
	for _, channel := range rgb[:3] {
		if channel < 0 || channel > 255 {
			return errors.New("invalid WLED pixel color")
		}
	}
	payload := map[string]any{"seg": []map[string]any{{
		"id": segmentID,
		"i":  []any{start, stop, rgb[:3]},
	}}}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(bounded, http.MethodPost, controllerURL(controller, "/json/state"), strings.NewReader(string(data)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return checkWLEDWriteResponse(resp)
}

func colorRGB(color string) []int {
	rgb, _ := HexToRGB(color)
	return rgb
}

func scaleRGB(rgb []int, brightness int) []int {
	out := make([]int, 3)
	for i := range out {
		out[i] = rgb[i] * brightness / 255
	}
	return out
}
