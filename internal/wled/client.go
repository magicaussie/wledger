package wled

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type Client struct {
	httpClient *http.Client
}

func New() *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: 1 * time.Second,
		},
	}
}

// LightUp lights up a specific range of LEDs on a controller
func (c *Client) LightUp(ctx context.Context, ip string, index int, count int, hexColor string) error {
	// "live": true tells WLED this is a realtime preview
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

// Ping checks if a controller is online
func (c *Client) Ping(ctx context.Context, ip string) (bool, error) {
	url := fmt.Sprintf("http://%s/json/info", ip)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return false, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200, nil
}
