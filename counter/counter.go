package counter

import (
	"fmt"
	"log"
	"math"
	"sort"
	"sync"
)

// Counter 是带水位线的累积窗口计数器，可被多个 goroutine 并发使用。
// 所有变更路径都在 mu 写锁内完成，快照读取使用读锁，保证逐字段一致。
type Counter struct {
	cfg Config
	log *log.Logger

	mu        sync.RWMutex
	watermark int64
	wmSet     bool // 水位线是否已被至少一条被接收事件设置
	dropped   int64
	outputs   []Result
	// windows 按 key 再按大窗口起点组织“被保留”的大窗口。
	// 窗口一经创建便保留（即使其子窗口已全部触发），因此保留数量受
	// MaxWindows 约束；全部触发后仅保留元信息并释放计数桶内存。
	windows map[string]map[int64]*windowState
}

// windowState 是单个 (key, 大窗口) 的累积状态。
type windowState struct {
	start int64 // 大窗口起点
	// counts[i] 为落在 [start+i*step, start+(i+1)*step) 内的原始事件数。
	// 全部子窗口触发后置为 nil 以释放内存（迟到事件会在丢弃判定处被拦，
	// 不会再改动已完成窗口）。
	counts []int64
	// emitted 为已经触发输出的子窗口终点数（0..子窗口总数）。
	emitted int
	// cumulative 是已触发部分的持续累计和，必须跨多次 Add 调用保持：
	// 每次触发新终点时把对应桶计数累加进来。
	cumulative int64
}

// New 按 cfg 构造计数器；参数非法时返回带 ReasonInvalidConfig 的 *RejectError。
func New(cfg Config, opts ...Option) (*Counter, error) {
	switch {
	case cfg.WindowSize <= 0:
		return nil, &RejectError{ReasonInvalidConfig,
			fmt.Sprintf("window size must be positive, got %d", cfg.WindowSize)}
	case cfg.Step <= 0:
		return nil, &RejectError{ReasonInvalidConfig,
			fmt.Sprintf("step must be positive, got %d", cfg.Step)}
	case cfg.WindowSize%cfg.Step != 0:
		return nil, &RejectError{ReasonInvalidConfig,
			fmt.Sprintf("step %d must divide window size %d", cfg.Step, cfg.WindowSize)}
	case cfg.MaxWindows <= 0:
		return nil, &RejectError{ReasonInvalidConfig,
			fmt.Sprintf("max windows must be positive, got %d", cfg.MaxWindows)}
	}

	o := options{logger: log.Default().Writer()}
	for _, opt := range opts {
		opt(&o)
	}
	prefix := "counter "
	return &Counter{
		cfg:     cfg,
		log:     log.New(o.logger, prefix, log.LstdFlags|log.Lmicroseconds),
		windows: make(map[string]map[int64]*windowState),
	}, nil
}

// disposition 描述一条事件在锁内的最终去向。
type disposition int

const (
	dispAccepted disposition = iota // 被接收并累积
	dispDropped                     // 迟到丢弃
)

// Add 接收一条事件：校验输入、丢弃迟到事件或把事件累积进对应大窗口，
// 然后单调推进水位线并触发所有到期子窗口。返回本次新触发的结果；
// 输入被拒绝（空键 / 保留窗口数超限）时返回带对应原因码的错误，
// 且不改变水位线、丢弃数或已输出结果。
func (c *Counter) Add(e Event) ([]Result, error) {
	out, _, err := c.addClassified(e)
	return out, err
}

// addClassified 与 Add 相同，但额外返回事件去向，供包内测试精确区分
// “被接收”和“被丢弃”（丢弃不返回错误，无法靠 error 区分）。
func (c *Counter) addClassified(e Event) ([]Result, disposition, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.addLocked(e)
}

// addLocked 必须在持有 c.mu 写锁时调用，返回新触发结果、事件去向与拒绝错误。
func (c *Counter) addLocked(e Event) ([]Result, disposition, error) {
	// 1) 空键拒绝：发生在一切状态变更之前。
	if e.Key == "" {
		err := &RejectError{ReasonEmptyKey,
			fmt.Sprintf("event at ts=%d rejected: key is empty", e.Timestamp)}
		c.logf("input  %v => REJECT %s", e, err.Error())
		return nil, dispAccepted, err
	}

	start := windowStart(e.Timestamp, c.cfg.WindowSize)
	firstEnd := firstSubEnd(e.Timestamp, start, c.cfg.Step)

	// 2) 丢弃判定：水位线已设置，且事件的最小子窗口终点不超过水位线。
	//    必须先于“创建/计入窗口”，迟到事件既不计数也不推进水位线。
	//    尚无被接收事件（水位线未设置）时不做丢弃，首个事件即使时间戳
	//    为负也必须被接收——否则零值水位线 0 会误杀所有负时间戳首事件。
	if c.wmSet && firstEnd <= c.watermark {
		c.dropped++
		c.logf("input  %v => DROP window_start=%d first_sub_end=%d <= watermark=%d (dropped=%d)",
			e, start, firstEnd, c.watermark, c.dropped)
		return nil, dispDropped, nil
	}

	byStart := c.windows[e.Key]
	_, exists := byStart[start]

	// 3) 大窗口保留数上限：仅当需要新建大窗口时计数，拒绝不产生任何变更。
	if !exists && len(byStart) >= c.cfg.MaxWindows {
		err := &RejectError{ReasonTooManyWindows, fmt.Sprintf(
			"event %v rejected: key %q already retains %d windows (limit %d)",
			e, e.Key, len(byStart), c.cfg.MaxWindows)}
		c.logf("input  %v => REJECT %s", e, err.Error())
		return nil, dispAccepted, err
	}

	// 4) 接收：水位线随最大事件时间单调前进（只增不减）。
	if !c.wmSet || e.Timestamp > c.watermark {
		c.watermark = e.Timestamp
		c.wmSet = true
	}

	// 5) 事件计入最小终点及其之后的所有子窗口：实现为在所属步长桶上 +1，
	//    触发时对桶做前缀和，即得到从大窗口起点到该终点的累计计数。
	if byStart == nil {
		byStart = make(map[int64]*windowState)
		c.windows[e.Key] = byStart
	}
	ws := byStart[start]
	if ws == nil {
		ws = &windowState{
			start:  start,
			counts: make([]int64, c.cfg.WindowSize/c.cfg.Step),
		}
		byStart[start] = ws
	}
	bucket := (e.Timestamp - start) / c.cfg.Step // offset 非负，可直接整除
	ws.counts[bucket]++
	c.logf("input  %v => ACCEPT window_start=%d bucket=%d watermark=%d",
		e, start, bucket, c.watermark)

	// 6) 水位线推进后触发所有到期子窗口（按 key、大窗口起点、终点确定序）。
	emitted := c.advanceAndEmit()
	return emitted, dispAccepted, nil
}

// advanceAndEmit 必须在持有 c.mu 写锁时调用：触发所有终点不超过水位线的
// 子窗口，每个到期子窗口恰好输出一次（累计计数为零也输出）。
// 大窗口全部触发后保留空壳并继续计入“保留窗口数”（由 MaxWindows 限制）；
// 迟到事件会在丢弃判定处被拦住，不会重建或改动已完成窗口。
func (c *Counter) advanceAndEmit() []Result {
	var fresh []Result

	keys := make([]string, 0, len(c.windows))
	for k := range c.windows {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	totalBuckets := int(c.cfg.WindowSize / c.cfg.Step)

	for _, k := range keys {
		byStart := c.windows[k]
		starts := make([]int64, 0, len(byStart))
		for s := range byStart {
			starts = append(starts, s)
		}
		sort.Slice(starts, func(i, j int) bool { return starts[i] < starts[j] })

		for _, s := range starts {
			ws := byStart[s]
			for ws.emitted < totalBuckets {
				end := ws.start + int64(ws.emitted+1)*c.cfg.Step
				if end > c.watermark {
					break // 水位线尚未到达该终点
				}
				// 持续累计和跨 Add 调用保持：本次新触发桶的原始计数
				// 累加进此前已输出的累计值，得到“起点到当前终点”的累计。
				ws.cumulative += ws.counts[ws.emitted]
				r := Result{
					Key:         k,
					WindowStart: ws.start,
					End:         end,
					Count:       ws.cumulative,
				}
				ws.emitted++
				c.outputs = append(c.outputs, r)
				fresh = append(fresh, r)
				c.logf("output %v <= watermark=%d EMIT cumulative_count=%d",
					r, c.watermark, r.Count)
			}
			if ws.emitted == totalBuckets && ws.counts != nil {
				ws.counts = nil // 全部触发：释放计数桶内存，保留窗口壳计入上限
			}
		}
	}
	return fresh
}

// Snapshot 返回计数器当前状态的深拷贝快照，可在并发写入下安全读取，
// 多次读取之间字段逐字段一致（来自同一把读锁）。
// 尚无被接收事件时 Watermark 为 math.MinInt64、WatermarkSet 为 false。
func (c *Counter) Snapshot() State {
	c.mu.RLock()
	defer c.mu.RUnlock()
	wm := c.watermark
	if !c.wmSet {
		wm = math.MinInt64
	}
	return State{
		Watermark:    wm,
		WatermarkSet: c.wmSet,
		Dropped:      c.dropped,
		Outputs:      cloneResults(c.outputs),
	}
}

// Watermark / Dropped / Outputs 是 Snapshot 中单个字段的便捷读取，
// 同样在锁保护下完成；Watermark 在未设置时返回 math.MinInt64，
// Outputs 返回深拷贝。
func (c *Counter) Watermark() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.wmSet {
		return math.MinInt64
	}
	return c.watermark
}

func (c *Counter) Dropped() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.dropped
}

func (c *Counter) Outputs() []Result {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return cloneResults(c.outputs)
}

// logf 必须在持有 c.mu（读或写）时调用，使日志顺序与状态变更顺序一致。
func (c *Counter) logf(format string, args ...any) {
	c.log.Output(2, fmt.Sprintf(format, args...))
}

func cloneResults(in []Result) []Result {
	if len(in) == 0 {
		return []Result{}
	}
	out := make([]Result, len(in))
	copy(out, in)
	return out
}
