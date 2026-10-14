package main

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tuxedocurly/wledger/internal/api"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/wled"
)

func TestMCPWLEDWriteToolsCannotBypassInventoryOnlyAPIGuard(t *testing.T) {
	t.Setenv("WLEDGER_API_TOKEN", "mcp-inventory-only-test-token")
	t.Setenv("WLEDGER_PHYSICAL_OUTPUT_ENABLED", "")

	conn, err := db.Open("file:mcp-output-guard?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open disposable DB: %v", err)
	}
	defer conn.Close()
	if err := db.Migrate(conn); err != nil {
		t.Fatalf("migrate disposable DB: %v", err)
	}
	store := db.NewStore(conn)
	service := wled.NewService(store, wled.NewClient(), slog.Default())
	apiServer := httptest.NewServer(http.StripPrefix("/api/v1", api.NewHandler(store, service, nil).Routes()))
	defer apiServer.Close()
	tools := &controller{api: &apiClient{base: apiServer.URL, token: "mcp-inventory-only-test-token", http: apiServer.Client()}}

	requests := []struct {
		name string
		run  func() error
	}{
		{"global_off", func() error {
			_, err := tools.globalOff(context.Background(), mcp.CallToolRequest{})
			return err
		}},
		{"locate_part", func() error {
			_, err := tools.locatePart(context.Background(), mcp.CallToolRequest{
				Params: mcp.CallToolParams{Arguments: map[string]any{"part_id": float64(1)}},
			})
			return err
		}},
		{"locate_bin", func() error {
			_, err := tools.locateBin(context.Background(), mcp.CallToolRequest{
				Params: mcp.CallToolParams{Arguments: map[string]any{"bin_id": float64(1)}},
			})
			return err
		}},
	}
	for _, request := range requests {
		t.Run(request.name, func(t *testing.T) {
			err := request.run()
			if err == nil || !strings.Contains(err.Error(), "409 Conflict") || !strings.Contains(err.Error(), "inventory-only mode") {
				t.Fatalf("MCP tool error = %v, want API conflict stating inventory-only output is disabled", err)
			}
		})
	}
}
