package interrupt

import "context"

type contextKeyIsExternalConnection struct{}
type contextKeyConnectionTracker struct{}

type ConnectionTracker interface {
	RegisterGeneration(isExternal bool, generation uint64)
}

func ContextWithIsExternalConnection(ctx context.Context) context.Context {
	return context.WithValue(ctx, contextKeyIsExternalConnection{}, true)
}

func IsExternalConnectionFromContext(ctx context.Context) bool {
	return ctx.Value(contextKeyIsExternalConnection{}) != nil
}

func ContextWithConnectionTracker(ctx context.Context, tracker ConnectionTracker) context.Context {
	return context.WithValue(ctx, contextKeyConnectionTracker{}, tracker)
}

func RegisterConnectionFromContext(ctx context.Context, isExternal bool, generation uint64) {
	if tracker, loaded := ctx.Value(contextKeyConnectionTracker{}).(ConnectionTracker); loaded {
		tracker.RegisterGeneration(isExternal, generation)
	}
}
