package db

import "context"

// PhysicalBinPixel is independent of the legacy electrical Bin.LedIndex.
// Offset is zero-based within an immutable allocation identity. Pixel is the
// controller-absolute zero-based address observed at assignment time.
type PhysicalBinPixel struct {
	BinID               int64  `json:"bin_id"`
	DrawerID            int64  `json:"drawer_id"`
	AllocationID        int64  `json:"allocation_id"`
	Offset              int64  `json:"pixel_offset"`
	ControllerID        int64  `json:"controller_id"`
	Pixel               int64  `json:"pixel"`
	TopologyFingerprint string `json:"topology_fingerprint"`
}

type PhysicalBinPixelQuerier interface {
	GetPhysicalBinPixel(context.Context, int64) (PhysicalBinPixel, error)
	ListPhysicalBinPixels(context.Context) ([]PhysicalBinPixel, error)
	SavePhysicalBinPixel(context.Context, PhysicalBinPixel) error
	ClearPhysicalBinPixels(context.Context) error
	DeletePhysicalBinPixel(context.Context, int64) error
}

func (q *Queries) GetPhysicalBinPixel(ctx context.Context, binID int64) (PhysicalBinPixel, error) {
	var p PhysicalBinPixel
	err := q.db.QueryRowContext(ctx, `SELECT bin_id,drawer_id,allocation_id,pixel_offset,controller_id,pixel,topology_fingerprint FROM physical_bin_pixels WHERE bin_id=?`, binID).Scan(&p.BinID, &p.DrawerID, &p.AllocationID, &p.Offset, &p.ControllerID, &p.Pixel, &p.TopologyFingerprint)
	return p, err
}
func (q *Queries) ListPhysicalBinPixels(ctx context.Context) ([]PhysicalBinPixel, error) {
	rows, err := q.db.QueryContext(ctx, `SELECT bin_id,drawer_id,allocation_id,pixel_offset,controller_id,pixel,topology_fingerprint FROM physical_bin_pixels ORDER BY bin_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []PhysicalBinPixel{}
	for rows.Next() {
		var p PhysicalBinPixel
		if err := rows.Scan(&p.BinID, &p.DrawerID, &p.AllocationID, &p.Offset, &p.ControllerID, &p.Pixel, &p.TopologyFingerprint); err != nil {
			return nil, err
		}
		result = append(result, p)
	}
	return result, rows.Err()
}

// SavePhysicalBinPixel must be called under the operation claim after validation.
func (q *Queries) SavePhysicalBinPixel(ctx context.Context, p PhysicalBinPixel) error {
	_, err := q.db.ExecContext(ctx, `INSERT INTO physical_bin_pixels(bin_id,drawer_id,allocation_id,pixel_offset,controller_id,pixel,topology_fingerprint) VALUES(?,?,?,?,?,?,?) ON CONFLICT(bin_id) DO UPDATE SET drawer_id=excluded.drawer_id,allocation_id=excluded.allocation_id,pixel_offset=excluded.pixel_offset,controller_id=excluded.controller_id,pixel=excluded.pixel,topology_fingerprint=excluded.topology_fingerprint`, p.BinID, p.DrawerID, p.AllocationID, p.Offset, p.ControllerID, p.Pixel, p.TopologyFingerprint)
	return err
}

// DeletePhysicalBinPixel removes only the confirmed bin's override. Callers
// must hold the operation claim and validate its current drawer attachment.
func (q *Queries) DeletePhysicalBinPixel(ctx context.Context, binID int64) error {
	_, err := q.db.ExecContext(ctx, `DELETE FROM physical_bin_pixels WHERE bin_id=?`, binID)
	return err
}

func (q *Queries) ClearPhysicalBinPixels(ctx context.Context) error {
	_, err := q.db.ExecContext(ctx, `DELETE FROM physical_bin_pixels`)
	return err
}
