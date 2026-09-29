// Package ontology 实现多重集差（multiset difference）的增量物化视图。
//
// 对每个按 Key 标识的行，结果重数为 max(0, L(k) - R(k))，其中 L(k)、
// R(k) 分别为左右两侧该行的多重集重数。视图接受左右两侧的行变更流，
// 在重数变化时输出对应的结果增量，并维护一份可复放的变更日志。
package ontology

import (
	"errors"
	"fmt"
	"sync"
)

// Side 标识变更来自多重集差的哪一侧。
type Side int

const (
	// Left 为被减侧（正项）。
	Left Side = iota
	// Right 为减去侧（负项）；允许右侧先于左侧到达。
	Right
	// Result 仅用于输出增量，标识该变更作用于差视图本身。
	Result
)

// Change 描述对某一侧某一行（按 Key 标识）的多重集重数变更。
// Delta 为正表示插入若干份，为负表示删除若干份。
type Change struct {
	Side  Side
	Key   string
	Delta int64
}

// Entry 是变更日志中的一条记录，Seq 从 1 开始严格递增。
// 被拒绝的输入不会产生任何 Entry（失败不留痕）。
type Entry struct {
	Seq     int64
	Input   Change
	Output  Change
	Emitted bool
	Reason  string
}

// 输入必须被整体拒绝时返回的、互不相同的错误类别。
var (
	// ErrEmptyKey 表示空串行（Key 为空）。
	ErrEmptyKey = errors.New("ontology: empty change key")
	// ErrZeroDelta 表示零增量（Delta 为 0）。
	ErrZeroDelta = errors.New("ontology: zero delta")
	// ErrUnderflow 表示删除导致该侧该行重数变为负数。
	ErrUnderflow = errors.New("ontology: multiplicity underflow")
	// ErrTooManyRows 表示提交后该侧不同行数量超过配置上限。
	ErrTooManyRows = errors.New("ontology: too many distinct rows")
	// ErrMultiplicityOverflow 表示提交后单重数超过 int64 可表示范围。
	ErrMultiplicityOverflow = errors.New("ontology: multiplicity overflow")
)

// errBadSide 防御性错误：输入变更的 Side 不是 Left 或 Right。
var errBadSide = errors.New("ontology: change side must be Left or Right")

// DefaultMaxRows 是 New 传入非正数时使用的每侧不同行上限。
const DefaultMaxRows int64 = 1 << 20

// View 是多重集差 L ∖ R 的线程安全增量物化视图。
// 所有方法可被多个执行体并发调用，且读操作可与 Apply 并发。
type View struct {
	mu      sync.RWMutex
	left    map[string]int64
	right   map[string]int64
	result  map[string]int64
	log     []Entry
	seq     int64
	maxRows int64
}

// New 创建一个每侧不同行数量上限为 maxRows 的空视图；
// maxRows <= 0 时使用 DefaultMaxRows。
func New(maxRows int64) *View {
	if maxRows <= 0 {
		maxRows = DefaultMaxRows
	}
	return &View{
		left:    make(map[string]int64),
	right:   make(map[string]int64),
		result:  make(map[string]int64),
		maxRows: maxRows,
	}
}

// Apply 以多重集语义原子地应用一条输入变更。
//
// 任一校验失败时错误先于任何状态修改返回：两侧重数、视图与已输出日志
// 均保持不变（失败不留痕）。校验通过后：
//   - 该侧该行重数按 Delta 增减；
//   - 结果重数由 old 变为 max(0, L-R)；
//   - 结果重数变化时恰好输出一条 Result 侧增量，否则不输出；
//   - 无论是否输出都追加一条日志，写明判定依据。
func (v *View) Apply(ch Change) (out Change, emitted bool, err error) {
	if ch.Key == "" {
		return Change{}, false, ErrEmptyKey
	}
	if ch.Delta == 0 {
		return Change{}, false, ErrZeroDelta
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	var side map[string]int64
	switch ch.Side {
	case Left:
		side = v.left
	case Right:
		side = v.right
	default:
		return Change{}, false, errBadSide
	}

	current := side[ch.Key]
	next := current + ch.Delta
	// 正增量导致 int64 回绕。
	if ch.Delta > 0 && next < current {
		return Change{}, false, ErrMultiplicityOverflow
	}
	// 删除导致负多重集。
	if next < 0 {
		return Change{}, false, ErrUnderflow
	}
	// 仅插入一个此前不存在的行才会增加不同行数量。
	if current == 0 && ch.Delta > 0 && int64(len(side)) >= v.maxRows {
		return Change{}, false, ErrTooManyRows
	}

	oldResult := v.result[ch.Key]

	// 全部校验通过后才开始修改状态，保证原子性。
	if next == 0 {
		delete(side, ch.Key)
	} else {
		side[ch.Key] = next
	}

	var newResult int64
	switch ch.Side {
	case Left:
		newResult = next - v.right[ch.Key]
	case Right:
		newResult = v.left[ch.Key] - next
	}
	if newResult < 0 {
		newResult = 0
	}
	if newResult == 0 {
		delete(v.result, ch.Key)
	} else {
		v.result[ch.Key] = newResult
	}

	v.seq++
	entry := Entry{Seq: v.seq, Input: ch}
	if newResult != oldResult {
		out = Change{Side: Result, Key: ch.Key, Delta: newResult - oldResult}
		emitted = true
		entry.Output = out
		entry.Emitted = true
		entry.Reason = fmt.Sprintf(
			"accepted: result multiplicity %d -> %d, emit delta %d",
			oldResult, newResult, out.Delta)
	} else {
		entry.Reason = fmt.Sprintf(
			"accepted: result multiplicity unchanged at %d, no output", oldResult)
	}
	v.log = append(v.log, entry)

	return out, emitted, nil
}

// Count 返回某行当前的结果重数；被完全抵消或从不存在时为 0。
func (v *View) Count(key string) int64 {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.result[key]
}

// Snapshot 返回当前视图的一份拷贝，仅含结果重数 > 0 的行。
func (v *View) Snapshot() map[string]int64 {
	v.mu.RLock()
	defer v.mu.RUnlock()
	snapshot := make(map[string]int64, len(v.result))
	for key, multiplicity := range v.result {
		snapshot[key] = multiplicity
	}
	return snapshot
}

// Log 返回已提交变更日志的一份拷贝（不含任何被拒绝的输入）。
func (v *View) Log() []Entry {
	v.mu.RLock()
	defer v.mu.RUnlock()
	entries := make([]Entry, len(v.log))
	copy(entries, v.log)
	return entries
}

// SelfCheck 在与提交并发安全的前提下做两件事并与当前物化视图比对：
//  1. 依据日志中的输入流批量重算差视图 max(0, L-R)；
//  2. 仅按序应用日志中的输出增量得到投影视图；
//
// 三者必须逐项一致，两侧及视图重数非负，日志序号从 1 连续递增。
func (v *View) SelfCheck() error {
	v.mu.RLock()
	log := make([]Entry, len(v.log))
	copy(log, v.log)
	snapshot := make(map[string]int64, len(v.result))
	for key, multiplicity := range v.result {
		snapshot[key] = multiplicity
	}
	v.mu.RUnlock()

	left := make(map[string]int64)
	right := make(map[string]int64)
	projected := make(map[string]int64)

	var expectedSeq int64
	for _, entry := range log {
		expectedSeq++
		if entry.Seq != expectedSeq {
			return fmt.Errorf("ontology: log seq gap at #%d, got %d", expectedSeq, entry.Seq)
		}
		switch entry.Input.Side {
		case Left:
			left[entry.Input.Key] += entry.Input.Delta
		case Right:
			right[entry.Input.Key] += entry.Input.Delta
		default:
			return fmt.Errorf("ontology: log entry %d has invalid input side", entry.Seq)
		}
		if left[entry.Input.Key] < 0 {
			return fmt.Errorf("ontology: left multiplicity negative at %q", entry.Input.Key)
		}
		if right[entry.Input.Key] < 0 {
			return fmt.Errorf("ontology: right multiplicity negative at %q", entry.Input.Key)
		}
		if entry.Emitted {
			if entry.Output.Side != Result {
				return fmt.Errorf("ontology: log entry %d output has wrong side", entry.Seq)
			}
			projected[entry.Output.Key] += entry.Output.Delta
			if projected[entry.Output.Key] == 0 {
				delete(projected, entry.Output.Key)
			}
			if projected[entry.Output.Key] < 0 {
				return fmt.Errorf("ontology: projected multiplicity negative at %q", entry.Output.Key)
			}
		}
	}

	recomputed := make(map[string]int64)
	for key, l := range left {
		if r := right[key]; l > r {
			recomputed[key] = l - r
		}
	}

	if err := compareMaps("recompute", recomputed, snapshot); err != nil {
		return err
	}
	return compareMaps("log-replay", projected, snapshot)
}

func compareMaps(kind string, expected, actual map[string]int64) error {
	if len(expected) != len(actual) {
		return fmt.Errorf("ontology: view row count %d != %s %d",
			len(actual), kind, len(expected))
	}
	for key, multiplicity := range expected {
		if actual[key] != multiplicity {
			return fmt.Errorf("ontology: mismatch at %q: view=%d %s=%d",
				key, actual[key], kind, multiplicity)
		}
	}
	if len(actual) > len(expected) {
		return fmt.Errorf("ontology: view has rows absent from %s", kind)
	}
	return nil
}
