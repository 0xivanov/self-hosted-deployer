package store

import (
	"context"
	"errors"
)

var ErrNotFound = errors.New("not found")

type HealthChecker interface {
	Ping(ctx context.Context) error
}
