// Package tracing 实现分布式追踪的跨度组装器：把乱序到达的跨度组装成调用树，
// 校正跨服务时钟偏移，并在追踪完结后输出关键路径。
package tracing

import (
	"sort"
	"sync"
)

// Span 描述一个追踪跨度。根跨度的 ParentID 为空字符串。
type Span struct {
	TraceID  string
	SpanID   string
	ParentID string
	Service  string
	Start    int64
	End      int64
}

// Clock 为注入时钟，返回单调递增的时间戳（与 Span.Start/End 同单位）。
type Clock interface {
	Now() int64
}

// RejectReason 区分跨度被拒绝的原因。
type RejectReason int

const (
	RejectNone RejectReason = iota
	// RejectEndBeforeStart 结束时刻早于开始时刻。
	RejectEndBeforeStart
	// RejectSelfParent 跨度以自己为父。
	RejectSelfParent
	// RejectDuplicateRoot 同一追踪出现第二个根。
	RejectDuplicateRoot
	// RejectConflictingDuplicate 同一跨度号出现内容不同的重复。
	RejectConflictingDuplicate
	// RejectTraceCompleted 追踪已完结后迟到的跨度。
	RejectTraceCompleted
	// RejectBufferFull 接受后会使未完结追踪的跨度总数超过上限。
	RejectBufferFull
)

func (r RejectReason) String() string {
	switch r {
	case RejectNone:
		return "none"
	case RejectEndBeforeStart:
		return "end-before-start"
	case RejectSelfParent:
		return "self-parent"
	case RejectDuplicateRoot:
		return "duplicate-root"
	case RejectConflictingDuplicate:
		return "conflicting-duplicate"
	case RejectTraceCompleted:
		return "trace-completed"
	case RejectBufferFull:
		return "buffer-full"
	}
	return "unknown"
}

// SubmitResult 为跨度投递结果。
type SubmitResult struct {
	Accepted  bool
	Duplicate bool // 内容相同的幂等重复，不改变状态
	Reason    RejectReason
}

// PlacedSpan 为完结输出中的跨度及其累计平移量。
type PlacedSpan struct {
	Span  Span
	Shift int64
}

// TraceResult 为追踪完结后的输出。
type TraceResult struct {
	TraceID      string
	Spans        []PlacedSpan // 树中跨度，按跨度号排序
	Orphans      []Span       // 无父或父子成环的跨度，按跨度号排序
	CriticalPath []string     // 从根起每层结束最晚（并列取跨度号最小）的跨度号
}

// Stats 为组装器运行统计。
type Stats struct {
	AcceptedTotal   int // 历史接受的跨度总数（不含幂等重复与被拒绝者）
	CompletedSpans  int // 已完结追踪释放的跨度总数（树中 + 孤儿）
	BufferedSpans   int // 未完结追踪暂存的跨度总数
	LateRejected    int // 完结后到达而被拒绝的跨度数
	RejectedTotal   int // 被拒绝的跨度总数（含完结后到达）
	OpenTraces      int // 未完结追踪数
	CompletedTraces int // 已完结追踪数
}

// Config 为组装器配置。
type Config struct {
	SilenceTimeout   int64
	MaxBufferedSpans int // <=0 表示不限
	Clock            Clock
	Logf             func(format string, args ...any)
}

type traceState struct {
	spans        map[string]Span
	hasRoot      bool
	lastActivity int64
}

// Assembler 为跨度组装器，Submit 与 Poll 可被并发调用。
type Assembler struct {
	mu          sync.Mutex
	silence     int64
	maxBuffered int
	clock       Clock
	logf        func(format string, args ...any)

	traces    map[string]*traceState
	completed map[string]bool

	acceptedTotal  int
	completedSpans int
	lateRejected   int
	rejectedTotal  int
}

// NewAssembler 创建组装器。
func NewAssembler(cfg Config) *Assembler {
	return &Assembler{
		silence:     cfg.SilenceTimeout,
		maxBuffered: cfg.MaxBufferedSpans,
		clock:       cfg.Clock,
		logf:        cfg.Logf,
		traces:      make(map[string]*traceState),
		completed:   make(map[string]bool),
	}
}

func (a *Assembler) log(format string, args ...any) {
	if a.logf != nil {
		a.logf(format, args...)
	}
}

// Submit 投递一个跨度。被拒绝的跨度不改变任何状态。
func (a *Assembler) Submit(s Span) SubmitResult {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := a.clock.Now()
	a.finalizeExpired(now)

	a.log("submit trace=%s span=%s parent=%q svc=%s [%d,%d] now=%d",
		s.TraceID, s.SpanID, s.ParentID, s.Service, s.Start, s.End, now)

	reject := func(r RejectReason) SubmitResult {
		a.rejectedTotal++
		if r == RejectTraceCompleted {
			a.lateRejected++
		}
		a.log("reject trace=%s span=%s reason=%s", s.TraceID, s.SpanID, r)
		return SubmitResult{Reason: r}
	}

	if s.End < s.Start {
		return reject(RejectEndBeforeStart)
	}
	if s.ParentID != "" && s.ParentID == s.SpanID {
		return reject(RejectSelfParent)
	}
	if a.completed[s.TraceID] {
		return reject(RejectTraceCompleted)
	}

	ts := a.traces[s.TraceID]
	if ts != nil {
		if prev, ok := ts.spans[s.SpanID]; ok {
			if prev == s {
				a.log("idempotent duplicate trace=%s span=%s", s.TraceID, s.SpanID)
				return SubmitResult{Accepted: true, Duplicate: true}
			}
			return reject(RejectConflictingDuplicate)
		}
	}
	if s.ParentID == "" && ts != nil && ts.hasRoot {
		return reject(RejectDuplicateRoot)
	}
	if a.maxBuffered > 0 && a.bufferedLocked()+1 > a.maxBuffered {
		return reject(RejectBufferFull)
	}

	if ts == nil {
		ts = &traceState{spans: make(map[string]Span)}
		a.traces[s.TraceID] = ts
	}
	ts.spans[s.SpanID] = s
	if s.ParentID == "" {
		ts.hasRoot = true
	}
	ts.lastActivity = now
	a.acceptedTotal++
	a.log("accept trace=%s span=%s buffered=%d", s.TraceID, s.SpanID, a.bufferedLocked())
	return SubmitResult{Accepted: true}
}

// Poll 检查所有未完结追踪，完结并释放静默超时的追踪，返回完结结果。
func (a *Assembler) Poll() []TraceResult {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.finalizeExpired(a.clock.Now())
}

// Stats 返回当前统计。恒有 AcceptedTotal == CompletedSpans + BufferedSpans。
func (a *Assembler) Stats() Stats {
	a.mu.Lock()
	defer a.mu.Unlock()
	return Stats{
		AcceptedTotal:   a.acceptedTotal,
		CompletedSpans:  a.completedSpans,
		BufferedSpans:   a.bufferedLocked(),
		LateRejected:    a.lateRejected,
		RejectedTotal:   a.rejectedTotal,
		OpenTraces:      len(a.traces),
		CompletedTraces: len(a.completed),
	}
}

// bufferedLocked 返回未完结追踪暂存的跨度总数，调用方须持锁。
func (a *Assembler) bufferedLocked() int {
	n := 0
	for _, ts := range a.traces {
		n += len(ts.spans)
	}
	return n
}

// finalizeExpired 完结并释放静默超时的追踪，调用方须持锁。
func (a *Assembler) finalizeExpired(now int64) []TraceResult {
	var results []TraceResult
	for id, ts := range a.traces {
		if !ts.hasRoot || now-ts.lastActivity < a.silence {
			continue
		}
		res := a.finalize(id, ts)
		results = append(results, res)
		delete(a.traces, id)
		a.completed[id] = true
		a.completedSpans += len(ts.spans)
	}
	sort.Slice(results, func(i, j int) bool { return results[i].TraceID < results[j].TraceID })
	return results
}

// sortSpans 按跨度号排序，保证输出与到达顺序无关。
func sortSpans(spans []Span) {
	sort.Slice(spans, func(i, j int) bool { return spans[i].SpanID < spans[j].SpanID })
}
