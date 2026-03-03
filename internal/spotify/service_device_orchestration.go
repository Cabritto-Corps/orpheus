package spotify

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	spotifyapi "github.com/zmb3/spotify/v2"
)

func (s *Service) FindDeviceByName(ctx context.Context, target string) (*spotifyapi.PlayerDevice, error) {
	devices, err := s.getPlayerDevices(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetch spotify devices: %w", err)
	}
	device, _, err := s.resolveDevice(target, devices)
	if err == nil || !errors.Is(err, ErrDeviceNotFound) {
		return device, err
	}
	deadline := time.Now().Add(deviceLookupRetryWindow)
	for {
		devices, err = s.refreshPlayerDevices(ctx)
		if err != nil {
			return nil, fmt.Errorf("fetch spotify devices: %w", err)
		}
		device, _, err = s.resolveDevice(target, devices)
		if err == nil || !errors.Is(err, ErrDeviceNotFound) {
			return device, err
		}
		if time.Now().After(deadline) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(deviceLookupRetryDelay):
		}
	}
}

func (s *Service) DeviceDoctor(ctx context.Context, target string) (*DeviceDoctorReport, error) {
	devices, err := s.getPlayerDevices(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetch spotify devices: %w", err)
	}
	report := &DeviceDoctorReport{
		TargetDevice: target,
		Mode:         s.mode,
		Discovered:   make([]string, 0, len(devices)),
	}
	for _, d := range devices {
		report.Discovered = append(report.Discovered, fmt.Sprintf("%s (active=%t restricted=%t)", d.Name, d.Active, d.Restricted))
	}
	device, matchedBy, resolveErr := s.resolveDevice(target, devices)
	if resolveErr != nil {
		report.Notes = append(report.Notes, "No device match. Check that the target device is running and spotify_device_name matches.")
		return report, resolveErr
	}
	report.MatchedBy = matchedBy
	report.MatchedName = device.Name
	if device.Restricted {
		report.Notes = append(report.Notes, "Matched device is restricted and cannot receive commands.")
	}
	if !device.Active {
		report.Notes = append(report.Notes, "Device is discoverable but not active; transfer will be attempted on play actions.")
	}
	return report, nil
}

func (s *Service) EnsureDeviceActive(ctx context.Context, target string) (*spotifyapi.PlayerDevice, error) {
	device, err := s.FindDeviceByName(ctx, target)
	if err != nil {
		return nil, err
	}
	if device.Restricted {
		return nil, errors.New("spotify device is restricted")
	}
	if device.Active {
		return device, nil
	}
	if err := s.transferWithRetry(ctx, device.ID); err != nil {
		return nil, fmt.Errorf("transfer playback to %q: %w", target, err)
	}
	if err := s.waitForDeviceActive(ctx, device.ID); err != nil {
		return nil, err
	}
	device.Active = true
	return device, nil
}

func (s *Service) ListDevices(ctx context.Context) ([]spotifyapi.PlayerDevice, error) {
	devices, err := s.getPlayerDevices(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetch spotify devices: %w", err)
	}
	return devices, nil
}

func (s *Service) withDeviceCommand(ctx context.Context, target string, command func(deviceID spotifyapi.ID) error) error {
	device, err := s.FindDeviceByName(ctx, target)
	if err != nil {
		return err
	}
	if device.Restricted {
		return errors.New("spotify device is restricted")
	}

	if err := command(device.ID); err != nil {
		if device.Active || !shouldRetryDeviceCommandAfterTransfer(err) {
			return err
		}
		if err := s.transferWithRetry(ctx, device.ID); err != nil {
			return fmt.Errorf("transfer playback to %q: %w", target, err)
		}
		if err := s.waitForDeviceActive(ctx, device.ID); err != nil {
			return err
		}
		return command(device.ID)
	}
	return nil
}

func shouldRetryDeviceCommandAfterTransfer(err error) bool {
	var apiErr spotifyapi.Error
	if errors.As(err, &apiErr) {
		if apiErr.Status == 404 {
			return true
		}
		if apiErr.Status == 403 {
			msg := strings.ToLower(apiErr.Message)
			return strings.Contains(msg, "active device") || strings.Contains(msg, "no active device")
		}
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "active device") || strings.Contains(msg, "no active device")
}

func (s *Service) resolveDevice(target string, devices []spotifyapi.PlayerDevice) (*spotifyapi.PlayerDevice, string, error) {
	target = strings.TrimSpace(target)
	targetLower := strings.ToLower(target)

	for _, device := range devices {
		if strings.EqualFold(device.Name, target) {
			d := device
			return &d, "exact", nil
		}
	}
	if s.mode == DeviceModeRelaxed && targetLower != "" {
		for _, device := range devices {
			if strings.Contains(strings.ToLower(device.Name), targetLower) {
				d := device
				return &d, "contains", nil
			}
		}
	}
	if s.mode == DeviceModeRelaxed && s.allowActiveFallback {
		for _, device := range devices {
			if device.Active && !device.Restricted {
				d := device
				return &d, "active-fallback", nil
			}
		}
	}

	names := make([]string, 0, len(devices))
	for _, device := range devices {
		names = append(names, device.Name)
	}
	return nil, "", fmt.Errorf("%w: target=%q discovered=%v", ErrDeviceNotFound, target, names)
}

func (s *Service) waitForDeviceActive(ctx context.Context, deviceID spotifyapi.ID) error {
	deadline := time.Now().Add(deviceActivationWaitTimeout)
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		devices, err := s.refreshPlayerDevices(ctx)
		if err == nil {
			for _, d := range devices {
				if d.ID == deviceID && d.Active {
					return nil
				}
			}
		}
		time.Sleep(deviceActivationPollInterval)
	}
	s.invalidateDeviceCache()
	return errors.New("device transfer timed out while waiting to become active")
}

func (s *Service) transferWithRetry(ctx context.Context, deviceID spotifyapi.ID) error {
	wait := transferInitialRetryDelay
	for i := 0; i < transferMaxAttempts; i++ {
		err := s.client.TransferPlayback(ctx, deviceID, false)
		if err == nil {
			s.invalidateDeviceCache()
			return nil
		}
		s.invalidateDeviceCache()
		if !IsTransientAPIError(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
			wait *= 2
		}
	}
	return errors.New("transfer playback failed after retries")
}

func (s *Service) getPlayerDevices(ctx context.Context) ([]spotifyapi.PlayerDevice, error) {
	now := time.Now()
	s.deviceCacheMu.RLock()
	if s.deviceCacheSet && now.Before(s.deviceCacheExpiry) {
		devices := append([]spotifyapi.PlayerDevice(nil), s.deviceCacheDevices...)
		s.deviceCacheMu.RUnlock()
		return devices, nil
	}
	s.deviceCacheMu.RUnlock()
	return s.refreshPlayerDevices(ctx)
}

func (s *Service) refreshPlayerDevices(ctx context.Context) ([]spotifyapi.PlayerDevice, error) {
	devices, err := apiCallWithRetry(ctx, func() ([]spotifyapi.PlayerDevice, error) {
		return s.client.PlayerDevices(ctx)
	})
	if err != nil {
		s.invalidateDeviceCache()
		return nil, err
	}
	copied := append([]spotifyapi.PlayerDevice(nil), devices...)
	s.deviceCacheMu.Lock()
	s.deviceCacheDevices = copied
	s.deviceCacheExpiry = time.Now().Add(s.deviceCacheTTL)
	s.deviceCacheSet = true
	s.deviceCacheMu.Unlock()
	return copied, nil
}

func (s *Service) invalidateDeviceCache() {
	s.deviceCacheMu.Lock()
	s.deviceCacheDevices = nil
	s.deviceCacheExpiry = time.Time{}
	s.deviceCacheSet = false
	s.deviceCacheMu.Unlock()
}
