package hypcheck

import (
	"errors"
	"sort"
)

// EventKind 标识追加日志中的事件种类。
type EventKind string

const (
	evTypeDefined   EventKind = "type_defined"
	evHookPublished EventKind = "hook_published"
	evHookBound     EventKind = "hook_bound"
	evPrincipal     EventKind = "principal_lifecycle"
	evEdgeToggled   EventKind = "edge_toggled"
	evGrantToggled  EventKind = "grant_toggled"
	evStateWritten  EventKind = "state_written"
	evCompacted     EventKind = "compacted"
)

// Event 是全局追加日志中的一条不可变记录。
type Event struct {
	Seq  Seq       `json:"seq"`
	At   Timestamp `json:"at"`
	Kind EventKind `json:"kind"`

	TypeID    string       `json:"type_id,omitempty"`
	HookID    string       `json:"hook_id,omitempty"`
	Phase     Phase        `json:"phase,omitempty"`
	HV        *HookVersion `json:"hook_version,omitempty"`
	TV        *TypeVersion `json:"type_version,omitempty"`
	Principal string       `json:"principal,omitempty"`
	Exists    *bool        `json:"exists,omitempty"`
	Parent    string       `json:"parent,omitempty"`
	Child     string       `json:"child,omitempty"`
	Node      string       `json:"node,omitempty"`
	Granted   *bool        `json:"granted,omitempty"`
	Key       string       `json:"key,omitempty"`
	Value     *Value       `json:"value,omitempty"`
	Horizon   *Timestamp   `json:"horizon,omitempty"`
}

// BirthRecord 是日志压实（截断）后保留下来的“诞生索引”，
// 用于在历史缺口之前仍能区分 E1/E3 与 E2。
type BirthRecord struct {
	BornAt  Timestamp `json:"born_at"`
	Existed bool      `json:"existed"`
}

// Manifest 是压实时切点 horizon 处的不可变清册。
type Manifest struct {
	Horizon    Timestamp              `json:"horizon"`
	Types      map[string]BirthRecord `json:"types"`
	Principals map[string]BirthRecord `json:"principals"`
	// 切点世界基线：压实后重建 at>=horizon 快照所必需的最小保留数据。
	TypeValues map[string]BaselineType `json:"type_values,omitempty"`
	Hooks      map[string]BaselineHook `json:"hooks,omitempty"`
	Bindings   map[string]BaselineBind `json:"bindings,omitempty"`
	Edges      map[string]BaselineBool `json:"edges,omitempty"`
	Grants     map[string]BaselineBool `json:"grants,omitempty"`
	State      map[string]BaselineVal  `json:"state,omitempty"`
}

type BaselineType struct {
	At Timestamp   `json:"at"`
	TV TypeVersion `json:"tv"`
}
type BaselineHook struct {
	At Timestamp   `json:"at"`
	HV HookVersion `json:"hv"`
}
type BaselineBind struct {
	At    Timestamp `json:"at"`
	Phase Phase     `json:"phase"`
}
type BaselineBool struct {
	At Timestamp `json:"at"`
	V  bool      `json:"v"`
}
type BaselineVal struct {
	At Timestamp `json:"at"`
	V  Value     `json:"v"`
}

// Journal 是只追加的全局事件日志，所有真实调用与版本演进在此取得全局串行序号。
type Journal struct {
	events   []Event
	manifest *Manifest
}

func NewJournal() *Journal { return &Journal{} }

// append 追加一条事件并赋予线性化序号（仅引擎在持锁时调用）。
func (j *Journal) append(e Event) Event {
	if len(j.events) > 0 && e.At < j.events[len(j.events)-1].At {
		panic("hypcheck: event timestamps must be monotonic non-decreasing")
	}
	e.Seq = Seq(len(j.events) + 1)
	j.events = append(j.events, e)
	return e
}

// Events 返回日志切片副本。
func (j *Journal) Events() []Event {
	out := make([]Event, len(j.events))
	copy(out, j.events)
	return out
}

// Horizon 返回当前压实切点；0 表示历史完整无缺口。
func (j *Journal) Horizon() Timestamp {
	if j.manifest == nil {
		return 0
	}
	return j.manifest.Horizon
}

// Manifest 返回压实清册（可能为 nil）。
func (j *Journal) Manifest() *Manifest { return j.manifest }

// compact 丢弃 at < horizon 的事件并固化切点清册。
func (j *Journal) compact(horizon Timestamp, m *Manifest) {
	if m == nil {
		m = &Manifest{Horizon: horizon, Types: map[string]BirthRecord{}, Principals: map[string]BirthRecord{}}
	}
	m.Horizon = horizon
	if j.manifest != nil && horizon < j.manifest.Horizon {
		panic("hypcheck: compaction horizon cannot move backwards")
	}
	i := sort.Search(len(j.events), func(i int) bool { return j.events[i].At >= horizon })
	j.events = append([]Event(nil), j.events[i:]...)
	j.manifest = m
}

// errPastHorizon 由各存储在 as-of 查询早于压切点时返回，统一映射为 E2。
var errPastHorizon = errors.New("hypcheck: requested time precedes retained history horizon")
