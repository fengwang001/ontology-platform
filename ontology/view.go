package ontology

import (
	"errors"
	"sync"
)

// Event 表示一条针对某个键的增量计数事件。
type Event struct {
	Key   string
	Delta int64
}

// 控制流错误：四类非法控制操作互不相同，可用 errors.Is 判定。
var (
	ErrSwitchWithoutRebuild = errors.New("switch rejected: no rebuild in progress")
	ErrAbortWithoutRebuild  = errors.New("abort rejected: no rebuild in progress")
	ErrRebuildAlreadyActive = errors.New("begin rebuild rejected: rebuild already in progress")
	ErrReplayWithoutRebuild = errors.New("replay rejected: no rebuild in progress")
)

// 非法事件错误，彼此及与控制流错误互不相同。
var (
	ErrEmptyKey  = errors.New("invalid event: key must not be empty")
	ErrZeroDelta = errors.New("invalid event: delta must not be zero")
)

// View 是支持双缓冲重建的物化计数视图。
type View struct {
	mu sync.RWMutex

	// 前台视图：始终对外提供服务。
	front map[string]int64
	// 全量事件日志，按到达顺序追加，朴素重放以它为唯一事实来源。
	log []Event

	// 重建中状态；rebuilding 为 false 时以下字段全部为空值。
	rebuilding bool
	// 快照点：重建开始时已存在的日志条数。
	snapshotSeq int64
	// 后台重建缓冲区，从空开始按日志顺序重放。
	back map[string]int64
	// 重建期间到达的新事件，切换前统一补齐。
	pending []Event
}

// New 创建一个空视图。
func New() *View {
	return &View{front: make(map[string]int64)}
}

// Apply 将一批事件原子地应用到前台视图并追加到日志。
// 重建期间同时把该批记入待补齐列表。
// 任一条事件非法则整批拒绝（前台、日志、待补齐列表均不变）。
func (v *View) Apply(events []Event) error {
	if err := validateBatch(events); err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, e := range events {
		v.front[e.Key] += e.Delta
		v.log = append(v.log, e)
		if v.rebuilding {
			v.pending = append(v.pending, e)
		}
	}
	return nil
}

// BeginRebuild 记录快照点并启动从空开始的重建缓冲。
func (v *View) BeginRebuild() (snapshotSeq int64, err error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.rebuilding {
		return 0, ErrRebuildAlreadyActive
	}
	v.rebuilding = true
	v.snapshotSeq = int64(len(v.log))
	v.back = make(map[string]int64)
	v.pending = nil
	return v.snapshotSeq, nil
}

// Replay 将历史事件按日志顺序逐条重放进重建缓冲区（朴素重放：逐条累加）。
// 整批校验，任一条非法则重建缓冲区保持不变。
func (v *View) Replay(events []Event) error {
	if err := validateBatch(events); err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.rebuilding {
		return ErrReplayWithoutRebuild
	}
	for _, e := range events {
		v.back[e.Key] += e.Delta
	}
	return nil
}

// SwitchRebuild 先把待补齐事件补进重建缓冲区，再原子切换前台指针并清空重建状态。
func (v *View) SwitchRebuild() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.rebuilding {
		return ErrSwitchWithoutRebuild
	}
	// 待补齐事件只可能来自前台双写，必然合法；补齐与切换在同一临界区内完成，
	// 因而“补齐 + 切换”对外是原子的：读端要么看到完整旧视图，要么看到完整新视图。
	for _, e := range v.pending {
		v.back[e.Key] += e.Delta
	}
	v.front = v.back
	v.back = nil
	v.pending = nil
	v.snapshotSeq = 0
	v.rebuilding = false
	return nil
}

// AbortRebuild 丢弃重建缓冲区与待补齐列表，前台不变。
func (v *View) AbortRebuild() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.rebuilding {
		return ErrAbortWithoutRebuild
	}
	v.back = nil
	v.pending = nil
	v.snapshotSeq = 0
	v.rebuilding = false
	return nil
}

// Snapshot 返回当前前台视图的一致完整拷贝。
func (v *View) Snapshot() map[string]int64 {
	v.mu.RLock()
	defer v.mu.RUnlock()
	out := make(map[string]int64, len(v.front))
	for key, count := range v.front {
		out[key] = count
	}
	return out
}

// LogEvents 返回全量日志的拷贝，供后台按快照点取出历史事件交给 Replay。
func (v *View) LogEvents() []Event {
	v.mu.RLock()
	defer v.mu.RUnlock()
	out := make([]Event, len(v.log))
	copy(out, v.log)
	return out
}

// SnapshotPoint 返回重建开始时记录的日志条数；非重建中为 0。
func (v *View) SnapshotPoint() int64 {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.snapshotSeq
}

// Rebuilding 返回是否处于重建中。
func (v *View) Rebuilding() bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.rebuilding
}

// validateBatch 校验整批事件；返回 BatchError 包装具体的非法事件原因。
func validateBatch(events []Event) error {
	for i, e := range events {
		if e.Key == "" {
			return &BatchError{Index: i, Event: e, Err: ErrEmptyKey}
		}
		if e.Delta == 0 {
			return &BatchError{Index: i, Event: e, Err: ErrZeroDelta}
		}
	}
	return nil
}

// BatchError 指明整批被拒时第一条非法事件的位置与原因。
// errors.Is(err, ErrEmptyKey) / errors.Is(err, ErrZeroDelta) 仍可判定。
type BatchError struct {
	Index int
	Event Event
	Err   error
}

func (e *BatchError) Error() string {
	return e.Err.Error()
}

func (e *BatchError) Unwrap() error {
	return e.Err
}
