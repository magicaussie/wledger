package wledsim_test

import (
	"context"
	"database/sql"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/wled"
	"github.com/tuxedocurly/wledger/internal/wledsim"
)

// The discovery path only needs these two reads. Embedding the unused store
// interface makes any unexpected database access fail instead of touching disk.
type discoveryStore struct {
	db.Store
	controller db.Controller
}

func (s discoveryStore) GetController(context.Context, int64) (db.Controller, error) {
	return s.controller, nil
}
func (s discoveryStore) ListPhysicalLocateSessions(context.Context) ([]db.PhysicalLocateSession, error) {
	return nil, nil
}

func TestApplicationDiscoveryCompatibility(t *testing.T) {
	sim, err := wledsim.New(wledsim.Config{Count: 10, MAC: "02:11:22:33:44:66", Outputs: []wledsim.Output{{Length: 4, Pins: []int{2}}, {Start: 4, Length: 6, Pins: []int{4}, Reverse: true}}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(sim)
	defer server.Close()
	store := discoveryStore{controller: db.Controller{ID: 1, IpAddress: strings.TrimPrefix(server.URL, "http://"), LedCount: 10, ControllerType: "wled-esp32", MacAddress: sql.NullString{String: "02:11:22:33:44:66", Valid: true}}}
	service := wled.NewPhysicalLocateService(store, wled.NewClient())
	layout, err := service.DiscoverPhysicalOutputs(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if layout.MAC != store.controller.MacAddress.String || layout.Fingerprint == "" || len(layout.Outputs) != 2 || layout.Outputs[1].FirstIndex != 5 || !layout.Outputs[1].Reversed {
		t.Fatalf("layout %+v", layout)
	}
	commands := sim.Commands()
	if len(commands) != 2 || commands[0].Method != "GET" || commands[0].Path != "/json/info" || commands[1].Path != "/json/cfg" {
		t.Fatalf("unexpected requests: %+v", commands)
	}
}
