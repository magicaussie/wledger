package backup

import (
	"context"
	"fmt"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/hardware"
	"github.com/tuxedocurly/wledger/internal/ledspace"
)

func (s *service) applyRestoredDatabase(ctx context.Context, qtx db.Querier, manifest Manifest, envelope restoreEnvelope, space, generation string) error {
	if err := clearPhysicalStorage(ctx, qtx); err != nil {
		return err
	}
	clearers := []struct {
		name string
		fn   func(context.Context) error
	}{
		{"audit logs", qtx.ClearAuditLogs},
		{"part tags", qtx.ClearPartTags},
		{"tags", qtx.ClearTags},
		{"part assignments", qtx.ClearPartAssignments},
		{"part docs", qtx.ClearPartDocs},
		{"part links", qtx.ClearPartLinks},
		{"part AI prompts", qtx.ClearPartAiPrompts},
		{"part attribute values", qtx.ClearPartAttributeValues},
		{"part details", qtx.ClearPartDetails},
		{"part categories", qtx.ClearPartCategories},
		{"category attributes", qtx.ClearCategoryAttributes},
		{"categories", qtx.ClearCategories},
		{"parts", qtx.ClearParts},
		{"bins", qtx.ClearBins},
		{"wall cards", qtx.ClearWallCards},
		{"walls", qtx.ClearWalls},
		{"containers", qtx.ClearContainers},
		{"controllers", qtx.ClearControllers},
		{"users", qtx.ClearUsers},
		{"price history", qtx.ClearPriceHistory},
		{"part pricing", qtx.ClearPartPricing},
		{"part parameters", qtx.ClearPartParameters},
		{"supplier references", qtx.ClearSupplierReferences},
		{"supplier credentials", qtx.ClearSupplierCredentials},
	}
	for _, clear := range clearers {
		if err := clear.fn(ctx); err != nil {
			return fmt.Errorf("failed to clear %s: %w", clear.name, err)
		}
	}
	if err := s.restoreData(ctx, qtx, manifest); err != nil {
		return err
	}
	if envelope.Current != nil {
		if err := restorePhysicalStorage(ctx, qtx, envelope.Current.PhysicalStorage); err != nil {
			return err
		}
		if envelope.Current.Version != supportedCurrentManifestVersion {
			if err := synthesizeLegacyControllerOutputs(ctx, qtx); err != nil {
				return fmt.Errorf("legacy controller output migration during restore: %w", err)
			}
		}
		if err := restoreSystemFlags(ctx, qtx, envelope.Current.SystemFlags); err != nil {
			return err
		}
	} else {
		if err := synthesizeLegacyControllerOutputs(ctx, qtx); err != nil {
			return fmt.Errorf("legacy controller output migration during restore: %w", err)
		}
		if err := ledspace.Set(ctx, qtx, space); err != nil {
			return err
		}
		if space == ledspace.Segment {
			if err := qtx.SetFlag(ctx, db.SetFlagParams{Key: hardware.Migration005FlagKey, Value: "true"}); err != nil {
				return fmt.Errorf("failed to mark migration 005 applied: %w", err)
			}
		}
	}
	if err := qtx.SetRestoreGeneration(ctx, generation); err != nil {
		return fmt.Errorf("failed to set restore generation: %w", err)
	}
	return nil
}
