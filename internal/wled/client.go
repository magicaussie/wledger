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

// Clear turns the controller's LEDs off. It uses the device-wide power state
// rather than a pixel range, so it is independent of the controller's LED count,
// segment layout and segment IDs. live:false exits realtime mode; tt:0 disables
// the transition. No segment or individual-pixel data is sent, so unrelated
// segment configuration and effects are left untouched.
func (c *Client) Clear(ctx context.Context, ip string) error {
	payload := map[string]any{
		"on":   false,
		"live": false,
		"tt":   0,
	}

	return c.SetState(ctx, ip, payload)
}

// LED modes for State.
const (
	ModeSolid = "solid"
	ModeFlash = "flash"
)

// State describes how a range of LEDs should be lit.
type State struct {
	Color string // hex colour, e.g. "#0000FF"
	Mode  string // ModeSolid (default) or ModeFlash
}

// flashTimes and flashInterval control the blink loop. They are variables so
// tests can shorten them.
var (
	flashTimes    = 3
	flashInterval = 250 * time.Millisecond
)

// Apply lights a range of LEDs according to the state. Solid states are applied
// immediately; flash states run a bounded blink loop in the background so the
// caller is not blocked.
func (c *Client) Apply(ctx context.Context, ip string, segmentID, index, count int, state State) error {
	if state.Mode == ModeFlash {
		go c.flash(ip, segmentID, index, count, state.Color, flashTimes, flashInterval)
		return nil
	}
	return c.LightUp(ctx, ip, segmentID, index, count, state.Color)
}

// flash blinks a range between the colour and off `times` times, then leaves it
// off. Best-effort: it stops on the first error or when the deadline passes.
func (c *Client) flash(ip string, segmentID, index, count int, hexColor string, times int, interval time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(times)*2*interval+time.Second)
	defer cancel()

	for i := 0; i < times; i++ {
		if err := c.LightUp(ctx, ip, segmentID, index, count, hexColor); err != nil {
			return
		}
		if !sleepCtx(ctx, interval) {
			return
		}
		if err := c.LightUp(ctx, ip, segmentID, index, count, "#000000"); err != nil {
			return
		}
		if !sleepCtx(ctx, interval) {
			return
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-time.After(d):
		return true
	case <-ctx.Done():
		return false
	}
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
