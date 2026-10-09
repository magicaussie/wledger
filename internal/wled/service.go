package wled

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/hardware/mapper"
	"github.com/tuxedocurly/wledger/internal/ledspace"
)

// ErrCoordinateSpaceUnresolved indicates that an LED operation which relies on
// stored bin LED indices cannot proceed because the coordinate system of those
// indices is unresolved (for example after restoring a backup without an
// explicit coordinate-space marker). No WLED command is sent in this case.
var ErrCoordinateSpaceUnresolved = errors.New("LED coordinate space is unresolved; locate and flash operations are disabled until it is resolved")

type Service interface {
	LocatePart(ctx context.Context, partID int64) error
	LocateBin(ctx context.Context, controllerID, binID int64) error
	LocateDrawer(ctx context.Context, controllerID, containerID int64) error
	FlashError(ctx context.Context, controllerID, binID int64) error
	GlobalOff(ctx context.Context) error
	Ping(ctx context.Context, ip string) (bool, error)
}

type service struct {
	store  db.Store
	client *Client
	logger *slog.Logger
}

func NewService(store db.Store, client *Client, logger *slog.Logger) Service {
	return &service{
		store:  store,
		client: client,
		logger: logger,
	}
}

func (s *service) Ping(ctx context.Context, ip string) (bool, error) {
	return s.client.Ping(ctx, ip)
}

// ensureCoordinateSpaceResolved returns ErrCoordinateSpaceUnresolved when the
// stored bin LED indices are in an unresolved coordinate space, so that no WLED
// command is sent against a possibly incorrect physical location.
func (s *service) ensureCoordinateSpaceResolved(ctx context.Context) error {
	unresolved, err := ledspace.IsUnresolved(ctx, s.store)
	if err != nil {
		return fmt.Errorf("failed to check LED coordinate space: %w", err)
	}
	if unresolved {
		return ErrCoordinateSpaceUnresolved
	}
	return nil
}

func (s *service) GlobalOff(ctx context.Context) error {
	controllers, err := s.store.GetControllers(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch controllers: %w", err)
	}

	for _, c := range controllers {
		go func(ctrlName, ip string) {
			bgCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			err := s.client.Clear(bgCtx, ip)
			if err != nil {
				s.logger.Error("failed to clear controller", "name", ctrlName, "ip", ip, "err", err)
			}
		}(c.Name, c.IpAddress)
	}

	return nil
}

func (s *service) LocatePart(ctx context.Context, partID int64) error {
	if err := s.ensureCoordinateSpaceResolved(ctx); err != nil {
		return err
	}

	assignments, err := s.store.GetPartAssignments(ctx, partID)
	if err != nil {
		return fmt.Errorf("failed to fetch part locations: %w", err)
	}

	settings, _ := s.store.GetSettings(ctx)
	if settings.ColorLocate.String == "" {
		settings.ColorLocate.String = "#0000FF" // Fallback
	}

	foundAny := false
	for _, a := range assignments {
		if !a.ControllerIp.Valid || a.ControllerIp.String == "" || !a.LedIndex.Valid {
			continue
		}

		// Fetch containers for this controller to calculate global index
		containers, err := s.store.GetContainersByController(ctx, a.ControllerID.Int64)
		if err != nil {
			s.logger.Error("failed to fetch containers for locate", "controller_id", a.ControllerID.Int64, "err", err)
			continue
		}

		// Construct db.Bin from assignment row
		bin := db.Bin{
			ID:          a.BinID.Int64,
			ContainerID: a.ContainerID.Int64,
			LedIndex:    a.LedIndex,
			Width:       a.Width,
		}

		segID, globalIdx, err := mapper.CalculateGlobalIndex(containers, bin)
		if err != nil {
			s.logger.Error("mapping failed for part locate", "bin_id", bin.ID, "err", err)
			continue
		}

		foundAny = true
		err = s.triggerLocate(ctx, a.ControllerIp.String, int(segID), int(globalIdx), int(a.Width.Int64), settings)
		if err != nil {
			s.logger.Error("failed to locate assignment", "assignment_id", a.ID, "err", err)
		}
	}

	if !foundAny {
		s.logger.Info("no valid assignments found to locate", "part_id", partID)
	}

	return nil
}

func (s *service) LocateBin(ctx context.Context, controllerID, binID int64) error {
	if err := s.ensureCoordinateSpaceResolved(ctx); err != nil {
		return err
	}

	controller, err := s.store.GetController(ctx, controllerID)
	if err != nil {
		return fmt.Errorf("controller not found: %w", err)
	}

	bin, err := s.store.GetBin(ctx, binID)
	if err != nil {
		return fmt.Errorf("bin not found: %w", err)
	}

	// Fetch containers for this controller
	containers, err := s.store.GetContainersByController(ctx, controllerID)
	if err != nil {
		return fmt.Errorf("failed to fetch containers for controller: %w", err)
	}

	segID, globalIdx, err := mapper.CalculateGlobalIndex(containers, bin)
	if err != nil {
		return fmt.Errorf("mapping failed: %w", err)
	}

	settings, _ := s.store.GetSettings(ctx)
	if settings.ColorLocate.String == "" {
		settings.ColorLocate.String = "#0000FF"
	}

	return s.triggerLocate(ctx, controller.IpAddress, int(segID), int(globalIdx), int(bin.Width.Int64), settings)
}

// LocateDrawer lights up a drawer's full LED range so it can be found
// physically. A drawer is a container and owns its physical LED allocation
// (led_start/led_count, segment-relative); the range is taken directly from the
// allocation and is independent of the drawer's bins.
func (s *service) LocateDrawer(ctx context.Context, controllerID, containerID int64) error {
	controller, err := s.store.GetController(ctx, controllerID)
	if err != nil {
		return fmt.Errorf("controller not found: %w", err)
	}

	container, err := s.store.GetContainer(ctx, containerID)
	if err != nil {
		return fmt.Errorf("drawer not found: %w", err)
	}

	if container.LedCount < 1 {
		return fmt.Errorf("drawer %d has no LED allocation to locate", containerID)
	}

	settings, _ := s.store.GetSettings(ctx)
	color := settings.ColorLocate.String
	if color == "" {
		color = "#0000FF"
	}

	return s.client.Apply(ctx, controller.IpAddress, int(container.SegmentID), int(container.LedStart), int(container.LedCount), State{Color: color, Mode: ModeSolid})
}

// FlashError flashes a bin's LEDs in the configured error colour (default red)
// to signal a failed action. Best-effort.
func (s *service) FlashError(ctx context.Context, controllerID, binID int64) error {
	if err := s.ensureCoordinateSpaceResolved(ctx); err != nil {
		return err
	}

	controller, err := s.store.GetController(ctx, controllerID)
	if err != nil {
		return fmt.Errorf("controller not found: %w", err)
	}

	bin, err := s.store.GetBin(ctx, binID)
	if err != nil {
		return fmt.Errorf("bin not found: %w", err)
	}

	if !bin.LedIndex.Valid {
		return fmt.Errorf("bin %d has no LED to flash", binID)
	}

	containers, err := s.store.GetContainersByController(ctx, controllerID)
	if err != nil {
		return fmt.Errorf("failed to fetch containers: %w", err)
	}

	segID, globalIdx, err := mapper.CalculateGlobalIndex(containers, bin)
	if err != nil {
		return fmt.Errorf("mapping failed: %w", err)
	}

	width := int(bin.Width.Int64)
	if width < 1 {
		width = 1
	}

	settings, _ := s.store.GetSettings(ctx)
	color := settings.ColorError.String
	if color == "" {
		color = "#FF0000"
	}

	return s.client.Apply(ctx, controller.IpAddress, int(segID), int(globalIdx), width, State{Color: color, Mode: ModeFlash})
}

func (s *service) triggerLocate(ctx context.Context, ip string, segmentID, index, width int, settings db.Setting) error {
	if width < 1 {
		width = 1
	}

	// Light Up
	state := State{Color: settings.ColorLocate.String, Mode: ModeSolid}
	err := s.client.Apply(ctx, ip, segmentID, index, width, state)
	if err != nil {
		return err
	}

	// Handle Auto-Off Timer
	if settings.EnableLocateTimeout.Bool && settings.LocateTimeoutSeconds.Int64 > 0 {
		timeoutDuration := time.Duration(settings.LocateTimeoutSeconds.Int64) * time.Second

		go func(ipAddr string, sID, idx, count int, duration time.Duration) {
			time.Sleep(duration)
			bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			_ = s.client.LightUp(bgCtx, ipAddr, sID, idx, count, "#000000")
		}(ip, segmentID, index, width, timeoutDuration)
	}

	return nil
}
