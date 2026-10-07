package precheck

// Moment 是全局逻辑时间，全部事件在一个全局串行序列上单调不递减。
type Moment = int64

// EventKind 标识全局事件日志中的事件类型。
type EventKind string

const (
	EvTypeSchema   EventKind = "type_schema_introduced"
	EvHookSet      EventKind = "hook_set_activated"
	EvEdgeAdded    EventKind = "edge_added"
	EvEdgeRemoved  EventKind = "edge_removed"
	EvUserAdded    EventKind = "user_added"
	EvObjectUpsert EventKind = "object_upserted"
	EvObjectDelete EventKind = "object_deleted"
	EvRealCall     EventKind = "real_action_called"
	// EvHistoryGap 表示某条键在半开区间 [GapStart, GapEnd) 内的历史记录缺失。
	EvHistoryGap EventKind = "history_gap"
)

// Event 是全局串行日志中的一条不可变事件。
type Event struct {
	Seq  int64     `json:"seq"`
	At   Moment    `json:"at"`
	Kind EventKind `json:"kind"`

	ActionType string         `json:"action_type,omitempty"`
	From       string         `json:"from,omitempty"`
	To         string         `json:"to,omitempty"`
	User       string         `json:"user,omitempty"`
	ObjectKey  string         `json:"object_key,omitempty"`
	Value      any            `json:"value,omitempty"`
	Schema     *ParamSchema   `json:"schema,omitempty"`
	HookSet    *HookSet       `json:"hook_set,omitempty"`
	Caller     string         `json:"caller,omitempty"`
	Params     map[string]any `json:"params,omitempty"`
	Outcome    string         `json:"outcome,omitempty"`
	GapKey     string         `json:"gap_key,omitempty"`
	GapStart   Moment         `json:"gap_start,omitempty"`
	GapEnd     Moment         `json:"gap_end,omitempty"`
	GapDomain  string         `json:"gap_domain,omitempty"`
}

// 历史缺口事件的领域常量：决定缺口作用于哪个时态视图。
const (
	GapDomainSchema = "schema"
	GapDomainHooks  = "hooks"
	GapDomainUser   = "user"
	GapDomainEdges  = "edges"
	GapDomainObject = "object"
)

// ParamSchema 是某一时刻生效的动作参数结构约束。
type ParamSchema struct {
	Version            Moment            `json:"version"`
	Required           []string          `json:"required"`
	Types              map[string]string `json:"types"`
	RequiredPermission string            `json:"required_permission"`
}

// HookRef 引用钩子注册表中一个不可变的钩子实现。
type HookRef struct {
	ID string `json:"id"`
}

// HookSet 是某动作类型在某一时刻生效的前置/后置钩子有序集合。
type HookSet struct {
	Version Moment    `json:"version"`
	Pre     []HookRef `json:"pre"`
	Post    []HookRef `json:"post"`
}

// HookFailure 记录单个钩子的校验失败。
type HookFailure struct {
	Stage   string `json:"stage"`
	HookID  string `json:"hook_id"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Intent 是动作若被允许时将产生的一条状态改变意图（不落地执行）。
type Intent struct {
	Op        string `json:"op"`
	ObjectKey string `json:"object_key"`
	Value     any    `json:"value,omitempty"`
	Source    string `json:"source"`
}

// Verdict 是一次假设性预检的演算结论。
type Verdict string

const (
	VerdictAllowed         Verdict = "allowed"
	VerdictDenied          Verdict = "denied"
	VerdictResolutionError Verdict = "resolution_error"
)

// FailureAggregation 控制失败聚合方式。
type FailureAggregation string

const (
	// FailFast 只报告所到达阶段的第一个失败。
	FailFast FailureAggregation = "fail_fast"
	// CollectAll 聚合所到达阶段内的全部失败（阶段间规则固定，见设计说明）。
	CollectAll FailureAggregation = "collect_all"
)

// PrecheckRequest 是一次只读的假设性重新预检请求。
type PrecheckRequest struct {
	At          Moment             `json:"at"`
	ActionType  string             `json:"action_type"`
	Caller      string             `json:"caller"`
	Params      map[string]any     `json:"params"`
	Aggregation FailureAggregation `json:"aggregation"`
}

// PrecheckResult 是预检结果。
type PrecheckResult struct {
	Verdict        Verdict          `json:"verdict"`
	ResolutionErr  *ResolutionError `json:"resolution_error,omitempty"`
	Failures       []HookFailure    `json:"failures,omitempty"`
	Intents        []Intent         `json:"intents,omitempty"`
	SchemaVersion  Moment           `json:"schema_version"`
	HookSetVersion Moment           `json:"hook_set_version"`
	AuditSeq       int64            `json:"audit_seq"`
}
