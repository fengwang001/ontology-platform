// Package ontology 提供物化视图的双缓冲重建与原子切换实现。
package ontology

import (
	"errors"
	"sync"
)

// 各类可判定的错误，互不相同的哨兵值，调用方可用 errors.Is 判定。
var (
	// ErrNotRebuilding 表示在非重建状态下执行了切换或中止。
	ErrNotRebuilding = errors.New("ontology: 当前不在重建中，无法切换或中止")
	// ErrAlreadyRebuilding 表示在重建进行中重复发起重建。
	ErrAlreadyRebuilding = errors.New("ontology: 重建已在进行中，不能重复开始")
	// ErrReplayNotRebuilding 表示在非重建状态下执行了重放。
	ErrReplayNotRebuilding = errors.New("ontology: 当前不在重建中，无法重放")
	// ErrEmptyKey 表示事件键为空。
	ErrEmptyKey = errors.New("ontology: 事件键不能为空")
	// ErrZeroDelta 表示事件增量为零。
	ErrZeroDelta = errors.New("ontology: 事件增量不能为零")
)

// Event 是一条增量事件：对某个键施加一个非零增量。
type Event struct {
	Key   string
	Delta int64
}

// View 是双缓冲物化视图：前台缓冲始终对外服务，
// 重建期间后台缓冲从空开始按日志重放，切换时原子替换前台指针。
type View struct {
	mu sync.RWMutex

	front map[string]int64 // 前台视图，始终对外服务
	log   []Event          // 全量事件日志，按到达顺序追加

	rebuilding bool // 是否处于重建中
	snapshot   int  // 开始重建时的日志长度（快照点）
	replayed   int  // 已重放到后台缓冲的日志条数
	back       map[string]int64
	pending    []Event // 重建期间到达、待切换前补齐的事件
}

// NewView 创建一个空视图。
func NewView() *View {
	return &View{front: make(map[string]int64)}
}

// validate 校验单条事件的合法性。
func validate(ev Event) error {
	if ev.Key == "" {
		return ErrEmptyKey
	}
	if ev.Delta == 0 {
		return ErrZeroDelta
	}
	return nil
}

// Apply 将一批事件原子地应用到前台视图并追加到日志；
// 重建期间到达的事件同时记入待补齐列表。
// 批内任一事件非法则整批拒绝，不改变任何状态。
func (v *View) Apply(events ...Event) error {
	for _, ev := range events {
		if err := validate(ev); err != nil {
			return err
		}
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, ev := range events {
		v.front[ev.Key] += ev.Delta
		v.log = append(v.log, ev)
	}
	if v.rebuilding {
		v.pending = append(v.pending, events...)
	}
	return nil
}

// BeginRebuild 开始一次重建：记录快照点，后台缓冲从空开始。
// 重建进行中重复开始将被拒绝。
func (v *View) BeginRebuild() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.rebuilding {
		return ErrAlreadyRebuilding
	}
	v.rebuilding = true
	v.snapshot = len(v.log)
	v.replayed = 0
	v.back = make(map[string]int64)
	v.pending = nil
	return nil
}

// ReplayNext 将快照点内的下一条历史事件重放到后台缓冲，
// 返回本次重放的事件；快照内事件全部重放完成后返回 done=true。
// 非重建状态下调用将被拒绝。
func (v *View) ReplayNext() (ev Event, done bool, err error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.rebuilding {
		return Event{}, false, ErrReplayNotRebuilding
	}
	if v.replayed >= v.snapshot {
		return Event{}, true, nil
	}
	ev = v.log[v.replayed]
	v.back[ev.Key] += ev.Delta
	v.replayed++
	return ev, v.replayed >= v.snapshot, nil
}

// Switch 先把快照内剩余历史事件重放完，再补齐重建期间到达的
// 待补齐事件，然后原子地把前台指针切换到后台缓冲并清空重建状态。
// 非重建状态下调用将被拒绝。
func (v *View) Switch() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.rebuilding {
		return ErrNotRebuilding
	}
	for v.replayed < v.snapshot {
		ev := v.log[v.replayed]
		v.back[ev.Key] += ev.Delta
		v.replayed++
	}
	for _, ev := range v.pending {
		v.back[ev.Key] += ev.Delta
	}
	v.front = v.back
	v.back = nil
	v.pending = nil
	v.rebuilding = false
	v.snapshot = 0
	v.replayed = 0
	return nil
}

// Abort 中止重建：丢弃后台缓冲与待补齐列表，前台保持不变。
// 非重建状态下调用将被拒绝。
func (v *View) AbortRebuild() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.rebuilding {
		return ErrNotRebuilding
	}
	v.back = nil
	v.pending = nil
	v.rebuilding = false
	v.snapshot = 0
	v.replayed = 0
	return nil
}

// Get 读取前台视图中某个键的当前计数，可并发调用。
func (v *View) Get(key string) (int64, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	n, ok := v.front[key]
	return n, ok
}

// Snapshot 返回前台视图的完整一致快照（副本），可并发调用。
func (v *View) Snapshot() map[string]int64 {
	v.mu.RLock()
	defer v.mu.RUnlock()
	out := make(map[string]int64, len(v.front))
	for k, n := range v.front {
		out[k] = n
	}
	return out
}

// Log 返回全量事件日志的副本。
func (v *View) Log() []Event {
	v.mu.RLock()
	defer v.mu.RUnlock()
	out := make([]Event, len(v.log))
	copy(out, v.log)
	return out
}

// Rebuilding 报告当前是否处于重建中。
func (v *View) Rebuilding() bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.rebuilding
}
