package ports

import "context"

type CommandBus interface {
	Send(ctx context.Context, command string, payload any) error
}

type QueueProvider[T any] interface {
	Current(ctx context.Context) ([]T, error)
}

type ImageProvider interface {
	Fetch(ctx context.Context, url string) ([]byte, error)
}

type StateStore[T any] interface {
	Load(ctx context.Context, key string) (T, error)
	Save(ctx context.Context, key string, value T) error
}
