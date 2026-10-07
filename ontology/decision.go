package ontology

import (
	"sort"
	"sync"
)

// Verdict 是单次冲突判定的最终裁定结果。
type Verdict int

const (
	VerdictCommitted Verdict = iota + 1
	VerdictStaleBaseline
	VerdictDeleted
	VerdictPropertyConflict
	VerdictValidation
)

func (v Verdict) String() string {
	switch v {
	case VerdictCommitted:
		return "committed"
	case VerdictStaleBaseline:
		return "stale-baseline"
	case VerdictDeleted:
		return "deleted"
	case VerdictPropertyConflict:
		return "property-conflict"
	case VerdictValidation:
		return "validation"
	default:
		return "unknown"
	}
}

// DecisionRecord 完整记录一次判定过程涉及的输入与裁定依据，
// 可供重放核验。记录中的集合均已排序，保证字节级可比较。
type DecisionRecord struct {
	// ID 是全局单调递增的判定序号，给出判定的串行顺序。
	ID       uint64
	ObjectID string
	// Baseline 是调用方声明的基线版本。
	Baseline uint64
	// CurrentVersion 是提交时刻实例的最新已提交版本。
	CurrentVersion uint64
	// WriteSet 是本次写入的显式写集合（排序后）。
	WriteSet []string
	// WriteValues 是本次写入的属性值副本，使记录可以独立重放。
	WriteValues map[string]string
	// ReadSet 是本次写入的相关读集合中本实例属性部分（排序后）。
	ReadSet []string
	// RemoteReadSet 是相关读集合中跨实例部分（排序后）。
	RemoteReadSet []RemoteRead
	// LastFootprint 是判定所依据的最近一次已提交写入的
	// （写集合 ∪ 相关读集合）之本实例属性部分。
	LastFootprint []string
	// PendingRemote 是判定时实例上挂起的跨实例失效集合。
	PendingRemote []RemoteRead
	// FootprintsConsulted 是本次判定查阅的历史足迹数量，恒为 1。
	// 该字段是"判定开销不随历史版本总数增长"的可验证证据：
	// 它由判定路径本身写入日志，不依赖额外对外暴露的状态。
	FootprintsConsulted int
	// HistoryLen 是判定时该实例已提交的版本总数，用于对照
	// FootprintsConsulted 验证开销与历史长度无关。
	HistoryLen int
	Verdict    Verdict
	// Reason 是裁定依据的人类可读说明（如交集属性）。
	Reason string
	// NewVersion 仅在 VerdictCommitted 时有效，为提交产生的新版本号。
	NewVersion uint64
}

// DecisionLog 是追加只读的判定日志，记录全局判定顺序。
type DecisionLog struct {
	mu      sync.Mutex
	nextID  uint64
	records []DecisionRecord
}

func newDecisionLog() *DecisionLog { return &DecisionLog{} }

// append 追加一条记录并返回其全局序号。
func (l *DecisionLog) append(rec DecisionRecord) uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.nextID++
	rec.ID = l.nextID
	l.records = append(l.records, rec)
	return rec.ID
}

// Records 返回全部判定记录的副本，按全局判定顺序排列。
func (l *DecisionLog) Records() []DecisionRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]DecisionRecord, len(l.records))
	copy(out, l.records)
	return out
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedRemoteReads(m map[RemoteRead]struct{}) []RemoteRead {
	out := make([]RemoteRead, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TargetType != out[j].TargetType {
			return out[i].TargetType < out[j].TargetType
		}
		if out[i].Prop != out[j].Prop {
			return out[i].Prop < out[j].Prop
		}
		return out[i].Link < out[j].Link
	})
	return out
}
