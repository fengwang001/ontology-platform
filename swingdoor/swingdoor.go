// Package swingdoor 实现流式旋转门趋势压缩器。
package swingdoor

import (
	"fmt"
	"io"
	"math/big"
	"sync"
)

const (
	maxCoord     int64 = 1_000_000_000
	maxTolerance int64 = 4_000_000_000
)

// Sample 是一个时序采样点。
type Sample struct {
	T int64
	V int64
}

// bound 表示一条斜率上下界。den 恒为正；negInf/posInf 表示负/正无穷。
type bound struct {
	num    int64
	den    int64
	negInf bool
	posInf bool
}

// Compressor 逐点接收时序采样，仅保留必要的存档点。
type Compressor struct {
	mu       sync.RWMutex
	e        int64
	log      io.Writer
	closed   bool
	started  bool
	origin   Sample
	pend     []Sample
	lo       bound
	hi       bound
	lastT    int64
	archives []Sample
}

// New 以容差 E 创建压缩器。
func New(e int64) (*Compressor, error) {
	return NewWithLogger(e, nil)
}

// NewWithLogger 与 New 相同，但会将输入、输出与判定依据写入 log。
func NewWithLogger(e int64, log io.Writer) (*Compressor, error) {
	if e < 0 || e > maxTolerance {
		return nil, ErrInvalidTolerance
	}
	return &Compressor{
		e:   e,
		log: log,
		lo:  bound{negInf: true},
		hi:  bound{posInf: true},
	}, nil
}

// Write 接收一个采样。
func (c *Compressor) Write(t, v int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 拒绝顺序：已关闭 → 数值超限 → 时间次序。
	if c.closed {
		c.logf("reject (%d,%d): closed", t, v)
		return ErrClosed
	}
	if abs64(v) > maxCoord {
		c.logf("reject (%d,%d): value out of range", t, v)
		return ErrValueOutOfRange
	}
	if abs64(t) > maxCoord {
		c.logf("reject (%d,%d): timestamp out of range", t, v)
		return ErrTimeOutOfRange
	}
	if c.started {
		if t == c.lastT {
			c.logf("reject (%d,%d): duplicate timestamp", t, v)
			return ErrDuplicateTime
		}
		if t < c.lastT {
			c.logf("reject (%d,%d): timestamp before previous", t, v)
			return ErrTimeBeforePrev
		}
	}

	s := Sample{T: t, V: v}
	if !c.started {
		c.started = true
		c.origin = s
		c.archives = []Sample{s}
		c.lastT = t
		c.logf("first (%d,%d): archived as origin", t, v)
		return nil
	}

	sNum := v - c.origin.V
	dt := t - c.origin.T // 严格为正

	if len(c.pend) == 0 || (cmpFrac(sNum, dt, c.lo) >= 0 && cmpFrac(sNum, dt, c.hi) <= 0) {
		c.pend = append(c.pend, s)
		loNum := v - c.e - c.origin.V
		hiNum := v + c.e - c.origin.V
		if len(c.pend) == 1 {
			c.lo = bound{num: loNum, den: dt}
			c.hi = bound{num: hiNum, den: dt}
		} else {
			c.lo = maxBound(c.lo, bound{num: loNum, den: dt})
			c.hi = minBound(c.hi, bound{num: hiNum, den: dt})
		}
		c.lastT = t
		c.logf("accept (%d,%d) pending: slope %d/%d within [%s,%s]; pending=%d",
			t, v, sNum, dt, c.lo.text(), c.hi.text(), len(c.pend))
		return nil
	}

	oldLo, oldHi := c.lo.text(), c.hi.text()

	// 超界：上一个待定点成为存档点与新起点，待定点全部丢弃。
	anchor := c.pend[len(c.pend)-1]
	c.archives = append(c.archives, anchor)
	c.origin = anchor
	c.pend = c.pend[:0]

	// 新采样相对新起点成为第一个待定点，上下界只由它决定。
	newDt := t - anchor.T
	c.pend = append(c.pend, s)
	c.lo = bound{num: v - c.e - anchor.V, den: newDt}
	c.hi = bound{num: v + c.e - anchor.V, den: newDt}
	c.lastT = t
	c.logf("slope %d/%d exceeds [%s,%s]: archive previous (%d,%d) as new origin; new pending (%d,%d) bounds [%s,%s]",
		sNum, dt, oldLo, oldHi, anchor.T, anchor.V, t, v, c.lo.text(), c.hi.text())
	return nil
}

// Close 关闭压缩器，追加末点为存档点。
func (c *Compressor) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		c.logf("reject close: already closed")
		return ErrClosed
	}
	if !c.started {
		c.logf("reject close: empty stream")
		return ErrEmptyClose
	}
	c.closed = true
	if len(c.pend) > 0 {
		last := c.pend[len(c.pend)-1]
		c.archives = append(c.archives, last)
		c.logf("close: append last sample (%d,%d) as archive", last.T, last.V)
	} else {
		c.logf("close: last sample already archived")
	}
	return nil
}

// Archives 返回存档点序列的快照。
func (c *Compressor) Archives() []Sample {
	c.mu.RLock()
	defer c.mu.RUnlock()

	out := make([]Sample, len(c.archives))
	copy(out, c.archives)
	return out
}

// Closed 返回压缩器是否已关闭。
func (c *Compressor) Closed() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.closed
}

func (c *Compressor) logf(format string, args ...any) {
	if c.log != nil {
		fmt.Fprintf(c.log, "swingdoor: "+format+"\n", args...)
	}
}

func abs64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

// cmpFrac 比较 n/d 与 b（d>0），结果为 -1/0/1。全程整数交叉相乘。
func cmpFrac(n, d int64, b bound) int {
	switch {
	case b.negInf:
		return 1
	case b.posInf:
		return -1
	}
	lhs := new(big.Int).Mul(big.NewInt(n), big.NewInt(b.den))
	rhs := new(big.Int).Mul(big.NewInt(d), big.NewInt(b.num))
	return lhs.Cmp(rhs)
}

// maxBound 返回 a 与 b 中较大的斜率界。
func maxBound(a, b bound) bound {
	switch {
	case a.negInf:
		return b
	case b.negInf:
		return a
	case a.posInf:
		return a
	case b.posInf:
		return b
	}
	if cmpFrac(a.num, a.den, b) >= 0 {
		return a
	}
	return b
}

// minBound 返回 a 与 b 中较小的斜率界。
func minBound(a, b bound) bound {
	switch {
	case a.posInf:
		return b
	case b.posInf:
		return a
	case a.negInf:
		return a
	case b.negInf:
		return b
	}
	if cmpFrac(a.num, a.den, b) <= 0 {
		return a
	}
	return b
}

func (b bound) text() string {
	switch {
	case b.negInf:
		return "-inf"
	case b.posInf:
		return "+inf"
	default:
		return fmt.Sprintf("%d/%d", b.num, b.den)
	}
}
