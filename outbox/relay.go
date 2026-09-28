package outbox

import "context"

// Logger 是组件使用的最小日志接口。
type Logger interface {
	Printf(format string, args ...any)
}

// Options 配置中继。
type Options struct {
	// MaxPending 为提交后待投消息积压上限，<=0 表示不限制。
	MaxPending int
	Logger     Logger
}

// Relay 是事务发件箱中继。骨架为桩。
type Relay struct{}

// New 构造一个中继实例。
func New(opts Options) *Relay { return nil }

// Begin 按名字开启事务。
func (r *Relay) Begin(name string) error { return nil }

// Write 向指定事务写入一条消息。
func (r *Relay) Write(txName string, msg Message) error { return nil }

// Commit 提交事务并分配提交序。
func (r *Relay) Commit(ctx context.Context, txName string) (commitSeq int64, err error) {
	return 0, nil
}

// Abort 中止事务，其消息永不投递。
func (r *Relay) Abort(ctx context.Context, txName string) error { return nil }

// RelayOnce 取出全部已提交未标记消息，按提交序与写入序先投递后标记。
func (r *Relay) RelayOnce(ctx context.Context) (delivered int, err error) { return 0, nil }

// PendingCount 返回当前待投消息数。
func (r *Relay) PendingCount() int { return 0 }

// NextCommitSeq 返回下一个将分配的提交序。
func (r *Relay) NextCommitSeq() int64 { return 0 }
