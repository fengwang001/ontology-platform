// Package register 实现首写胜出（first-write-wins）的按键去重寄存器。
//
// 每个键的生效值始终是迄今见过的逻辑序号最小的写入；生效序号随写入只减不增。
// Apply 以批为单位原子提交：批内任一条写入非法则整批拒绝、状态不变。
package register

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// 可区分的拒绝原因。
var (
	// ErrEmptyKey 表示写入的键为空字符串。
	ErrEmptyKey = errors.New("register: empty key")
	// ErrNonPositiveSeq 表示写入的逻辑序号不为正数。
	ErrNonPositiveSeq = errors.New("register: sequence must be positive")
	// ErrSeqDuplicate 表示同一键的写入序号与该键当前生效序号重复。
	ErrSeqDuplicate = errors.New("register: sequence duplicates the active sequence for key")
	// ErrEmptyBatch 表示提交的写入批次为空。
	ErrEmptyBatch = errors.New("register: empty batch")
)

// RejectError 描述一条导致整批被拒绝的写入及其原因。
type RejectError struct {
	// Index 是该写入在提交批次中的下标。
	Index int
	// Write 是被拒绝的写入内容。
	Write Write
	// Reason 是可区分的拒绝原因（ErrEmptyKey / ErrNonPositiveSeq / ErrSeqDuplicate）。
	Reason error
}

func (e *RejectError) Error() string {
	return fmt.Sprintf("register: write at index %d rejected for key %q seq %d: %v",
		e.Index, e.Write.Key, e.Write.Seq, e.Reason)
}
func (e *RejectError) Unwrap() error { return e.Reason }

// Write 是一条按键写入：Key 上的逻辑序号 Seq 对应值 Value。
type Write struct {
	Key   string
	Seq   int64
	Value string
}

// Op 是变更日志中的操作类型。
type Op string

// 变更日志操作。
const (
	// OpEstablish 表示键此前无生效值，建立新的生效值。
	OpEstablish Op = "establish"
	// OpRetract 表示撤回当时已物化的生效值。
	OpRetract Op = "retract"
)

// Change 是一条变更日志。撤回必须恰好匹配当时已物化的那条写入。
type Change struct {
	Op    Op
	Key   string
	Seq   int64
	Value string
}

// Decision 是单条写入相对于当前生效值的判定依据。
type Decision string

// 判定依据。
const (
	// DecisionEstablish 表示键尚无生效值，写入建立生效值。
	DecisionEstablish Decision = "establish"
	// DecisionRetractEstablish 表示序号更小，先撤回当前值再建立新值。
	DecisionRetractEstablish Decision = "retract-then-establish"
	// DecisionDiscard 表示序号更大（后写落败），写入被丢弃并计入丢弃数。
	DecisionDiscard Decision = "discard-late-write"
)

// EntryView 是某键当前生效值的一致性快照。
type EntryView struct {
	Key   string
	Seq   int64
	Value string
}

// Register 是首写胜出的去重寄存器，可被并发调用。
type Register struct {
	mu        sync.RWMutex
	active    map[string]EntryView
	discarded int64
}

// New 创建一个空寄存器。
func New() *Register { return &Register{active: make(map[string]EntryView)} }

// ApplyResult 是一次成功批次提交的结果。
type ApplyResult struct {
	// Changes 是按提交顺序产生的建立/撤回变更日志。
	Changes []Change
	// Decisions 是与批次一一对应的判定依据。
	Decisions []Decision
	// Discarded 是本批中被丢弃（后写落败）的条数。
	Discarded int
}

// Apply 原子提交一批写入；任一条非法则整批不生效、状态不变。
func (r *Register) Apply(writes []Write) (*ApplyResult, error) {
	if len(writes) == 0 {
		return nil, ErrEmptyBatch
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// 第一阶段：在工作副本上整批校验并模拟。任何一条非法都不会触碰正式状态。
	working := make(map[string]EntryView, len(r.active))
	for key, entry := range r.active {
		working[key] = entry
	}

	result := &ApplyResult{
		Changes:   make([]Change, 0, len(writes)*2),
		Decisions: make([]Decision, len(writes)),
	}

	for index, write := range writes {
		if write.Key == "" {
			return nil, &RejectError{Index: index, Write: write, Reason: ErrEmptyKey}
		}
		if write.Seq <= 0 {
			return nil, &RejectError{Index: index, Write: write, Reason: ErrNonPositiveSeq}
		}

		current, exists := working[write.Key]
		switch {
		case !exists:
			working[write.Key] = EntryView{Key: write.Key, Seq: write.Seq, Value: write.Value}
			result.Changes = append(result.Changes,
				Change{Op: OpEstablish, Key: write.Key, Seq: write.Seq, Value: write.Value})
			result.Decisions[index] = DecisionEstablish
		case write.Seq == current.Seq:
			// 同一键序号与当前生效序号重复，整批拒绝。
			return nil, &RejectError{Index: index, Write: write, Reason: ErrSeqDuplicate}
		case write.Seq < current.Seq:
			// 序号更小：先撤回恰好匹配的当前已物化值，再建立新值。
			result.Changes = append(result.Changes,
				Change{Op: OpRetract, Key: current.Key, Seq: current.Seq, Value: current.Value},
				Change{Op: OpEstablish, Key: write.Key, Seq: write.Seq, Value: write.Value})
			working[write.Key] = EntryView{Key: write.Key, Seq: write.Seq, Value: write.Value}
			result.Decisions[index] = DecisionRetractEstablish
		default:
			// 序号更大：后写落败，丢弃计数。
			result.Decisions[index] = DecisionDiscard
			result.Discarded++
		}
	}

	// 第二阶段：全部合法后一次性提交。
	r.active = working
	r.discarded += int64(result.Discarded)
	return result, nil
}

// Get 返回某键当前生效值；键不存在时 ok 为 false。
func (r *Register) Get(key string) (EntryView, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.active[key]
	return entry, ok
}

// Snapshot 返回所有键生效值的一致性视图。
func (r *Register) Snapshot() []EntryView {
	r.mu.RLock()
	defer r.mu.RUnlock()
	snapshot := make([]EntryView, 0, len(r.active))
	for _, entry := range r.active {
		snapshot = append(snapshot, entry)
	}
	sort.Slice(snapshot, func(i, j int) bool { return snapshot[i].Key < snapshot[j].Key })
	return snapshot
}

// Discarded 返回累计丢弃（后写落败）的写入条数。
func (r *Register) Discarded() int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.discarded
}

// Check 对内部状态做一致性自检，发现不变量被破坏时返回错误。
func (r *Register) Check() error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.discarded < 0 {
		return fmt.Errorf("register: discarded count is negative: %d", r.discarded)
	}
	for key, entry := range r.active {
		if key == "" {
			return errors.New("register: active state contains empty key")
		}
		if entry.Key != key {
			return fmt.Errorf("register: entry key %q does not match map key %q", entry.Key, key)
		}
		if entry.Seq <= 0 {
			return fmt.Errorf("register: active seq for key %q is non-positive: %d", key, entry.Seq)
		}
	}
	return nil
}
