package server

import "context"

type CallerKind string

const (
	CallerAdmin CallerKind = "admin"
	CallerAgent CallerKind = "agent"
	CallerJoin  CallerKind = "join"
)

type Caller struct {
	Kind    CallerKind
	NodeID  string
	TokenID string // opaque identifier derived from the stored token hash
}

type callerContextKey struct{}
type requestIDContextKey struct{}

func WithCaller(ctx context.Context, caller Caller) context.Context {
	return context.WithValue(ctx, callerContextKey{}, caller)
}

func CallerFromContext(ctx context.Context) (Caller, bool) {
	caller, ok := ctx.Value(callerContextKey{}).(Caller)
	return caller, ok
}

func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDContextKey{}, requestID)
}

func RequestIDFromContext(ctx context.Context) (string, bool) {
	requestID, ok := ctx.Value(requestIDContextKey{}).(string)
	return requestID, ok
}
