package window

import (
	"fmt"
	"log"
	"os"
	"sort"
	"sync"
)

// Event 是进入滚动窗口聚合器的单条事件。
type Event struct {
	Key  string
	Time int64 // 整数事件时间
	V    int64 // 整数值
}

// Result 是一个 [Start, End) 窗口在某键上的聚合输出。
type Result struct {
	Key   string
	Start int64
	End   int64
	Count int64
	Sum   int64
}

// Logger 是聚合器使用的最小日志接口。
type Logger interface {
	Printf(format string, args ...any)
}

// Sink 接收按 (右端, 键) 升序输出的窗口结果。
type Sink func(Result)

// 升级被拒绝的可区分原因。
var (
	ErrNonPositiveSize  = fmt.Errorf("window: new size must be positive")
	ErrUpgradePending   = fmt.Errorf("window: an upgrade is already pending")
	ErrSameSize         = fmt.Errorf("window: new size equals current size")
	ErrLCMLimitExceeded = fmt.Errorf("window: lcm of sizes exceeds %d", maxLCM)
)

const maxLCM = 1_000_000_000

// Aggregator 是可在线升级窗口大小的滚动窗口聚合器。
//
// 所有提交与升级申请都由同一把互斥锁串行化：方法返回即代表该操作已在
// 某个全局串行顺序中处理完毕，因而每个事件只归属该顺序下的一套窗口划分；
// 按相同的操作序列（相同调用次序与参数）重放会得到完全相同的输出。
type Aggregator struct {
	mu      sync.Mutex
	size    int64 // 当前窗口大小
	delay   int64 // 水位固定延迟
	wm      int64 // 当前水位（只增不减，初值 0）
	maxSeen int64 // 已见最大事件时间

	windows map[int64]map[string]*acc // 窗口右端 -> 键 -> 聚合

	pending bool  // 是否有升级待完成
	newSize int64 // 待生效的新大小 S
	bound   int64 // 生效点 B

	submitted int64 // 总提交事件数
	late      int64 // 迟到丢弃事件数

	sink Sink
	log  Logger
}

type acc struct {
	count int64
	sum   int64
}

// New 创建聚合器：初始窗口大小 size（正整数）、水位固定延迟 delay（非负）、
// 输出回调 sink（不得为 nil；回调在锁内执行，不得回调聚合器方法）。
func New(size, delay int64, sink Sink) *Aggregator {
	if size <= 0 {
		panic("window: size must be positive")
	}
	if delay < 0 {
		panic("window: delay must be non-negative")
	}
	if sink == nil {
		panic("window: sink must not be nil")
	}
	return &Aggregator{
		size:    size,
		delay:   delay,
		windows: make(map[int64]map[string]*acc),
		sink:    sink,
		log:     log.New(os.Stderr, "window: ", log.LstdFlags|log.Lmicroseconds),
	}
}

// Submit 提交一条事件。事件先按处理前水位判定迟到（所属窗口右端 <= 水位
// 即迟到，丢弃并计数），否则计入；随后更新水位并输出到期窗口。
// 返回 true 表示计入，false 表示迟到。
func (a *Aggregator) Submit(e Event) bool {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.submitted++

	// 归属所用的划分：存在待完成升级且生效点为 B 时，
	// t < B 走旧划分，t >= B 走新划分；否则走当前划分。
	s := a.size
	if a.pending && e.Time >= a.bound {
		s = a.newSize
	}
	end := windowEnd(e.Time, s)

	// 迟到判定使用处理前水位；迟到事件只计数，不改变任何状态。
	if end <= a.wm {
		a.late++
		a.log.Printf("input event key=%q t=%d v=%d -> LATE: window [%d,%d) under size %d ends <= watermark %d (late=%d)",
			e.Key, e.Time, e.V, end-s, end, s, a.wm, a.late)
		return false
	}

	byKey, ok := a.windows[end]
	if !ok {
		byKey = make(map[string]*acc)
		a.windows[end] = byKey
	}
	c, ok := byKey[e.Key]
	if !ok {
		c = &acc{}
		byKey[e.Key] = c
	}
	c.count++
	c.sum += e.V
	a.log.Printf("input event key=%q t=%d v=%d -> ACCEPT: window [%d,%d) under size %d (watermark before update=%d)",
		e.Key, e.Time, e.V, end-s, end, s, a.wm)

	if e.Time > a.maxSeen {
		a.maxSeen = e.Time
	}

	a.advanceAndEmitLocked()
	return true
}

// Resize 申请把窗口大小升级为 newSize。以下情况按序只报第一个错误，且
// 拒绝不改变水位、窗口状态与迟到计数：
//  1. newSize <= 0；
//  2. 已有升级待完成；
//  3. newSize 与当前大小相同；
//  4. lcm(当前大小, newSize) 超过 10^9。
//
// 生效点 B 为 lcm 的整数倍中不小于 M 的最小者，
// M = max(水位, 所有持有状态窗口右端的最大值)。
// 水位 >= B 时升级完成，当前大小改为 newSize。
func (a *Aggregator) Resize(newSize int64) (boundary int64, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if newSize <= 0 {
		a.log.Printf("resize to %d REJECTED: non-positive size", newSize)
		return 0, ErrNonPositiveSize
	}
	if a.pending {
		a.log.Printf("resize to %d REJECTED: upgrade pending (size %d -> %d at B=%d)",
			newSize, a.size, a.newSize, a.bound)
		return 0, ErrUpgradePending
	}
	if newSize == a.size {
		a.log.Printf("resize to %d REJECTED: same as current size", newSize)
		return 0, ErrSameSize
	}

	l, ok := lcmChecked(a.size, newSize, maxLCM)
	if !ok {
		a.log.Printf("resize to %d REJECTED: lcm(%d,%d) exceeds %d", newSize, a.size, newSize, maxLCM)
		return 0, ErrLCMLimitExceeded
	}

	m := a.wm
	for end := range a.windows {
		if end > m {
			m = end
		}
	}
	b := ceilMultiple(m, l)

	a.pending = true
	a.newSize = newSize
	a.bound = b
	a.log.Printf("resize %d -> %d ACCEPTED: lcm=%d M=%d boundary B=%d (events t < B use size %d, t >= B use size %d)",
		a.size, newSize, l, m, b, a.size, newSize)

	// 若水位已达到 B（无在途状态且 M 即水位时可能立即发生），立即完成升级。
	if a.wm >= b {
		a.completeUpgradeLocked()
	}
	return b, nil
}

// CurrentSize 返回当前生效的窗口大小。
func (a *Aggregator) CurrentSize() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.size
}

// Watermark 返回当前水位。
func (a *Aggregator) Watermark() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.wm
}

// Pending 返回待完成升级（新大小与生效点）；无待完成升级时 ok 为 false。
func (a *Aggregator) Pending() (newSize, boundary int64, ok bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.pending {
		return 0, 0, false
	}
	return a.newSize, a.bound, true
}

// Stats 返回总提交数、迟到数与在途窗口内条数。
func (a *Aggregator) Stats() (submitted, late, pendingEvents int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.submitted, a.late, a.pendingEventsLocked()
}

// SetLogger 替换日志输出；传 nil 使用写到 stderr 的默认 logger。
func (a *Aggregator) SetLogger(l Logger) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if l == nil {
		l = log.New(os.Stderr, "window: ", log.LstdFlags|log.Lmicroseconds)
	}
	a.log = l
}

// advanceAndEmitLocked 先把水位推进到 max(wm, maxSeen-delay)（只增不减），
// 再输出并清除所有右端 <= 新水位的窗口；最后判定升级是否完成。
// 输出按 (右端, 键) 升序。调用方必须持有 mu。
func (a *Aggregator) advanceAndEmitLocked() {
	if cand := a.maxSeen - a.delay; cand > a.wm {
		a.wm = cand
	}

	var ends []int64
	for end := range a.windows {
		if end <= a.wm {
			ends = append(ends, end)
		}
	}
	sort.Slice(ends, func(i, j int) bool { return ends[i] < ends[j] })

	for _, end := range ends {
		byKey := a.windows[end]
		keys := make([]string, 0, len(byKey))
		for k := range byKey {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		s := a.size
		if a.pending && end > a.bound {
			s = a.newSize
		}
		for _, k := range keys {
			c := byKey[k]
			r := Result{
				Key:   k,
				Start: end - s,
				End:   end,
				Count: c.count,
				Sum:   c.sum,
			}
			a.log.Printf("output key=%q window=[%d,%d) count=%d sum=%d (watermark=%d)",
				r.Key, r.Start, r.End, r.Count, r.Sum, a.wm)
			a.sink(r) // sink 在锁内串行调用；不得回调聚合器
		}
		delete(a.windows, end)
	}

	if a.pending && a.wm >= a.bound {
		a.completeUpgradeLocked()
	}
}

// completeUpgradeLocked 在水位达到生效点后提交升级：当前大小改为新大小。
// 此时所有右端 <= B 的旧窗口必然已被输出清除，且右端 > B 的窗口只可能是
// 新大小划分下的窗口（首个新窗口右端为 B+S），两套划分互不重叠。
func (a *Aggregator) completeUpgradeLocked() {
	a.log.Printf("upgrade completed at watermark=%d >= B=%d: current size %d -> %d",
		a.wm, a.bound, a.size, a.newSize)
	a.size = a.newSize
	a.pending = false
	a.newSize = 0
	a.bound = 0
}

func (a *Aggregator) pendingEventsLocked() int64 {
	var n int64
	for _, byKey := range a.windows {
		for _, c := range byKey {
			n += c.count
		}
	}
	return n
}

// windowEnd 返回时间 t 在以 0 为原点、左闭右开、大小为 s 的划分中的窗口右端。
// 使用数学 floor 除法，事件时间为负时同样正确（例如 s=4、t=-1 -> 右端 0）。
func windowEnd(t, s int64) int64 {
	return (floorDiv(t, s) + 1) * s
}

// floorDiv 返回数学意义上的 floor(t/s)（s > 0），对负数向负无穷取整。
func floorDiv(t, s int64) int64 {
	q := t / s
	r := t % s
	if r != 0 && t < 0 {
		q--
	}
	return q
}

// ceilMultiple 返回 step 的整数倍（含 0 与负倍数）中不小于 v 的最小者。
func ceilMultiple(v, step int64) int64 {
	b := floorDiv(v, step) * step
	if b < v {
		b += step
	}
	return b
}

// lcmChecked 计算最小公倍数；结果超过 limit 时返回 false。
func lcmChecked(x, y, limit int64) (int64, bool) {
	g := gcd(x, y)
	xg := x / g
	if xg > limit/y { // 等价于 xg*y > limit，且避免乘法溢出
		return 0, false
	}
	l := xg * y
	if l > limit {
		return 0, false
	}
	return l, true
}

func gcd(x, y int64) int64 {
	for y != 0 {
		x, y = y, x%y
	}
	if x < 0 {
		x = -x
	}
	return x
}
