package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// controller holds a reference to the WLEDger API client for tool handlers.
type controller struct {
	api *apiClient
}

// partResult matches the JSON returned by GET /api/v1/parts.
type partResult struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	PartNumber    string `json:"part_number"`
	Manufacturer  string `json:"manufacturer"`
	Barcode       string `json:"barcode"`
	TotalStock    int64  `json:"total_stock"`
	ValidStock    int64  `json:"valid_stock"`
	OrphanedStock int64  `json:"orphaned_stock"`
	ImageURL      string `json:"image_url"`
}

func withString(name, desc string) mcp.ToolOption {
	return mcp.WithString(name, mcp.Description(desc), mcp.Required())
}

// arg returns the named argument from the tool call as a string.
func argString(a any, name string) string {
	m, ok := a.(map[string]any)
	if !ok {
		return ""
	}
	s, _ := m[name].(string)
	return s
}

func argNumber(a any, name string) float64 {
	m, ok := a.(map[string]any)
	if !ok {
		return 0
	}
	f, _ := m[name].(float64)
	return f
}

// registerTools wires the available tools onto the MCP server.
func registerTools(s *server.MCPServer, c *controller) {
	s.AddTool(
		mcp.NewTool(
			"search_parts",
			mcp.WithDescription("Search the LEDger inventory for parts by name, part number, or barcode."),
			withString("query", "Search term (part name, part number, or barcode)"),
		),
		c.searchParts,
	)

	s.AddTool(
		mcp.NewTool(
			"locate_part",
			mcp.WithDescription("Flash the LEDs of every bin/container a part is stored in, so you can find it in the real world."),
			mcp.WithNumber("part_id", mcp.Description("Numeric part ID from search results")),
			withString("query", "Part name, part number, or barcode to look up and locate"),
		),
		c.locatePart,
	)

	s.AddTool(
		mcp.NewTool(
			"locate_bin",
			mcp.WithDescription("Flash the LEDs of a single storage bin."),
			mcp.WithNumber("bin_id", mcp.Description("Numeric bin ID")),
		),
		c.locateBin,
	)

	s.AddTool(
		mcp.NewTool(
			"global_off",
			mcp.WithDescription("Turn off all LEDs on every controller."),
		),
		c.globalOff,
	)

	s.AddTool(
		mcp.NewTool(
			"list_controllers",
			mcp.WithDescription("List all LED controllers and whether they are online."),
		),
		c.listControllers,
	)
}

func (c *controller) searchParts(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	q := argString(req.Params.Arguments, "query")
	if q == "" {
		return mcp.NewToolResultText("query is required"), nil
	}

	var out struct {
		Parts []partResult `json:"parts"`
	}
	if err := c.api.get(ctx, "parts?q="+urlEscape(q), &out); err != nil {
		return nil, err
	}
	if len(out.Parts) == 0 {
		return mcp.NewToolResultText("no parts matched " + q), nil
	}
	b, _ := json.MarshalIndent(out.Parts, "", "  ")
	return mcp.NewToolResultText(string(b)), nil
}

func (c *controller) locatePart(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	q := argString(req.Params.Arguments, "query")
	id := int64(argNumber(req.Params.Arguments, "part_id"))

	if id == 0 && q != "" {
		var out struct {
			Parts []partResult `json:"parts"`
		}
		if err := c.api.get(ctx, "parts?q="+urlEscape(q), &out); err != nil {
			return nil, err
		}
		if len(out.Parts) == 0 {
			return mcp.NewToolResultText("no parts matched " + q), nil
		}
		id = out.Parts[0].ID
	}
	if id == 0 {
		return mcp.NewToolResultText("provide part_id or query"), nil
	}

	var out map[string]any
	if err := c.api.post(ctx, fmt.Sprintf("parts/%d/locate", id), &out); err != nil {
		return nil, err
	}
	return mcp.NewToolResultText(fmt.Sprintf("located part %d", id)), nil
}

func (c *controller) locateBin(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	idF := argNumber(req.Params.Arguments, "bin_id")
	if idF == 0 {
		return mcp.NewToolResultText("bin_id is required"), nil
	}
	var out map[string]any
	if err := c.api.post(ctx, fmt.Sprintf("bins/%d/locate", int64(idF)), &out); err != nil {
		return nil, err
	}
	return mcp.NewToolResultText(fmt.Sprintf("located bin %d", int64(idF))), nil
}

func (c *controller) globalOff(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var out map[string]any
	if err := c.api.post(ctx, "global-off", &out); err != nil {
		return nil, err
	}
	return mcp.NewToolResultText("turned off all LEDs"), nil
}

func (c *controller) listControllers(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var out struct {
		Controllers []struct {
			ID        int64  `json:"id"`
			Name      string `json:"name"`
			IPAddress string `json:"ip_address"`
			Online    bool   `json:"online"`
		} `json:"controllers"`
	}
	if err := c.api.get(ctx, "hardware", &out); err != nil {
		return nil, err
	}
	b, _ := json.MarshalIndent(out.Controllers, "", "  ")
	return mcp.NewToolResultText(string(b)), nil
}

func urlEscape(s string) string {
	// Simple percent-encoding for query values.
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteString(fmt.Sprintf("%%%02X", r))
		}
	}
	return b.String()
}
