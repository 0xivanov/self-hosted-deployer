package server

import "context"

type CallerKind string

const (
	CallerAdmin CallerKind = "admin"
	CallerAgent CallerKind = "agent"
	CallerJoin  CallerKind = "join"
)

type Caller struct {
	Kind   CallerKind
	NodeID string
}

type callerContextKey struct{}

func WithCaller(ctx context.Context, caller Caller) context.Context {
	return context.WithValue(ctx, callerContextKey{}, caller)
}

func CallerFromContext(ctx context.Context) (Caller, bool) {
	caller, ok := ctx.Value(callerContextKey{}).(Caller)
	return caller, ok
}
