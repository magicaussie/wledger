package hardware

import (
	"database/sql"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
)

func TestRestoredAllocationValidatorsIgnoreBinsWithoutContainer(t *testing.T) {
	containers := []db.Container{
		{ID: 0, Name: "Container zero", LedStart: 0, LedCount: 10},
	}
	bins := []db.Bin{
		{
			ID:          1,
			ContainerID: sql.NullInt64{},
			LedIndex:    sql.NullInt64{Int64: -1, Valid: true},
			Width:       sql.NullInt64{Int64: 1, Valid: true},
		},
	}

	modern, err := ValidateRestoredAllocations(containers, bins)
	if err != nil {
		t.Fatalf("ValidateRestoredAllocations: %v", err)
	}
	if !modern {
		t.Fatal("ValidateRestoredAllocations reports legacy data despite explicit allocation")
	}

	if err := ValidateRestoredDrawerAllocations(containers, bins); err != nil {
		t.Fatalf("ValidateRestoredDrawerAllocations: %v", err)
	}
}
