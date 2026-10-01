// Package counter 提供带重置检测的累计计数器增量统计器。
//
// 每个序列独立接收按时间单调递增的累计读数 (t, v)。
// 相邻读数 (p, q)：q.v >= p.v 时增量为 q.v-p.v；q.v < p.v 判为一次重置，
// 增量为 q.v（视为重置后从 0 起算，重置前未上报部分不补）。
// 窗口 (a, b] 的增量为所有「q 的时间戳落在 (a, b] 内」的相邻对增量之和，
// p 可以在窗口之外；序列首个读数无前驱，不产生增量。重置次数同口径统计。
package counter

import (
	"errors"
	"math"
	"sort"
	"sync"
)

var (
	// ErrNegativeValue 写入数值为负。
	ErrNegativeValue = errors.New("counter: negative value")
	// ErrTimestampOrder 写入时间戳小于序列最新读数时间戳。
	ErrTimestampOrder = errors.New("counter: timestamp before latest reading")
	// ErrConflictingDuplicate 时间戳等于最新读数但数值不同。
	ErrConflictingDuplicate = errors.New("counter: conflicting duplicate reading")
	// ErrInvalidWindow 查询窗口非法（a >= b）。
	ErrInvalidWindow = errors.New("counter: invalid window (a must be < b)")
	// ErrUnknownSeries 查询未出现过的序列。
	ErrUnknownSeries = errors.New("counter: unknown series")
	// ErrOverflow 窗口增量求和溢出 int64。
	ErrOverflow = errors.New("counter: delta sum overflows int64")
)

// point 是一条读数及其相对前驱的增量信息。
type point struct {
	t     int64
	v     int64
	delta int64 // 相对前驱的增量；首个读数为 0
	reset bool  // 相对前驱是否发生重置
}

// series 保存单个序列的读数与前缀和。
//
// 由于写入时间戳必须严格递增（或幂等重复），读数为纯追加，
// 前缀和可增量维护。增量非负，前缀和单调不减。
// 前缀和以 (carry, sum) 二元组表示真实值 carry*2^64 + sum，
// 从而能精确判断任意区间和是否溢出 int64。
type series struct {
	pts       []point
	prefSum   []uint64 // prefSum[k] = pts[0..k-1] 增量之和 mod 2^64
	prefCarry []uint64 // prefCarry[k] = 上述真实和 / 2^64
	prefReset []int64  // prefReset[k] = pts[0..k-1] 重置次数
}

// Tracker 是并发安全的累计计数器增量统计器。
// 所有方法可并发调用，效果等价于某个串行顺序。
type Tracker struct {
	mu     sync.RWMutex
	series map[string]*series
}

// NewTracker 返回一个空的统计器。
func NewTracker() *Tracker {
	return &Tracker{series: make(map[string]*series)}
}

// Add 向序列 seq 追加读数 (t, v)。
//
// 时间戳等于最新读数且数值相同视为幂等重复：成功且不改变状态。
// 被拒绝的写入不改变任何序列。数值为负先于时间次序检查。
func (t *Tracker) Add(seq string, ts int64, v int64) error {
	if v < 0 {
		return ErrNegativeValue
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	s, ok := t.series[seq]
	if !ok {
		s = &series{
			prefSum:   []uint64{0},
			prefCarry: []uint64{0},
			prefReset: []int64{0},
		}
		t.series[seq] = s
	}
	if n := len(s.pts); n > 0 {
		last := s.pts[n-1]
		switch {
		case ts < last.t:
			return ErrTimestampOrder
		case ts == last.t:
			if v == last.v {
				return nil // 幂等重复：成功且不改变状态
			}
			return ErrConflictingDuplicate
		}
	}

	p := point{t: ts, v: v}
	var resetAdd int64
	if n := len(s.pts); n > 0 {
		last := s.pts[n-1]
		if v >= last.v {
			p.delta = v - last.v
		} else {
			p.delta = v // 重置：视为从 0 起算
			p.reset = true
			resetAdd = 1
		}
	}
	k := len(s.prefSum) - 1
	sum := s.prefSum[k] + uint64(p.delta)
	carry := s.prefCarry[k]
	if sum < s.prefSum[k] { // 2^64 回绕，进位
		carry++
	}
	s.pts = append(s.pts, p)
	s.prefSum = append(s.prefSum, sum)
	s.prefCarry = append(s.prefCarry, carry)
	s.prefReset = append(s.prefReset, s.prefReset[k]+resetAdd)
	return nil
}

// Query 返回序列 seq 在窗口 (a, b] 内的增量之和与重置次数。
//
// 窗口非法先于序列不存在检查。序列存在但窗口内无相邻对时返回 (0, 0, nil)。
func (t *Tracker) Query(seq string, a, b int64) (delta int64, resets int64, err error) {
	if a >= b {
		return 0, 0, ErrInvalidWindow
	}
	t.mu.RLock()
	defer t.mu.RUnlock()

	s, ok := t.series[seq]
	if !ok {
		return 0, 0, ErrUnknownSeries
	}
	// 增量归属于 q（相邻对的右端点）：下标范围 [lo, hi]，t 落在 (a, b]。
	lo := sort.Search(len(s.pts), func(i int) bool { return s.pts[i].t > a })
	hi := sort.Search(len(s.pts), func(i int) bool { return s.pts[i].t > b }) - 1
	if lo > hi {
		return 0, 0, nil // 空窗口：无相邻对落入
	}
	resets = s.prefReset[hi+1] - s.prefReset[lo]
	if s.prefCarry[hi+1] != s.prefCarry[lo] {
		return 0, 0, ErrOverflow
	}
	sum := s.prefSum[hi+1] - s.prefSum[lo]
	if sum > math.MaxInt64 {
		return 0, 0, ErrOverflow
	}
	return int64(sum), resets, nil
}
