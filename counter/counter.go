package counter

import (
	"errors"
	"sort"
	"sync"
)

// u128 是无符号 128 位整数，用于保存增量前缀和。
// 单条增量最大为 math.MaxInt64，因此 uint64 足以容纳每条增量；
// 使用双字前缀和可以保证任意窗口求和在 int64 范围内时得到精确结果，
// 同时让 int64 溢出的判定不依赖前缀和本身是否回绕。
type u128 struct {
	hi uint64
	lo uint64
}

// addU64 返回 x + n（n 由调用方保证 <= math.MaxInt64）。
func (x u128) addU64(n uint64) u128 {
	lo := x.lo + n
	var carry uint64
	if lo < x.lo {
		carry = 1
	}
	return u128{hi: x.hi + carry, lo: lo}
}

// sub 返回 x - y，要求 x >= y（前缀和单调不减）。
func (x u128) sub(y u128) u128 {
	lo := x.lo - y.lo
	var borrow uint64
	if x.lo < y.lo {
		borrow = 1
	}
	return u128{hi: x.hi - y.hi - borrow, lo: lo}
}

// fitsInt64 报告 x 是否可表示为非负 int64。
func (x u128) fitsInt64() bool {
	return x.hi == 0 && x.lo <= 1<<63-1
}

// seriesData 保存单个序列的全部状态。
//
// 读数写入时间戳严格递增（同一时间戳只允许幂等重复）。
// 对于第 i 个读数 r[i]（i >= 1），记相邻对 (r[i-1], r[i]) 的增量为 d[i]、
// 重置标记为 z[i]（z[i] ∈ {0,1}），则：
//
//	incSum[i] = d[1] + ... + d[i]
//	resSum[i] = z[1] + ... + z[i]
//
// incSum[0] 与 resSum[0] 为零值。长度为 len(ts) 的这些数组通过索引 i
// 直接与读数 r[i] 对齐，便于二分后做常数时间窗口求和。
type seriesData struct {
	ts     []int64
	vals   []int64
	incSum []u128
	resSum []int64
}

// lastValue 返回该序列最新读数的值。调用时序列至少有一个读数。
func (s *seriesData) lastValue() int64 {
	return s.vals[len(s.vals)-1]
}

// Result 是对窗口 (a,b] 的查询结果。
type Result struct {
	// Increment 是窗口内所有相邻对的增量之和。
	Increment int64
	// Resets 是窗口内发生的重置次数。
	Resets int64
}

var (
	// ErrNegativeValue 表示写入的读数为负数（先于时间次序检查）。
	ErrNegativeValue = errors.New("counter: negative value")
	// ErrTimestampOutOfOrder 表示新读数的时间戳小于该序列最新读数的时间戳。
	ErrTimestampOutOfOrder = errors.New("counter: timestamp out of order")
	// ErrValueConflict 表示同一时间戳被写入了不同的数值。
	ErrValueConflict = errors.New("counter: value conflict at same timestamp")
	// ErrInvalidWindow 表示查询窗口不满足 a < b（先于序列不存在检查）。
	ErrInvalidWindow = errors.New("counter: invalid window (a >= b)")
	// ErrSeriesNotFound 表示查询了一个从未出现过的序列。
	ErrSeriesNotFound = errors.New("counter: series not found")
	// ErrOverflow 表示窗口内的增量之和超出 int64 范围。
	ErrOverflow = errors.New("counter: increment sum overflows int64")
)

// Tracker 按序列接收累计读数，并支持对左开右闭时间窗口求增量与重置次数。
// 所有方法均可被并发调用。
type Tracker struct {
	mu     sync.RWMutex
	series map[string]*seriesData
}

// New 创建一个空的 Tracker。
func New() *Tracker {
	return &Tracker{series: make(map[string]*seriesData)}
}

// Write 写入序列 key 的一个读数 (t,v)。
//
// 校验顺序：v < 0 先于时间次序检查；被拒绝时不改变任何状态。
// 同一时间戳写入相同数值为幂等重复，写入成功且状态不变；
// 同一时间戳写入不同数值返回 ErrValueConflict。
func (tr *Tracker) Write(key string, t, v int64) error {
	if v < 0 {
		return ErrNegativeValue
	}

	tr.mu.Lock()
	defer tr.mu.Unlock()

	s := tr.series[key]
	if s != nil && len(s.ts) > 0 {
		last := len(s.ts) - 1
		switch {
		case t < s.ts[last]:
			return ErrTimestampOutOfOrder
		case t == s.ts[last]:
			// 时间戳与最新读数相同：仅允许数值相同的幂等重复。
			if v != s.lastValue() {
				return ErrValueConflict
			}
			return nil
		}
	}

	if s == nil {
		s = &seriesData{}
		tr.series[key] = s
	}

	// 首个读数没有前驱，不产生增量与重置；incSum/resSum 在索引 0 处为零值。
	var delta u128
	var reset int64
	if n := len(s.ts); n > 0 {
		prev := s.lastValue()
		if uint64(v) < uint64(prev) {
			// 重置：增量按重置后的读数 v 计算（重置前未上报的部分不补）。
			delta = s.incSum[n-1].addU64(uint64(v))
			reset = s.resSum[n-1] + 1
		} else {
			delta = s.incSum[n-1].addU64(uint64(v - prev))
			reset = s.resSum[n-1]
		}
	}

	s.ts = append(s.ts, t)
	s.vals = append(s.vals, v)
	s.incSum = append(s.incSum, delta)
	s.resSum = append(s.resSum, reset)
	return nil
}

// Query 查询序列 key 在窗口 (a,b] 内的增量与重置次数。
//
// 校验顺序：a >= b 先于序列不存在检查。
// 只有相邻对的“后一个读数”的时间戳落在 (a,b] 内时，该相邻对才计入；
// 序列存在但窗口内没有相邻对时返回零值 Result 而非错误。
func (tr *Tracker) Query(key string, a, b int64) (Result, error) {
	if a >= b {
		return Result{}, ErrInvalidWindow
	}

	tr.mu.RLock()
	defer tr.mu.RUnlock()

	s := tr.series[key]
	if s == nil {
		return Result{}, ErrSeriesNotFound
	}

	n := len(s.ts)
	// 窗口 (a,b] 对读数时间戳而言即 {r[i] : a < r[i].ts <= b}。
	// 首个读数（索引 0）没有前驱，不计入任何窗口。
	left := sort.Search(n, func(i int) bool { return s.ts[i] > a })
	right := sort.Search(n, func(i int) bool { return s.ts[i] > b })

	var incLo, incHi u128
	var resLo, resHi int64
	if right > 0 {
		incHi = s.incSum[right-1]
		resHi = s.resSum[right-1]
	}
	// left 指向第一个 ts > a 的读数；窗口计入的相邻对其后读索引属于
	// [left, right)。前缀和区间和需要去掉索引 < left 的相邻对，
	// 即减去后读索引为 left-1 的前缀和（left==0 或 left==1 时均为零）。
	if left > 1 {
		incLo = s.incSum[left-1]
		resLo = s.resSum[left-1]
	}

	inc := incHi.sub(incLo)
	if !inc.fitsInt64() {
		return Result{}, ErrOverflow
	}
	return Result{Increment: int64(inc.lo), Resets: resHi - resLo}, nil
}
