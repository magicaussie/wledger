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

// Client handles low-level WLED JSON API communication
type Client struct {
	httpClient *http.Client
	cache      sync.Map
}

type cachedResult struct {
	online    bool
	timestamp time.Time
}

func NewClient() *Client {
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

// SetState updates the WLED state with a custom payload
func (c *Client) SetState(ctx context.Context, ip string, payload map[string]any) error {
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

// LightUp uses the individual LED ('i') API to light up a range
func (c *Client) LightUp(ctx context.Context, ip string, segmentID int, index int, count int, hexColor string) error {
	rgb, err := HexToRGB(hexColor)
	if err != nil {
		rgb = []int{255, 255, 255}
	}

	payload := map[string]any{
		"on": true,
		"tt": 0,
		"seg": []map[string]any{
			{
				"id": segmentID,
				"on": true,
				"i":  []any{index, index + count, rgb},
			},
		},
	}

	return c.SetState(ctx, ip, payload)
}

// Clear resets the controller to Solid Black
func (c *Client) Clear(ctx context.Context, ip string) error {
	payload := map[string]any{
		"on":   true,
		"live": false,
		"tt":   0,
		"seg": []map[string]any{
			{
				"id": 0,
				"on": true,
				"fx": 0,
				"i":  []any{0, 5000, []int{0, 0, 0}}, // Wipe first 5000 pixels
				// TODO: Handle this dynamically based on actual LED count or segment config
			},
		},
	}

	return c.SetState(ctx, ip, payload)
}

// HexToRGB converts a hex string to an RGB slice
func HexToRGB(hex string) ([]int, error) {
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
