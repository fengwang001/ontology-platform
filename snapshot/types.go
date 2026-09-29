package snapshot

import "context"

// Entry 是快照中的单个键值对。
type Entry struct {
	Key   string
	Value string
}

// Op 表示变更日志条目的操作类型。
type Op string

const (
	OpInsert Op = "insert"
	OpDelete Op = "delete"
	OpUpdate Op = "update"
)

// Change 是变更日志中的一条记录。
type Change struct {
	Op       Op
	Key      string
	OldValue string
	NewValue string
}

// Config 控制差分行为。
type Config struct {
	// MaxChanges 限制一次差分允许产出的变更条数上限，<=0 表示不限制。
	MaxChanges int
}

// Stats 是差分累计统计。
type Stats struct {
	Applied  int
	Inserted int
	Deleted  int
	Updated  int
}

// Logger 用于打印每步输入、变更与判定依据。
type Logger interface {
	Printf(format string, args ...any)
}

// noopLogger 在未提供日志器时使用。
type noopLogger struct{}

func (noopLogger) Printf(string, ...any) {}

type loggerCtxKey struct{}

// WithLogger 将日志器放入 context，差分过程中的每一步都会输出到该日志器。
func WithLogger(ctx context.Context, logger Logger) context.Context {
	return context.WithValue(ctx, loggerCtxKey{}, logger)
}

func loggerFromCtx(ctx context.Context) Logger {
	if ctx != nil {
		if l, ok := ctx.Value(loggerCtxKey{}).(Logger); ok && l != nil {
			return l
		}
	}
	return noopLogger{}
}
