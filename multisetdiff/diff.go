// Package multisetdiff 实现多重集差（multiset difference）的增量物化视图。
//
// 视图对每一个行键维护其在左侧与右侧的重数（multiset multiplicity），
// 结果重数为 max(left-right, 0)。两侧的行变更以串行（series）形式提交，
// 提交是原子的：串行中任一变更非法则整体拒绝，不留任何痕迹。
package multisetdiff

import (
	"errors"
	"fmt"
	"sync"
)

// DefaultMaxRows 是视图可同时追踪的不同行键数量上限（左右两侧合并计数）。
const MaxRows = 1 << 20

const (
	maxInt64 = 1<<63 - 1
	minInt64 = -1 << 63
)

// Side 标识多重集差的左/右侧。
type Side uint8

const (
	Left Side = iota + 1
	Right
)

// Delta 是一条针对单行的重数变更：Count > 0 表示插入 Count 份，
// Count < 0 表示删除 -Count 份。Count == 0 是非法的零增量。
type Delta struct {
	Row   string
	Count int64
}

// Change 标记一条变更作用于哪一侧。
type Change struct {
	Side  Side
	Delta Delta
}

// Event 是结果变更日志中的一条记录：Row 的结果重数净变化为 Count。
// Count > 0 表示结果行重数增加，Count < 0 表示减少。
type Event struct {
	Row   string
	Count int64
}

// 可区分的拒绝原因。调用方使用 errors.Is 判定错误类别。
var (
	// ErrEmptySeries 表示提交的变更串行是空的。
	ErrEmptySeries = errors.New("multisetdiff: empty change series")
	// ErrZeroDelta 表示某条变更的重数增量为 0。
	ErrZeroDelta = errors.New("multisetdiff: zero delta")
	// ErrUnknownSide 表示某条变更的 Side 字段既不是 Left 也不是 Right。
	ErrUnknownSide = errors.New("multisetdiff: unknown side")
	// ErrUnderflow 表示删除会使某一侧某行重数变为负数。
	ErrUnderflow = errors.New("multisetdiff: multiplicity underflow on delete")
	// ErrTooManyRows 表示变更会使不同行键总数超过 MaxRows。
	ErrTooManyRows = errors.New("multisetdiff: too many distinct rows")
	// ErrDeltaOverflow 表示变更会使重数发生整数溢出。
	ErrDeltaOverflow = errors.New("multisetdiff: multiplicity integer overflow")
)

// RejectError 携带被拒绝串行的逐条判定依据，便于日志输出。
type RejectError struct {
	Err   error
	Index int
}

func (e *RejectError) Error() string { return e.Err.Error() }
func (e *RejectError) Unwrap() error { return e.Err }

// Logger 是提交时每步输入/输出/判定依据的日志钩子。
type Logger interface {
	Logf(format string, args ...any)
}

type sideState struct {
	// mult 保存当前已知非零重数的行；重数归零的键会被删除。
	mult map[string]int64
}

// View 是多重集差的增量物化视图，可被多个执行体并发使用。
type View struct {
	mu    sync.RWMutex
	left  sideState
	right sideState
	// result 是严格按已提交的结果变更日志（Event）增量维护的物化视图，
	// 重数为 0 的键不保留。自检时与批量重算结果逐行比对。
	result  map[string]int64
	maxRows int
}

// New 创建空视图。
func New() *View { return NewWithLimit(MaxRows) }

// NewWithLimit 创建一个不同行键数量上限为 maxRows 的空视图，主要用于
// 在测试中以较小的上限触发 ErrTooManyRows。
func NewWithLimit(maxRows int) *View {
	return &View{
		left:    sideState{mult: map[string]int64{}},
		right:   sideState{mult: map[string]int64{}},
		result:  map[string]int64{},
		maxRows: maxRows,
	}
}

func (v *View) logf(log Logger, format string, args ...any) {
	if log != nil {
		log.Logf(format, args...)
	}
}

func sideName(side Side) string {
	switch side {
	case Left:
		return "L"
	case Right:
		return "R"
	}
	return "?"
}

func resultOf(l, r int64) int64 {
	if l > r {
		return l - r
	}
	return 0
}

// distinctRows 统计左右两侧合并后出现过的不同行键数量。
// 调用方持有 v.mu（读锁或写锁均可，因为不修改任何状态）。
func (v *View) distinctRows() int {
	seen := make(map[string]struct{}, len(v.left.mult)+len(v.right.mult))
	for row := range v.left.mult {
		seen[row] = struct{}{}
	}
	for row := range v.right.mult {
		seen[row] = struct{}{}
	}
	return len(seen)
}

// Commit 原子地应用一批行变更并返回结果变更日志。桩实现。
func (v *View) Commit(changes []Change, log Logger) ([]Event, error) {
	v.logf(log, "commit: begin, series length=%d", len(changes))

	// 空串行整体拒绝（独立于逐条校验的错误类别）。
	if len(changes) == 0 {
		v.logf(log, "commit: reject reason=%v (empty series leaves no trace)", ErrEmptySeries)
		return nil, ErrEmptySeries
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	// 在局部副本上逐条校验并派生事件；只有全部成功后副本才替换正式状态，
	// 因此任何拒绝都不会改变两侧重数、视图或已输出日志。
	left := make(map[string]int64, len(v.left.mult))
	right := make(map[string]int64, len(v.right.mult))
	for row, m := range v.left.mult {
		left[row] = m
	}
	for row, m := range v.right.mult {
		right[row] = m
	}
	result := make(map[string]int64, len(v.result))
	for row, m := range v.result {
		result[row] = m
	}

	events := make([]Event, 0, len(changes))
	for i, ch := range changes {
		v.logf(log, "step %d: input side=%s row=%q delta=%d", i, sideName(ch.Side), ch.Delta.Row, ch.Delta.Count)

		if ch.Side != Left && ch.Side != Right {
			err := &RejectError{Err: ErrUnknownSide, Index: i}
			v.logf(log, "step %d: reject reason=%v -> whole series rolled back, state unchanged", i, err)
			return nil, err
		}
		if ch.Delta.Count == 0 {
			err := &RejectError{Err: ErrZeroDelta, Index: i}
			v.logf(log, "step %d: reject reason=%v -> whole series rolled back, state unchanged", i, err)
			return nil, err
		}

		var mult map[string]int64
		if ch.Side == Left {
			mult = left
		} else {
			mult = right
		}
		row := ch.Delta.Row
		oldSide := mult[row]
		newSide, overflow := addChecked(oldSide, ch.Delta.Count)
		if overflow {
			err := &RejectError{Err: ErrDeltaOverflow, Index: i}
			v.logf(log, "step %d: reject reason=%v (side multiplicity %d + %d overflows int64) -> rolled back, state unchanged",
				i, err, oldSide, ch.Delta.Count)
			return nil, err
		}
		if newSide < 0 {
			err := &RejectError{Err: ErrUnderflow, Index: i}
			v.logf(log, "step %d: reject reason=%v (side multiplicity %d, delete %d) -> rolled back, state unchanged",
				i, err, oldSide, ch.Delta.Count)
			return nil, err
		}

		// 行键数量上限：只有此前两侧都不存在、本次又使其出现的行才会新增计数。
		_, existedLeft := left[row]
		_, existedRight := right[row]
		existed := existedLeft || existedRight

		oldResult := resultOf(left[row], right[row])
		if ch.Side == Left {
			applyMult(left, row, newSide)
		} else {
			applyMult(right, row, newSide)
		}
		newResult := resultOf(left[row], right[row])

		// 上限判定基于本次变更后的副本；若超限则在提交前回滚。
		if !existed && newSide != 0 && countDistinct(left, right) > v.maxRows {
			err := &RejectError{Err: ErrTooManyRows, Index: i}
			v.logf(log, "step %d: reject reason=%v (limit=%d) -> rolled back, state unchanged", i, err, v.maxRows)
			return nil, err
		}

		if diff := newResult - oldResult; diff != 0 {
			applyMult(result, row, result[row]+diff)
			events = append(events, Event{Row: row, Count: diff})
			v.logf(log, "step %d: output event row=%q delta=%d (result %d -> %d, reason: side %s multiplicity changed)",
				i, row, diff, oldResult, newResult, sideName(ch.Side))
		} else {
			v.logf(log, "step %d: no event (result stays %d, reason: change clipped at zero / does not cross other side)",
				i, oldResult)
		}
	}

	v.left.mult = left
	v.right.mult = right
	v.result = result
	v.logf(log, "commit: accepted, %d event(s) appended to change log", len(events))
	return events, nil
}

func applyMult(m map[string]int64, row string, value int64) {
	if value == 0 {
		delete(m, row)
		return
	}
	m[row] = value
}

func addChecked(a, b int64) (int64, bool) {
	if b > 0 && a > maxInt64-b {
		return 0, true
	}
	if b < 0 && a < minInt64-b {
		return 0, true
	}
	return a + b, false
}

func countDistinct(left, right map[string]int64) int {
	seen := make(map[string]struct{}, len(left)+len(right))
	for row := range left {
		seen[row] = struct{}{}
	}
	for row := range right {
		seen[row] = struct{}{}
	}
	return len(seen)
}

// Multiplicity 返回某行当前的结果重数（只读，可与提交并发）。
func (v *View) Multiplicity(row string) int64 {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.result[row]
}

// Snapshot 返回结果视图的副本（重数为 0 的行不出现）。
func (v *View) Snapshot() map[string]int64 {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.snapshotLocked()
}

// BatchRecompute 依据左右两侧当前重数批量重算结果。
func (v *View) BatchRecompute() map[string]int64 {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.batchLocked()
}

// batchLocked 依据两侧当前重数批量重算；调用方持有读锁或写锁。
func (v *View) batchLocked() map[string]int64 {
	out := map[string]int64{}
	for row, l := range v.left.mult {
		if r := resultOf(l, v.right.mult[row]); r > 0 {
			out[row] = r
		}
	}
	// 右侧独有行的结果恒为 0，无需写入。
	return out
}

// snapshotLocked 返回按变更日志增量维护的物化视图副本。
func (v *View) snapshotLocked() map[string]int64 {
	out := make(map[string]int64, len(v.result))
	for row, m := range v.result {
		out[row] = m
	}
	return out
}

// SelfCheck 校验按变更日志维护的增量视图与批量重算一致，
// 且两侧及结果的各行重数均非负、行数未超限。
func (v *View) SelfCheck() error {
	v.mu.RLock()
	defer v.mu.RUnlock()

	for row, m := range v.left.mult {
		if m < 0 {
			return fmt.Errorf("multisetdiff: self-check negative left multiplicity for %q: %d", row, m)
		}
	}
	for row, m := range v.right.mult {
		if m < 0 {
			return fmt.Errorf("multisetdiff: self-check negative right multiplicity for %q: %d", row, m)
		}
	}
	distinct := v.distinctRows()
	if distinct > v.maxRows {
		return fmt.Errorf("multisetdiff: self-check distinct row count %d exceeds limit %d", distinct, v.maxRows)
	}

	inc := v.snapshotLocked()
	batch := v.batchLocked()
	if len(inc) != len(batch) {
		return fmt.Errorf("multisetdiff: self-check mismatch: incremental has %d rows, batch has %d", len(inc), len(batch))
	}
	for row, m := range inc {
		if bm, ok := batch[row]; !ok || bm != m {
			return fmt.Errorf("multisetdiff: self-check mismatch for %q: incremental=%d batch=%d", row, m, bm)
		}
		if m <= 0 {
			return fmt.Errorf("multisetdiff: self-check non-positive result multiplicity for %q: %d", row, m)
		}
	}
	return nil
}
