package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const testToken = "sekret"

// newTestMCPServer builds a minimal MCP server with a single tool so the
// transports have something to expose.
func newTestMCPServer() *server.MCPServer {
	s := server.NewMCPServer("wledger-test", "0.0.0", server.WithToolCapabilities(true))
	s.AddTool(
		mcp.NewTool("ping", mcp.WithDescription("test tool")),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("pong"), nil
		},
	)
	return s
}

const initializeBody = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`

func testHTTPClient() *http.Client {
	return &http.Client{Timeout: 5 * time.Second}
}

func TestStreamableHTTPAuth(t *testing.T) {
	handler := authMiddleware(testToken, nil, server.NewStreamableHTTPServer(newTestMCPServer()))
	ts := httptest.NewServer(handler)
	defer ts.Close()

	post := func(auth string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(initializeBody))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := testHTTPClient().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	r := post("")
	r.Body.Close()
	if r.StatusCode != http.StatusUnauthorized {
		t.Errorf("streamable missing token: want 401, got %d", r.StatusCode)
	}

	r = post("Bearer wrong")
	r.Body.Close()
	if r.StatusCode != http.StatusUnauthorized {
		t.Errorf("streamable invalid token: want 401, got %d", r.StatusCode)
	}

	r = post("Bearer " + testToken)
	defer r.Body.Close()
	if r.StatusCode != http.StatusOK {
		t.Errorf("streamable valid token: want 200, got %d", r.StatusCode)
	}
}

func TestSSEAuth(t *testing.T) {
	handler := authMiddleware(testToken, nil, server.NewSSEServer(newTestMCPServer()))
	ts := httptest.NewServer(handler)
	defer ts.Close()

	get := func(auth string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, ts.URL+"/sse", nil)
		if err != nil {
			t.Fatal(err)
		}
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := testHTTPClient().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	r := get("")
	r.Body.Close()
	if r.StatusCode != http.StatusUnauthorized {
		t.Errorf("sse missing token: want 401, got %d", r.StatusCode)
	}

	r = get("Bearer wrong")
	r.Body.Close()
	if r.StatusCode != http.StatusUnauthorized {
		t.Errorf("sse invalid token: want 401, got %d", r.StatusCode)
	}

	r = get("Bearer " + testToken)
	r.Body.Close()
	if r.StatusCode != http.StatusOK {
		t.Errorf("sse valid token: want 200, got %d", r.StatusCode)
	}
}

// TestSSEMessageEndpointAuth ensures the SSE message-submission endpoint is
// also protected, not just the connection-establishment endpoint.
func TestSSEMessageEndpointAuth(t *testing.T) {
	handler := authMiddleware(testToken, nil, server.NewSSEServer(newTestMCPServer()))
	ts := httptest.NewServer(handler)
	defer ts.Close()

	post := func(auth string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, ts.URL+"/message", strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := testHTTPClient().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	r := post("")
	r.Body.Close()
	if r.StatusCode != http.StatusUnauthorized {
		t.Errorf("sse message missing token: want 401, got %d", r.StatusCode)
	}

	r = post("Bearer wrong")
	r.Body.Close()
	if r.StatusCode != http.StatusUnauthorized {
		t.Errorf("sse message invalid token: want 401, got %d", r.StatusCode)
	}
}

// TestStreamableHTTPOriginRejected confirms the Origin policy is enforced on
// the network transport.
func TestStreamableHTTPOriginRejected(t *testing.T) {
	handler := authMiddleware(testToken, []string{"https://allowed.example"}, server.NewStreamableHTTPServer(newTestMCPServer()))
	ts := httptest.NewServer(handler)
	defer ts.Close()

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(initializeBody))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Origin", "https://evil.example")
	resp, err := testHTTPClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("disallowed origin: want 403, got %d", resp.StatusCode)
	}
}

// TestStreamableHTTPHostRebindingProtection documents that the MCP library's
// built-in DNS-rebinding protection is preserved: a request arriving over a
// loopback connection with a non-loopback Host header is rejected with 403.
// This is the behaviour a same-host reverse proxy must account for.
func TestStreamableHTTPHostRebindingProtection(t *testing.T) {
	handler := authMiddleware(testToken, nil, server.NewStreamableHTTPServer(newTestMCPServer()))
	ts := httptest.NewServer(handler)
	defer ts.Close()

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(initializeBody))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Host = "storage.localdomain" // non-loopback Host over a loopback connection
	resp, err := testHTTPClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("non-loopback Host: want 403 (DNS-rebinding protection), got %d", resp.StatusCode)
	}
}
