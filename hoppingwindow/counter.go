package hoppingwindow

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// Config 描述跳跃窗口计数器的参数。
//
// WindowSize 为窗口长度（左闭右开区间的宽度），Slide 为相邻窗口起点的步长，
// MaxOpenWindows 为同一时刻允许保留的、尚未关闭的窗口数上限（按不同窗口起点计）。
// 要求 0 < Slide <= WindowSize，MaxOpenWindows > 0，且单个事件最多落入的
// 窗口数 ceil(WindowSize/Slide) 不得超过 MaxOpenWindows。
//
// Logger 可选，用于打印输入、输出与判定依据；为 nil 时不打印日志。
type Config struct {
	WindowSize     time.Duration
	Slide          time.Duration
	MaxOpenWindows int
	Logger         Logger
}

// Logger 是计数器使用的最小日志接口，log.Logger 与 log/slog 的包装均可满足。
type Logger interface {
	Printf(format string, args ...any)
}

// WindowResult 是一个已关闭窗口中某个键的最终计数。
type WindowResult struct {
	// WindowStart / WindowEnd 为窗口的左闭右开区间 [WindowStart, WindowEnd)。
	WindowStart time.Time
	WindowEnd   time.Time
	Key         string
	Count       int64
}

// OpenWindowCount 描述一个尚未关闭窗口内某个键的当前计数。
type OpenWindowCount struct {
	WindowStart time.Time
	WindowEnd   time.Time
	Key         string
	Count       int64
}

// StateSnapshot 是计数器某一时刻逐字段一致的只读视图。
// WindowCounts 按窗口起点、再按键排序，保证重复读取结果稳定。
type StateSnapshot struct {
	Clock        time.Time
	Dropped      int64
	WindowCounts []OpenWindowCount
}

// Counter 是并发安全的跳跃窗口计数器。零值不可用，请使用 New 构造。
type Counter struct {
	mu         sync.Mutex
	cfg        Config
	sizeNanos  int64
	slideNanos int64
	// clock 为当前调用方时钟（Unix 纳秒），只进不退。
	// clockSet 为 false 表示时钟从未推进过，语义上等价于 -∞（不关闭任何窗口）。
	clock    int64
	clockSet bool
	dropped  int64
	// open 按窗口起点（Unix 纳秒）保存每个尚未关闭窗口内各键的计数。
	open map[int64]map[string]int64
}

// New 校验配置并返回一个时钟初始化为 time.Time 零值的计数器。
func New(cfg Config) (*Counter, error) {
	if cfg.WindowSize <= 0 {
		return nil, invalidConfigf("WindowSize must be > 0, got %v", cfg.WindowSize)
	}
	if cfg.Slide <= 0 {
		return nil, invalidConfigf("Slide must be > 0, got %v", cfg.Slide)
	}
	if cfg.Slide > cfg.WindowSize {
		return nil, invalidConfigf("Slide (%v) must not exceed WindowSize (%v)", cfg.Slide, cfg.WindowSize)
	}
	if cfg.MaxOpenWindows <= 0 {
		return nil, invalidConfigf("MaxOpenWindows must be > 0, got %d", cfg.MaxOpenWindows)
	}
	sizeNanos := int64(cfg.WindowSize)
	slideNanos := int64(cfg.Slide)
	// 单个事件最多落入的窗口数 = ceil(size / slide)。
	maxOverlap := sizeNanos / slideNanos
	if sizeNanos%slideNanos != 0 {
		maxOverlap++
	}
	if maxOverlap > int64(cfg.MaxOpenWindows) {
		return nil, invalidConfigf("an event can belong to %d windows but MaxOpenWindows is %d",
			maxOverlap, cfg.MaxOpenWindows)
	}
	return &Counter{
		cfg:        cfg,
		sizeNanos:  sizeNanos,
		slideNanos: slideNanos,
		open:       make(map[int64]map[string]int64),
	}, nil
}

// Record 把一个带时间戳的事件按键计入所有包含 ts 的、尚未关闭的窗口。
//
// 窗口相对 Unix 纪元按步长对齐：第 k 个窗口为 [k*Slide, k*Slide+WindowSize)。
// 包含 ts 的窗口起点满足 ts-WindowSize < start <= ts。
// 其中终点已不晚于当前时钟的窗口视为已关闭，事件不再计入；
// 若所有包含窗口均已关闭，则整条丢弃，丢弃数加一。
//
// 空键或会使打开窗口数超过上限时返回错误，且不改变任何状态。
func (c *Counter) Record(ts time.Time, key string) error {
	if key == "" {
		c.logf("RECORD ts=%s key=<empty> -> REJECT empty key (state unchanged)", ts.Format(time.RFC3339Nano))
		return ErrEmptyKey
	}

	tsNanos := ts.UnixNano()
	// 最大的满足 k*slide <= t 的 k。
	kHi := floorDiv(tsNanos, c.slideNanos)
	// 最小的满足 k*slide > t-size 的 k = floor((t-size)/slide)+1。
	kLo := floorDiv(tsNanos-c.sizeNanos, c.slideNanos) + 1

	c.mu.Lock()
	defer c.mu.Unlock()

	// 收集尚未关闭的包含窗口（start+size > clock）；时钟未设置时等价于 -∞。
	var openStarts []int64
	var closedStarts []int64
	for k := kLo; k <= kHi; k++ {
		start := k * c.slideNanos
		if c.clockSet && start+c.sizeNanos <= c.clock {
			closedStarts = append(closedStarts, start)
		} else {
			openStarts = append(openStarts, start)
		}
	}

	if len(openStarts) == 0 {
		c.dropped++
		c.logf("RECORD ts=%s key=%q windows=%v clock=%s -> DROP all containing windows closed (dropped=%d)",
			ts.Format(time.RFC3339Nano), key, fmtStarts(closedStarts, c.sizeNanos),
			c.clockLabel(), c.dropped)
		return nil
	}

	// 仅统计本事件将新引入的窗口；已存在的窗口不重复计数。
	newWindows := 0
	for _, start := range openStarts {
		if _, ok := c.open[start]; !ok {
			newWindows++
		}
	}
	if len(c.open)+newWindows > c.cfg.MaxOpenWindows {
		c.logf("RECORD ts=%s key=%q -> REJECT too many open windows: open=%d new=%d limit=%d (state unchanged)",
			ts.Format(time.RFC3339Nano), key, len(c.open), newWindows, c.cfg.MaxOpenWindows)
		return ErrTooManyOpenWindows
	}

	for _, start := range openStarts {
		counts, ok := c.open[start]
		if !ok {
			counts = make(map[string]int64)
			c.open[start] = counts
		}
		counts[key]++
	}
	c.logf("RECORD ts=%s key=%q -> COUNT into open windows=%v; closed-and-skipped=%v; clock=%s",
		ts.Format(time.RFC3339Nano), key, fmtStarts(openStarts, c.sizeNanos),
		fmtStarts(closedStarts, c.sizeNanos), c.clockLabel())
	return nil
}

// Advance 把时钟单调推进到 t（含），关闭所有终点不晚于 t 的窗口，
// 并按窗口终点、再按键的顺序返回这些窗口的计数结果。每个窗口只输出一次。
// t 早于当前时钟时返回 KindClockRegression 错误且不改变任何状态；
// t 等于当前时钟时不关闭任何窗口，返回空切片。
func (c *Counter) Advance(t time.Time) ([]WindowResult, error) {
	tNanos := t.UnixNano()

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.clockSet && tNanos < c.clock {
		c.logf("ADVANCE to=%s -> REJECT clock regression (current=%s, state unchanged)",
			t.Format(time.RFC3339Nano), c.clockLabel())
		return nil, ErrClockRegression
	}
	if c.clockSet && tNanos == c.clock {
		c.logf("ADVANCE to=%s -> NOOP clock unchanged, no windows closed",
			t.Format(time.RFC3339Nano))
		return nil, nil
	}

	// 收集所有到期窗口（start+size <= t），按起点（等同按终点）升序。
	var closing []int64
	for start := range c.open {
		if start+c.sizeNanos <= tNanos {
			closing = append(closing, start)
		}
	}
	sort.Slice(closing, func(i, j int) bool { return closing[i] < closing[j] })

	results := make([]WindowResult, 0)
	for _, start := range closing {
		counts := c.open[start]
		keys := make([]string, 0, len(counts))
		for key := range counts {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		end := start + c.sizeNanos
		for _, key := range keys {
			results = append(results, WindowResult{
				WindowStart: time.Unix(0, start),
				WindowEnd:   time.Unix(0, end),
				Key:         key,
				Count:       counts[key],
			})
		}
		delete(c.open, start)
	}
	c.clock = tNanos
	c.clockSet = true

	c.logf("ADVANCE to=%s -> closed %d windows, emitted %d results: %v",
		t.Format(time.RFC3339Nano), len(closing), len(results), results)
	return results, nil
}

// Snapshot 返回逐字段一致的只读视图：当前时钟、已丢弃事件数、
// 尚未关闭窗口中各键的累计计数。WindowCounts 按窗口起点、键排序。
func (c *Counter) Snapshot() StateSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()

	snap := StateSnapshot{
		Clock:   c.clockTime(),
		Dropped: c.dropped,
	}
	starts := make([]int64, 0, len(c.open))
	for start := range c.open {
		starts = append(starts, start)
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i] < starts[j] })
	for _, start := range starts {
		counts := c.open[start]
		keys := make([]string, 0, len(counts))
		for key := range counts {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		end := start + c.sizeNanos
		for _, key := range keys {
			snap.WindowCounts = append(snap.WindowCounts, OpenWindowCount{
				WindowStart: time.Unix(0, start),
				WindowEnd:   time.Unix(0, end),
				Key:         key,
				Count:       counts[key],
			})
		}
	}
	return snap
}

func (c *Counter) logf(format string, args ...any) {
	if c.cfg.Logger != nil {
		c.cfg.Logger.Printf(format, args...)
	}
}

// clockTime 返回当前时钟；从未推进过时返回 time.Time{} 零值作为"负无穷"标记。
func (c *Counter) clockTime() time.Time {
	if !c.clockSet {
		return time.Time{}
	}
	return time.Unix(0, c.clock)
}

// clockLabel 仅用于日志：时钟未设置时显示 -inf。
func (c *Counter) clockLabel() string {
	if !c.clockSet {
		return "-inf"
	}
	return time.Unix(0, c.clock).Format(time.RFC3339Nano)
}

// floorDiv 返回数学意义上的向下取整除法 a/b，b 必须为正数。
// 对负数 a 同样成立，这是负时间戳窗口归属正确的关键。
func floorDiv(a, b int64) int64 {
	q := a / b
	if r := a % b; r != 0 && r < 0 {
		q--
	}
	return q
}

// fmtStarts 把窗口起点格式化为 [start,end) 区间列表，仅用于日志。
func fmtStarts(starts []int64, size int64) string {
	s := "["
	for i, start := range starts {
		if i > 0 {
			s += ", "
		}
		s += fmt.Sprintf("[%s,%s)",
			time.Unix(0, start).Format(time.RFC3339Nano),
			time.Unix(0, start+size).Format(time.RFC3339Nano))
	}
	return s + "]"
}
