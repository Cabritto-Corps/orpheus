package spotify

import (
	"context"
	"errors"
	"fmt"
	"strings"

	spotifyapi "github.com/zmb3/spotify/v2"
)

func (s *Service) Play(ctx context.Context, target string) error {
	return s.withDeviceCommand(ctx, target, func(deviceID spotifyapi.ID) error {
		err := s.client.PlayOpt(ctx, &spotifyapi.PlayOptions{DeviceID: &deviceID})
		if err == nil {
			return nil
		}
		var apiErr spotifyapi.Error
		if errors.As(err, &apiErr) && (apiErr.Status == 403 || apiErr.Status == 404) {
			state, stateErr := s.playerStateWithRetry(ctx)
			if stateErr == nil && (state == nil || state.Item == nil) {
				return ErrNoPlaybackContext
			}
		}
		return err
	})
}

func (s *Service) Pause(ctx context.Context, target string) error {
	return s.withDeviceCommand(ctx, target, func(deviceID spotifyapi.ID) error {
		return s.client.PauseOpt(ctx, &spotifyapi.PlayOptions{DeviceID: &deviceID})
	})
}

func (s *Service) Next(ctx context.Context, target string) error {
	return s.withDeviceCommand(ctx, target, func(deviceID spotifyapi.ID) error {
		return s.client.NextOpt(ctx, &spotifyapi.PlayOptions{DeviceID: &deviceID})
	})
}

func (s *Service) Previous(ctx context.Context, target string) error {
	return s.withDeviceCommand(ctx, target, func(deviceID spotifyapi.ID) error {
		return s.client.PreviousOpt(ctx, &spotifyapi.PlayOptions{DeviceID: &deviceID})
	})
}

func (s *Service) SetVolume(ctx context.Context, target string, volume int) error {
	if volume < 0 || volume > 100 {
		return errors.New("volume must be in range 0..100")
	}
	return s.withDeviceCommand(ctx, target, func(deviceID spotifyapi.ID) error {
		return s.client.VolumeOpt(ctx, volume, &spotifyapi.PlayOptions{DeviceID: &deviceID})
	})
}

func (s *Service) Seek(ctx context.Context, target string, positionMS int) error {
	if positionMS < 0 {
		return errors.New("position must be >= 0")
	}
	return s.withDeviceCommand(ctx, target, func(deviceID spotifyapi.ID) error {
		return s.client.SeekOpt(ctx, positionMS, &spotifyapi.PlayOptions{DeviceID: &deviceID})
	})
}

func (s *Service) Shuffle(ctx context.Context, target string, shuffle bool) error {
	return s.withDeviceCommand(ctx, target, func(deviceID spotifyapi.ID) error {
		return s.client.ShuffleOpt(ctx, shuffle, &spotifyapi.PlayOptions{DeviceID: &deviceID})
	})
}

func (s *Service) SetRepeat(ctx context.Context, target string, state string) error {
	state = strings.ToLower(strings.TrimSpace(state))
	if state != "off" && state != "context" && state != "track" {
		return errors.New("repeat state must be one of off, context, track")
	}
	return s.withDeviceCommand(ctx, target, func(deviceID spotifyapi.ID) error {
		return s.client.RepeatOpt(ctx, state, &spotifyapi.PlayOptions{DeviceID: &deviceID})
	})
}

func (s *Service) PlayPlaylist(ctx context.Context, target, playlistURI string) error {
	if strings.TrimSpace(playlistURI) == "" {
		return errors.New("playlist URI must not be empty")
	}

	device, err := s.FindDeviceByName(ctx, target)
	if err != nil {
		return err
	}

	uri := spotifyapi.URI(playlistURI)
	return s.client.PlayOpt(ctx, &spotifyapi.PlayOptions{
		DeviceID:        &device.ID,
		PlaybackContext: &uri,
	})
}

func (s *Service) QueueTracks(ctx context.Context, target string, trackIDs []string) ([]string, error) {
	if len(trackIDs) == 0 {
		return nil, nil
	}
	device, err := s.EnsureDeviceActive(ctx, target)
	if err != nil {
		return nil, err
	}
	queued := make([]string, 0, len(trackIDs))
	for _, trackID := range trackIDs {
		trackID = strings.TrimSpace(trackID)
		id := spotifyapi.ID(trackID)
		if id == "" {
			continue
		}
		if err := s.client.QueueSongOpt(ctx, id, &spotifyapi.PlayOptions{DeviceID: &device.ID}); err != nil {
			return queued, fmt.Errorf("queue track %q: %w", trackID, err)
		}
		queued = append(queued, trackID)
	}
	return queued, nil
}
