package mapper_test

import (
	"database/sql"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/hardware/mapper"
)

func TestCalculateGlobalIndex(t *testing.T) {
	tests := []struct {
		name           string
		containers     []db.Container // Should be sorted by order
		targetBin      db.Bin
		wantSegment    int64
		wantIndex      int64
		wantErr        bool
	}{
		{
			name: "Single Container (8x8)",
			containers: []db.Container{
				{ID: 1, Name: "C1", SegmentID: 0, ConfigJson: sql.NullString{String: `{"type":"grid","rows":8,"cols":8}`, Valid: true}},
			},
			targetBin: db.Bin{ContainerID: 1, LedIndex: sql.NullInt64{Int64: 10, Valid: true}},
			wantSegment: 0,
			wantIndex: 10,
		},
		{
			name: "Two Containers Same Segment (Absolute Index 69)",
			containers: []db.Container{
				{ID: 1, SegmentID: 0, ConfigJson: sql.NullString{String: `{"type":"grid","rows":8,"cols":8}`, Valid: true}}, // 64
				{ID: 2, SegmentID: 0, ConfigJson: sql.NullString{String: `{"type":"linear","total":16}`, Valid: true}},      // 16
			},
			targetBin: db.Bin{ContainerID: 2, LedIndex: sql.NullInt64{Int64: 69, Valid: true}},
			wantSegment: 0,
			wantIndex: 69,
		},
		{
			name: "Two Containers Different Segments",
			containers: []db.Container{
				{ID: 1, SegmentID: 0, ConfigJson: sql.NullString{String: `{"type":"grid","rows":8,"cols":8}`, Valid: true}},
				{ID: 2, SegmentID: 1, ConfigJson: sql.NullString{String: `{"type":"linear","total":16}`, Valid: true}},
			},
			targetBin: db.Bin{ContainerID: 2, LedIndex: sql.NullInt64{Int64: 5, Valid: true}},
			wantSegment: 1,
			wantIndex: 5,
		},
		{
			name: "Compound Container (Absolute Index 32)",
			containers: []db.Container{
				// Compound: 2 sections of 4x4 = 16 + 16 = 32
				{ID: 1, SegmentID: 0, ConfigJson: sql.NullString{String: `{"type":"compound","sections":[{"rows":4,"cols":4},{"rows":4,"cols":4}]}`, Valid: true}},
				{ID: 2, SegmentID: 0, ConfigJson: sql.NullString{String: `{"type":"linear","total":10}`, Valid: true}},
			},
			targetBin: db.Bin{ContainerID: 2, LedIndex: sql.NullInt64{Int64: 32, Valid: true}},
			wantSegment: 0,
			wantIndex: 32,
		},
        {
            name: "Container Not Found",
            containers: []db.Container{
                {ID: 1, SegmentID: 0},
            },
            targetBin: db.Bin{ContainerID: 99, LedIndex: sql.NullInt64{Int64: 0, Valid: true}},
            wantErr: true,
        },
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seg, idx, err := mapper.CalculateGlobalIndex(tt.containers, tt.targetBin)
			if (err != nil) != tt.wantErr {
				t.Errorf("CalculateGlobalIndex() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if seg != tt.wantSegment {
					t.Errorf("CalculateGlobalIndex() segment = %v, want %v", seg, tt.wantSegment)
				}
				if idx != tt.wantIndex {
					t.Errorf("CalculateGlobalIndex() index = %v, want %v", idx, tt.wantIndex)
				}
			}
		})
	}
}
