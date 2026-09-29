package snapshot

// Entry 是快照中的一个键值对。
type Entry struct {
	Key   string
	Value string
}

// Op 标识变更类型。
type Op int

const (
	OpInsert Op = iota + 1
	OpDelete
	OpUpdate
)

func (o Op) String() string { return "" }

// Change 是变更日志中的一条记录。
type Change struct {
	Key      string
	Op       Op
	OldValue string
	NewValue string
}

// Config 是差分服务的配置。
type Config struct {
	// MaxChanges 限制单次差分允许输出的最大变更条数，必须 > 0。
	MaxChanges int
}

// Logger 接收逐步输入、变更与判定依据。
type Logger interface {
	Printf(format string, args ...any)
}

// LoggerFunc 把函数适配为 Logger。
type LoggerFunc func(format string, args ...any)

func (f LoggerFunc) Printf(format string, args ...any) {}
