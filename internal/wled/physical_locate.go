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
	"net/http"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tuxedocurly/wledger/internal/db"
)

var (
	ErrLocateMappingUnverified = errors.New("physical drawer Locate is disabled until an administrator verifies the controller's linear WLED output mapping")
	ErrPhysicalBinMissing      = errors.New("inventory bin has no physical drawer association")
	ErrNoDrawerLEDAllocation   = errors.New("physical drawer has no saved LED ranges")
	ErrLocateSessionOwned      = errors.New("active Locate session belongs to another user")
	ErrLocateSessionActive     = errors.New("stop the active drawer Locate before changing mapping verification")
	ErrLocateTopology          = errors.New("WLED segment or output topology is not safe for physical Locate")
	ErrLocateSessionStale      = errors.New("WLED configuration changed during the Locate session; cleanup was withheld to avoid writing to a different mapping")
	ErrHardwareBusy            = errors.New("controller is reserved by an active physical Locate session")
)

const locateBrightness = 128

var (
	locateSessionTimeout      = 2 * time.Minute
	physicalLocateCoordinator sync.Mutex
)

type ControllerRange struct {
	Start int64 `json:"start"` // controller-wide, 1-based
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
}

type resolvedPhysicalLocation struct {
	controller db.Controller
	drawer     db.Drawer
	cabinet    db.Cabinet
	ranges     []ControllerRange
	color      string
}

type physicalPixelCommand struct {
	SegmentID int   `json:"segment_id"`
	Start     int64 `json:"start"`
	Stop      int64 `json:"stop"`
}

type physicalSessionState struct {
	PhysicalLocateSession
	fingerprint string
	commands    []physicalPixelCommand
	timer       *time.Timer
}

type PhysicalLocateService struct {
	store  db.Store
	client *Client

	loadMu   sync.Mutex
	loadErr  error
	mu       sync.Mutex
	locks    map[int64]*sync.Mutex
	sessions map[int64]*physicalSessionState
}

func NewPhysicalLocateService(store db.Store, client *Client) *PhysicalLocateService {
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
	if drawerID <= 0 || binID <= 0 || ownerID <= 0 {
		return PhysicalLocateSession{}, errors.New("drawer, bin, and authenticated user are required")
	}
	return s.locate(ctx, drawerID, binID, ownerID)
}

func (s *PhysicalLocateService) locate(ctx context.Context, expectedDrawerID, binID, ownerID int64) (PhysicalLocateSession, error) {
	physicalLocateCoordinator.Lock()
	defer physicalLocateCoordinator.Unlock()
	if err := s.ensureLoaded(ctx); err != nil {
		return PhysicalLocateSession{}, fmt.Errorf("load prior Locate cleanup state: %w", err)
	}
	location, err := s.resolve(ctx, expectedDrawerID, binID)
	if err != nil {
		return PhysicalLocateSession{}, err
	}
	controllerID := location.controller.ID
	if oldControllerID, old := s.activeSession(); old != nil {
		if old.OwnerID != ownerID {
			return PhysicalLocateSession{}, ErrLocateSessionOwned
		}
		oldLock := s.controllerLock(oldControllerID)
		oldLock.Lock()
		current := s.getSession(oldControllerID)
		if current != nil && (current.State == "active" || current.State == "partial" || current.State == "activating" || current.State == "stopping") {
			if err := s.restoreSession(ctx, current); err != nil {
				oldLock.Unlock()
				return PhysicalLocateSession{}, fmt.Errorf("could not safely stop the previous drawer Locate: %w", err)
			}
		}
		oldLock.Unlock()
	}
	lock := s.controllerLock(controllerID)
	lock.Lock()
	defer lock.Unlock()

	if !location.controller.LocateMappingVerified {
		return PhysicalLocateSession{}, ErrLocateMappingUnverified
	}
	if len(location.ranges) == 0 {
		return PhysicalLocateSession{}, ErrNoDrawerLEDAllocation
	}
	info, cfg, state, mappingFingerprint, err := s.client.inspectPhysicalController(ctx, location.controller, location.controller.LedCount)
	if err != nil {
		return PhysicalLocateSession{}, err
	}
	if mappingFingerprint != location.controller.LocateMappingFingerprint {
		return PhysicalLocateSession{}, ErrLocateMappingUnverified
	}
	commands, fingerprint, err := mapPhysicalRanges(location.ranges, location.controller, location.controller.LedCount, info, cfg, state, location.color, locateBrightness)
	if err != nil {
		return PhysicalLocateSession{}, err
	}
	id, err := newLocateSessionID()
	if err != nil {
		return PhysicalLocateSession{}, fmt.Errorf("create Locate session: %w", err)
	}
	now := time.Now().UTC()
	session := &physicalSessionState{
		PhysicalLocateSession: PhysicalLocateSession{
			ID: id, ControllerID: controllerID, ControllerAddress: location.controller.IpAddress,
			ControllerPort: controllerPort(location.controller), DrawerID: location.drawer.ID, BinID: binID,
			CabinetName: location.cabinet.Name, DrawerName: location.drawer.Label, OwnerID: ownerID,
			State: "activating", Ranges: append([]ControllerRange(nil), location.ranges...),
			Color: location.color, Brightness: locateBrightness, LastOperation: "locate requested", LastOperationAt: now,
		},
		fingerprint: fingerprint,
		commands:    commands,
	}
	setPhysicalReservation(session.ID, session.ControllerID, session.ControllerAddress, session.ControllerPort, true)
	if err := s.persistSession(ctx, session); err != nil {
		setPhysicalReservation(session.ID, session.ControllerID, session.ControllerAddress, session.ControllerPort, false)
		return PhysicalLocateSession{}, fmt.Errorf("record Locate session before hardware request: %w", err)
	}
	s.setSession(controllerID, session)
	for _, command := range commands {
		if err := s.client.setPhysicalPixels(ctx, location.controller, command.SegmentID, command.Start, command.Stop, scaleRGB(colorRGB(location.color), locateBrightness)); err != nil {
			session.State = "partial"
			session.LastError = err.Error()
			session.LastOperation = "locate partially failed"
			session.LastOperationAt = time.Now().UTC()
			if cleanupErr := s.restoreSession(ctx, session); cleanupErr != nil {
				session.State = "partial"
				session.LastError = fmt.Sprintf("%v; cleanup not confirmed: %v", err, cleanupErr)
				_ = s.persistSession(context.Background(), session)
				return session.PhysicalLocateSession, fmt.Errorf("Locate partially failed and cleanup could not be confirmed: %w", cleanupErr)
			}
			session.State = "failed"
			session.LastError = err.Error()
			session.LastOperation = "Locate failed; WLED acknowledged range-off cleanup"
			session.LastOperationAt = time.Now().UTC()
			_ = s.persistSession(context.Background(), session)
			return PhysicalLocateSession{}, fmt.Errorf("Locate failed: %w", err)
		}
		session.LastOperation = "drawer LED range activated"
		session.LastOperationAt = time.Now().UTC()
		session.LastSuccessfulOperation = "drawer LED range activated"
		session.LastSuccessfulAt = session.LastOperationAt.Format(time.RFC3339Nano)
		if err := s.persistSession(ctx, session); err != nil {
			session.State = "partial"
			session.LastError = fmt.Sprintf("pixel activation was confirmed but session update failed: %v", err)
			if cleanupErr := s.restoreSession(ctx, session); cleanupErr != nil {
				return session.PhysicalLocateSession, fmt.Errorf("session update failed and cleanup was not confirmed: %w", cleanupErr)
			}
			return PhysicalLocateSession{}, fmt.Errorf("activation was cleaned up because session update failed: %w", err)
		}
	}
	session.State = "active"
	session.LastError = ""
	session.LastOperation = "drawer LED ranges activated"
	session.LastOperationAt = time.Now().UTC()
	if err := s.persistSession(ctx, session); err != nil {
		if cleanupErr := s.restoreSession(ctx, session); cleanupErr != nil {
			return session.PhysicalLocateSession, fmt.Errorf("Locate state could not be persisted and cleanup was not confirmed: %w", cleanupErr)
		}
		return PhysicalLocateSession{}, fmt.Errorf("Locate activation was cleaned up because its state could not be persisted: %w", err)
	}
	sessionID := session.ID
	session.timer = time.AfterFunc(locateSessionTimeout, func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.stopControllerSession(stopCtx, controllerID, ownerID, sessionID, 0)
	})
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
	lock := s.controllerLock(controllerID)
	lock.Lock()
	session := s.getSession(controllerID)
	active := session != nil && (session.State == "active" || session.State == "partial" || session.State == "activating" || session.State == "stopping")
	lock.Unlock()
	if active {
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
	rows, err := s.store.UpdateControllerLocateMappingVerification(ctx, db.UpdateControllerLocateMappingVerificationParams{
		LocateMappingVerified: verified, LocateMappingFingerprint: fingerprint, ID: controllerID,
	})
	if err != nil {
		return err
	}
	if rows != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *PhysicalLocateService) resolve(ctx context.Context, expectedDrawerID, binID int64) (resolvedPhysicalLocation, error) {
	var result resolvedPhysicalLocation
	err := s.store.ExecTx(ctx, func(q db.Querier) error {
		attachment, err := q.GetBinDrawerAttachment(ctx, binID)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: bin %d", ErrPhysicalBinMissing, binID)
		}
		if err != nil {
			return fmt.Errorf("resolve physical bin attachment: %w", err)
		}
		if expectedDrawerID != 0 && attachment.DrawerID != expectedDrawerID {
			return fmt.Errorf("bin %d is not physically attached to drawer %d", binID, expectedDrawerID)
		}
		drawer, err := q.GetDrawer(ctx, attachment.DrawerID)
		if err != nil {
			return fmt.Errorf("physical drawer not found: %w", err)
		}
		if drawer.Archived != 0 {
			return fmt.Errorf("physical drawer %d is archived", drawer.ID)
		}
		cabinet, err := q.GetCabinet(ctx, drawer.CabinetID)
		if err != nil {
			return fmt.Errorf("physical cabinet not found: %w", err)
		}
		if cabinet.Archived != 0 {
			return fmt.Errorf("physical cabinet %d is archived", cabinet.ID)
		}
		if !cabinet.ControllerID.Valid {
			return fmt.Errorf("cabinet %q has no assigned WLED controller", cabinet.Name)
		}
		controller, err := q.GetController(ctx, cabinet.ControllerID.Int64)
		if err != nil {
			return fmt.Errorf("assigned WLED controller not found: %w", err)
		}
		if !controller.LocateMappingVerified || strings.TrimSpace(controller.LocateMappingFingerprint) == "" {
			return ErrLocateMappingUnverified
		}
		if controller.LedCount <= 0 {
			return fmt.Errorf("controller %q has no configured LED capacity", controller.Name)
		}
		allocations, err := q.ListDrawerLEDAllocationsByDrawer(ctx, drawer.ID)
		if err != nil {
			return fmt.Errorf("read saved drawer LED ranges: %w", err)
		}
		ranges := make([]ControllerRange, 0, len(allocations))
		for _, allocation := range allocations {
			if allocation.CabinetID != cabinet.ID || allocation.ControllerID != controller.ID || allocation.DrawerID != drawer.ID {
				return fmt.Errorf("saved LED range %d has stale cabinet/controller ownership", allocation.ID)
			}
			if allocation.StartPixel < 1 || allocation.PixelCount < 1 || allocation.StartPixel-1 > controller.LedCount-allocation.PixelCount {
				return fmt.Errorf("saved LED range %d is outside the current controller capacity", allocation.ID)
			}
			ranges = append(ranges, ControllerRange{Start: allocation.StartPixel, Count: allocation.PixelCount})
		}
		settings, err := q.GetSettings(ctx)
		if err != nil {
			return fmt.Errorf("read Locate color setting: %w", err)
		}
		color := strings.TrimSpace(settings.ColorLocate.String)
		if color == "" {
			color = "#0000FF"
		}
		if _, err := HexToRGB(color); err != nil {
			return fmt.Errorf("invalid configured Locate color: %w", err)
		}
		result = resolvedPhysicalLocation{controller: controller, drawer: drawer, cabinet: cabinet, ranges: ranges, color: color}
		return nil
	})
	if err != nil {
		return resolvedPhysicalLocation{}, err
	}
	return result, nil
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
	s.mu.Lock()
	var controllerIDs []int64
	for id, session := range s.sessions {
		if session.DrawerID == drawerID {
			controllerIDs = append(controllerIDs, id)
		}
	}
	s.mu.Unlock()
	for _, controllerID := range controllerIDs {
		if err := s.stopControllerSessionLocked(ctx, controllerID, ownerID, "", drawerID); err != nil {
			return err
		}
	}
	return nil // repeated Stop is idempotent; no hardware command is needed.
}

func (s *PhysicalLocateService) stopControllerSession(ctx context.Context, controllerID, ownerID int64, expectedSessionID string, expectedDrawerID int64) error {
	physicalLocateCoordinator.Lock()
	defer physicalLocateCoordinator.Unlock()
	if err := s.ensureLoaded(ctx); err != nil {
		return fmt.Errorf("load prior Locate cleanup state: %w", err)
	}
	return s.stopControllerSessionLocked(ctx, controllerID, ownerID, expectedSessionID, expectedDrawerID)
}

func (s *PhysicalLocateService) stopControllerSessionLocked(ctx context.Context, controllerID, ownerID int64, expectedSessionID string, expectedDrawerID int64) error {
	lock := s.controllerLock(controllerID)
	lock.Lock()
	defer lock.Unlock()
	session := s.getSession(controllerID)
	if session == nil || (session.State != "active" && session.State != "partial" && session.State != "activating" && session.State != "stopping") {
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
	if err := s.restoreSession(ctx, session); err != nil {
		return err
	}
	return nil
}

func (s *PhysicalLocateService) restoreSession(ctx context.Context, session *physicalSessionState) error {
	if session.timer != nil {
		session.timer.Stop()
		session.timer = nil
	}
	controller, err := s.store.GetController(ctx, session.ControllerID)
	if err != nil {
		session.State = "partial"
		session.LastError = "controller is unavailable; restore was not confirmed"
		session.LastOperation = "cleanup could not reach controller"
		session.LastOperationAt = time.Now().UTC()
		_ = s.persistSession(context.Background(), session)
		s.scheduleCleanupRetry(session)
		return fmt.Errorf("controller is unavailable: %w", err)
	}
	if controller.IpAddress != session.ControllerAddress || controllerPort(controller) != session.ControllerPort {
		session.State = "partial"
		session.LastError = ErrLocateSessionStale.Error() + ": controller address or port changed"
		session.LastOperation = "cleanup blocked by changed controller identity"
		session.LastOperationAt = time.Now().UTC()
		_ = s.persistSession(context.Background(), session)
		s.scheduleCleanupRetry(session)
		return ErrLocateSessionStale
	}
	info, cfg, state, mappingFingerprint, err := s.client.inspectPhysicalController(ctx, controller, controller.LedCount)
	if err != nil {
		session.State = "partial"
		session.LastError = err.Error()
		session.LastOperation = "cleanup could not verify controller state"
		session.LastOperationAt = time.Now().UTC()
		_ = s.persistSession(context.Background(), session)
		s.scheduleCleanupRetry(session)
		return fmt.Errorf("cannot verify controller state before cleanup: %w", err)
	}
	if mappingFingerprint != controller.LocateMappingFingerprint {
		session.State = "partial"
		session.LastError = ErrLocateSessionStale.Error() + ": verified output configuration changed"
		session.LastOperation = "cleanup blocked by changed output mapping"
		session.LastOperationAt = time.Now().UTC()
		_ = s.persistSession(context.Background(), session)
		s.scheduleCleanupRetry(session)
		return ErrLocateSessionStale
	}
	expectedCommands, fingerprint, err := mapPhysicalRanges(session.Ranges, controller, controller.LedCount, info, cfg, state, session.Color, session.Brightness)
	if err != nil || fingerprint != session.fingerprint || !reflect.DeepEqual(expectedCommands, session.commands) {
		session.State = "partial"
		session.LastError = ErrLocateSessionStale.Error()
		session.LastOperation = "cleanup blocked by changed WLED configuration"
		session.LastOperationAt = time.Now().UTC()
		_ = s.persistSession(context.Background(), session)
		s.scheduleCleanupRetry(session)
		return ErrLocateSessionStale
	}
	session.State = "stopping"
	session.LastOperation = "cleanup requested"
	session.LastOperationAt = time.Now().UTC()
	if err := s.persistSession(ctx, session); err != nil {
		session.State = "partial"
		session.LastError = fmt.Sprintf("cleanup ownership could not be saved: %v", err)
		session.LastOperation = "cleanup withheld because session ownership was not confirmed"
		session.LastOperationAt = time.Now().UTC()
		s.scheduleCleanupRetry(session)
		return fmt.Errorf("cleanup ownership could not be saved: %w", err)
	}
	for _, command := range session.commands {
		if err := s.client.setPhysicalPixels(ctx, controller, command.SegmentID, command.Start, command.Stop, []int{0, 0, 0}); err != nil {
			session.State = "partial"
			session.LastError = fmt.Sprintf("cleanup not confirmed: %v", err)
			session.LastOperation = "cleanup failed"
			session.LastOperationAt = time.Now().UTC()
			_ = s.persistSession(context.Background(), session)
			s.scheduleCleanupRetry(session)
			return fmt.Errorf("cleanup not confirmed: %w", err)
		}
		session.LastSuccessfulOperation = "WLED acknowledged a targeted range-off command"
		session.LastSuccessfulAt = time.Now().UTC().Format(time.RFC3339Nano)
		session.LastOperation = session.LastSuccessfulOperation
		session.LastOperationAt = time.Now().UTC()
		_ = s.persistSession(context.Background(), session)
	}
	session.State = "stopped"
	session.LastError = ""
	session.LastOperation = "WLED acknowledged range-off commands for this drawer"
	session.LastOperationAt = time.Now().UTC()
	session.LastSuccessfulOperation = "WLED acknowledged range-off commands for this drawer"
	session.LastSuccessfulAt = session.LastOperationAt.Format(time.RFC3339Nano)
	if err := s.persistSession(ctx, session); err != nil {
		session.State = "partial"
		session.LastError = fmt.Sprintf("WLED acknowledged range-off commands, but durable session finalization failed: %v", err)
		session.LastOperation = "range-off acknowledged; durable session finalization pending"
		session.LastOperationAt = time.Now().UTC()
		s.scheduleCleanupRetry(session)
		return fmt.Errorf("WLED acknowledged cleanup but session state could not be saved: %w", err)
	}
	return nil
}

func (s *PhysicalLocateService) scheduleCleanupRetry(session *physicalSessionState) {
	if session.timer != nil {
		session.timer.Stop()
	}
	controllerID, ownerID, sessionID := session.ControllerID, session.OwnerID, session.ID
	session.timer = time.AfterFunc(15*time.Second, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.stopControllerSession(ctx, controllerID, ownerID, sessionID, 0)
	})
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
			s.loadErr = fmt.Errorf("decode saved ranges for controller %d: %w", row.ControllerID, err)
			return s.loadErr
		}
		if err := json.Unmarshal([]byte(row.CommandsJson), &commands); err != nil {
			s.loadErr = fmt.Errorf("decode saved cleanup commands for controller %d: %w", row.ControllerID, err)
			return s.loadErr
		}
		loaded[row.ControllerID] = &physicalSessionState{
			PhysicalLocateSession: PhysicalLocateSession{
				ID: row.SessionID, ControllerID: row.ControllerID, ControllerAddress: row.ControllerAddress,
				ControllerPort: row.ControllerPort, DrawerID: row.DrawerID, BinID: row.BinID,
				OwnerID: row.OwnerID, State: row.State, Ranges: ranges, Color: row.Color,
				Brightness: int(row.Brightness), LastOperation: row.LastOperation,
				LastOperationAt: row.LastOperationAt, LastSuccessfulOperation: row.LastSuccessfulOperation,
				LastSuccessfulAt: row.LastSuccessfulAt, LastError: row.LastError,
			},
			fingerprint: row.Fingerprint, commands: commands,
		}
	}
	s.loadErr = nil
	s.mu.Lock()
	cachedIDs := make([]int64, 0, len(s.sessions))
	for id := range s.sessions {
		cachedIDs = append(cachedIDs, id)
	}
	s.mu.Unlock()
	for id := range loaded {
		found := false
		for _, cachedID := range cachedIDs {
			if cachedID == id {
				found = true
				break
			}
		}
		if !found {
			cachedIDs = append(cachedIDs, id)
		}
	}
	for _, id := range cachedIDs {
		lock := s.controllerLock(id)
		lock.Lock()
		s.mu.Lock()
		old := s.sessions[id]
		fresh := loaded[id]
		if fresh == nil {
			if old != nil && old.timer != nil {
				old.timer.Stop()
			}
			delete(s.sessions, id)
			s.mu.Unlock()
			lock.Unlock()
			if old != nil {
				setPhysicalReservation(old.ID, old.ControllerID, old.ControllerAddress, old.ControllerPort, false)
			}
			continue
		}
		if old != nil && old.ID == fresh.ID {
			timer := old.timer
			if old.State == "partial" && strings.Contains(old.LastOperation, "finalization pending") && fresh.State == "stopping" && fresh.LastError == "" {
				fresh.State = "partial"
				fresh.LastError = old.LastError
				fresh.LastOperation = old.LastOperation
				fresh.LastOperationAt = old.LastOperationAt
			}
			*old = *fresh
			old.timer = timer
			fresh = old
		} else {
			if old != nil && old.timer != nil {
				old.timer.Stop()
			}
			s.sessions[id] = fresh
		}
		s.mu.Unlock()
		active := fresh.State == "active" || fresh.State == "partial" || fresh.State == "activating" || fresh.State == "stopping"
		if !active && fresh.timer != nil {
			fresh.timer.Stop()
			fresh.timer = nil
		} else if active && fresh.timer == nil {
			remaining := time.Until(fresh.LastOperationAt.Add(locateSessionTimeout))
			if remaining < 0 {
				remaining = 0
			}
			controllerID, ownerID, sessionID := id, fresh.OwnerID, fresh.ID
			fresh.timer = time.AfterFunc(remaining, func() {
				stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = s.stopControllerSession(stopCtx, controllerID, ownerID, sessionID, 0)
			})
		}
		lock.Unlock()
		active = fresh.State == "active" || fresh.State == "partial" || fresh.State == "activating" || fresh.State == "stopping"
		setPhysicalReservation(fresh.ID, fresh.ControllerID, fresh.ControllerAddress, fresh.ControllerPort, active)
	}
	return nil
}

func (s *PhysicalLocateService) persistSession(ctx context.Context, session *physicalSessionState) error {
	ranges, err := json.Marshal(session.Ranges)
	if err != nil {
		return err
	}
	commands, err := json.Marshal(session.commands)
	if err != nil {
		return err
	}
	rows, err := s.store.SavePhysicalLocateSession(ctx, db.SavePhysicalLocateSessionParams{
		ControllerID: session.ControllerID, ControllerAddress: session.ControllerAddress,
		ControllerPort: session.ControllerPort, SessionID: session.ID, DrawerID: session.DrawerID,
		BinID: session.BinID, OwnerID: session.OwnerID, State: session.State,
		RangesJson: string(ranges), CommandsJson: string(commands), Fingerprint: session.fingerprint,
		Color: session.Color, Brightness: int64(session.Brightness), LastOperation: session.LastOperation,
		LastOperationAt: session.LastOperationAt, LastSuccessfulOperation: session.LastSuccessfulOperation,
		LastSuccessfulAt: session.LastSuccessfulAt, LastError: session.LastError,
	})
	if err != nil {
		return err
	}
	if rows != 1 {
		return errors.New("another Locate session owns this controller")
	}
	active := session.State == "active" || session.State == "partial" || session.State == "activating" || session.State == "stopping"
	setPhysicalReservation(session.ID, session.ControllerID, session.ControllerAddress, session.ControllerPort, active)
	return nil
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
	url := controllerURL(controller, path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP status %d", resp.StatusCode)
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, controllerURL(controller, "/json/state"), strings.NewReader(string(data)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("WLED returned HTTP status %d", resp.StatusCode)
	}
	var response struct {
		Success *bool `json:"success"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return fmt.Errorf("decode WLED command acknowledgement: %w", err)
	}
	if response.Success == nil || !*response.Success {
		return errors.New("WLED did not confirm the pixel command")
	}
	return nil
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
