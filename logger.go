package depsolver

import "context"

// Logger 接收求解过程中的可复现判定日志。
type Logger interface {
	Logf(format string, args ...any)
}

type nopLogger struct{}

func (nopLogger) Logf(string, ...any) {}

func loggerFromContext(ctx context.Context) Logger {
	if l, ok := ctx.Value(loggerKey{}).(Logger); ok && l != nil {
		return l
	}
	return nopLogger{}
}

type loggerKey struct{}

// WithLogger 将判定日志记录器附加到 ctx。
func WithLogger(ctx context.Context, l Logger) context.Context {
	return context.WithValue(ctx, loggerKey{}, l)
}
