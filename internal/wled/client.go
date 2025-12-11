package wled

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const CacheDuration = 5 * time.Second

type Client struct {
	httpClient *http.Client
	cache      sync.Map
}

type cachedResult struct {
	online    bool
	timestamp time.Time
}

func New() *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: 2 * time.Second,
		},
	}
}

// Ping checks if a controller is online
func (c *Client) Ping(ctx context.Context, ip string) (bool, error) {
	if val, ok := c.cache.Load(ip); ok {
		entry := val.(cachedResult)
		if time.Since(entry.timestamp) < CacheDuration {
			return entry.online, nil
		}
	}

	url := fmt.Sprintf("http://%s/json/info", ip)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return false, err
	}

	resp, err := c.httpClient.Do(req)
	isOnline := false
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == 200 {
			isOnline = true
		}
	}

	c.cache.Store(ip, cachedResult{
		online:    isOnline,
		timestamp: time.Now(),
	})

	return isOnline, nil
}

// LightUp lights up a specific range of LEDs on a controller using standard JSON API (No Live Mode)
func (c *Client) LightUp(ctx context.Context, ip string, index int, count int, hexColor string) error {
	rgb, err := hexToRGB(hexColor)
	if err != nil {
		rgb = []int{255, 255, 255}
	}

	// Standard State Update
	// Developer Note: don't use live mode here
	payload := map[string]any{
		"on": true, // Master Power ON
		"tt": 0,    // Instant transition
		"seg": []map[string]any{
			{
				"id": 0,                                // Main Segment
				"on": true,                             // Segment ON
				"i":  []any{index, index + count, rgb}, // Set these pixels
			},
		},
	}

	return c.sendPayload(ctx, ip, payload)
}

// Clear resets the controller to Solid Black.
func (c *Client) Clear(ctx context.Context, ip string) error {
	// Force all LEDs to Black
	payload := map[string]any{
		"on":   true,  // Keep Master Power ON
		"live": false, // Ensure Live is OFF (just in case)
		"tt":   0,     // Instant
		"seg": []map[string]any{
			{
				"id":  0,                  // Main Segment
				"on":  true,               // Enabled
				"fx":  0,                  // Effect: Solid
				"col": [][]int{{0, 0, 0}}, // Color: Black
				// TODO: Update this so that the LED number is set based on the length
				// of LEDs for a particular controller
				"i": []any{0, 1000, []int{0, 0, 0}}, // Force wipe the first 1000 pixels to black manually
			},
		},
	}

	return c.sendPayload(ctx, ip, payload)
}

// sendPayload handles the JSON marshaling and HTTP request
func (c *Client) sendPayload(ctx context.Context, ip string, payload map[string]any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("http://%s/json/state", ip)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("wled returned status: %d", resp.StatusCode)
	}

	return nil
}

func hexToRGB(hex string) ([]int, error) {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		return nil, fmt.Errorf("invalid hex length")
	}

	val, err := strconv.ParseUint(hex, 16, 32)
	if err != nil {
		return nil, err
	}

	r := int(val >> 16)
	g := int((val >> 8) & 0xFF)
	b := int(val & 0xFF)

	return []int{r, g, b}, nil
}
