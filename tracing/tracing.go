// Package tracing 提供分布式追踪的跨度组装器：把乱序到达的跨度组装成
// 调用树，校正跨服务时钟偏移，并在追踪完结后输出关键路径。
package tracing

import (
	"fmt"
	"sort"
	"sync"
)

// RejectReason 区分跨度被拒绝的原因。
type RejectReason string

const (
	// ReasonInvalidInterval 结束时刻早于开始时刻。
	ReasonInvalidInterval RejectReason = "invalid_interval"
	// ReasonSelfParent 跨度以自己为父。
	ReasonSelfParent RejectReason = "self_parent"
	// ReasonDuplicateRoot 同一追踪出现第二个根跨度。
	ReasonDuplicateRoot RejectReason = "duplicate_root"
	// ReasonConflictingDuplicate 同一跨度号出现内容不同的重复。
	ReasonConflictingDuplicate RejectReason = "conflicting_duplicate"
	// ReasonTraceFinalized 追踪已完结，迟到的跨度被拒绝并计数。
	ReasonTraceFinalized RejectReason = "trace_finalized"
	// ReasonCapacityExceeded 接受该跨度会使未完结追踪的跨度总数超过上限。
	ReasonCapacityExceeded RejectReason = "capacity_exceeded"
)

// RejectError 描述一次跨度拒绝，Reason 可用于区分原因。
type RejectError struct {
	Reason  RejectReason
	TraceID string
	SpanID  string
}

func (e *RejectError) Error() string {
	return fmt.Sprintf("span %s/%s rejected: %s", e.TraceID, e.SpanID, e.Reason)
}

// Span 是一个分布式追踪跨度。根跨度的 ParentID 为空。
type Span struct {
	TraceID  string
	SpanID   string
	ParentID string
	Service  string
	Start    int64
	End      int64
}

// AcceptedSpan 是完结输出中的跨度，Offset 为时钟偏移校正产生的平移量。
type AcceptedSpan struct {
	Span
	Offset int64
}

// AdjustedStart 返回校正后的开始时刻。
func (a AcceptedSpan) AdjustedStart() int64 { return a.Start + a.Offset }

// AdjustedEnd 返回校正后的结束时刻。
func (a AcceptedSpan) AdjustedEnd() int64 { return a.End + a.Offset }

// TraceResult 是追踪完结时的输出，与跨度到达顺序无关。
type TraceResult struct {
	TraceID string
	// Spans 按跨度号排序的已接受跨度（含平移量）。
	Spans []AcceptedSpan
	// Orphans 完结 时仍无父或父子成环的跨度号，升序。
	Orphans []string
	// CriticalPath 从根起每层选结束最晚的子跨度（并列取跨度号最小者）。
	CriticalPath []string
}

// Assembler 组装乱序到达的跨度。AddSpan 与 AdvanceClock 可并发调用。
type Assembler struct {
	mu        sync.Mutex
	silence   int64
	maxOpen   int
	traces    map[string]*traceState
	finalized map[string]struct{}
	now       int64
	late      int
}

type traceState struct {
	spans      map[string]Span
	rootSeen   bool
	lastActive int64
}

// NewAssembler 创建组装器。silence 为静默满时长 T（注入时钟），
// maxOpenSpans 为未完结追踪暂存跨度总数上限（<=0 表示不限制）。
func NewAssembler(silence int64, maxOpenSpans int) *Assembler {
	return &Assembler{
		silence:   silence,
		maxOpen:   maxOpenSpans,
		traces:    make(map[string]*traceState),
		finalized: make(map[string]struct{}),
	}
}

// AddSpan 投递一个跨度。内容相同的重复为幂等（返回 nil），
// 被拒绝的跨度返回 *RejectError 且不改变任何状态。
func (a *Assembler) AddSpan(s Span) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	reject := func(reason RejectReason) error {
		return &RejectError{Reason: reason, TraceID: s.TraceID, SpanID: s.SpanID}
	}

	if s.End < s.Start {
		return reject(ReasonInvalidInterval)
	}
	if s.ParentID == s.SpanID {
		return reject(ReasonSelfParent)
	}
	if _, ok := a.traces[s.TraceID]; !ok {
		if a.rootSeen(s.TraceID) {
			a.late++
			return reject(ReasonTraceFinalized)
		}
	}

	tr, ok := a.traces[s.TraceID]
	if !ok {
		tr = &traceState{spans: make(map[string]Span)}
	}
	if prev, dup := tr.spans[s.SpanID]; dup {
		if prev == s {
			return nil // 内容相同的重复：幂等
		}
		return reject(ReasonConflictingDuplicate)
	}
	if s.ParentID == "" && tr.rootSeen {
		return reject(ReasonDuplicateRoot)
	}
	if a.maxOpen > 0 && a.openSpanCount()+1 > a.maxOpen {
		return reject(ReasonCapacityExceeded)
	}

	tr.spans[s.SpanID] = s
	if s.ParentID == "" {
		tr.rootSeen = true
	}
	if s.End > tr.lastActive {
		tr.lastActive = s.End
	}
	a.traces[s.TraceID] = tr
	return nil
}

// AdvanceClock 推进注入时钟到 now（只允许前进），完结静默满 T 的追踪。
func (a *Assembler) AdvanceClock(now int64) []TraceResult {
	a.mu.Lock()
	defer a.mu.Unlock()

	if now <= a.now {
		return nil
	}
	a.now = now

	var results []TraceResult
	for id, tr := range a.traces {
		if !tr.rootSeen || now-tr.lastActive < a.silence {
			continue
		}
		results = append(results, finalize(id, tr.spans))
		delete(a.traces, id)
		a.finalized[id] = struct{}{}
	}
	sort.Slice(results, func(i, j int) bool { return results[i].TraceID < results[j].TraceID })
	return results
}

// LateCount 返回完结后到达而被拒绝的跨度数。
func (a *Assembler) LateCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.late
}

// AcceptedCount 返回当前已接受且仍暂存的跨度总数。
func (a *Assembler) AcceptedCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.openSpanCount()
}

// rootSeen 报告该追踪的根是否曾出现（含已完结的追踪）。
func (a *Assembler) rootSeen(traceID string) bool {
	if tr, ok := a.traces[traceID]; ok && tr.rootSeen {
		return true
	}
	_, done := a.finalized[traceID]
	return done
}

// openSpanCount 返回未完结追踪暂存的跨度总数。
func (a *Assembler) openSpanCount() int {
	total := 0
	for _, tr := range a.traces {
		total += len(tr.spans)
	}
	return total
}
