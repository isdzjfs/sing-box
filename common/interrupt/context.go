package interrupt

import "context"

type contextKeyIsExternalConnection struct{}
type contextKeyConnectionTracker struct{}

type ConnectionTracker interface {
	RegisterGeneration(isExternal bool, generation uint64)
}

func ContextWithIsExternalConnection(ctx context.Context, isExternal bool) context.Context {
	return context.WithValue(ctx, contextKeyIsExternalConnection{}, isExternal)
}

func IsExternalConnectionFromContext(ctx context.Context) bool {
	isExternal, _ := ctx.Value(contextKeyIsExternalConnection{}).(bool)
	return isExternal
}

func ContextWithConnectionTracker(ctx context.Context, tracker ConnectionTracker) context.Context {
	return context.WithValue(ctx, contextKeyConnectionTracker{}, tracker)
}

func RegisterConnectionFromContext(ctx context.Context, isExternal bool, generation uint64) {
	// Internal DNS and shared sessions may inherit an inbound tracker, but must
	// not bind that inbound to the session's selection generation.
	if !isExternal {
		return
	}
	if tracker, loaded := ctx.Value(contextKeyConnectionTracker{}).(ConnectionTracker); loaded {
		tracker.RegisterGeneration(isExternal, generation)
	}
}
