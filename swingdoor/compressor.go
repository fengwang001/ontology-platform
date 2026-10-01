// Package swingdoor 实现流式旋转门趋势压缩器。
//
// 压缩器逐点接收时序采样，只保留必要的存档点，使得任一被丢弃点到其
// 前后相邻存档点连线的纵向偏差都不超过容差 E。所有斜率比较均用整数
// 交叉相乘完成（乘积可能超出 int64 时退化为 big.Int），不使用浮点，
// 因此相同输入序列重放得到逐位相同的存档点序列。
package swingdoor

import (
	"errors"
	"math/big"
	"sync"
)

// 合法输入范围。
const (
	// MaxTolerance 是容差 E 的上限（含）。
	MaxTolerance = int64(4_000_000_000)
	// MaxAbsValue 是时间戳与数值绝对值的上限（含）。
	MaxAbsValue = int64(1_000_000_000)
)

// 可区分的拒绝原因。检查顺序为：已关闭 -> 数值超限 -> 时间次序。
var (
	ErrInvalidTolerance  = errors.New("swingdoor: 容差为负或超过上限")
	ErrAlreadyClosed     = errors.New("swingdoor: 压缩器已关闭")
	ErrValueOutOfRange   = errors.New("swingdoor: 时间戳或数值绝对值超限")
	ErrTimestampEqual    = errors.New("swingdoor: 时间戳与上一采样相同")
	ErrTimestampBackward = errors.New("swingdoor: 时间戳早于上一采样")
	ErrEmptyStream       = errors.New("swingdoor: 空流关闭")
)

// Point 是一个时序采样，T 为时间戳，V 为数值。
type Point struct {
	T int64
	V int64
}

// bound 表示斜率下界或上界，可为正负无穷；有限时为分数 num/den（den > 0）。
type bound struct {
	inf int // -1 负无穷，0 有限，+1 正无穷
	num int64
	den int64
}

func negInf() bound               { return bound{inf: -1} }
func posInf() bound               { return bound{inf: +1} }
func finite(num, den int64) bound { return bound{num: num, den: den} }

func abs64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

// safeFactor 保证两个绝对值不超过它的 int64 相乘不会溢出：
// 3e9 * 3e9 = 9e18 < math.MaxInt64。
const safeFactor = int64(3_000_000_000)

// cmpFrac 比较 aNum/aDen 与 bNum/bDen（分母恒正），返回 -1、0 或 +1。
// 乘积不超过 int64 时直接比较，否则使用 big.Int 交叉相乘。
func cmpFrac(aNum, aDen, bNum, bDen int64) int {
	if abs64(aNum) <= safeFactor && abs64(bNum) <= safeFactor &&
		aDen <= safeFactor && bDen <= safeFactor {
		lhs := aNum * bDen
		rhs := bNum * aDen
		switch {
		case lhs < rhs:
			return -1
		case lhs > rhs:
			return 1
		default:
			return 0
		}
	}
	lhs := new(big.Int).Mul(big.NewInt(aNum), big.NewInt(bDen))
	rhs := new(big.Int).Mul(big.NewInt(bNum), big.NewInt(aDen))
	return lhs.Cmp(rhs)
}

// leq 判断斜率界 b 是否不大于分数 num/den。
func (b bound) leq(num, den int64) bool {
	switch b.inf {
	case -1:
		return true
	case +1:
		return false
	}
	return cmpFrac(b.num, b.den, num, den) <= 0
}

// geq 判断斜率界 b 是否不小于分数 num/den。
func (b bound) geq(num, den int64) bool {
	switch b.inf {
	case +1:
		return true
	case -1:
		return false
	}
	return cmpFrac(b.num, b.den, num, den) >= 0
}

// maxBound 返回两个斜率界中较大者。
func maxBound(a, b bound) bound {
	if a.inf == +1 || b.inf == -1 {
		return a
	}
	if a.inf == -1 || b.inf == +1 {
		return b
	}
	if cmpFrac(a.num, a.den, b.num, b.den) >= 0 {
		return a
	}
	return b
}

// minBound 返回两个斜率界中较小者。
func minBound(a, b bound) bound {
	if a.inf == -1 || b.inf == +1 {
		return a
	}
	if a.inf == +1 || b.inf == -1 {
		return b
	}
	if cmpFrac(a.num, a.den, b.num, b.den) <= 0 {
		return a
	}
	return b
}

// Compressor 是流式旋转门趋势压缩器，可并发使用。
type Compressor struct {
	mu      sync.Mutex
	e       int64
	closed  bool
	archive []Point

	start    Point // 当前起点 (t0, v0)
	hasStart bool
	pending  Point // 最近一个待定点
	hasPend  bool
	last     Point // 上一个采样，用于时间次序检查
	hasLast  bool
	lo, hi   bound
}

// New 创建容差为 e 的压缩器。e 为负或超过 MaxTolerance 时返回
// ErrInvalidTolerance。
func New(e int64) (*Compressor, error) {
	if e < 0 || e > MaxTolerance {
		return nil, ErrInvalidTolerance
	}
	c := &Compressor{e: e}
	c.lo = negInf()
	c.hi = posInf()
	return c, nil
}

// Write 接收一个采样。被拒绝时不改变任何内部状态。
// 拒绝检查顺序：已关闭 -> 数值超限 -> 时间次序。
func (c *Compressor) Write(t, v int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return ErrAlreadyClosed
	}
	if abs64(t) > MaxAbsValue || abs64(v) > MaxAbsValue {
		return ErrValueOutOfRange
	}
	if c.hasLast {
		if t == c.last.T {
			return ErrTimestampEqual
		}
		if t < c.last.T {
			return ErrTimestampBackward
		}
	}

	c.process(Point{T: t, V: v})
	c.last = Point{T: t, V: v}
	c.hasLast = true
	return nil
}

// process 按旋转门算法处理一个采样，调用方须持有锁且已完成校验。
func (c *Compressor) process(p Point) {
	if !c.hasStart {
		// 第一个采样即存档点并成为当前起点。
		c.archive = append(c.archive, p)
		c.start = p
		c.hasStart = true
		c.lo = negInf()
		c.hi = posInf()
		return
	}

	dt := p.T - c.start.T
	sNum := p.V - c.start.V
	if !c.hasPend || (c.lo.leq(sNum, dt) && c.hi.geq(sNum, dt)) {
		// 斜率在 [lo, hi] 内（恰等于界也算可继续），成为待定点并收紧上下界。
		c.pending = p
		c.hasPend = true
		c.lo = maxBound(c.lo, finite(p.V-c.e-c.start.V, dt))
		c.hi = minBound(c.hi, finite(p.V+c.e-c.start.V, dt))
		return
	}

	// 上一个采样（最近一个待定点）成为存档点并成为新起点，
	// 新采样相对新起点重新计算，成为新起点之后的第一个待定点。
	c.archive = append(c.archive, c.pending)
	c.start = c.pending
	dt = p.T - c.start.T
	c.pending = p
	c.lo = finite(p.V-c.e-c.start.V, dt)
	c.hi = finite(p.V+c.e-c.start.V, dt)
}

// Close 关闭压缩器：若最后一个采样尚非存档点则追加为存档点，
// 并返回完整存档点序列。空流关闭返回 ErrEmptyStream，
// 重复关闭返回 ErrAlreadyClosed。
func (c *Compressor) Close() ([]Point, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil, ErrAlreadyClosed
	}
	if !c.hasStart {
		return nil, ErrEmptyStream
	}
	c.closed = true
	if c.hasPend {
		c.archive = append(c.archive, c.pending)
		c.hasPend = false
	}
	out := make([]Point, len(c.archive))
	copy(out, c.archive)
	return out, nil
}

// Points 返回当前已确定的存档点快照（不含待定点），可并发调用。
func (c *Compressor) Points() []Point {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Point, len(c.archive))
	copy(out, c.archive)
	return out
}
