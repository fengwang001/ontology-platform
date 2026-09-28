package outbox

import (
	"context"
	"errors"
	"sync"
)

// ErrCrashed 表示中继已崩溃，需 Restart 后才能继续投递。
var ErrCrashed = errors.New("outbox: relay crashed, restart required")

// Sink 是下游投递接口。
type Sink interface {
	Deliver(ctx context.Context, msg Message) error
}

// Relay 把发件箱中已提交未标记的消息按序投递到下游。
// 每条消息严格“先投递、后标记”；崩溃重启后未标记的消息会被重投。
type Relay struct {
	mu      sync.Mutex
	store   *Store
	sink    Sink
	crashed bool

	// crashAfterMarks >= 0 时模拟崩溃点：完成该数量的“投递+标记”后，
	// 下一条消息只投递不标记即崩溃（即最后一条已投递未标记）。
	crashAfterMarks int
	marksDone       int
}

// NewRelay 创建中继。crashAfterMarks < 0 表示不注入崩溃。
func NewRelay(store *Store, sink Sink, crashAfterMarks int) *Relay {
	return &Relay{store: store, sink: sink, crashAfterMarks: crashAfterMarks}
}

// RelayOnce 取当前全部待投消息，按提交序与写入序逐条投递并标记。
// 返回本轮成功“投递+标记”的消息数。
func (r *Relay) RelayOnce(ctx context.Context) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.crashed {
		return 0, ErrCrashed
	}
	batch := r.store.Pending()
	done := 0
	for _, msg := range batch {
		// 先投递。
		if err := r.sink.Deliver(ctx, msg); err != nil {
			return done, err
		}
		// 崩溃点注入：本条已投递但不标记，模拟崩溃。
		if r.crashAfterMarks >= 0 && r.marksDone >= r.crashAfterMarks {
			r.crashed = true
			return done, ErrCrashed
		}
		// 后标记。
		r.store.Mark(msg.ID)
		r.marksDone++
		done++
	}
	return done, nil
}

// Restart 模拟崩溃后的重启：恢复中继，未标记的消息将被重投。
// 崩溃注入只生效一次，重启后正常投递。
func (r *Relay) Restart() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.crashed = false
	r.crashAfterMarks = -1
}

// Crashed 报告中继当前是否处于崩溃状态。
func (r *Relay) Crashed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.crashed
}
