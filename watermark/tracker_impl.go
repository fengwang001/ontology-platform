package watermark

import (
	"fmt"
	"sort"
	"sync"
)

type partition struct {
	start     Offset
	confirmed Offset
	final     Offset
	finished  bool
}

type trackerState struct {
	partitions map[string]*partition
	// 单调不减的已发布全局位点；drained 后保持上一个有限值。
	global         Offset
	globalInfinite bool
	history        []Event
}

// Tracker 是多分区汇总流的全局位点推进器，并发安全。
type Tracker struct {
	mu        sync.RWMutex
	maxActive int
	state     trackerState
}

// New 创建推进器，maxActive 为同时存在的分区数上限（含已完结分区）。
func New(maxActive int) *Tracker {
	if maxActive <= 0 {
		panic(ErrInvalidMaxActive)
	}
	t := &Tracker{maxActive: maxActive}
	t.state.partitions = make(map[string]*partition)
	t.state.globalInfinite = true
	return t
}

// Register 注册分区并给定起始位点。
// 起始位点不得低于当前有限全局位点，否则会破坏全局位点单调不减。
func (t *Tracker) Register(name string, start Offset) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if _, ok := t.state.partitions[name]; ok {
		return fmt.Errorf("%w: %q", ErrPartitionExists, name)
	}
	if len(t.state.partitions) >= t.maxActive {
		return fmt.Errorf("%w: %q", ErrTooManyPartitions, name)
	}
	if !t.state.globalInfinite && start < t.state.global {
		return fmt.Errorf("%w: %q start=%d global=%d", ErrStartBeforeGlobal, name, start, t.state.global)
	}

	t.state.partitions[name] = &partition{start: start, confirmed: start}
	t.state.history = append(t.state.history, Event{Op: OpRegister, Partition: name, Offset: start})
	t.recomputeGlobalLocked()
	return nil
}

// Report 独立报告确认位点，只进不退；相同位点幂等。
func (t *Tracker) Report(name string, offset Offset) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	p, ok := t.state.partitions[name]
	if !ok {
		return fmt.Errorf("%w: %q", ErrPartitionNotFound, name)
	}
	if p.finished {
		return fmt.Errorf("%w: %q", ErrPartitionFinished, name)
	}
	if offset < p.confirmed {
		return fmt.Errorf("%w: %q report=%d confirmed=%d", ErrOffsetRegressed, name, offset, p.confirmed)
	}

	// 幂等：相同位点重复报告不产生新历史，也不改变任何状态。
	if offset == p.confirmed {
		return nil
	}

	p.confirmed = offset
	t.state.history = append(t.state.history, Event{Op: OpReport, Partition: name, Offset: offset})
	t.recomputeGlobalLocked()
	return nil
}

// Finish 宣告分区完结并记录最终位点；最终位点不得低于已确认位点。
// 对已完结分区以相同最终位点重复调用是幂等的。
func (t *Tracker) Finish(name string, final Offset) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	p, ok := t.state.partitions[name]
	if !ok {
		return fmt.Errorf("%w: %q", ErrPartitionNotFound, name)
	}
	if p.finished {
		if final == p.final {
			return nil
		}
		return fmt.Errorf("%w: %q", ErrPartitionFinished, name)
	}
	if final < p.confirmed {
		return fmt.Errorf("%w: %q final=%d confirmed=%d", ErrOffsetRegressed, name, final, p.confirmed)
	}

	p.confirmed = final
	p.final = final
	p.finished = true
	t.state.history = append(t.state.history, Event{Op: OpFinish, Partition: name, Offset: final})
	t.recomputeGlobalLocked()
	return nil
}

// Global 返回当前全局位点；无未完结分区时 infinite 为 true。
func (t *Tracker) Global() (offset Offset, infinite bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.state.global, t.state.globalInfinite
}

// FinalOffsets 返回所有已完结分区最终位点的副本。
func (t *Tracker) FinalOffsets() map[string]Offset {
	t.mu.RLock()
	defer t.mu.RUnlock()

	out := make(map[string]Offset)
	for name, p := range t.state.partitions {
		if p.finished {
			out[name] = p.final
		}
	}
	return out
}

// Snapshot 返回逐字段一致的完整快照；同一读锁内完成全部字段拷贝。
func (t *Tracker) Snapshot() Snapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.snapshotLocked()
}

func (t *Tracker) snapshotLocked() Snapshot {
	views := make([]PartitionView, 0, len(t.state.partitions))
	for name, p := range t.state.partitions {
		views = append(views, PartitionView{
			Partition: name,
			Start:     p.start,
			Confirmed: p.confirmed,
			Final:     p.final,
			Finished:  p.finished,
		})
	}
	sort.Slice(views, func(i, j int) bool { return views[i].Partition < views[j].Partition })
	return Snapshot{
		Partitions:     views,
		Global:         t.state.global,
		GlobalInfinite: t.state.globalInfinite,
	}
}

// History 返回已应用操作历史的副本。
func (t *Tracker) History() []Event {
	t.mu.RLock()
	defer t.mu.RUnlock()

	out := make([]Event, len(t.state.history))
	copy(out, t.state.history)
	return out
}

// SelfCheck 校验内部不变量：确认位点只进不退、完结分区移出最小值集合、
// 全局位点等于未完结分区确认位点最小值且单调不减。
func (t *Tracker) SelfCheck() (Snapshot, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var min Offset
	active := 0
	for name, p := range t.state.partitions {
		if p.confirmed < p.start {
			return Snapshot{}, fmt.Errorf("%w: %q confirmed %d below start %d", ErrInconsistentState, name, p.confirmed, p.start)
		}
		if p.finished {
			if p.final != p.confirmed {
				return Snapshot{}, fmt.Errorf("%w: %q final %d != confirmed %d", ErrInconsistentState, name, p.final, p.confirmed)
			}
			continue
		}
		if active == 0 || p.confirmed < min {
			min = p.confirmed
		}
		active++
	}

	if active == 0 {
		// 无未完结分区：全局视为无限大，且不回退到此前的有限值。
		if !t.state.globalInfinite {
			return Snapshot{}, fmt.Errorf("%w: no active partitions but global reported finite %d", ErrInconsistentState, t.state.global)
		}
	} else {
		if t.state.globalInfinite || t.state.global != min {
			return Snapshot{}, fmt.Errorf("%w: global=%d(infinite=%t) want=%d", ErrInconsistentState, t.state.global, t.state.globalInfinite, min)
		}
	}
	return t.snapshotLocked(), nil
}

// recomputeGlobalLocked 在每次成功写操作后重算全局位点。
// 规则：所有未完结分区确认位点的最小值；无未完结分区时为无限大。
// 额外保证：有限观测值单调不减（未完结分区只会推进，完结只移出元素，
// 而低于当前全局位点的新分区注册被 Register 拒绝）。
func (t *Tracker) recomputeGlobalLocked() {
	var min Offset
	active := 0
	for _, p := range t.state.partitions {
		if p.finished {
			continue
		}
		if active == 0 || p.confirmed < min {
			min = p.confirmed
		}
		active++
	}
	if active == 0 {
		t.state.globalInfinite = true
		t.state.global = 0
		return
	}
	t.state.globalInfinite = false
	t.state.global = min
}

// Replay 用历史事件按序重建等价推进器；任一事件非法则整体失败、不返回半成品。
func Replay(history []Event, maxActive int) (*Tracker, error) {
	if maxActive <= 0 {
		return nil, ErrInvalidMaxActive
	}
	t := New(maxActive)
	for i, e := range history {
		var err error
		switch e.Op {
		case OpRegister:
			err = t.Register(e.Partition, e.Offset)
		case OpReport:
			err = t.Report(e.Partition, e.Offset)
		case OpFinish:
			err = t.Finish(e.Partition, e.Offset)
		default:
			err = fmt.Errorf("%w: event %d unknown op %d", ErrInconsistentState, i, e.Op)
		}
		if err != nil {
			return nil, fmt.Errorf("replay event %d (%s %q @%d): %w", i, e.Op, e.Partition, e.Offset, err)
		}
	}
	return t, nil
}
