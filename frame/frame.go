// Package frame 实现窗口函数的帧边界计算器。
//
// 分区中的行按 int64 排序键非降序追加，键相等的行构成一个并列组。
// Frame 支持 ROWS、RANGE、GROUPS 三种帧模式以及 NONE、CURRENT ROW、
// GROUP、TIES 四种排除项，返回升序、互不相邻的非空半开区间 [From, To) 序列。
package frame

import (
	"errors"
	"math/big"
	"strconv"
	"sync"
)

// Mode 是帧模式。
type Mode int

const (
	Rows   Mode = iota // 差值按行下标 j-i 度量
	Range              // 差值按键 key_j-key_i 度量
	Groups             // 差值按并列组编号 g_j-g_i 度量
)

// BoundKind 是帧界类型，常量值的大小次序就是强弱次序。
type BoundKind int

const (
	UnboundedPreceding BoundKind = iota
	Preceding
	CurrentRowBound
	Following
	UnboundedFollowing
)

// Exclusion 是排除项。
type Exclusion int

const (
	ExcludeNone Exclusion = iota
	ExcludeCurrentRow
	ExcludeGroup
	ExcludeTies
)

// Bound 描述一个帧界。只有 Preceding 与 Following 使用 N，
// 其余界的 N 被忽略（校验时也不检查其取值）。
type Bound struct {
	Kind BoundKind
	N    int64
}

// Spec 描述一次帧计算请求：（模式，起点界，终点界，排除项）。
type Spec struct {
	Mode Mode
	From Bound
	To   Bound
	Excl Exclusion
}

// Interval 是一个半开行下标区间 [From, To)，From < To。
type Interval struct {
	From int
	To   int
}

// 构造与追加阶段的可区分错误。
var (
	ErrInvalidCapacity = errors.New("frame: capacity must be positive")
	ErrCapacityFull    = errors.New("frame: partition is full")
	ErrKeyOutOfOrder   = errors.New("frame: key is smaller than the last appended key")
)

// Frame 阶段的四种可区分错误，按校验次序排列。
var (
	ErrInvalidSpec      = errors.New("frame: invalid mode, bound kind or exclusion")
	ErrInvalidBoundPair = errors.New("frame: invalid start/end bound combination")
	ErrNegativeOffset   = errors.New("frame: offset must not be negative")
	ErrRowOutOfRange    = errors.New("frame: row index out of range")
)

// Partition 是按排序键非降序追加得到的分区，可被并发使用。
type Partition struct {
	mu     sync.RWMutex
	cap    int
	keys   []int64
	groups []int // groupStart[g] 为第 g 个并列组的首行下标
}

// New 创建容量上限为 capacity 的空分区。
func New(capacity int) (*Partition, error) {
	if capacity <= 0 {
		return nil, ErrInvalidCapacity
	}
	return &Partition{
		cap:    capacity,
		keys:   make([]int64, 0, capacity),
		groups: make([]int, 0),
	}, nil
}

// Append 追加一行；被拒绝时不改变分区。
func (p *Partition) Append(key int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.keys) >= p.cap {
		return ErrCapacityFull
	}
	if len(p.keys) > 0 && key < p.keys[len(p.keys)-1] {
		return ErrKeyOutOfOrder
	}
	if len(p.keys) == 0 || key != p.keys[len(p.keys)-1] {
		p.groups = append(p.groups, len(p.keys))
	}
	p.keys = append(p.keys, key)
	return nil
}

// Len 返回分区当前行数。
func (p *Partition) Len() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.keys)
}

// Frame 返回第 i 行在 spec 下的帧区间序列；被拒绝时不改变分区。
// 返回的切片为独立分配，不与内部存储共享。
func (p *Partition) Frame(i int, spec Spec) ([]Interval, error) {
	if err := validateSpec(spec); err != nil {
		return nil, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if i < 0 || i >= len(p.keys) {
		return nil, ErrRowOutOfRange
	}

	inStart, inEnd := p.buildPredicates(i, spec)

	groupStart := p.groups
	var exclLo, exclHi int
	switch spec.Excl {
	case ExcludeNone:
		exclLo, exclHi = -1, -1
	case ExcludeCurrentRow:
		exclLo, exclHi = i, i+1
	case ExcludeGroup:
		g := p.groupOf(i)
		exclLo = groupStart[g]
		if g+1 < len(groupStart) {
			exclHi = groupStart[g+1]
		} else {
			exclHi = len(p.keys)
		}
	case ExcludeTies:
		g := p.groupOf(i)
		exclLo = groupStart[g]
		if g+1 < len(groupStart) {
			exclHi = groupStart[g+1]
		} else {
			exclHi = len(p.keys)
		}
	}

	result := make([]Interval, 0)
	n := len(p.keys)
	j := 0
	for j < n {
		for j < n && !(inStart(j) && inEnd(j) && !excluded(j, exclLo, exclHi, spec.Excl, i)) {
			j++
		}
		if j >= n {
			break
		}
		lo := j
		for j < n && inStart(j) && inEnd(j) && !excluded(j, exclLo, exclHi, spec.Excl, i) {
			j++
		}
		result = append(result, Interval{From: lo, To: j})
	}
	return result, nil
}

func excluded(j, exclLo, exclHi int, excl Exclusion, current int) bool {
	switch excl {
	case ExcludeNone:
		return false
	case ExcludeCurrentRow:
		return j == current
	case ExcludeGroup:
		return j >= exclLo && j < exclHi
	case ExcludeTies:
		return j >= exclLo && j < exclHi && j != current
	}
	return false
}

func (p *Partition) groupOf(i int) int {
	lo, hi := 0, len(p.groups)-1
	for lo < hi {
		mid := lo + (hi-lo+1)/2
		if p.groups[mid] <= i {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo
}

func validateSpec(spec Spec) error {
	if spec.Mode < Rows || spec.Mode > Groups ||
		spec.From.Kind < UnboundedPreceding || spec.From.Kind > UnboundedFollowing ||
		spec.To.Kind < UnboundedPreceding || spec.To.Kind > UnboundedFollowing ||
		spec.Excl < ExcludeNone || spec.Excl > ExcludeTies {
		return ErrInvalidSpec
	}
	if spec.From.Kind == UnboundedFollowing ||
		spec.To.Kind == UnboundedPreceding ||
		spec.From.Kind > spec.To.Kind {
		return ErrInvalidBoundPair
	}
	if (spec.From.Kind == Preceding || spec.From.Kind == Following) && spec.From.N < 0 {
		return ErrNegativeOffset
	}
	if (spec.To.Kind == Preceding || spec.To.Kind == Following) && spec.To.N < 0 {
		return ErrNegativeOffset
	}
	return nil
}

// buildPredicates 构造“第 j 行满足起点条件 / 终点条件”的判定函数。
// RANGE 模式的键差按 math/big 精确计算，绝不因 int64 溢出改变结论。
func (p *Partition) buildPredicates(i int, spec Spec) (start, end func(j int) bool) {
	switch spec.Mode {
	case Range:
		keyI := big.NewInt(p.keys[i])
		diff := new(big.Int)
		delta := func(j int) *big.Int {
			return diff.SetInt64(p.keys[j]).Sub(diff, keyI)
		}
		start = rangeStart(spec.From, delta)
		end = rangeEnd(spec.To, delta)
	case Rows:
		diffI := i
		start = func(j int) bool { return intStart(spec.From, j-diffI) }
		end = func(j int) bool { return intEnd(spec.To, j-diffI) }
	case Groups:
		gi := p.groupOf(i)
		start = func(j int) bool { return intStart(spec.From, p.groupOf(j)-gi) }
		end = func(j int) bool { return intEnd(spec.To, p.groupOf(j)-gi) }
	}
	return start, end
}

func rangeStart(b Bound, delta func(j int) *big.Int) func(j int) bool {
	switch b.Kind {
	case UnboundedPreceding:
		return func(int) bool { return true }
	case Preceding:
		t := big.NewInt(-b.N)
		return func(j int) bool { return delta(j).Cmp(t) >= 0 }
	case CurrentRowBound:
		t := big.NewInt(0)
		return func(j int) bool { return delta(j).Cmp(t) >= 0 }
	case Following:
		t := big.NewInt(b.N)
		return func(j int) bool { return delta(j).Cmp(t) >= 0 }
	case UnboundedFollowing:
		return func(int) bool { return true }
	}
	return nil
}

func rangeEnd(b Bound, delta func(j int) *big.Int) func(j int) bool {
	switch b.Kind {
	case UnboundedPreceding:
		return func(int) bool { return true }
	case Preceding:
		t := big.NewInt(-b.N)
		return func(j int) bool { return delta(j).Cmp(t) <= 0 }
	case CurrentRowBound:
		t := big.NewInt(0)
		return func(j int) bool { return delta(j).Cmp(t) <= 0 }
	case Following:
		t := big.NewInt(b.N)
		return func(j int) bool { return delta(j).Cmp(t) <= 0 }
	case UnboundedFollowing:
		return func(int) bool { return true }
	}
	return nil
}

func intStart(b Bound, d int) bool {
	switch b.Kind {
	case UnboundedPreceding:
		return true
	case Preceding:
		return int64(d) >= -b.N
	case CurrentRowBound:
		return d >= 0
	case Following:
		return int64(d) >= b.N
	case UnboundedFollowing:
		return true
	}
	return false
}

func intEnd(b Bound, d int) bool {
	switch b.Kind {
	case UnboundedPreceding:
		return true
	case Preceding:
		return int64(d) <= -b.N
	case CurrentRowBound:
		return d <= 0
	case Following:
		return int64(d) <= b.N
	case UnboundedFollowing:
		return true
	}
	return false
}

func (m Mode) String() string {
	switch m {
	case Rows:
		return "ROWS"
	case Range:
		return "RANGE"
	case Groups:
		return "GROUPS"
	}
	return "MODE?"
}

func (k BoundKind) String() string {
	switch k {
	case UnboundedPreceding:
		return "UNBOUNDED PRECEDING"
	case Preceding:
		return "PRECEDING"
	case CurrentRowBound:
		return "CURRENT ROW"
	case Following:
		return "FOLLOWING"
	case UnboundedFollowing:
		return "UNBOUNDED FOLLOWING"
	}
	return "KIND?"
}

func (b Bound) String() string {
	switch b.Kind {
	case Preceding:
		return strconv.FormatInt(b.N, 10) + " PRECEDING"
	case Following:
		return strconv.FormatInt(b.N, 10) + " FOLLOWING"
	}
	return b.Kind.String()
}

func (e Exclusion) String() string {
	switch e {
	case ExcludeNone:
		return "NONE"
	case ExcludeCurrentRow:
		return "CURRENT ROW"
	case ExcludeGroup:
		return "GROUP"
	case ExcludeTies:
		return "TIES"
	}
	return "EXCL?"
}

func (s Spec) String() string {
	return s.Mode.String() + " BETWEEN " + s.From.String() + " AND " + s.To.String() + " EXCLUDE " + s.Excl.String()
}
