package wled

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"sort"
	"strings"
	"time"

	"github.com/tuxedocurly/wledger/internal/db"
)

// ErrPhysicalOwnershipConflict also matches the historical Locate conflict.
var ErrPhysicalOwnershipConflict = ErrLocateSessionActive

type ControllerOffResult struct {
	ControllerID int64
	Address      string
	Port         int64
	Attempted    bool
	Acknowledged bool
	Error        string
}

// StartRanges uses controller-local, physical, one-based ranges, not cabinet
// offsets or WLED logical segment indices. Functions are ownership metadata;
// this API activates a solid color, not an unverified device effect.
func (s *PhysicalLocateService) StartRanges(ctx context.Context, controllerID int64, ranges []ControllerRange, ownerID int64, function, color string, brightness int, duration time.Duration) (PhysicalLocateSession, error) {
	if controllerID <= 0 || ownerID <= 0 || strings.TrimSpace(function) == "" || brightness < 0 || brightness > 255 || duration <= 0 {
		return PhysicalLocateSession{}, fmt.Errorf("invalid physical operation parameters")
	}
	if _, err := HexToRGB(color); err != nil {
		return PhysicalLocateSession{}, err
	}
	ranges = append([]ControllerRange(nil), ranges...)
	return s.startPhysical(ctx, 0, ownerID, function, brightness, duration, func(q db.LEDQuerier) (resolvedPhysicalLocation, error) {
		controller, err := q.GetController(ctx, controllerID)
		if err != nil {
			return resolvedPhysicalLocation{}, err
		}
		if len(ranges) == 0 {
			return resolvedPhysicalLocation{}, ErrNoDrawerLEDAllocation
		}
		for _, r := range ranges {
			if r.Start < 1 || r.Count < 1 || r.Count > controller.LedCount || r.Start-1 > controller.LedCount-r.Count {
				return resolvedPhysicalLocation{}, ErrLocateTopology
			}
		}
		ranges = mergeControllerRanges(ranges)
		return resolvedPhysicalLocation{controller: controller, ranges: ranges, color: color, targets: []resolvedControllerTarget{{controller: controller, ranges: ranges}}}, nil
	})
}

func (s *PhysicalLocateService) sessionByID(id string) *physicalSessionState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[id]
}

// StopOperation may perform only verified, persisted targeted cleanup even when
// new physical output is disabled, matching restart recovery and drawer Stop.
func (s *PhysicalLocateService) StopOperation(ctx context.Context, id string, ownerID int64) error {
	if id == "" || ownerID <= 0 {
		return fmt.Errorf("operation and authenticated owner are required")
	}
	if err := lockPhysicalLocateCoordinator(ctx); err != nil {
		return err
	}
	defer physicalLocateCoordinator.Unlock()
	if err := s.ensureLoaded(ctx); err != nil {
		return err
	}
	session := s.sessionByID(id)
	if session == nil {
		return nil
	}
	if session.OwnerID != ownerID {
		return ErrLocateSessionOwned
	}
	if !isActivePhysicalSession(session.State) {
		return nil
	}
	unlock := s.lockSessionTargets(session)
	defer unlock()
	return s.restoreSession(ctx, session)
}

// Operations returns active/unresolved operations and recent failed tests, so a
// background test error stays visible after acknowledged cleanup.
func (s *PhysicalLocateService) Operations(ctx context.Context) ([]PhysicalLocateSession, error) {
	if err := lockPhysicalLocateCoordinator(ctx); err != nil {
		return nil, err
	}
	defer physicalLocateCoordinator.Unlock()
	if err := s.ensureLoaded(ctx); err != nil {
		return nil, err
	}
	result := []PhysicalLocateSession{}
	for _, session := range s.sessions {
		if isActivePhysicalSession(session.State) || (session.State == "failed" && IsPatternFunction(session.Function) && session.LastError != "" && time.Since(session.LastOperationAt) < 24*time.Hour) {
			copy := session.PhysicalLocateSession
			copy.Ranges = append([]ControllerRange(nil), copy.Ranges...)
			copy.AffectedControllerIDs = append([]int64(nil), copy.AffectedControllerIDs...)
			result = append(result, copy)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func physicalOwnershipRanges(info physicalInfo, cfg physicalConfig, ranges []ControllerRange, capacity int64) ([]db.PhysicalOwnershipRange, error) {
	mac, err := NormalizeControllerMAC(info.MAC)
	if err != nil {
		return nil, fmt.Errorf("%w: controller must report a real MAC for physical ownership", ErrLocateTopology)
	}
	// Validate complete coverage using the same topology mapper as activation.
	if _, err := controllerRangesToWLEDAddresses(ranges, cfg.Hardware.LED.Inputs, capacity); err != nil {
		return nil, err
	}
	var claims []db.PhysicalOwnershipRange
	for _, r := range ranges {
		for _, output := range cfg.Hardware.LED.Inputs {
			left, right := max(r.Start-1, *output.Start), min(r.Start-1+r.Count, *output.Start+output.Length)
			if left >= right {
				continue
			}
			pins, err := activeOutputPins(output)
			if err != nil {
				return nil, err
			}
			key, _ := json.Marshal(pins)
			// Identity is the real device and electrical output pins plus the physical
			// offset on that output. Reversal changes API addresses, not ownership.
			claims = append(claims, db.PhysicalOwnershipRange{MAC: mac, Output: string(key), Start: left - *output.Start, Stop: right - *output.Start})
		}
	}
	return claims, nil
}

// rejectKnownPhysicalOverlap gives immediate conflict feedback while another
// process holds a network command claim. It is only an early rejection; the
// live MAC/output reservation is still repeated under the SQLite writer claim.
func rejectKnownPhysicalOverlap(ctx context.Context, q db.LEDQuerier, location resolvedPhysicalLocation) error {
	rows, err := q.ListPhysicalLocateSessions(ctx)
	if err != nil {
		return err
	}
	for _, session := range rows {
		if !isActivePhysicalSession(session.State) {
			continue
		}
		targets, err := q.ListPhysicalLocateSessionTargets(ctx, session.SessionID)
		if err != nil {
			return err
		}
		if len(targets) == 0 {
			return fmt.Errorf("%w: unresolved legacy operation %s", ErrLocateSessionActive, session.SessionID)
		}
		for _, saved := range targets {
			if session.State != "activating" && saved.MayHaveReceived == 0 {
				continue
			}
			var ranges []ControllerRange
			if err := json.Unmarshal([]byte(saved.RangesJson), &ranges); err != nil {
				return err
			}
			for _, desired := range location.targets {
				alias := saved.ControllerID == desired.controller.ID
				if !alias {
					old, err := q.GetController(ctx, saved.ControllerID)
					if err != nil {
						continue
					}
					alias = ConfiguredControllersAlias(old, desired.controller)
				}
				if !alias {
					continue
				}
				for _, old := range ranges {
					for _, requested := range desired.ranges {
						if requested.Start <= old.Start+old.Count-1 && old.Start <= requested.Start+requested.Count-1 {
							return fmt.Errorf("%w: physical pixels are owned by operation %s; stop that operation explicitly before reusing them", ErrLocateSessionActive, session.SessionID)
						}
					}
				}
			}
		}
	}
	return nil
}

func reservePhysicalOwnership(ctx context.Context, q db.LEDQuerier, session *physicalSessionState) error {
	pending, err := q.PhysicalEmergencyPending(ctx)
	if err != nil {
		return err
	}
	if pending {
		return fmt.Errorf("%w: emergency off recovery is pending", ErrPhysicalOwnershipConflict)
	}
	existing, err := q.ListPhysicalOwnershipRanges(ctx)
	if err != nil {
		return err
	}
	sessions, err := q.ListPhysicalLocateSessions(ctx)
	if err != nil {
		return err
	}
	identified := map[string]bool{}
	for _, r := range existing {
		identified[r.SessionID] = true
	}
	for _, old := range sessions {
		targets, err := q.ListPhysicalLocateSessionTargets(ctx, old.SessionID)
		if err != nil {
			return err
		}
		owns := isActivePhysicalSession(old.State)
		for _, target := range targets {
			owns = owns || target.MayHaveReceived != 0
		}
		m, err := q.GetPhysicalOperationMetadata(ctx, old.SessionID)
		if err != nil {
			return err
		}
		if owns && !identified[old.SessionID] && m.SessionID == "" {
			return fmt.Errorf("%w: legacy operation %s requires cleanup before concurrent ownership", ErrPhysicalOwnershipConflict, old.SessionID)
		}
	}
	var prepared []db.PhysicalOwnershipRange
	for _, target := range session.targets {
		prepared = append(prepared, target.ownership...)
	}
	for i, claim := range prepared {
		for _, other := range prepared[:i] {
			if claim.MAC == other.MAC && claim.Output == other.Output && claim.Start < other.Stop && other.Start < claim.Stop {
				return fmt.Errorf("%w: operation aliases its own physical pixels", ErrPhysicalOwnershipConflict)
			}
		}
	}
	for _, target := range session.targets {
		for _, claim := range target.ownership {
			for _, old := range existing {
				if claim.MAC == old.MAC && claim.Output == old.Output && claim.Start < old.Stop && old.Start < claim.Stop {
					return fmt.Errorf("%w: physical pixels owned by operation %s; Locate ownership is never overwritten", ErrPhysicalOwnershipConflict, old.SessionID)
				}
			}
		}
	}
	return nil
}

// EmergencyStatus returns the current emergency's outcomes and any unresolved
// older endpoints, including failures with no inventory operation. Read failures
// must be surfaced by the caller, never treated as an empty/successful status.
func (s *PhysicalLocateService) EmergencyStatus(ctx context.Context) ([]ControllerOffResult, error) {
	rows, err := s.store.ListPhysicalEmergencyOutcomes(ctx)
	if err != nil {
		return nil, err
	}
	results := make([]ControllerOffResult, 0, len(rows))
	for _, r := range rows {
		results = append(results, offResult(r))
	}
	return results, nil
}

func offResult(r db.PhysicalEmergencyOutcome) ControllerOffResult {
	return ControllerOffResult{ControllerID: r.ControllerID, Address: r.Address, Port: r.Port, Attempted: r.Attempted, Acknowledged: r.Acknowledged, Error: r.Error}
}

type emergencyEndpoint struct {
	controllerID int64
	address      string
	port         int64
}

func emergencyKey(r db.PhysicalEmergencyOutcome) emergencyEndpoint {
	return emergencyEndpoint{r.ControllerID, r.Address, r.Port}
}

const emergencyPersistenceTimeout = 5 * time.Second
const emergencyDeliveryClaimTimeout = 40 * time.Second

// EmergencyOff requires explicit user confirmation at the handler. Acceptance
// commits both cancellation fences and an endpoint manifest before any output.
// After that commit, request cancellation cannot abort later deliveries or roll
// back earlier acknowledgements. Each endpoint has independent bounded claims.
// Only endpoints still configured for the same controller are command-authorized.
// Historical endpoints are reported, never blindly contacted. Such endpoints
// retain the global barrier until explicit, identity-safe manual reconciliation
// and a successful retry; this method cannot authorize configuration repairs.
func (s *PhysicalLocateService) EmergencyOff(ctx context.Context) ([]ControllerOffResult, error) {
	if err := requirePhysicalOutput(); err != nil {
		return nil, err
	}
	if err := lockPhysicalLocateCoordinator(ctx); err != nil {
		return nil, err
	}
	defer physicalLocateCoordinator.Unlock()
	var plan []db.PhysicalEmergencyOutcome
	var emergencyID string
	authorized := map[emergencyEndpoint]bool{}
	err := db.WithLEDOperationClaim(ctx, s.store, func(q db.LEDQuerier) error {
		if err := q.FencePhysicalOperations(ctx); err != nil {
			return err
		}
		var err error
		emergencyID, err = q.PhysicalEmergencyID(ctx)
		if err != nil {
			return err
		}
		controllers, err := q.GetControllers(ctx)
		if err != nil {
			return err
		}
		endpoints := map[emergencyEndpoint]db.PhysicalEmergencyOutcome{}
		for _, c := range controllers {
			r := db.PhysicalEmergencyOutcome{ControllerID: c.ID, Address: c.IpAddress, Port: controllerPort(c)}
			key := emergencyKey(r)
			authorized[key] = true
			endpoints[key] = r
		}
		// Carry failures even if there was never a session, or configuration vanished.
		previous, err := q.ListPhysicalEmergencyOutcomes(ctx)
		if err != nil {
			return err
		}
		for _, r := range previous {
			if !r.Acknowledged {
				key := emergencyKey(r)
				if !authorized[key] {
					endpoints[key] = r
				}
			}
		}
		sessions, err := q.ListPhysicalLocateSessions(ctx)
		if err != nil {
			return err
		}
		for _, session := range sessions {
			targets, err := q.ListPhysicalLocateSessionTargets(ctx, session.SessionID)
			if err != nil {
				return err
			}
			for _, t := range targets {
				if t.MayHaveReceived != 0 {
					r := db.PhysicalEmergencyOutcome{ControllerID: t.ControllerID, Address: t.ControllerAddress, Port: t.ControllerPort}
					key := emergencyKey(r)
					if !authorized[key] {
						endpoints[key] = r
					}
				}
			}
		}
		for key, r := range endpoints {
			r.EmergencyID = emergencyID
			r.Attempted = false
			r.Acknowledged = false
			r.Error = "emergency off delivery pending"
			if !authorized[key] {
				r.Error = "unresolved saved endpoint is not currently configured for this controller; off unattempted; manual identity reconciliation and emergency retry required"
			}
			if err := q.SavePhysicalEmergencyOutcome(ctx, r); err != nil {
				return err
			}
			plan = append(plan, r)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(plan, func(i, j int) bool {
		a, b := plan[i], plan[j]
		if a.ControllerID != b.ControllerID {
			return a.ControllerID < b.ControllerID
		}
		if a.Address != b.Address {
			return a.Address < b.Address
		}
		return a.Port < b.Port
	})
	// A service-owned overall bound covers every controller's maximum request
	// timeout plus independent DB acquisition/result persistence budgets.
	serviceCtx, cancel := context.WithTimeout(context.Background(), time.Duration(len(plan)+1)*(emergencyDeliveryClaimTimeout+2*emergencyPersistenceTimeout))
	defer cancel()
	var infrastructure []error
	results := make([]ControllerOffResult, 0, len(plan))
	for _, r := range plan {
		if authorized[emergencyKey(r)] {
			r.Error = ""
			claimCtx, claimCancel := context.WithTimeout(serviceCtx, emergencyDeliveryClaimTimeout)
			commandErr := db.WithLEDOperationClaim(claimCtx, s.store, func(q db.LEDQuerier) error {
				if err := checkEmergencyGeneration(claimCtx, q, emergencyID); err != nil {
					return err
				}
				c, err := q.GetController(claimCtx, r.ControllerID)
				if err != nil {
					if !errors.Is(err, sql.ErrNoRows) {
						infrastructure = append(infrastructure, fmt.Errorf("reload controller #%d: %w", r.ControllerID, err))
					}
					r.Error = fmt.Sprintf("current controller unavailable; off unattempted: %v", err)
					return nil
				}
				if c.IpAddress != r.Address || controllerPort(c) != r.Port {
					r.Error = "configured endpoint changed after emergency acceptance; off unattempted"
					return nil
				}
				if err := requirePhysicalOutput(); err != nil {
					r.Error = err.Error()
					return nil
				}
				// Attempt evidence is durable even for an HTTP timeout. A marker write
				// failure must not suppress this safety-off or later controllers' attempts.
				r.Attempted = true
				marker := r
				marker.Error = "off attempted; acknowledgement pending"
				if err := q.SavePhysicalEmergencyOutcome(claimCtx, marker); err != nil {
					infrastructure = append(infrastructure, fmt.Errorf("record controller #%d attempt: %w", r.ControllerID, err))
				}
				deliveryCtx, deliveryCancel := controllerRequestContext(claimCtx, c)
				offErr := s.client.postStateAt(deliveryCtx, c.IpAddress, controllerPort(c), map[string]any{"on": false, "live": false, "tt": 0})
				deliveryCancel()
				r.Acknowledged = offErr == nil
				if offErr != nil {
					r.Error = offErr.Error()
				}
				// HTTP errors are delivery outcomes, not transaction aborts. In particular
				// their child timeout cannot cancel the claim or its attempted marker.
				return nil
			})
			claimCancel()
			if commandErr != nil {
				infrastructure = append(infrastructure, fmt.Errorf("controller #%d delivery claim: %w", r.ControllerID, commandErr))
				r.Acknowledged = false
				r.Error = fmt.Sprintf("delivery claim failed; off not durably confirmed: %v", commandErr)
			}
		}
		persistCtx, persistCancel := context.WithTimeout(serviceCtx, emergencyPersistenceTimeout)
		persistErr := db.WithLEDOperationClaim(persistCtx, s.store, func(q db.LEDQuerier) error {
			if err := checkEmergencyGeneration(persistCtx, q, emergencyID); err != nil {
				return err
			}
			if err := q.SavePhysicalEmergencyOutcome(persistCtx, r); err != nil {
				return err
			}
			if r.Acknowledged {
				return q.ResolvePhysicalControllerOff(persistCtx, r.ControllerID, r.Address, r.Port)
			}
			return nil
		})
		persistCancel()
		if persistErr != nil {
			infrastructure = append(infrastructure, fmt.Errorf("persist controller #%d endpoint %s:%d outcome: %w", r.ControllerID, r.Address, r.Port, persistErr))
			if r.Acknowledged {
				r.Error = "off acknowledged but durable ownership resolution failed"
			}
			r.Acknowledged = false
			r.Error = fmt.Sprintf("%s; outcome persistence failed: %v", r.Error, persistErr)
		}
		results = append(results, offResult(r))
	}
	finishCtx, finishCancel := context.WithTimeout(serviceCtx, emergencyPersistenceTimeout)
	finishErr := db.WithLEDOperationClaim(finishCtx, s.store, func(q db.LEDQuerier) error {
		if err := checkEmergencyGeneration(finishCtx, q, emergencyID); err != nil {
			return err
		}
		return q.FinishPhysicalEmergencyOff(finishCtx)
	})
	finishCancel()
	if finishErr != nil {
		infrastructure = append(infrastructure, fmt.Errorf("finalize emergency off: %w", finishErr))
	}
	// Refresh local reservations without reviving the canceled request context.
	loadCtx, loadCancel := context.WithTimeout(serviceCtx, emergencyPersistenceTimeout)
	if err := s.ensureLoaded(loadCtx); err != nil {
		infrastructure = append(infrastructure, fmt.Errorf("reload emergency ownership: %w", err))
	}
	loadCancel()
	return results, errors.Join(infrastructure...)
}

func checkEmergencyGeneration(ctx context.Context, q db.LEDQuerier, expected string) error {
	current, err := q.PhysicalEmergencyID(ctx)
	if err != nil {
		return err
	}
	pending, err := q.PhysicalEmergencyPending(ctx)
	if err != nil {
		return err
	}
	if current != expected || !pending {
		return fmt.Errorf("emergency off was superseded before delivery or persistence")
	}
	return nil
}
