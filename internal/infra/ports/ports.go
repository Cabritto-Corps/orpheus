package ports

import "context"

// CommandBus publishes user intents or playback commands to a backend.
type CommandBus interface {
	Send(ctx context.Context, command string, payload any) error
}

// QueueProvider exposes queue snapshots for UI/domain consumers.
type QueueProvider[T any] interface {
	Current(ctx context.Context) ([]T, error)
}

// ImageProvider resolves image bytes by URL or key.
type ImageProvider interface {
	Fetch(ctx context.Context, url string) ([]byte, error)
}

// StateStore persists small state snapshots/checkpoints.
type StateStore[T any] interface {
	Load(ctx context.Context, key string) (T, error)
	Save(ctx context.Context, key string, value T) error
}

