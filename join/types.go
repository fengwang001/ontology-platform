package join

import (
	"errors"
	"log/slog"
)

// Entry 是内连接结果中的一条记录：左行值与匹配右行值。
type Entry struct {
	LeftKey  string
	LeftVal  string
	RightKey string
	RightVal string
	LeftHash uint64
}

// Response 是右表侧先进先出队列中的一个异步响应。
// LeftHash 为该响应产生时左行的哈希；投递时与当前左行哈希比对。
type Response struct {
	LeftKey  string
	RightKey string
	RightVal string
	Exists   bool
	LeftHash uint64
}

// Delivery 描述一次 Deliver 的判定结果，便于日志记录。
type Delivery struct {
	Resp    Response
	Applied bool
	Reason  string
}

// 各类可区分的拒绝原因。调用方可用 errors.Is 判定。
var (
	// ErrEmptyKey：左/右表行的主键为空字符串。
	ErrEmptyKey = errors.New("join: empty primary key")
	// ErrNilForeignKey：左行外键为 nil（通过 *string 传 nil 表示）。
	ErrNilForeignKey = errors.New("join: nil foreign key")
	// ErrEmptyQueue：投递时待投递响应队列为空。
	ErrEmptyQueue = errors.New("join: empty response queue")
	// ErrQueueFull：本次操作会使待投递响应数量超过上限。
	ErrQueueFull = errors.New("join: pending response limit exceeded")
)

// Option 配置 Joiner。
type Option func(*Joiner)

// WithMaxPending 设置待投递响应队列上限，必须 > 0。
func WithMaxPending(n int) Option {
	return func(j *Joiner) {
		if n > 0 {
			j.maxPending = n
		}
	}
}

// WithLogger 注入结构化日志记录器；nil 时使用 slog.Default()。
func WithLogger(l *slog.Logger) Option {
	return func(j *Joiner) {
		j.log = l
	}
}
