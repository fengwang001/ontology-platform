// Package window 提供带水位线与迟到容限的翻滚窗口计数器。
//
// 语义概要（详细规则见 README.md）：
//   - 窗口按固定大小 Size 划分，左闭右开 [start, start+Size)，事件时间可正可负；
//   - 水位线 = 见过的最大事件时间 - Delay，只单调前进；
//   - 窗口在 水位线 >= 窗口结束时间 时触发并输出计数；
//   - 触发后的窗口在 水位线 <= 窗口结束时间+AllowedLateness 期间仍接受迟到事件并修正输出，
//     超过容限后窗口被清除，之后落入该窗口的事件被丢弃并计数。
package window

import (
	"errors"
	"sync"
)

// 可区分的拒绝原因。调用方可用 errors.Is 判定。
var (
	// ErrNonPositiveSize 窗口大小非正。
	ErrNonPositiveSize = errors.New("window: size must be positive")
	// ErrNegativeDelay 水位线延迟为负。
	ErrNegativeDelay = errors.New("window: delay must not be negative")
	// ErrNegativeAllowedLateness 迟到容限为负。
	ErrNegativeAllowedLateness = errors.New("window: allowed lateness must not be negative")
	// ErrInvalidMaxOpenWindows 未结算窗口数上限为负。
	ErrInvalidMaxOpenWindows = errors.New("window: max open windows must not be negative")
	// ErrEmptyKey 输入事件键为空。
	ErrEmptyKey = errors.New("window: event key must not be empty")
	// ErrTooManyOpenWindows 同时保留的未结算窗口数超限。
	ErrTooManyOpenWindows = errors.New("window: too many open windows")
)

// Event 是一条带事件时间的输入。
type Event struct {
	Key       string // 分组键，不允许为空
	Timestamp int64  // 事件时间，可正可负
}

// Config 是计数器配置。
type Config struct {
	Size            int64  // 窗口大小，必须为正
	Delay           int64  // 水位线相对最大事件时间的滞后，必须非负
	AllowedLateness int64  // 窗口触发后仍接受迟到事件的容限，必须非负
	MaxOpenWindows  int    // 同时保留的未结算窗口数上限，0 表示不限制
	Logger          Logger // 判定日志，nil 表示不输出
}

// Logger 接收判定日志（输入、输出与判定依据）。
type Logger interface {
	Logf(format string, args ...any)
}

// EmissionKind 区分一次输出的类型。
type EmissionKind int

const (
	// EmissionFired 窗口到期首次触发输出。
	EmissionFired EmissionKind = iota
	// EmissionCorrected 迟到事件被接受后对已输出值的修正。
	EmissionCorrected
)

func (k EmissionKind) String() string {
	switch k {
	case EmissionFired:
		return "fired"
	case EmissionCorrected:
		return "corrected"
	default:
		return "unknown"
	}
}

// WindowResult 是某个键在某个窗口上的当前计数结果。
type WindowResult struct {
	Key   string
	Start int64 // 窗口起点（含）
	End   int64 // 窗口终点（不含）
	Count int64
}

// Emission 是一次输出（触发或修正），Seq 为全局单调序号。
type Emission struct {
	Seq    int64
	Kind   EmissionKind
	Result WindowResult
}

// State 是计数器在某一时刻的完整只读快照。
type State struct {
	WatermarkSet bool           // 是否已见过事件（水位线是否已初始化）
	Watermark    int64          // 当前水位线
	Dropped      int64          // 因超过迟到容限被丢弃的事件总数
	Results      []WindowResult // 各键各窗口的最新结果，按 (Key, Start) 排序
	Emissions    []Emission     // 全部输出历史，按 Seq 排序
}

// Counter 是翻滚窗口计数器，可并发使用。
type Counter struct {
	mu sync.RWMutex

	cfg Config

	// 以下字段均在 mu 保护下访问。
	maxTS     int64
	hasMax    bool
	wm        int64
	wmSet     bool
	open      map[string]map[int64]int64        // 未清除窗口：key -> 起点 -> 计数
	fired     map[string]map[int64]int64        // 已触发窗口（open 的子集）：key -> 起点 -> 计数
	openN     int                               // 未结算窗口总数（跨所有键）
	results   map[string]map[int64]WindowResult // 已触发窗口的最新结果
	emissions []Emission
	dropped   int64
	seq       int64
}

// New 校验配置并创建计数器；配置非法时整体拒绝并返回可区分的原因。
func New(cfg Config) (*Counter, error) {
	if cfg.Size <= 0 {
		return nil, ErrNonPositiveSize
	}
	if cfg.Delay < 0 {
		return nil, ErrNegativeDelay
	}
	if cfg.AllowedLateness < 0 {
		return nil, ErrNegativeAllowedLateness
	}
	if cfg.MaxOpenWindows < 0 {
		return nil, ErrInvalidMaxOpenWindows
	}
	return &Counter{
		cfg:     cfg,
		open:    make(map[string]map[int64]int64),
		fired:   make(map[string]map[int64]int64),
		results: make(map[string]map[int64]WindowResult),
	}, nil
}

// cloneEngineLocked 在锁内构造当前状态的独立工作副本。
func (c *Counter) cloneEngineLocked() *engine {
	e := &engine{
		cfg:     c.cfg,
		hasMax:  c.hasMax,
		maxTS:   c.maxTS,
		wm:      c.wm,
		wmSet:   c.wmSet,
		open:    make(map[string]map[int64]int64, len(c.open)),
		fired:   make(map[string]map[int64]int64, len(c.fired)),
		openN:   c.openN,
		results: make(map[string]map[int64]WindowResult, len(c.results)),
		dropped: c.dropped,
		seq:     c.seq,
	}
	for k, m := range c.open {
		cp := make(map[int64]int64, len(m))
		for s, n := range m {
			cp[s] = n
		}
		e.open[k] = cp
	}
	for k, m := range c.fired {
		cp := make(map[int64]int64, len(m))
		for s, n := range m {
			cp[s] = n
		}
		e.fired[k] = cp
	}
	for k, m := range c.results {
		cp := make(map[int64]WindowResult, len(m))
		for s, r := range m {
			cp[s] = r
		}
		e.results[k] = cp
	}
	e.history = append([]Emission(nil), c.emissions...)
	return e
}

// commitLocked 仅在整批成功后，用工作副本覆盖计数器状态。
func (c *Counter) commitLocked(e *engine) {
	c.maxTS = e.maxTS
	c.hasMax = e.hasMax
	c.wm = e.wm
	c.wmSet = e.wmSet
	c.open = e.open
	c.fired = e.fired
	c.openN = e.openN
	c.results = e.results
	c.emissions = e.history
	c.dropped = e.dropped
	c.seq = e.seq
}

// Process 原子地处理一批事件：要么全部接受，要么整体拒绝（返回非 nil error），
// 被拒绝的批次不改变水位线、丢弃数与已产生的输出。
//
// 实现方式是先在状态的深拷贝上运行整批，只有全部成功才一次性提交；
// 因此处理期间持有的写锁也保证多批事件的串行化与可复现顺序。
// 返回本批新产生的输出（触发与修正，按 Seq 排序）的独立拷贝。
func (c *Counter) Process(events []Event) ([]Emission, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	e := c.cloneEngineLocked()
	out, err := runEngine(e, events)
	if err != nil {
		return nil, err // 批次作废：状态与日志缓冲一并丢弃
	}
	c.commitLocked(e)
	e.flushLog()
	return append([]Emission(nil), out...), nil
}

// Snapshot 返回当前完整状态的深拷贝，可与 Process 并发调用；
// 多次并发只读得到的结果逐字段一致，返回后不受后续处理影响。
func (c *Counter) Snapshot() State {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return buildState(c)
}
