package abac

import (
	"context"
	"io"
	"log/slog"
	"os"
)

// SlogLogger 用 slog 输出判定审计日志：主体、资源、动作、策略与判定依据。
type SlogLogger struct {
	logger *slog.Logger
}

// NewSlogLogger 创建写入 stderr 的文本格式审计日志器。
func NewSlogLogger() *SlogLogger {
	return NewSlogLoggerWithWriter(os.Stderr)
}

// NewSlogLoggerWithWriter 创建写入指定输出的日志器，便于测试捕获。
func NewSlogLoggerWithWriter(w io.Writer) *SlogLogger {
	return &SlogLogger{logger: slog.New(slog.NewTextHandler(w, nil))}
}

// LogDecision 实现 Logger。
func (l *SlogLogger) LogDecision(ctx context.Context, entry LogEntry) {
	_ = ctx
	l.logger.Info("abac decision",
		slog.String("action", entry.Action),
		slog.Bool("allowed", entry.Allowed),
		slog.Any("subject", entry.Subject),
		slog.Any("resource", entry.Resource),
		slog.String("reason", string(entry.Reason)),
		slog.String("deny_policy", entry.DenyPolicy),
		slog.String("allow_policy", entry.AllowPolicy),
		slog.Any("matched_policies", entry.Matched),
		slog.Any("indeterminate_policies", entry.Indeterminate),
		slog.String("basis", entry.Basis),
	)
}
