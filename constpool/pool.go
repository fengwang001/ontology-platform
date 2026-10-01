package constpool

import (
	"math"
	"sync"
)

// Kind 标识常量种类。不同种类的常量永不相等。
type Kind uint8

const (
	KindInt Kind = iota + 1
	KindFloat
	KindString
)

func (k Kind) valid() bool {
	return k == KindInt || k == KindFloat || k == KindString
}

// Const 是常量池中的一个条目。Kind 决定其余字段应如何解释：
// KindInt 用 Int，KindFloat 用 Float，KindString 用 Str。
type Const struct {
	Kind  Kind
	Int   int64
	Float float64
	Str   string
}

// 所有 NaN 统一规范化为同一个静默 NaN 位模式。
const canonicalNaNBits = uint64(0x7FF8000000000001)

// key 是去重用的内部键：不同种类的键永不相等；
// 整数按值、浮点按位（+0 与 -0 不同、所有 NaN 统一）、字符串按字节比较。
type key struct {
	kind Kind
	intV int64
	bits uint64
	strV string
}

// canonicalize 校验种类并把 Const 转换为规范化键与规范化值。
func canonicalize(c Const) (key, Const, error) {
	if !c.Kind.valid() {
		return key{}, Const{}, ErrUnknownKind
	}
	canon := Const{Kind: c.Kind}
	k := key{kind: c.Kind}
	switch c.Kind {
	case KindInt:
		k.intV = c.Int
		canon.Int = c.Int
	case KindFloat:
		bits := math.Float64bits(c.Float)
		if math.IsNaN(c.Float) {
			bits = canonicalNaNBits
		}
		k.bits = bits
		canon.Float = math.Float64frombits(bits)
	case KindString:
		k.strV = c.Str
		canon.Str = c.Str
	}
	return k, canon, nil
}

// Pool 是线程安全的字节码常量池。零值不可用，必须用 New 构造。
type Pool struct {
	mu     sync.Mutex
	cap    int
	elems  []Const
	lookup map[key]int
}

// New 构造容量为 capacity 的常量池；capacity 小于 1 时返回 ErrInvalidCapacity。
func New(capacity int) (*Pool, error) {
	if capacity < 1 {
		return nil, ErrInvalidCapacity
	}
	return &Pool{
		cap:    capacity,
		elems:  make([]Const, 0, capacity),
		lookup: make(map[key]int),
	}, nil
}

// Intern 驻留一个常量，返回其下标；常量已存在时返回原下标。
// 种类未知返回 ErrUnknownKind；池满且常量为新时返回 ErrPoolFull。
func (p *Pool) Intern(c Const) (int, error) {
	k, canon, err := canonicalize(c)
	if err != nil {
		return 0, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if idx, ok := p.lookup[k]; ok {
		return idx, nil
	}
	if len(p.elems) >= p.cap {
		return 0, ErrPoolFull
	}
	idx := len(p.elems)
	p.elems = append(p.elems, canon)
	p.lookup[k] = idx
	return idx, nil
}

// Merge 将快照（按源下标序的常量列表）合并进本池，
// 返回源下标到本池下标的重定位表。
//
// 先统计快照中本池尚不存在的不同常量数；连同当前大小超过容量则整体拒绝，
// 即使其中部分常量已存在于本池也不会有任何新增。
// 快照含未知种类时按源下标序优先报 ErrUnknownKind。
func (p *Pool) Merge(snapshot []Const) ([]int, error) {
	// 阶段一（锁外）：规范化与校验，失败时绝不触碰池状态。
	keys := make([]key, len(snapshot))
	canons := make([]Const, len(snapshot))
	missing := make(map[key]struct{})
	for i, c := range snapshot {
		k, canon, err := canonicalize(c)
		if err != nil {
			return nil, err
		}
		keys[i] = k
		canons[i] = canon
		if _, ok := missing[k]; !ok {
			missing[k] = struct{}{}
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	for k := range missing {
		if _, ok := p.lookup[k]; ok {
			delete(missing, k)
		}
	}
	if len(p.elems)+len(missing) > p.cap {
		return nil, ErrPoolFull
	}

	// 阶段二：通过校验，按源下标序逐个驻留并构建重定位表。
	reloc := make([]int, len(snapshot))
	for i := range snapshot {
		k := keys[i]
		if idx, ok := p.lookup[k]; ok {
			reloc[i] = idx
			continue
		}
		idx := len(p.elems)
		p.elems = append(p.elems, canons[i])
		p.lookup[k] = idx
		reloc[i] = idx
	}
	return reloc, nil
}

// Get 按下标返回规范化后的常量；下标为负或不小于池大小时返回 ErrIndexOutOfRange。
func (p *Pool) Get(index int) (Const, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if index < 0 || index >= len(p.elems) {
		return Const{}, ErrIndexOutOfRange
	}
	return p.elems[index], nil
}

// Snapshot 返回按本池下标序排列的常量快照。
func (p *Pool) Snapshot() []Const {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]Const, len(p.elems))
	copy(out, p.elems)
	return out
}

// Len 返回当前常量数量。
func (p *Pool) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.elems)
}
