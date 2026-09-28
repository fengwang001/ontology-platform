package hopping

import (
	"fmt"
	"math"
	"sort"
	"sync"
)

// Logger 是计数器接受的日志接口，与 log.Printf 签名一致。
// 日志中必须能看到输入、输出以及判定依据。
//
// 注意：Logger 的实现不得回调同一个计数器（回调会在锁上自死锁）。
type Logger interface {
	Printf(format string, args ...any)
}

// Config 描述跳跃窗口的划分方式与容量上限。
type Config struct {
	// WindowSize 是窗口长度（> 0）。
	WindowSize int64
	// SlideStep 是相邻窗口起点的步长（0 < SlideStep <= WindowSize）。
	// 步长小于窗长时窗口互相重叠，一个事件可同时落入多个窗口。
	SlideStep int64
	// MaxOpenWindows 是允许同时保留的打开窗口数量上限（> 0）。
	MaxOpenWindows int

	// Logger 可选；为 nil 时不输出日志。
	Logger Logger
}

// WindowCount 是某个键在某个已关闭窗口内的计数结果。
type WindowCount struct {
	WindowStart int64
	WindowEnd   int64
	Key         string
	Count       int64
}

// Stats 是计数器某一时刻的一致快照（逐字段来自同一把读锁）。
type Stats struct {
	// Watermark 是当前时钟水位（初始为 math.MinInt64）。
	Watermark int64
	// OpenWindows 是当前仍打开（已缓存、未关闭）的窗口数。
	OpenWindows int
	// BufferedEvents 是所有打开窗口内尚未输出的计数增量之和
	// （一个事件计入 n 个窗口即贡献 n）。
	BufferedEvents int64
	// DroppedEvents 是因全部归属窗口已关闭而被整条丢弃的事件数。
	DroppedEvents int64
	// EmittedWindows 是已关闭并输出过的窗口总数（每个窗口只输出一次）。
	EmittedWindows int64
	// EmittedRecords 是已输出的（窗口, 键）记录总数。
	EmittedRecords int64
}

// Counter 是并发安全的跳跃窗口计数器。
type Counter struct {
	mu sync.RWMutex

	size int64
	step int64
	max  int
	log  Logger

	// watermark 为当前时钟水位；终点 <= watermark 的窗口视为已关闭。
	watermark int64
	// open 以窗口起点为键，保存每个打开窗口内每键的累计计数。
	open map[int64]map[string]int64

	dropped        int64
	emittedWindows int64
	emittedRecords int64
}

// New 校验配置并创建计数器。初始水位为 math.MinInt64。
func New(cfg Config) (*Counter, error) {
	if cfg.WindowSize <= 0 {
		return nil, fmt.Errorf("%w: window size must be positive, got %d",
			ErrInvalidConfig, cfg.WindowSize)
	}
	if cfg.SlideStep <= 0 {
		return nil, fmt.Errorf("%w: slide step must be positive, got %d",
			ErrInvalidConfig, cfg.SlideStep)
	}
	if cfg.SlideStep > cfg.WindowSize {
		return nil, fmt.Errorf("%w: slide step %d exceeds window size %d",
			ErrInvalidConfig, cfg.SlideStep, cfg.WindowSize)
	}
	if cfg.MaxOpenWindows <= 0 {
		return nil, fmt.Errorf("%w: max open windows must be positive, got %d",
			ErrInvalidConfig, cfg.MaxOpenWindows)
	}
	return &Counter{
		size:      cfg.WindowSize,
		step:      cfg.SlideStep,
		max:       cfg.MaxOpenWindows,
		log:       cfg.Logger,
		watermark: math.MinInt64,
		open:      make(map[int64]map[string]int64),
	}, nil
}

// Add 把一条事件（时间戳、键、正增量）计入所有仍打开的归属窗口。
//
// 归属窗口为所有满足 [start, start+size) 包含 timestamp 的窗口；
// 其中终点 <= 当前水位的窗口已关闭，事件不计入；若全部归属窗口都已
// 关闭，则整条事件丢弃（DroppedEvents +1）。任何拒绝都不改变状态。
func (c *Counter) Add(timestamp int64, key string, count int64) error {
	// 参数校验在锁外即可（不触碰状态），但日志统一在持锁后打印，
	// 保证日志顺序与状态变更顺序一致。
	if key == "" {
		c.rejectLog("ADD timestamp=%d key=\"\" count=%d", ErrEmptyKey, timestamp, count)
		return ErrEmptyKey
	}
	if count <= 0 {
		c.rejectLog("ADD timestamp=%d key=%q count=%d", ErrInvalidCount, timestamp, key, count)
		return ErrInvalidCount
	}
	// 归属窗口的起点落在 [t-size+1, t]、终点最大可达 t+size（事件恰在
	// 某窗口起点时），故要求整个区间都能在 int64 内安全表示。
	if timestamp > math.MaxInt64-c.size || timestamp < math.MinInt64+c.size-1 {
		err := fmt.Errorf("%w: %d (window size %d)", ErrInvalidTimestamp, timestamp, c.size)
		c.rejectLog("ADD timestamp=%d key=%q count=%d", err, timestamp, key, count)
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// 归属窗口下标区间 [kMin, kMax]：k*step <= t < k*step+size。
	kMax := floorDiv(timestamp, c.step)
	kMin := ceilDiv(timestamp-c.size+1, c.step)

	// 收集仍打开的归属窗口起点，并统计其中尚未缓存的新窗口数。
	openStarts := make([]int64, 0, kMax-kMin+1)
	newWindows := 0
	for k := kMin; k <= kMax; k++ {
		start := k * c.step
		end := start + c.size
		if end <= c.watermark {
			continue // 窗口已关闭
		}
		openStarts = append(openStarts, start)
		if _, exists := c.open[start]; !exists {
			newWindows++
		}
	}

	if len(openStarts) == 0 {
		// 全部归属窗口已关闭：整条丢弃。
		c.dropped++
		c.logf("ADD timestamp=%d key=%q count=%d -> DROPPED: all %d owning window(s) closed at watermark=%d",
			timestamp, key, count, kMax-kMin+1, c.watermark)
		return nil
	}

	if len(c.open)+newWindows > c.max {
		// 容量超限：拒绝，且尚未做任何写入。
		err := fmt.Errorf("%w: need %d, limit %d (currently %d open)",
			ErrTooManyOpenWindows, len(c.open)+newWindows, c.max, len(c.open))
		c.logf("ADD timestamp=%d key=%q count=%d -> REJECTED: %v",
			timestamp, key, count, err)
		return err
	}

	// 校验与预算都通过，正式落状态。
	for _, start := range openStarts {
		counts := c.open[start]
		if counts == nil {
			counts = make(map[string]int64)
			c.open[start] = counts
		}
		counts[key] += count
	}
	c.logf("ADD timestamp=%d key=%q count=%d -> ACCEPTED into %d open window(s) %s (%d new, %d open total)",
		timestamp, key, count, len(openStarts), formatWindowRange(openStarts, c.size),
		newWindows, len(c.open))
	return nil
}

// Advance 把时钟推进到 watermark，关闭所有终点 <= watermark 的窗口，
// 并按（窗口终点, 键）有序返回每个窗口每键一次的计数结果。
// 时钟只进不退：传入早于当前水位的值会被拒绝且不改变状态。
// 推进到当前水位是空操作，返回空切片。
func (c *Counter) Advance(watermark int64) ([]WindowCount, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if watermark < c.watermark {
		err := fmt.Errorf("%w: %d < current watermark %d",
			ErrClockRewind, watermark, c.watermark)
		c.logf("ADVANCE watermark=%d -> REJECTED: %v", watermark, err)
		return nil, err
	}
	if watermark == c.watermark {
		c.logf("ADVANCE watermark=%d -> NOOP: clock unchanged, 0 window(s) closed", watermark)
		return []WindowCount{}, nil
	}

	old := c.watermark
	c.watermark = watermark

	var out []WindowCount
	closedWindows := 0
	for start, counts := range c.open {
		end := start + c.size
		if end > watermark {
			continue
		}
		closedWindows++
		for key, n := range counts {
			out = append(out, WindowCount{
				WindowStart: start,
				WindowEnd:   end,
				Key:         key,
				Count:       n,
			})
		}
		delete(c.open, start)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].WindowEnd != out[j].WindowEnd {
			return out[i].WindowEnd < out[j].WindowEnd
		}
		return out[i].Key < out[j].Key
	})

	c.emittedWindows += int64(closedWindows)
	c.emittedRecords += int64(len(out))
	c.logf("ADVANCE watermark=%d (was %d) -> closed %d window(s), emitted %d record(s) %s",
		watermark, old, closedWindows, len(out), formatEmitted(out))
	return out, nil
}

// Snapshot 返回当前状态的一致快照；可与写入并发调用，字段彼此一致。
func (c *Counter) Snapshot() Stats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var buffered int64
	for _, counts := range c.open {
		for _, n := range counts {
			buffered += n
		}
	}
	return Stats{
		Watermark:      c.watermark,
		OpenWindows:    len(c.open),
		BufferedEvents: buffered,
		DroppedEvents:  c.dropped,
		EmittedWindows: c.emittedWindows,
		EmittedRecords: c.emittedRecords,
	}
}

func (c *Counter) logf(format string, args ...any) {
	if c.log != nil {
		c.log.Printf(format, args...)
	}
}

// rejectLog 用于不持锁的拒绝路径；当前没有任何状态需要保护，直接打印。
func (c *Counter) rejectLog(format string, err error, args ...any) {
	if c.log == nil {
		return
	}
	c.log.Printf(format+" -> REJECTED: %v", append(args, any(err))...)
}

// floorDiv 返回 ⌊a/b⌋，要求 b > 0。Go 原生整除向零取整，负余数时需修正。
func floorDiv(a, b int64) int64 {
	q := a / b
	if r := a % b; r != 0 && r < 0 {
		q--
	}
	return q
}

// ceilDiv 返回 ⌈a/b⌉，要求 b > 0。
func ceilDiv(a, b int64) int64 {
	q := a / b
	if r := a % b; r != 0 && r > 0 {
		q++
	}
	return q
}

func formatWindowRange(starts []int64, size int64) string {
	s := "["
	for i, st := range starts {
		if i > 0 {
			s += ", "
		}
		s += fmt.Sprintf("[%d,%d)", st, st+size)
	}
	return s + "]"
}

func formatEmitted(out []WindowCount) string {
	if len(out) == 0 {
		return "[]"
	}
	s := "["
	for i, r := range out {
		if i > 0 {
			s += ", "
		}
		s += fmt.Sprintf("window=[%d,%d) key=%q count=%d",
			r.WindowStart, r.WindowEnd, r.Key, r.Count)
	}
	return s + "]"
}
