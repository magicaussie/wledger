package wled

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tuxedocurly/wledger/internal/config"
	"github.com/tuxedocurly/wledger/internal/db"
)

const CacheDuration = 5 * time.Second

// Client handles low-level WLED JSON API communication
type Client struct {
	httpClient *http.Client
	cache      sync.Map
	store      db.Store
}

type controllerWriteGate struct {
	mu       sync.Mutex
	reserved bool
}

var (
	controllerWriteGates   sync.Map
	controllerIDWriteGates sync.Map
	hardwareWriteLease     struct {
		mu             sync.Mutex
		physicalOwners map[string]struct{}
	}
)

func writeGate(address string, port int64) *controllerWriteGate {
	host := strings.TrimSpace(address)
	if parsedHost, parsedPort, err := net.SplitHostPort(host); err == nil {
		host = parsedHost
		if value, parseErr := strconv.ParseInt(parsedPort, 10, 64); parseErr == nil && value > 0 {
			port = value
		}
	} else if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = host[1 : len(host)-1]
	}
	if port <= 0 {
		port = 80
	}
	key := net.JoinHostPort(host, strconv.FormatInt(port, 10))
	gate, _ := controllerWriteGates.LoadOrStore(key, &controllerWriteGate{})
	return gate.(*controllerWriteGate)
}

func controllerPort(controller db.Controller) int64 {
	if _, parsedPort, err := net.SplitHostPort(strings.TrimSpace(controller.IpAddress)); err == nil {
		if value, parseErr := strconv.ParseInt(parsedPort, 10, 64); parseErr == nil && value > 0 {
			return value
		}
	}
	if controller.Port.Valid && controller.Port.Int64 > 0 {
		return controller.Port.Int64
	}
	return 80
}

func controllerIDGate(controllerID int64) *controllerWriteGate {
	gate, _ := controllerIDWriteGates.LoadOrStore(controllerID, &controllerWriteGate{})
	return gate.(*controllerWriteGate)
}

// setPhysicalReservation is process-local arbitration. Reservations are keyed
// both by stable database identity and the configured endpoint so legacy routes
// remain blocked if an administrator changes a controller address mid-session.
// Separate WLEDger processes must not share a controller while Locate is enabled.
func setPhysicalReservation(sessionID string, controllerID int64, address string, port int64, reserved bool) {
	hardwareWriteLease.mu.Lock()
	defer hardwareWriteLease.mu.Unlock()
	idGate := controllerIDGate(controllerID)
	endpointGate := writeGate(address, port)
	idGate.mu.Lock()
	endpointGate.mu.Lock()
	if hardwareWriteLease.physicalOwners == nil {
		hardwareWriteLease.physicalOwners = make(map[string]struct{})
	}
	if reserved {
		hardwareWriteLease.physicalOwners[sessionID] = struct{}{}
	} else {
		delete(hardwareWriteLease.physicalOwners, sessionID)
	}
	idGate.reserved = reserved
	endpointGate.reserved = reserved
	endpointGate.mu.Unlock()
	idGate.mu.Unlock()
}

func withLegacyWrite(address string, port int64, action func() error) error {
	if !config.PhysicalWLEDOutputEnabled() {
		return ErrPhysicalOutputDisabled
	}
	hardwareWriteLease.mu.Lock()
	defer hardwareWriteLease.mu.Unlock()
	if len(hardwareWriteLease.physicalOwners) > 0 {
		return ErrHardwareBusy
	}
	gate := writeGate(address, port)
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if gate.reserved {
		return ErrHardwareBusy
	}
	return action()
}

func withLegacyController(controller db.Controller, action func() error) error {
	if !config.PhysicalWLEDOutputEnabled() {
		return ErrPhysicalOutputDisabled
	}
	hardwareWriteLease.mu.Lock()
	defer hardwareWriteLease.mu.Unlock()
	if len(hardwareWriteLease.physicalOwners) > 0 {
		return ErrHardwareBusy
	}
	idGate := controllerIDGate(controller.ID)
	endpointGate := writeGate(controller.IpAddress, controllerPort(controller))
	idGate.mu.Lock()
	endpointGate.mu.Lock()
	defer idGate.mu.Unlock()
	defer endpointGate.mu.Unlock()
	if idGate.reserved || endpointGate.reserved {
		return ErrHardwareBusy
	}
	return action()
}

type cachedResult struct {
	online    bool
	timestamp time.Time
}

func NewClient(stores ...db.Store) *Client {
	client := &Client{
		httpClient: &http.Client{},
	}
	if len(stores) != 0 {
		client.store = stores[0]
	}
	return client
}

// SetOperationStore binds the client to the SQLite database used for WLEDger
// operation coordination. A client without a store fails closed on every write.
func (c *Client) SetOperationStore(store db.Store) {
	c.store = store
}

func (c *Client) withOperationClaim(ctx context.Context, action func() error) error {
	if c.store == nil {
		return db.ErrOperationClaimRequired
	}
	return db.WithOperationClaim(ctx, c.store, func(q db.Querier) error {
		owned, err := db.HasPhysicalLocateOwnership(ctx, q)
		if err != nil {
			return fmt.Errorf("check durable physical Locate ownership: %w", err)
		}
		if owned {
			return ErrHardwareBusy
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return action()
	})
}

// Ping checks a legacy endpoint with the historical two-second timeout.
func (c *Client) Ping(ctx context.Context, ip string) (bool, error) {
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return c.pingURL(bounded, ip, fmt.Sprintf("http://%s/json/info", ip))
}

// PingController checks the configured controller endpoint using its saved timeout.
func (c *Client) PingController(ctx context.Context, controller db.Controller) (bool, error) {
	bounded, cancel := controllerRequestContext(ctx, controller)
	defer cancel()
	port := controllerPort(controller)
	cacheKey := controllerURLForAddress(controller.IpAddress, port, "")
	return c.pingURL(bounded, cacheKey, controllerURLForAddress(controller.IpAddress, port, "/json/info"))
}

func (c *Client) pingURL(ctx context.Context, cacheKey, url string) (bool, error) {
	if val, ok := c.cache.Load(cacheKey); ok {
		entry := val.(cachedResult)
		if time.Since(entry.timestamp) < CacheDuration {
			return entry.online, nil
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}
	resp, err := c.httpClient.Do(req)
	isOnline := false
	if err == nil {
		defer resp.Body.Close()
		isOnline = resp.StatusCode == http.StatusOK
	}
	c.cache.Store(cacheKey, cachedResult{online: isOnline, timestamp: time.Now()})
	return isOnline, nil
}

func controllerRequestContext(ctx context.Context, controller db.Controller) (context.Context, context.CancelFunc) {
	timeout := controller.ConnectionTimeoutMs
	if timeout < 100 || timeout > 30000 {
		timeout = 2000
	}
	return context.WithTimeout(ctx, time.Duration(timeout)*time.Millisecond)
}

// SetState updates the WLED state with a custom payload
func (c *Client) SetState(ctx context.Context, ip string, payload map[string]any) error {
	return c.setStateAt(ctx, ip, 80, payload)
}

func (c *Client) setStateAt(ctx context.Context, address string, port int64, payload map[string]any) error {
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return withLegacyWrite(address, port, func() error {
		return c.withOperationClaim(bounded, func() error {
			return c.postStateAt(bounded, address, port, payload)
		})
	})
}

func (c *Client) setControllerState(ctx context.Context, controller db.Controller, payload map[string]any) error {
	bounded, cancel := controllerRequestContext(ctx, controller)
	defer cancel()
	return withLegacyController(controller, func() error {
		return c.withOperationClaim(bounded, func() error {
			return c.postStateAt(bounded, controller.IpAddress, controllerPort(controller), payload)
		})
	})
}

func (c *Client) postStateAt(ctx context.Context, address string, port int64, payload map[string]any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	url := controllerURLForAddress(address, port, "/json/state")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("wled returned status: %d", resp.StatusCode)
	}
	return nil
}

func controllerURLForAddress(address string, port int64, path string) string {
	host := strings.TrimSpace(address)
	if parsedHost, parsedPort, err := net.SplitHostPort(host); err == nil {
		return "http://" + net.JoinHostPort(parsedHost, parsedPort) + path
	}
	if strings.HasPrefix(host, "[") && !strings.HasSuffix(host, "]") {
		return "http://" + host + path
	}
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = host[1 : len(host)-1]
	}
	if port <= 0 {
		port = 80
	}
	return "http://" + net.JoinHostPort(host, strconv.FormatInt(port, 10)) + path
}

// LightUp uses the individual LED ('i') API to light up a range
func (c *Client) LightUp(ctx context.Context, ip string, segmentID int, index int, count int, hexColor string) error {
	return c.lightUpAt(ctx, ip, 80, segmentID, index, count, hexColor)
}

func (c *Client) lightUpAt(ctx context.Context, address string, port int64, segmentID, index, count int, hexColor string) error {
	rgb, err := HexToRGB(hexColor)
	if err != nil {
		rgb = []int{255, 255, 255}
	}
	payload := map[string]any{
		"on": true,
		"tt": 0,
		"seg": []map[string]any{{
			"id": segmentID,
			"on": true,
			"i":  []any{index, index + count, rgb},
		}},
	}
	return c.setStateAt(ctx, address, port, payload)
}

// Clear turns the controller's LEDs off. It uses the device-wide power state
// rather than a pixel range, so it is independent of the controller's LED count,
// segment layout and segment IDs. live:false exits realtime mode; tt:0 disables
// the transition. No segment or individual-pixel data is sent, so unrelated
// segment configuration and effects are left untouched.
func (c *Client) Clear(ctx context.Context, ip string) error {
	return c.clearAt(ctx, ip, 80)
}

func (c *Client) ClearController(ctx context.Context, controller db.Controller) error {
	return c.setControllerState(ctx, controller, map[string]any{"on": false, "live": false, "tt": 0})
}

func (c *Client) clearAt(ctx context.Context, address string, port int64) error {
	payload := map[string]any{"on": false, "live": false, "tt": 0}
	return c.setStateAt(ctx, address, port, payload)
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

// Apply lights a range according to the state. A flash runs synchronously so
// every state write is acknowledged before the caller reports success.
func (c *Client) Apply(ctx context.Context, ip string, segmentID, index, count int, state State) error {
	return c.applyAt(ctx, ip, 80, segmentID, index, count, state)
}

func (c *Client) ApplyController(ctx context.Context, controller db.Controller, segmentID, index, count int, state State) error {
	bounded, cancel := controllerRequestContext(ctx, controller)
	defer cancel()
	return withLegacyController(controller, func() error {
		return c.withOperationClaim(bounded, func() error {
			return c.applyUnlocked(bounded, controller.IpAddress, controllerPort(controller), segmentID, index, count, state)
		})
	})
}

func (c *Client) applyAt(ctx context.Context, address string, port int64, segmentID, index, count int, state State) error {
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return withLegacyWrite(address, port, func() error {
		return c.withOperationClaim(bounded, func() error {
			return c.applyUnlocked(bounded, address, port, segmentID, index, count, state)
		})
	})
}

func (c *Client) applyUnlocked(ctx context.Context, address string, port int64, segmentID, index, count int, state State) error {
	if state.Mode != ModeFlash {
		return c.lightUpUnlocked(ctx, address, port, segmentID, index, count, state.Color)
	}
	return c.flashUnlocked(ctx, address, port, segmentID, index, count, state.Color, flashTimes, flashInterval)
}

func (c *Client) lightUpController(ctx context.Context, controller db.Controller, segmentID, index, count int, color string) error {
	bounded, cancel := controllerRequestContext(ctx, controller)
	defer cancel()
	return withLegacyController(controller, func() error {
		return c.withOperationClaim(bounded, func() error {
			return c.lightUpUnlocked(bounded, controller.IpAddress, controllerPort(controller), segmentID, index, count, color)
		})
	})
}

func (c *Client) lightUpUnlocked(ctx context.Context, address string, port int64, segmentID, index, count int, hexColor string) error {
	rgb, err := HexToRGB(hexColor)
	if err != nil {
		rgb = []int{255, 255, 255}
	}
	payload := map[string]any{"on": true, "tt": 0, "seg": []map[string]any{{
		"id": segmentID, "on": true, "i": []any{index, index + count, rgb},
	}}}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, controllerURLForAddress(address, port, "/json/state"), bytes.NewBuffer(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("wled returned status: %d", resp.StatusCode)
	}
	return nil
}

// Flash runs synchronously and holds the controller write gate for its complete
// duration, so a physical session cannot begin between blink frames. Failures
// are returned to the caller rather than reported as a successful background job.
func (c *Client) flashUnlocked(ctx context.Context, address string, port int64, segmentID, index, count int, hexColor string, times int, interval time.Duration) error {
	for i := 0; i < times; i++ {
		if err := c.lightUpUnlocked(ctx, address, port, segmentID, index, count, hexColor); err != nil {
			return err
		}
		if !sleepCtx(ctx, interval) {
			return ctx.Err()
		}
		if err := c.lightUpUnlocked(ctx, address, port, segmentID, index, count, "#000000"); err != nil {
			return err
		}
		if !sleepCtx(ctx, interval) {
			return ctx.Err()
		}
	}
	return nil
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
