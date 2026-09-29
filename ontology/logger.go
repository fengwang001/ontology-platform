package ontology

import (
	"context"
	"fmt"
	"log/slog"
)

// NopLogger 丢弃所有过程日志。
func NopLogger() Logger { return nopLogger{} }

type nopLogger struct{}

func (nopLogger) Logf(context.Context, string, ...any) {}

// SlogLogger 用标准库 slog 记录差分每一步。
type SlogLogger struct {
	Logger *slog.Logger
	Level  slog.Level
}

// Logf 实现 Logger。
func (s SlogLogger) Logf(ctx context.Context, format string, args ...any) {
	l := s.Logger
	if l == nil {
		l = slog.Default()
	}
	l.Log(ctx, s.Level, fmt.Sprintf(format, args...))
}
