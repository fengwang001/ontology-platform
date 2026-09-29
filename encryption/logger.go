package encryption

import (
	"context"
	"log/slog"
)

// SlogLogger 把标准库 log/slog 适配为组件的 Logger。
type SlogLogger struct {
	Logger *slog.Logger
}

// Log 实现 Logger。
func (l SlogLogger) Log(ctx context.Context, level slog.Level, msg string, args ...any) {
	if l.Logger == nil {
		return
	}
	l.Logger.Log(ctx, level, msg, args...)
}
