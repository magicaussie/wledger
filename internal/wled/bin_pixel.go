package wled

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"strings"

	"github.com/tuxedocurly/wledger/internal/cabinetpixels"
	"github.com/tuxedocurly/wledger/internal/db"
)

// ClearBinPixel permits correction of stale mappings without requiring the old
// topology to remain valid. It is idempotent and never changes electrical bins
// or allocations. Current attachment ownership is still required.
func ClearBinPixel(ctx context.Context, store db.Store, binID, drawerID int64) error {
	if binID <= 0 || drawerID <= 0 {
		return fmt.Errorf("%w: bin and drawer IDs must be positive", ErrLocateTopology)
	}
	return db.WithLEDOperationClaim(ctx, store, func(q db.LEDQuerier) error {
		owned, err := db.HasPhysicalLocateOwnership(ctx, q)
		if err != nil {
			return err
		}
		if owned {
			return db.ErrPhysicalLocateActive
		}
		attachment, err := q.GetBinDrawerAttachment(ctx, binID)
		if err != nil {
			return err
		}
		if attachment.DrawerID != drawerID {
			return fmt.Errorf("%w: bin is not attached to this drawer", ErrLocateTopology)
		}
		if _, err = q.GetDrawer(ctx, drawerID); err != nil {
			return err
		}
		return q.DeletePhysicalBinPixel(ctx, binID)
	})
}

// ConfiguredControllersAlias uses only provable stored identity; it performs no
// DNS or hardware discovery. Unknown host aliases are not assumed equivalent.
func ConfiguredControllersAlias(a, b db.Controller) bool {
	if a.ID == b.ID {
		return true
	}
	am, ae := NormalizeControllerMAC(a.MacAddress.String)
	bm, be := NormalizeControllerMAC(b.MacAddress.String)
	if ae == nil && be == nil && am == bm {
		return true
	}
	ap, bp := int64(80), int64(80)
	if a.Port.Valid {
		ap = a.Port.Int64
	}
	if b.Port.Valid {
		bp = b.Port.Int64
	}
	if ap != bp {
		return false
	}
	ah, bh := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(a.IpAddress)), "."), strings.TrimSuffix(strings.ToLower(strings.TrimSpace(b.IpAddress)), ".")
	if ah != "" && ah == bh {
		return true
	}
	ai, bi := net.ParseIP(ah), net.ParseIP(bh)
	if ai != nil && bi != nil && ai.Equal(bi) {
		return true
	}
	return (ah == "localhost" && bi != nil && bi.IsLoopback()) || (bh == "localhost" && ai != nil && ai.IsLoopback())
}

// BinPixelTopologyFingerprint excludes labels, verification flags and UI
// geometry, but captures electrical order, output permutations and controller
// identity/address. Electrical changes invalidate overrides instead of silently
// assigning a different physical LED.
func BinPixelTopologyFingerprint(t cabinetpixels.Topology) string {
	type output struct {
		ID, Start, Count, OutputID int64
		Direction, Pattern         string
		Rows, Columns              int64
		Order                      []int64
	}
	type controller struct {
		ID, Start, Count, Port int64
		Address, MAC           string
		HardwareFingerprint    string
		Outputs                []output
	}
	signature := struct {
		CabinetID   int64
		Controllers []controller
	}{CabinetID: t.CabinetID}
	for _, c := range t.Controllers {
		port := int64(80)
		if c.Record.Port.Valid {
			port = c.Record.Port.Int64
		}
		item := controller{ID: c.Record.ID, Start: c.Start, Count: c.Pixels, Port: port, Address: c.Record.IpAddress, MAC: c.Record.MacAddress.String, HardwareFingerprint: c.Record.LocateMappingFingerprint}
		for _, s := range c.Strings {
			item.Outputs = append(item.Outputs, output{s.Record.ID, s.ControllerStart, s.Record.PixelCount, s.Record.ControllerOutputID.Int64, s.Direction, s.Pattern, s.Rows, s.Columns, s.CustomOrder})
		}
		signature.Controllers = append(signature.Controllers, item)
	}
	raw, _ := json.Marshal(signature)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// ResolveBinPixelOverride returns exactly one cabinet pixel for a configured bin
// and a zero-based drawer-relative index in persisted allocation order. Missing
// overrides return sql.ErrNoRows, allowing the historical drawer-wide fallback.
func ResolveBinPixelOverride(ctx context.Context, q db.LEDQuerier, binID, drawerID int64, t cabinetpixels.Topology, allocations []db.DrawerLedAllocation) (db.PhysicalBinPixel, int64, int64, error) {
	p, err := q.GetPhysicalBinPixel(ctx, binID)
	if err != nil {
		return p, 0, 0, err
	}
	stale := func() (db.PhysicalBinPixel, int64, int64, error) {
		return p, 0, 0, fmt.Errorf("%w: bin %d pixel override is stale; explicitly reassign it before Locate", ErrLocateTopology, binID)
	}
	if p.DrawerID != drawerID || p.TopologyFingerprint != BinPixelTopologyFingerprint(t) {
		return stale()
	}
	relative := int64(0)
	for _, a := range allocations {
		if a.DrawerID != drawerID || a.CabinetID != t.CabinetID || a.PixelCount < 1 {
			return stale()
		}
		if a.ID == p.AllocationID {
			if p.Offset < 0 || p.Offset >= a.PixelCount || a.StartPixel < 1 || p.Offset > math.MaxInt64-a.StartPixel {
				return stale()
			}
			cabinetPixel := a.StartPixel + p.Offset
			pieces, e := cabinetpixels.SplitTopologyRange(t, cabinetPixel, 1)
			if e != nil || len(pieces) != 1 || pieces[0].ControllerID != p.ControllerID || pieces[0].LocalStart-1 != p.Pixel || p.Offset > math.MaxInt64-relative {
				return stale()
			}
			return p, cabinetPixel, relative + p.Offset, nil
		}
		if a.PixelCount > math.MaxInt64-relative {
			return stale()
		}
		relative += a.PixelCount
	}
	return stale()
}

// AssignBinPixel validates and persists a controller-absolute zero-based pixel.
// It never contacts WLED or touches legacy electrical bin indexes.
func AssignBinPixel(ctx context.Context, store db.Store, binID, drawerID, controllerID, pixel int64) (db.PhysicalBinPixel, int64, error) {
	var result db.PhysicalBinPixel
	var relative int64
	err := db.WithLEDOperationClaim(ctx, store, func(q db.LEDQuerier) error {
		if binID <= 0 || drawerID <= 0 || controllerID <= 0 || pixel < 0 {
			return fmt.Errorf("%w: invalid bin pixel coordinates", ErrLocateTopology)
		}
		owned, err := db.HasPhysicalLocateOwnership(ctx, q)
		if err != nil {
			return err
		}
		if owned {
			return db.ErrPhysicalLocateActive
		}
		attachment, err := q.GetBinDrawerAttachment(ctx, binID)
		if err != nil {
			return err
		}
		if attachment.DrawerID != drawerID {
			return fmt.Errorf("%w: bin is not attached to this drawer", ErrLocateTopology)
		}
		drawer, err := q.GetDrawer(ctx, drawerID)
		if err != nil {
			return err
		}
		cab, err := q.GetCabinet(ctx, drawer.CabinetID)
		if err != nil {
			return err
		}
		if drawer.Archived != 0 || cab.Archived != 0 {
			return fmt.Errorf("%w: archived cabinet/drawer", ErrLocateTopology)
		}
		t, err := cabinetpixels.Load(ctx, q, cab.ID)
		if err != nil {
			return err
		}
		allocations, err := q.ListDrawerLEDAllocationsByDrawer(ctx, drawerID)
		if err != nil {
			return err
		}
		matches := 0
		relative = 0
		prefix := int64(0)
		for _, a := range allocations {
			if a.CabinetID != cab.ID || a.DrawerID != drawerID || a.StartPixel < 1 || a.PixelCount < 1 || a.PixelCount > math.MaxInt64-a.StartPixel+1 || a.PixelCount > math.MaxInt64-prefix {
				return fmt.Errorf("%w: invalid allocation", ErrLocateTopology)
			}
			pieces, err := cabinetpixels.SplitTopologyRange(t, a.StartPixel, a.PixelCount)
			if err != nil {
				return err
			}
			for _, piece := range pieces {
				if piece.ControllerID != controllerID || pixel < piece.LocalStart-1 || pixel >= piece.LocalStart-1+piece.Count {
					continue
				}
				// A reversed linear span has ascending physical addresses but descending
				// logical addresses. Invert through single-pixel canonical mapping.
				for n := int64(0); n < piece.Count; n++ {
					logical := piece.CabinetStart + n
					one, e := cabinetpixels.SplitTopologyRange(t, logical, 1)
					if e != nil {
						return e
					}
					if len(one) == 1 && one[0].ControllerID == controllerID && one[0].LocalStart-1 == pixel {
						offset := logical - a.StartPixel
						result = db.PhysicalBinPixel{BinID: binID, DrawerID: drawerID, AllocationID: a.ID, Offset: offset, ControllerID: controllerID, Pixel: pixel, TopologyFingerprint: BinPixelTopologyFingerprint(t)}
						relative = prefix + offset
						matches++
					}
				}
			}
			prefix += a.PixelCount
		}
		if matches != 1 {
			return fmt.Errorf("%w: pixel must resolve uniquely inside this drawer's saved ranges", ErrLocateTopology)
		}
		allAllocations, err := q.ListAllDrawerLEDAllocations(ctx)
		if err != nil {
			return err
		}
		selectedController, err := q.GetController(ctx, controllerID)
		if err != nil {
			return err
		}
		topologies := map[int64]cabinetpixels.Topology{cab.ID: t}
		for _, a := range allAllocations {
			if a.DrawerID == drawerID {
				continue
			}
			otherTopology, ok := topologies[a.CabinetID]
			if !ok {
				otherTopology, err = cabinetpixels.Load(ctx, q, a.CabinetID)
				if err != nil {
					return fmt.Errorf("%w: cannot validate existing allocation %d: %v", ErrLocateTopology, a.ID, err)
				}
				topologies[a.CabinetID] = otherTopology
			}
			pieces, e := cabinetpixels.SplitTopologyRange(otherTopology, a.StartPixel, a.PixelCount)
			if e != nil {
				return fmt.Errorf("%w: cannot validate existing allocation %d: %v", ErrLocateTopology, a.ID, e)
			}
			for _, piece := range pieces {
				if pixel < piece.LocalStart-1 || pixel >= piece.LocalStart-1+piece.Count {
					continue
				}
				otherController, e := q.GetController(ctx, piece.ControllerID)
				if e != nil {
					return e
				}
				if ConfiguredControllersAlias(selectedController, otherController) {
					return fmt.Errorf("%w: pixel overlaps drawer %d allocation", ErrLocateTopology, a.DrawerID)
				}
			}
		}
		others, err := q.ListPhysicalBinPixels(ctx)
		if err != nil {
			return err
		}
		for _, p := range others {
			alias := p.ControllerID == controllerID
			if p.BinID != binID && p.Pixel == pixel && !alias {
				a, e := q.GetController(ctx, p.ControllerID)
				if e != nil {
					return e
				}
				b, e := q.GetController(ctx, controllerID)
				if e != nil {
					return e
				}
				alias = ConfiguredControllersAlias(a, b)
			}
			if p.BinID != binID && ((alias && p.Pixel == pixel) || (p.AllocationID == result.AllocationID && p.Offset == result.Offset)) {
				return fmt.Errorf("%w: pixel is already assigned to bin %d", ErrLocateTopology, p.BinID)
			}
		}
		return q.SavePhysicalBinPixel(ctx, result)
	})
	return result, relative, err
}

// IsBinPixelUnconfigured distinguishes absent data from stale or unreadable data.
func IsBinPixelUnconfigured(err error) bool { return errors.Is(err, sql.ErrNoRows) }
