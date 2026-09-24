// Package view 在 dbuf 之上做前台视图与重建编排：Apply 双写、
// RebuildStep 重放、CommitSwitch/AbortRebuild、View。并发安全。
package view

import (
	"sync"

	"ontology/dbuf"
)

// View 维护 append-only 日志 L 与双缓冲状态，前台 F 始终对外服务。
type View struct {
	mu   sync.RWMutex
	log  []dbuf.Event // append-only 事件日志 L
	buf  *dbuf.Buffer
	bHit int // 非导出：重建期一次 Apply 双写对 B 的 Key 查找次数，不进公开接口
}

// New 以初始日志重放结果构造 View（initial 为历史事件，可为空）。
func New(initial []dbuf.Event) *View {
	v := &View{buf: dbuf.New(nil)}
	for _, ev := range initial {
		v.buf.ApplyFront(ev)
		v.log = append(v.log, ev)
	}
	return v
}

// Apply 应用一批事件：先整体校验，任一条非法则整批不生效。
// 每条事件应用到 F、追加到 L；重建期还要记入 pending（双写）。
func (v *View) Apply(evs ...dbuf.Event) error {
	for _, ev := range evs {
		if !ev.Valid() {
			return dbuf.ErrInvalidEvent
		}
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, ev := range evs {
		v.buf.ApplyFront(ev)
		v.log = append(v.log, ev)
		if v.buf.Rebuilding() {
			v.buf.RecordPending(ev)
			v.buf.HasInBack(ev.Key) // 双写记账：单次哈希查找，与 B 的规模无关
			v.bHit++
		}
	}
	return nil
}

// StartRebuild 开始后台重建：B 清空，快照点为当前 L 长度。
func (v *View) StartRebuild() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.buf.StartRebuild()
}

// RebuildStep 把快照点之前的一条历史事件重放进 B（调用方按日志顺序喂入）。
func (v *View) RebuildStep(ev dbuf.Event) error {
	if !ev.Valid() {
		return dbuf.ErrInvalidEvent
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.buf.Replay(ev)
}

// CommitSwitch 原子切换：补齐 pending 到 B 后换前台，持写锁一次完成。
func (v *View) CommitSwitch() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.buf.Commit()
}

// AbortRebuild 丢弃 B 与 pending，前台不变。
func (v *View) AbortRebuild() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.buf.Abort()
}

// View 任意时刻只返回前台 F 的拷贝。
func (v *View) View() map[string]int64 {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.buf.Front()
}

// Log 返回日志 L 的拷贝（自检与朴素参照用）。
func (v *View) Log() []dbuf.Event {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return append([]dbuf.Event(nil), v.log...)
}

// Snapshot 返回内部状态（B、pending、rebuilding）供编排层自检。
func (v *View) Snapshot() (back map[string]int64, pending []dbuf.Event, rebuilding bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.buf.Back(), v.buf.Pending(), v.buf.Rebuilding()
}
