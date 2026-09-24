// Package dbuf 实现物化视图的双缓冲状态：前台 F、后台 B、
// pending 列表与 rebuilding 标记，以及切换/中止/pending 补齐。
// 不依赖其他包；并发安全由上层 view 保证。
package dbuf

import "errors"

// 可判定的哨兵错误，四类故障互不相同。
var (
	ErrNoRebuild      = errors.New("dbuf: commit/abort without rebuild in progress")
	ErrRebuildRunning = errors.New("dbuf: rebuild already in progress")
	ErrReplayIdle     = errors.New("dbuf: replay step without rebuild in progress")
	ErrInvalidEvent   = errors.New("dbuf: invalid event (empty key or zero delta)")
)

// Event 是一条计数变更：应用后 count[Key] += Delta，归 0 删 Key。
type Event struct {
	Key   string
	Delta int64
}

// Valid 报告事件是否合法：Key 非空且 Delta 非零。
func (e Event) Valid() bool { return e.Key != "" && e.Delta != 0 }

// Buffer 是双缓冲状态体。零值不可用，须用 New 构造。
type Buffer struct {
	f          map[string]int64 // 前台，始终对外服务
	b          map[string]int64 // 后台重建区，非重建时为 nil
	pending    []Event          // 重建期间到达、待切换前补齐的新事件
	rebuilding bool
}

// New 以 front 为初始前台构造 Buffer（拷贝一份，不共享）。
func New(front map[string]int64) *Buffer {
	f := make(map[string]int64, len(front))
	for k, v := range front {
		f[k] = v
	}
	return &Buffer{f: f}
}

// apply 把一条事件应用到给定视图：归 0 删 Key。
func apply(view map[string]int64, ev Event) {
	v := view[ev.Key] + ev.Delta
	if v == 0 {
		delete(view, ev.Key)
	} else {
		view[ev.Key] = v
	}
}

// ApplyFront 把新事件应用到前台 F。
func (d *Buffer) ApplyFront(ev Event) { apply(d.f, ev) }

// Front 返回前台 F 的拷贝，供 View 读出。
func (d *Buffer) Front() map[string]int64 {
	out := make(map[string]int64, len(d.f))
	for k, v := range d.f {
		out[k] = v
	}
	return out
}

// Rebuilding 报告是否处于重建期。
func (d *Buffer) Rebuilding() bool { return d.rebuilding }

// Pending 返回 pending 列表的拷贝（演示/自检用）。
func (d *Buffer) Pending() []Event { return append([]Event(nil), d.pending...) }

// Back 返回 B 的拷贝；非重建期返回 nil。
func (d *Buffer) Back() map[string]int64 {
	if d.b == nil {
		return nil
	}
	out := make(map[string]int64, len(d.b))
	for k, v := range d.b {
		out[k] = v
	}
	return out
}

// StartRebuild 开启重建：B 置空、pending 清空、标记置位。
// 重建期重复调用整体失败，状态不变。
func (d *Buffer) StartRebuild() error {
	if d.rebuilding {
		return ErrRebuildRunning
	}
	d.b = make(map[string]int64)
	d.pending = nil
	d.rebuilding = true
	return nil
}

// Replay 把快照点之前的一条历史事件重放进 B。非重建期整体失败。
func (d *Buffer) Replay(ev Event) error {
	if !d.rebuilding {
		return ErrReplayIdle
	}
	apply(d.b, ev)
	return nil
}

// RecordPending 把重建期间到达的新事件记入 pending（不直接写 B）。
func (d *Buffer) RecordPending(ev Event) { d.pending = append(d.pending, ev) }

// HasInBack 报告 key 是否已在 B 中（单次哈希查找，供双写记账用）。
func (d *Buffer) HasInBack(key string) bool {
	_, ok := d.b[key]
	return ok
}

// Commit 先把 pending 全部回放进 B，再原子换前台：F=B、B=nil、清场。
// 非重建期整体失败，状态不变。
func (d *Buffer) Commit() error {
	if !d.rebuilding {
		return ErrNoRebuild
	}
	for _, ev := range d.pending {
		apply(d.b, ev)
	}
	d.f = d.b
	d.b = nil
	d.pending = nil
	d.rebuilding = false
	return nil
}

// Abort 丢弃 B 与 pending，F 不变。非重建期整体失败，状态不变。
func (d *Buffer) Abort() error {
	if !d.rebuilding {
		return ErrNoRebuild
	}
	d.b = nil
	d.pending = nil
	d.rebuilding = false
	return nil
}
