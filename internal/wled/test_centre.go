package wled

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tuxedocurly/wledger/internal/cabinetpixels"
	"github.com/tuxedocurly/wledger/internal/db"
)

// TestRequest uses zero-based controller physical pixel indexes at the HTTP
// boundary. Persisted ownership and drawer allocations remain one-based.
type TestRequest struct {
	ControllerID                             int64
	OutputOrder                              *int
	Start, Count, DrawerID, BinID, CabinetID int64
	Function, Mode                           string
	Style                                    FunctionStyle
	Duration                                 time.Duration
}

func (s *PhysicalLocateService) StartTest(ctx context.Context, in TestRequest, ownerID int64) (PhysicalLocateSession, error) {
	if ownerID <= 0 || in.Duration < time.Second || in.Duration > time.Hour || in.Style.Brightness < 0 || in.Style.Brightness > 255 {
		return PhysicalLocateSession{}, fmt.Errorf("%w: invalid test duration, user or brightness", ErrLocateTopology)
	}
	var err error
	in.Style.Color, err = NormalizeFunctionColor(in.Style.Color)
	if err != nil {
		return PhysicalLocateSession{}, err
	}
	switch in.Mode {
	case "solid", "flash", "sequence":
	default:
		return PhysicalLocateSession{}, fmt.Errorf("%w: invalid test mode", ErrLocateTopology)
	}
	switch in.Function {
	case "drawer_flash":
		in.Mode = "flash"
	case "drawer_sequence", "drawer_bins":
		in.Mode = "sequence"
	case "drawer_bin", "drawer_first", "drawer_last", "drawer_all", "locate_drawer", "test_pixel", "identify_pixel", "locate_cabinet", "locate_bin", "locate_part", "warning", "error", "success", "selected":
	default:
		return PhysicalLocateSession{}, fmt.Errorf("%w: invalid test function", ErrLocateTopology)
	}
	if in.CabinetID > 0 && (in.Function != "locate_cabinet" || in.DrawerID != 0 || in.BinID != 0 || in.OutputOrder != nil) {
		return PhysicalLocateSession{}, ErrLocateTopology
	}
	if in.Function == "locate_cabinet" && in.CabinetID <= 0 {
		return PhysicalLocateSession{}, ErrNoDrawerLEDAllocation
	}
	function := in.Function + ":" + in.Mode
	if in.Function == "drawer_bin" && (in.BinID <= 0 || in.DrawerID <= 0) {
		return PhysicalLocateSession{}, ErrPhysicalBinMissing
	}
	if in.BinID > 0 && in.Function != "drawer_bin" {
		return PhysicalLocateSession{}, fmt.Errorf("%w: a bin target requires drawer_bin", ErrLocateTopology)
	}
	if strings.HasPrefix(in.Function, "drawer_") || in.Function == "locate_drawer" {
		if in.DrawerID <= 0 {
			return PhysicalLocateSession{}, ErrNoDrawerLEDAllocation
		}
	}
	session, err := s.startPhysical(ctx, in.BinID, ownerID, function, in.Style.Brightness, in.Duration, func(q db.LEDQuerier) (resolvedPhysicalLocation, error) {
		var location resolvedPhysicalLocation
		if in.CabinetID > 0 {
			location, err = s.resolveTestCabinet(ctx, q, in)
		} else if in.DrawerID > 0 {
			location, err = s.resolveTestDrawer(ctx, q, in)
		} else {
			c, e := q.GetController(ctx, in.ControllerID)
			if e != nil {
				return location, e
			}
			if in.Start < 0 || in.Count < 1 || in.Count > c.LedCount || in.Start > c.LedCount-in.Count {
				return location, ErrLocateTopology
			}
			location = resolvedPhysicalLocation{controller: c, ranges: []ControllerRange{{Start: in.Start + 1, Count: in.Count}}, targets: []resolvedControllerTarget{{controller: c, ranges: []ControllerRange{{Start: in.Start + 1, Count: in.Count}}}}}
		}
		if err != nil {
			return location, err
		}
		if in.OutputOrder != nil {
			if *in.OutputOrder < 0 {
				return location, ErrLocateTopology
			}
			// Validate against WLED's real output array, never a logical string order.
			for _, target := range location.targets {
				if target.controller.ID != in.ControllerID {
					return location, fmt.Errorf("%w: selected output cannot cover a multi-controller drawer; choose whole controller", ErrLocateTopology)
				}
				_, cfg, _, _, e := s.client.inspectPhysicalController(ctx, target.controller, target.controller.LedCount, wholeControllerRange(target.ranges, target.controller.LedCount))
				if e != nil {
					return location, e
				}
				if *in.OutputOrder >= len(cfg.Hardware.LED.Inputs) {
					return location, ErrLocateTopology
				}
				output := cfg.Hardware.LED.Inputs[*in.OutputOrder]
				for _, r := range target.ranges {
					if output.Start == nil || r.Start-1 < *output.Start || r.Start-1+r.Count > *output.Start+output.Length {
						return location, fmt.Errorf("%w: range is outside the selected hardware output", ErrLocateTopology)
					}
				}
			}
		}
		location.color = in.Style.Color
		return location, nil
	})
	if err != nil {
		return session, err
	}
	if in.Mode != "solid" {
		go s.runTestPattern(session.ID, ownerID, in.Mode)
	}
	return session, nil
}

func (s *PhysicalLocateService) resolveTestCabinet(ctx context.Context, q db.LEDQuerier, in TestRequest) (resolvedPhysicalLocation, error) {
	cabinet, err := q.GetCabinet(ctx, in.CabinetID)
	if err != nil {
		return resolvedPhysicalLocation{}, err
	}
	if cabinet.Archived != 0 {
		return resolvedPhysicalLocation{}, ErrNoDrawerLEDAllocation
	}
	drawers, err := q.ListDrawersByCabinet(ctx, cabinet.ID)
	if err != nil {
		return resolvedPhysicalLocation{}, err
	}
	out := resolvedPhysicalLocation{cabinet: cabinet}
	byController := map[int64][]ControllerRange{}
	controllers := map[int64]db.Controller{}
	for _, drawer := range drawers {
		if drawer.Archived != 0 {
			continue
		}
		ranges, e := q.ListDrawerLEDAllocationsByDrawer(ctx, drawer.ID)
		if e != nil {
			return out, e
		}
		if len(ranges) == 0 {
			continue
		}
		request := in
		request.DrawerID, request.Function = drawer.ID, "drawer_all"
		location, e := s.resolveTestDrawer(ctx, q, request)
		if e != nil {
			return out, e
		}
		out.ranges = append(out.ranges, location.ranges...)
		for _, target := range location.targets {
			controllers[target.controller.ID] = target.controller
			byController[target.controller.ID] = append(byController[target.controller.ID], target.ranges...)
		}
	}
	// Cabinet tests target only saved allocations, never unassigned pixels.
	topology, err := cabinetpixels.Load(ctx, q, cabinet.ID)
	if err != nil {
		return out, err
	}
	for _, controller := range topology.Controllers {
		if ranges := byController[controller.Record.ID]; len(ranges) > 0 {
			out.targets = append(out.targets, resolvedControllerTarget{controller: controllers[controller.Record.ID], ranges: mergeControllerRanges(ranges)})
		}
	}
	if len(out.targets) == 0 {
		return out, ErrNoDrawerLEDAllocation
	}
	out.controller = out.targets[0].controller
	out.ranges = mergeControllerRanges(out.ranges)
	return out, nil
}

func (s *PhysicalLocateService) resolveTestDrawer(ctx context.Context, q db.LEDQuerier, in TestRequest) (resolvedPhysicalLocation, error) {
	var out resolvedPhysicalLocation
	drawer, err := q.GetDrawer(ctx, in.DrawerID)
	if err != nil {
		return out, err
	}
	cabinet, err := q.GetCabinet(ctx, drawer.CabinetID)
	if err != nil {
		return out, err
	}
	if drawer.Archived != 0 || cabinet.Archived != 0 {
		return out, ErrNoDrawerLEDAllocation
	}
	allocations, err := q.ListDrawerLEDAllocationsByDrawer(ctx, drawer.ID)
	if err != nil {
		return out, err
	}
	if len(allocations) == 0 {
		return out, ErrNoDrawerLEDAllocation
	}
	if in.BinID > 0 {
		return s.resolveWithQuerier(ctx, q, drawer.ID, in.BinID)
	}
	topology, err := cabinetpixels.Load(ctx, q, cabinet.ID)
	if err != nil {
		return out, err
	}
	ranges := []ControllerRange{}
	for _, a := range allocations {
		ranges = append(ranges, ControllerRange{Start: a.StartPixel, Count: a.PixelCount})
	}
	switch in.Function {
	case "drawer_first":
		ranges = []ControllerRange{{Start: ranges[0].Start, Count: 1}}
	case "drawer_last":
		last := ranges[len(ranges)-1]
		ranges = []ControllerRange{{Start: last.Start + last.Count - 1, Count: 1}}
	case "drawer_bins":
		ranges = nil
		attachments, e := q.ListBinDrawerAttachmentsByDrawer(ctx, drawer.ID)
		if e != nil {
			return out, e
		}
		for _, a := range attachments {
			location, e := s.resolveWithQuerier(ctx, q, drawer.ID, a.BinID)
			if e != nil {
				return out, e
			}
			// A bin without an explicit pixel still has the existing drawer-wide Locate
			// fallback. Do not pretend it has an independently verified bin pixel.
			mapped, e := q.GetPhysicalBinPixel(ctx, a.BinID)
			if errors.Is(e, sql.ErrNoRows) {
				continue
			}
			if e != nil {
				return out, e
			}
			if mapped.BinID != 0 {
				ranges = append(ranges, location.ranges...)
			}
		}
		if len(ranges) == 0 {
			return out, fmt.Errorf("%w: no bins have explicit pixel mappings", ErrNoDrawerLEDAllocation)
		}
		ranges = mergeControllerRanges(ranges)
	}
	local := map[int64][]ControllerRange{}
	for _, r := range ranges {
		pieces, e := cabinetpixels.SplitTopologyRange(topology, r.Start, r.Count)
		if e != nil {
			return out, e
		}
		for _, piece := range pieces {
			local[piece.ControllerID] = append(local[piece.ControllerID], ControllerRange{Start: piece.LocalStart, Count: piece.Count})
		}
	}
	targets := []resolvedControllerTarget{}
	for _, c := range topology.Controllers {
		if rs := local[c.Record.ID]; len(rs) > 0 {
			targets = append(targets, resolvedControllerTarget{controller: c.Record, ranges: mergeControllerRanges(rs)})
		}
	}
	if len(targets) == 0 {
		return out, ErrNoDrawerLEDAllocation
	}
	return resolvedPhysicalLocation{controller: targets[0].controller, drawer: drawer, cabinet: cabinet, ranges: ranges, targets: targets}, nil
}

func (s *PhysicalLocateService) runTestPattern(id string, ownerID int64, mode string) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	var step int64
	for range ticker.C {
		ctx, cancel := context.WithTimeout(context.Background(), physicalLocateControllerTimeout)
		done, err := s.testFrame(ctx, id, step, mode)
		cancel()
		if done || err != nil {
			// Startup recovery never restarts an illumination pattern. Only conservative
			// targeted cleanup survives process loss.
			cleanup, cancel := context.WithTimeout(context.Background(), physicalLocateCleanupRetryTimeout)
			cleanupErr := s.StopOperation(cleanup, id, ownerID)
			cancel()
			if err != nil {
				s.recordTestFailure(id, errors.Join(err, cleanupErr))
			}
			return
		}
		step++
	}
}

func (s *PhysicalLocateService) testFrame(ctx context.Context, id string, step int64, mode string) (bool, error) {
	if err := requirePhysicalOutput(); err != nil {
		return true, err
	}
	if err := lockPhysicalLocateCoordinator(ctx); err != nil {
		return false, err
	}
	defer physicalLocateCoordinator.Unlock()
	done := false
	err := db.WithLEDOperationClaim(ctx, s.store, func(q db.LEDQuerier) error {
		pending, e := q.PhysicalEmergencyPending(ctx)
		if e != nil {
			return e
		}
		if pending {
			done = true
			return nil
		}
		rows, e := q.ListPhysicalLocateSessions(ctx)
		if e != nil {
			return e
		}
		var current *db.PhysicalLocateSession
		for i := range rows {
			if rows[i].SessionID == id {
				current = &rows[i]
				break
			}
		}
		if current == nil || current.State != "active" {
			done = true
			return nil
		}
		metadata, e := q.GetPhysicalOperationMetadata(ctx, id)
		if e != nil {
			return e
		}
		if !metadata.ExpiresAt.After(time.Now()) {
			done = true
			return nil
		}
		targets, e := q.ListPhysicalLocateSessionTargets(ctx, id)
		if e != nil {
			return e
		}
		var total int64
		for _, target := range targets {
			var ranges []ControllerRange
			if e = decodeTestRanges(target.RangesJson, &ranges); e != nil {
				return e
			}
			total += countControllerRanges(ranges)
		}
		if total < 1 {
			return ErrNoDrawerLEDAllocation
		}
		chosen := step % total
		progress := fmt.Sprintf("%s frame %d acknowledged", mode, step)
		rgb := scaleRGB(colorRGB(current.Color), int(current.Brightness))
		for _, target := range targets {
			if target.MayHaveReceived == 0 {
				return errors.New("test no longer owns its target")
			}
			controller, e := q.GetController(ctx, target.ControllerID)
			if e != nil {
				return e
			}
			if controller.IpAddress != target.ControllerAddress || controllerPort(controller) != target.ControllerPort {
				return ErrLocateSessionStale
			}
			var ranges []ControllerRange
			if e = decodeTestRanges(target.RangesJson, &ranges); e != nil {
				return e
			}
			info, cfg, state, _, e := s.client.inspectPhysicalController(ctx, controller, controller.LedCount)
			if e != nil {
				return e
			}
			commands, fp, e := mapPhysicalRanges(ranges, controller, controller.LedCount, info, cfg, state, current.Color, int(current.Brightness))
			if e != nil {
				return e
			}
			if fp != target.Fingerprint {
				return ErrLocateSessionStale
			}
			color := rgb
			if mode == "sequence" || step%2 == 0 {
				color = []int{0, 0, 0}
			}
			for _, command := range commands {
				if e = s.client.setPhysicalPixels(ctx, controller, command.SegmentID, command.Start, command.Stop, color); e != nil {
					return e
				}
			}
			size := countControllerRanges(ranges)
			if mode == "sequence" && chosen >= 0 && chosen < size {
				n := chosen
				for _, r := range ranges {
					if n < r.Count {
						progress = fmt.Sprintf("sequence step %d acknowledged: controller #%d, absolute pixel %d (0-based)", step, controller.ID, r.Start+n-1)
						one, _, e := mapPhysicalRanges([]ControllerRange{{Start: r.Start + n, Count: 1}}, controller, controller.LedCount, info, cfg, state, current.Color, int(current.Brightness))
						if e != nil {
							return e
						}
						for _, command := range one {
							if e = s.client.setPhysicalPixels(ctx, controller, command.SegmentID, command.Start, command.Stop, rgb); e != nil {
								return e
							}
						}
						break
					}
					n -= r.Count
				}
			}
			chosen -= size
		}
		now := time.Now().UTC()
		_, e = q.UpdatePhysicalLocateSessionCAS(ctx, db.UpdatePhysicalLocateSessionCASParams{
			State: current.State, RangesJson: current.RangesJson, CommandsJson: current.CommandsJson, Fingerprint: current.Fingerprint,
			Color: current.Color, Brightness: current.Brightness, LastOperation: progress, LastOperationAt: now,
			LastSuccessfulOperation: progress, LastSuccessfulAt: now.Format(time.RFC3339Nano), LastError: "",
			ControllerID: current.ControllerID, SessionID: id, State_2: current.State,
		})
		return e
	})
	return done, err
}

func (s *PhysicalLocateService) recordTestFailure(id string, failure error) {
	ctx, cancel := context.WithTimeout(context.Background(), emergencyPersistenceTimeout)
	defer cancel()
	_ = db.WithLEDOperationClaim(ctx, s.store, func(q db.LEDQuerier) error {
		rows, err := q.ListPhysicalLocateSessions(ctx)
		if err != nil {
			return err
		}
		for _, current := range rows {
			if current.SessionID != id {
				continue
			}
			state := current.State
			if state == "stopped" {
				state = "failed"
			}
			_, err = q.UpdatePhysicalLocateSessionCAS(ctx, db.UpdatePhysicalLocateSessionCASParams{
				State: state, RangesJson: current.RangesJson, CommandsJson: current.CommandsJson, Fingerprint: current.Fingerprint,
				Color: current.Color, Brightness: current.Brightness, LastOperation: "test failed; targeted cleanup attempted", LastOperationAt: time.Now().UTC(),
				LastSuccessfulOperation: current.LastSuccessfulOperation, LastSuccessfulAt: current.LastSuccessfulAt, LastError: failure.Error(),
				ControllerID: current.ControllerID, SessionID: id, State_2: current.State,
			})
			return err
		}
		return nil
	})
}

func wholeControllerRange(ranges []ControllerRange, capacity int64) bool {
	return capacity > 0 && len(ranges) == 1 && ranges[0].Start == 1 && ranges[0].Count == capacity
}

func decodeTestRanges(raw string, out *[]ControllerRange) error {
	return json.Unmarshal([]byte(raw), out)
}

// IsPatternFunction is intentionally metadata-only: merely opening a page or
// loading a saved operation must never launch a physical test.
func IsPatternFunction(function string) bool {
	return strings.HasSuffix(function, ":flash") || strings.HasSuffix(function, ":sequence")
}
