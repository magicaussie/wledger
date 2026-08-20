// Command mcp-server exposes WLEDger as an MCP (Model Context Protocol) server
// so LLM-based voice/assistant agents (Home Assistant conversation agent,
// Open WebUI, Hermes, Claude, etc.) can control the LED inventory.
//
// It talks to the WLEDger HTTP API /api/v1 using the same bearer token.
//
// Configuration via environment:
//
//	WLEDGER_API_URL     base URL, default http://localhost:8080
//	WLEDGER_API_TOKEN   required bearer token for /api/v1 tools
//	MCP_TRANSPORT       "stdio" (default), "sse", or "http"
//	MCP_HTTP_ADDR       listen address for sse/http, default :9100
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/server"
)

type apiClient struct {
	base  string
	token string
	http  *http.Client
}

func newClient() *apiClient {
	base := strings.TrimRight(os.Getenv("WLEDGER_API_URL"), "/")
	if base == "" {
		base = "http://localhost:8080"
	}
	return &apiClient{
		base:  base,
		token: os.Getenv("WLEDGER_API_TOKEN"),
		http:  &http.Client{Timeout: 90 * time.Second},
	}
}

func (c *apiClient) url(path ...string) string {
	return c.base + "/api/v1/" + strings.Join(path, "/")
}

// request performs a JSON request to the WLEDger API and decodes the response
// into out (when out != nil).
func (c *apiClient) request(ctx context.Context, method, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("wledger api %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (c *apiClient) post(ctx context.Context, path string, out any) error {
	return c.request(ctx, http.MethodPost, c.url(path), out)
}
func (c *apiClient) get(ctx context.Context, path string, out any) error {
	return c.request(ctx, http.MethodGet, c.url(path), out)
}

func main() {
	token := os.Getenv("WLEDGER_API_TOKEN")
	if token == "" {
		log.Fatal("WLEDGER_API_TOKEN is required to talk to the WLEDger API")
	}

	client := newClient()

	srv := server.NewMCPServer(
		"wledger",
		"0.1.0",
		server.WithToolCapabilities(true),
	)

	c := &controller{api: client}
	registerTools(srv, c)

	transport := os.Getenv("MCP_TRANSPORT")
	switch transport {
	case "", "stdio":
		// stdio is the default MCP transport; write to stderr for logs.
		log.SetOutput(os.Stderr)
		if err := server.ServeStdio(srv); err != nil {
			log.Fatal(err)
		}
	case "sse":
		addr := os.Getenv("MCP_HTTP_ADDR")
		if addr == "" {
			addr = ":9100"
		}
		log.Printf("MCP SSE listening on %s", addr)
		httpServer := &http.Server{Addr: addr, Handler: server.NewSSEServer(srv)}
		log.Fatal(httpServer.ListenAndServe())
	case "http":
		addr := os.Getenv("MCP_HTTP_ADDR")
		if addr == "" {
			addr = ":9100"
		}
		log.Printf("MCP HTTP (streamable) listening on %s", addr)
		mux := http.NewServeMux()
		mux.Handle("/", server.NewStreamableHTTPServer(srv))
		httpServer := &http.Server{Addr: addr, Handler: mux}
		log.Fatal(httpServer.ListenAndServe())
	default:
		log.Fatalf("unknown MCP_TRANSPORT %q", transport)
	}
}
