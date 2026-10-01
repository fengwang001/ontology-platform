// Package constpool 实现字节码常量池的驻留与合并链接。
//
// 去重口径：
//   - 种类不同的常量永不相同（整数 1 与浮点 1.0 是两个常量）；
//   - 浮点按位比较：+0.0 与 -0.0 是两个常量；所有 NaN 视为同一个常量，
//     并规范为同一个位模式 canonicalNaNBits；
//   - 字符串按字节比较。
//
// 池容量 C 在构造时给定（C >= 1）。首次出现的常量取当前池大小为下标，
// 下标连续无空洞。池满时新常量被拒绝，已存在的常量仍可命中。
//
// 合并另一个池的快照（按其下标序的常量列表）：先校验快照内是否存在未知
// 种类，再统计其中在本池不存在的不同常量数，若加上当前大小超过 C 则整体
// 拒绝；否则按源下标序逐个驻留，返回由源下标到本池下标的重定位表。
// 被拒绝的操作不得改变池。
//
// 所有方法可并发调用，结果等价于某个串行顺序。
package constpool

import (
	"errors"
	"math"
	"sync"
)

// Kind 是常量种类。只有 KindInt、KindFloat、KindString 是已知种类，
// 其余值均为未知种类。
type Kind int

const (
	KindInt Kind = iota
	KindFloat
	KindString
)

// canonicalNaNBits 是所有 NaN 常量规范后的统一位模式。
const canonicalNaNBits uint64 = 0x7FF8000000000000

// 可区分的拒绝原因。
var (
	ErrInvalidCapacity      = errors.New("constpool: capacity must be >= 1")
	ErrUnknownKind          = errors.New("constpool: unknown constant kind")
	ErrPoolFull             = errors.New("constpool: pool is full and constant is new")
	ErrMergeExceedsCapacity = errors.New("constpool: merge exceeds pool capacity")
	ErrIndexOutOfRange      = errors.New("constpool: index out of range")
)

// Constant 是一个常量。根据 Kind 的不同，只有对应字段有意义。
type Constant struct {
	Kind  Kind
	Int   int64
	Float float64
	Str   string
}

// IntConst 构造整数常量。
func IntConst(v int64) Constant { return Constant{Kind: KindInt, Int: v} }

// FloatConst 构造浮点常量。
func FloatConst(v float64) Constant { return Constant{Kind: KindFloat, Float: v} }

// StringConst 构造字符串常量。
func StringConst(v string) Constant { return Constant{Kind: KindString, Str: v} }

// key 是去重用的规范化键。
type key struct {
	kind Kind
	i    int64
	bits uint64
	s    string
}

// Pool 是常量池。零值不可用，请用 NewPool 构造。
type Pool struct {
	mu      sync.Mutex
	cap     int
	consts  []Constant
	indexOf map[key]int
}

// NewPool 构造容量为 capacity 的常量池。capacity < 1 时返回
// ErrInvalidCapacity。
func NewPool(capacity int) (*Pool, error) {
	if capacity < 1 {
		return nil, ErrInvalidCapacity
	}
	return &Pool{
		cap:     capacity,
		indexOf: make(map[key]int),
	}, nil
}

// normalize 返回常量的规范化键；种类未知时 ok 为 false。
func normalize(c Constant) (key, bool) {
	switch c.Kind {
	case KindInt:
		return key{kind: KindInt, i: c.Int}, true
	case KindFloat:
		bits := math.Float64bits(c.Float)
		if math.IsNaN(c.Float) {
			bits = canonicalNaNBits
		}
		return key{kind: KindFloat, bits: bits}, true
	case KindString:
		return key{kind: KindString, s: c.Str}, true
	}
	return key{}, false
}

// normalizeConst 返回规范化后的常量（NaN 统一为 canonicalNaNBits）。
func normalizeConst(c Constant) Constant {
	if c.Kind == KindFloat && math.IsNaN(c.Float) {
		c.Float = math.Float64frombits(canonicalNaNBits)
	}
	return c
}

// Intern 驻留常量，返回其下标。首次出现的常量取当前池大小为下标；
// 已存在的常量命中并返回原下标；池满且常量为新时返回 ErrPoolFull；
// 种类未知时返回 ErrUnknownKind。被拒绝时不改变池。
func (p *Pool) Intern(c Constant) (int, error) {
	k, ok := normalize(c)
	if !ok {
		return 0, ErrUnknownKind
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if idx, hit := p.indexOf[k]; hit {
		return idx, nil
	}
	if len(p.consts) >= p.cap {
		return 0, ErrPoolFull
	}
	idx := len(p.consts)
	p.consts = append(p.consts, normalizeConst(c))
	p.indexOf[k] = idx
	return idx, nil
}

// Merge 合并另一个池的快照（按源下标序的常量列表），返回由源下标到
// 本池下标的重定位表。快照内含未知种类时报 ErrUnknownKind；合并后超过
// 容量时报 ErrMergeExceedsCapacity；按此序只报第一个。被拒绝时池不发生
// 任何变化，即使其中部分常量已存在于本池。
func (p *Pool) Merge(snapshot []Constant) ([]int, error) {
	keys := make([]key, len(snapshot))
	for i, c := range snapshot {
		k, ok := normalize(c)
		if !ok {
			return nil, ErrUnknownKind
		}
		keys[i] = k
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	// 统计快照内互不相同且在本池不存在的新常量数。
	seen := make(map[key]struct{})
	fresh := 0
	for _, k := range keys {
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		if _, hit := p.indexOf[k]; !hit {
			fresh++
		}
	}
	if len(p.consts)+fresh > p.cap {
		return nil, ErrMergeExceedsCapacity
	}
	reloc := make([]int, len(snapshot))
	for i, c := range snapshot {
		k := keys[i]
		if idx, hit := p.indexOf[k]; hit {
			reloc[i] = idx
			continue
		}
		idx := len(p.consts)
		p.consts = append(p.consts, normalizeConst(c))
		p.indexOf[k] = idx
		reloc[i] = idx
	}
	return reloc, nil
}

// Get 按下标返回规范化后的常量。下标为负或不小于池大小时返回
// ErrIndexOutOfRange。
func (p *Pool) Get(index int) (Constant, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if index < 0 || index >= len(p.consts) {
		return Constant{}, ErrIndexOutOfRange
	}
	return p.consts[index], nil
}

// Len 返回当前池大小。
func (p *Pool) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.consts)
}

// Capacity 返回池容量。
func (p *Pool) Capacity() int {
	return p.cap
}

// Snapshot 返回按下标序的规范化常量列表（即本池的快照）。
func (p *Pool) Snapshot() []Constant {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]Constant, len(p.consts))
	copy(out, p.consts)
	return out
}
