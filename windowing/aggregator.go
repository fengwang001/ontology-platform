// Package windowing 提供一个可在线升级窗口大小的滚动窗口聚合器。
//
// 窗口以时间 0 为原点、左闭右开（[n*S, (n+1)*S)）；事件先按处理前水位
// 判定迟到，再更新水位并输出到期窗口。窗口大小的升级在一个与旧、新
// 大小都对齐的边界点上生效，保证每个事件恰好落入一套窗口划分。
package windowing

import (
	"errors"
	"sort"
	"sync"
)

// ErrNegativeEventTime 在事件时间为负时由 Submit 返回。
var ErrNegativeEventTime = errors.New("windowing: negative event time")

// Event 是一条输入事件。
type Event struct {
	Key       string
	EventTime int64
	Value     int64
}

// Result 是一个窗口对单个键的聚合输出。
type Result struct {
	Key   string
	Start int64
	End   int64
	Count int64
	Sum   int64
}

// Logger 记录输入、输出与判定依据。nil 表示不记录。
type Logger interface {
	Printf(format string, args ...any)
}

// Config 构造聚合器所需的参数。
type Config struct {
	// InitialSize 初始窗口大小，必须为正。
	InitialSize int64
	// AllowedLateness 水位延迟：WM = max(WM, maxEventTime - AllowedLateness)。
	AllowedLateness int64
	// Emit 在聚合器内部串行上下文中被调用，接收每个到期窗口结果。
	Emit func(Result)
	// Logger 可选的日志接收器。
	Logger Logger
}

// Aggregator 可在线升级窗口大小的滚动窗口聚合器。
type Aggregator struct {
	mu sync.Mutex

	size  int64 // 当前窗口大小；存在待生效升级时仍为旧大小
	delay int64 // 水位固定延迟

	wm      int64 // 当前水位，只增不减，初值 0
	maxSeen int64 // 已见最大事件时间

	// windows 以窗口右端与键标识一个持有状态的聚合桶。
	windows map[windowID]*windowAcc

	pending *pendingResize

	late     int64 // 迟到（丢弃）事件总数
	accepted int64 // 计入窗口的事件总数
	emitted  int64 // 已随结果输出的事件条数

	emit func(Result)
	log  Logger
}

type windowID struct {
	end int64
	key string
}

type windowAcc struct {
	start int64
	count int64
	sum   int64
}

type pendingResize struct {
	newSize  int64
	boundary int64 // 生效点 B
}

// New 创建聚合器。
func New(cfg Config) *Aggregator {
	if cfg.InitialSize <= 0 {
		panic("windowing: Config.InitialSize must be positive")
	}
	if cfg.AllowedLateness < 0 {
		panic("windowing: Config.AllowedLateness must be non-negative")
	}
	emit := cfg.Emit
	if emit == nil {
		emit = func(Result) {}
	}
	return &Aggregator{
		size:    cfg.InitialSize,
		delay:   cfg.AllowedLateness,
		windows: make(map[windowID]*windowAcc),
		emit:    emit,
		log:     cfg.Logger,
	}
}

// Submit 提交一条事件，返回该事件是否迟到（迟到会被丢弃并计数）。
func (a *Aggregator) Submit(e Event) (late bool, err error) {
	if e.EventTime < 0 {
		a.logf("reject event key=%q time=%d value=%d: negative event time",
			e.Key, e.EventTime, e.Value)
		return false, ErrNegativeEventTime
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	size := a.size
	partition := "current"
	if a.pending != nil && e.EventTime >= a.pending.boundary {
		size = a.pending.newSize
		partition = "pending-new"
	}
	start := floorStart(e.EventTime, size)
	end := start + size
	wmBefore := a.wm

	if end <= wmBefore {
		a.late++
		a.logf("input event key=%q time=%d value=%d -> LATE by %s size=%d window=[%d,%d) wm=%d (end<=wm), discarded",
			e.Key, e.EventTime, e.Value, partition, size, start, end, wmBefore)
		return true, nil
	}

	id := windowID{end: end, key: e.Key}
	acc := a.windows[id]
	if acc == nil {
		acc = &windowAcc{start: start}
		a.windows[id] = acc
	}
	acc.count++
	acc.sum += e.Value
	a.accepted++
	if e.EventTime > a.maxSeen {
		a.maxSeen = e.EventTime
	}
	a.logf("input event key=%q time=%d value=%d -> accept by %s size=%d window=[%d,%d) wm=%d",
		e.Key, e.EventTime, e.Value, partition, size, start, end, wmBefore)

	a.advanceLocked()
	return false, nil
}

// Watermark 返回当前水位。
func (a *Aggregator) Watermark() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.wm
}

// LateCount 返回累计迟到事件数。
func (a *Aggregator) LateCount() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.late
}

// AcceptedCount 返回累计接受（非迟到）事件数。
func (a *Aggregator) AcceptedCount() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.accepted
}

// InFlightCount 返回当前仍持有在途窗口中的事件条数。
func (a *Aggregator) InFlightCount() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.inFlightLocked()
}

// CurrentSize 返回当前生效窗口大小。
func (a *Aggregator) CurrentSize() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.size
}

// PendingResize 返回是否存在尚未完成的升级。
func (a *Aggregator) PendingResize() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.pending != nil
}

// EmittedCount 返回已输出结果中包含的事件条数。
func (a *Aggregator) EmittedCount() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.emitted
}

func (a *Aggregator) inFlightLocked() int64 {
	var n int64
	for _, acc := range a.windows {
		n += acc.count
	}
	return n
}

// advanceLocked 推进水位、输出到期窗口并在水位越过生效点时完成升级。
// 调用方必须持有 a.mu。
func (a *Aggregator) advanceLocked() {
	candidate := a.maxSeen - a.delay
	if candidate > a.wm {
		a.wm = candidate
		a.logf("watermark advanced to %d (maxSeen=%d delay=%d)", a.wm, a.maxSeen, a.delay)
	}

	due := make([]windowID, 0)
	for id := range a.windows {
		if id.end <= a.wm {
			due = append(due, id)
		}
	}
	sort.Slice(due, func(i, j int) bool {
		if due[i].end != due[j].end {
			return due[i].end < due[j].end
		}
		return due[i].key < due[j].key
	})
	for _, id := range due {
		acc := a.windows[id]
		r := Result{Key: id.key, Start: acc.start, End: id.end, Count: acc.count, Sum: acc.sum}
		delete(a.windows, id)
		a.emitted += acc.count
		a.logf("output key=%q window=[%d,%d) count=%d sum=%d (wm=%d)",
			r.Key, r.Start, r.End, r.Count, r.Sum, a.wm)
		a.emit(r)
	}

	if a.pending != nil && a.wm >= a.pending.boundary {
		newSize := a.pending.newSize
		boundary := a.pending.boundary
		a.pending = nil
		a.size = newSize
		a.logf("resize completed at boundary=%d, current size=%d", boundary, newSize)
	}
}

// floorStart 返回事件时间 t 在大小为 size 的划分中所属窗口的左端点
// （窗口以 0 为原点、左闭右开）。
func floorStart(t, size int64) int64 {
	q := t / size
	r := t % size
	if r != 0 && r < 0 {
		q--
	}
	return q * size
}

func (a *Aggregator) logf(format string, args ...any) {
	if a.log != nil {
		a.log.Printf(format, args...)
	}
}

// ResizeError 表示升级申请被拒绝的可区分原因。
type ResizeError struct {
	Reason ResizeRejectReason
}

func (e *ResizeError) Error() string {
	switch e.Reason {
	case ResizeNonPositive:
		return "windowing: new window size must be positive"
	case ResizePending:
		return "windowing: a resize is already pending"
	case ResizeSameSize:
		return "windowing: new size equals current size"
	case ResizeLCMTooLarge:
		return "windowing: lcm of current and new size exceeds 1e9"
	default:
		return "windowing: resize rejected"
	}
}

// ResizeRejectReason 升级被拒原因。
type ResizeRejectReason int

const (
	// ResizeNonPositive：新大小非正。
	ResizeNonPositive ResizeRejectReason = iota + 1
	// ResizePending：已有升级待完成。
	ResizePending
	// ResizeSameSize：新大小与当前大小相同。
	ResizeSameSize
	// ResizeLCMTooLarge：lcm(当前大小, 新大小) 超过 10^9。
	ResizeLCMTooLarge
)

// RequestResize 申请把窗口大小升级为 newSize，返回生效点。
func (a *Aggregator) RequestResize(newSize int64) (boundary int64, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// 拒绝顺序固定：非正 -> 有待完成升级 -> 与当前相同 -> lcm 超限。
	// 任一种拒绝都不改变水位、窗口状态与迟到计数。
	if newSize <= 0 {
		a.logf("resize rejected: newSize=%d non-positive", newSize)
		return 0, &ResizeError{Reason: ResizeNonPositive}
	}
	if a.pending != nil {
		a.logf("resize rejected: pending resize to=%d boundary=%d exists",
			a.pending.newSize, a.pending.boundary)
		return 0, &ResizeError{Reason: ResizePending}
	}
	if newSize == a.size {
		a.logf("resize rejected: newSize=%d equals current size", newSize)
		return 0, &ResizeError{Reason: ResizeSameSize}
	}
	lcmVal, ok := lcmChecked(a.size, newSize)
	if !ok || lcmVal > maxLCM {
		a.logf("resize rejected: lcm(%d,%d) exceeds %d", a.size, newSize, maxLCM)
		return 0, &ResizeError{Reason: ResizeLCMTooLarge}
	}

	// M = max(当前水位, 所有持有状态窗口的右端)。
	m := a.wm
	for id := range a.windows {
		if id.end > m {
			m = id.end
		}
	}
	// B = lcm 的整数倍中不小于 M 的最小者。
	b := ((m + lcmVal - 1) / lcmVal) * lcmVal

	a.pending = &pendingResize{newSize: newSize, boundary: b}
	a.logf("resize accepted: %d -> %d, lcm=%d, M=%d, boundary B=%d (events with t<B keep old size, t>=B use new size)",
		a.size, newSize, lcmVal, m, b)
	// 若申请时水位已到达生效点，升级当场完成（边界点之前的窗口此时必已到期输出）。
	if a.wm >= b {
		a.pending = nil
		a.size = newSize
		a.logf("resize completed immediately at boundary=%d (wm=%d)", b, a.wm)
	}
	return b, nil
}

const maxLCM int64 = 1_000_000_000

// lcmChecked 返回 lcm(x,y)；当中间结果溢出 int64 时 ok=false。
// x、y 均为正整数。
func lcmChecked(x, y int64) (int64, bool) {
	g := gcd(x, y)
	xg := x / g
	r := xg * y
	if xg != 0 && r/xg != y {
		return 0, false
	}
	return r, true
}

func gcd(a, b int64) int64 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}
