package progress

import (
	"errors"
	"sort"
	"sync"
)

// Position 是单分区单调递增的位点。
type Position int64

// MaxPosition 表示没有任何未完结分区时的全局位点（视为无限大）。
const MaxPosition Position = 1<<63 - 1

// 拒绝操作的可区分原因。
var (
	ErrPartitionNotRegistered  = errors.New("progress: partition not registered")
	ErrPartitionFinished       = errors.New("progress: partition already finished")
	ErrPartitionExists         = errors.New("progress: partition already registered")
	ErrPositionRolledBack      = errors.New("progress: reported position rolled back")
	ErrTooManyActivePartitions = errors.New("progress: too many active partitions")
	ErrInvalidArgument         = errors.New("progress: invalid argument")
)

// PartitionState 是单个分区的状态快照。
type PartitionState struct {
	Start    Position
	Reported Position
	Finished bool
	FinalPos Position
}

// Snapshot 是推进器某一时刻的一致性快照。
type Snapshot struct {
	Partitions map[string]PartitionState
	Global     Position
	AllDone    bool
}

// Operation 是可重放的历史操作。
type Operation struct {
	Kind     string
	ID       string
	Position Position
}

// 操作类型。
const (
	OpRegister = "register"
	OpReport   = "report"
	OpFinish   = "finish"
)

type partition struct {
	start    Position
	reported Position
	finished bool
	final    Position
}

// Tracker 按分区独立确认位点并推进全局安全水位。
type Tracker struct {
	mu        sync.RWMutex
	maxActive int
	parts     map[string]*partition
	global    Position
	history   []Operation
}

// NewTracker 创建推进器，maxActive 为活跃分区数上限，<=0 表示不限制。
func NewTracker(maxActive int) *Tracker {
	return &Tracker{
		maxActive: maxActive,
		parts:     make(map[string]*partition),
		global:    MaxPosition,
	}
}

// Register 注册一个起始位点为 start 的分区。
func (t *Tracker) Register(id string, pos Position) error {
	if id == "" {
		return ErrInvalidArgument
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.parts[id]; ok {
		return ErrPartitionExists
	}
	if t.maxActive > 0 {
		active := 0
		for _, p := range t.parts {
			if !p.finished {
				active++
			}
		}
		if active >= t.maxActive {
			return ErrTooManyActivePartitions
		}
	}
	t.parts[id] = &partition{start: pos, reported: pos}
	t.recalcLocked()
	t.history = append(t.history, Operation{Kind: OpRegister, ID: id, Position: pos})
	return nil
}

// Report 报告某分区的确认位点（只进不退，相等为幂等）。
func (t *Tracker) Report(id string, pos Position) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	p, ok := t.parts[id]
	if !ok {
		return ErrPartitionNotRegistered
	}
	if p.finished {
		return ErrPartitionFinished
	}
	if pos < p.reported {
		return ErrPositionRolledBack
	}
	if pos == p.reported {
		return nil
	}
	p.reported = pos
	t.recalcLocked()
	t.history = append(t.history, Operation{Kind: OpReport, ID: id, Position: pos})
	return nil
}

// Finish 宣告分区完结，记录其最终位点。
func (t *Tracker) Finish(id string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	p, ok := t.parts[id]
	if !ok {
		return ErrPartitionNotRegistered
	}
	if p.finished {
		return ErrPartitionFinished
	}
	p.finished = true
	p.final = p.reported
	t.recalcLocked()
	t.history = append(t.history, Operation{Kind: OpFinish, ID: id, Position: p.final})
	return nil
}

// recalcLocked 以所有未完结分区确认位点的最小值重算全局位点；
// 调用方必须持有写锁。
func (t *Tracker) recalcLocked() {
	min := MaxPosition
	for _, p := range t.parts {
		if !p.finished && p.reported < min {
			min = p.reported
		}
	}
	t.global = min
}

// Global 返回全局安全水位；没有未完结分区时为 MaxPosition。
func (t *Tracker) Global() (Position, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.global, t.global == MaxPosition
}

// Final 返回已完结分区的最终位点。
func (t *Tracker) Final(id string) (Position, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	p, ok := t.parts[id]
	if !ok || !p.finished {
		return 0, false
	}
	return p.final, true
}

// State 返回单个分区的状态快照。
func (t *Tracker) State(id string) (PartitionState, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	p, ok := t.parts[id]
	if !ok {
		return PartitionState{}, false
	}
	return PartitionState{
		Start:    p.start,
		Reported: p.reported,
		Finished: p.finished,
		FinalPos: p.final,
	}, true
}

// Snapshot 返回所有分区与全局位点的一致性快照。
func (t *Tracker) Snapshot() Snapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()
	parts := make(map[string]PartitionState, len(t.parts))
	for id, p := range t.parts {
		parts[id] = PartitionState{
			Start:    p.start,
			Reported: p.reported,
			Finished: p.finished,
			FinalPos: p.final,
		}
	}
	return Snapshot{Partitions: parts, Global: t.global, AllDone: t.global == MaxPosition}
}

// History 返回已生效操作的副本，供重放核对。
func (t *Tracker) History() []Operation {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]Operation, len(t.history))
	copy(out, t.history)
	return out
}

// Replay 用操作历史重建推进器并核对快照是否逐字段一致。
func Replay(history []Operation, maxActive int) (*Tracker, Snapshot, error) {
	r := NewTracker(maxActive)
	for _, op := range history {
		var err error
		switch op.Kind {
		case OpRegister:
			err = r.Register(op.ID, op.Position)
		case OpReport:
			err = r.Report(op.ID, op.Position)
		case OpFinish:
			err = r.Finish(op.ID)
		default:
			err = ErrInvalidArgument
		}
		if err != nil {
			return nil, Snapshot{}, err
		}
	}
	return r, r.Snapshot(), nil
}

// EqualSnapshot 逐字段比较两份快照是否一致（map 内容相等即可，与遍历序无关）。
func EqualSnapshot(a, b Snapshot) bool {
	if a.Global != b.Global || a.AllDone != b.AllDone {
		return false
	}
	if len(a.Partitions) != len(b.Partitions) {
		return false
	}
	for id, pa := range a.Partitions {
		pb, ok := b.Partitions[id]
		if !ok || pa != pb {
			return false
		}
	}
	return true
}

// stableIDs 返回快照内按字典序排列的分区 ID，便于日志与复现。
func stableIDs(s Snapshot) []string {
	ids := make([]string, 0, len(s.Partitions))
	for id := range s.Partitions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
