package wled

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Developer Note: a controller status cache is implemented here to avoid unintentionally
// DDOSing the WELED controller in the event many users are hitting the /hardware
// page concurrently. Ther are alternatives to this solution, such as a central
// goroutine. It may make sense in the future to refactor this implementation
// to utilize a gorouting that runs every X seconds to internally update the
// controller status, simply returning the cached "controller status" to a every
// client upon request.

// CacheDuration determines how long to trust a ping result before checking hardware again
const CacheDuration = 10 * time.Second

type Client struct {
	httpClient *http.Client
	// cache stores the last known status of an IP
	// Key: string (IP), Value: cachedResult
	cache sync.Map
}

type cachedResult struct {
	online    bool
	timestamp time.Time
}

func New() *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: 1 * time.Second,
		},
	}
}

// Ping checks if a controller is online, using a short-term cache to protect hardware
func (c *Client) Ping(ctx context.Context, ip string) (bool, error) {
	// Check Cache
	if val, ok := c.cache.Load(ip); ok {
		entry := val.(cachedResult)
		// If the data is fresh (e.g. younger than 10s), return it immediately
		if time.Since(entry.timestamp) < CacheDuration {
			return entry.online, nil
		}
	}

	// Perform Real Request
	// proceed if cache is missing or expired
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

	// Update Cache
	// Even if it failed (offline), cache that result to avoid spamming retries immediately
	c.cache.Store(ip, cachedResult{
		online:    isOnline,
		timestamp: time.Now(),
	})

	return isOnline, nil
}

// LightUp lights up a specific range of LEDs on a controller.
func (c *Client) LightUp(ctx context.Context, ip string, index int, count int, hexColor string) error {
	payload := map[string]any{
		"seg": []map[string]any{
			{
				"i": []any{index, index + count, hexColor},
			},
		},
		"live": true,
	}

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
