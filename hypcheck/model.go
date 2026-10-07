package hypcheck

// Timestamp 是全局逻辑时间（事件在全局串行顺序中的提交时刻）。
// 所有 as-of 查询都以 [from, until) 左闭右开区间解释版本边界：
// 在边界时刻 T 发生的版本切换，时刻 T 取用新版本一侧。
type Timestamp int64

// Seq 是事件在全局追加日志中的串行序号（线性化点）。
type Seq int64

// Value 是参数 / 对象状态的标量值，使用显式标签以保证审计序列化稳定。
type Value struct {
	Kind ValueKind `json:"kind"`
	Int  int64     `json:"int,omitempty"`
	Str  string    `json:"str,omitempty"`
	Bool bool      `json:"bool,omitempty"`
}

type ValueKind string

const (
	KindInt  ValueKind = "int"
	KindStr  ValueKind = "str"
	KindBool ValueKind = "bool"
)

func IntValue(v int64) Value  { return Value{Kind: KindInt, Int: v} }
func StrValue(v string) Value { return Value{Kind: KindStr, Str: v} }
func BoolValue(v bool) Value  { return Value{Kind: KindBool, Bool: v} }

// Params 是动作调用参数。
type Params map[string]Value

// Phase 是校验钩子阶段。
type Phase string

const (
	PhasePre  Phase = "pre"
	PhasePost Phase = "post"
)

// Verdict 是假设性预检的裁决。
type Verdict string

const (
	VerdictAllowed Verdict = "allowed"
	VerdictDenied  Verdict = "denied"
	VerdictError   Verdict = "error"
)

// FailureCode 标识单个钩子 / 权限闸门失败原因。
type FailureCode string

const (
	FailurePermissionDenied FailureCode = "permission_denied"
	FailureHookDenied       FailureCode = "hook_denied"
)

// FieldType 是动作参数结构约束中的字段类型。
type FieldType ValueKind

// Field 是单个参数结构约束。
type Field struct {
	Name     string    `json:"name"`
	Type     FieldType `json:"type"`
	Required bool      `json:"required"`
}

// Schema 是某一版本的动作参数结构约束，字段按 Name 排序后生效。
type Schema struct {
	Fields []Field `json:"fields"`
}

// EffectDecl 声明动作被允许后将产生的状态改变意图模板。
// Key 为目标对象键；Param 非空时取同名参数值，否则取 Const。
type EffectDecl struct {
	Key   string `json:"key"`
	Param string `json:"param,omitempty"`
	Const Value  `json:"const,omitempty"`
}

// TypeVersion 是动作类型在某个生效时刻起的完整定义。
type TypeVersion struct {
	Schema  Schema       `json:"schema"`
	Effects []EffectDecl `json:"effects"`
}

// SpecKind 是内置确定性钩子种类。
type SpecKind string

const (
	// PreDenyParamEqual: 参数 Param 等于 Value 时拒绝。
	PreDenyParamEqual SpecKind = "pre.deny_param_equal"
	// PreDenyStateEqual: 只读快照中键 Key 的状态等于 Value 时拒绝。
	PreDenyStateEqual SpecKind = "pre.deny_state_equal"
	// PostDenyStateEqual: 后置阶段读取前置阶段冻结的只读快照判定。
	PostDenyStateEqual SpecKind = "post.deny_state_equal"
	// PostDenyIntendedSet: 意图集合中键 Key 将被写成 Value 时拒绝。
	PostDenyIntendedSet SpecKind = "post.deny_intended_set"
)

// HookSpec 是钩子的确定性可重演规格。
type HookSpec struct {
	Kind  SpecKind `json:"kind"`
	Key   string   `json:"key,omitempty"`
	Param string   `json:"param,omitempty"`
	Value Value    `json:"value"`
}

// HookVersion 是钩子在某个生效时刻起的版本。
type HookVersion struct {
	Version string   `json:"version"`
	Spec    HookSpec `json:"spec"`
}

// ResolvedHook 记录一次预检实际依据的钩子版本。
type ResolvedHook struct {
	HookID  string   `json:"hook_id"`
	Phase   Phase    `json:"phase"`
	Version string   `json:"version"`
	Spec    HookSpec `json:"spec"`
}

// Effect 是一条状态改变意图（预检中绝不落地）。
type Effect struct {
	Op    string `json:"op"`
	Key   string `json:"key"`
	Value Value  `json:"value"`
}

// PhaseFailure 是一个阶段内的单条失败。
type PhaseFailure struct {
	Phase   Phase       `json:"phase"`
	Code    FailureCode `json:"code"`
	HookID  string      `json:"hook_id,omitempty"`
	Message string      `json:"message"`
}

// EdgeSnapshot 记录权限判定实际走过的一条继承边及其在 t 时刻的版本依据。
type EdgeSnapshot struct {
	Parent    string    `json:"parent"`
	Child     string    `json:"child"`
	Active    bool      `json:"active"`
	Effective Timestamp `json:"effective"`
}

// GrantSnapshot 记录权限判定实际查询的一个授权点。
type GrantSnapshot struct {
	Node      string    `json:"node"`
	TypeID    string    `json:"type_id"`
	Granted   bool      `json:"granted"`
	Effective Timestamp `json:"effective"`
}

// PermTrace 是 t 时刻权限继承快照的可核查重建记录。
type PermTrace struct {
	Caller    string          `json:"caller"`
	Ancestors []string        `json:"ancestors"`
	Edges     []EdgeSnapshot  `json:"edges"`
	Grants    []GrantSnapshot `json:"grants"`
	Allowed   bool            `json:"allowed"`
	GrantedBy string          `json:"granted_by,omitempty"`
}

// PrecheckRequest 是一次只读假设性预检请求。
type PrecheckRequest struct {
	At         Timestamp `json:"at"`
	TypeID     string    `json:"type_id"`
	Caller     string    `json:"caller"`
	Params     Params    `json:"params"`
	ObjectKeys []string  `json:"object_keys"`
	CollectAll bool      `json:"collect_all"`
}

// PrecheckResult 是假设性预检结果。
type PrecheckResult struct {
	At         Timestamp  `json:"at"`
	LinearSeq  Seq        `json:"linear_seq"`
	Verdict    Verdict    `json:"verdict"`
	ErrorClass ErrorClass `json:"error_class,omitempty"`
	ErrorCode  string     `json:"error_code,omitempty"`
	Message    string     `json:"message,omitempty"`

	// 预检依据（即便裁决为 deny 也尽量记录已解析到的依据，便于审计）。
	TypeVersion string         `json:"type_version,omitempty"`
	Hooks       []ResolvedHook `json:"hooks,omitempty"`
	PermTrace   *PermTrace     `json:"perm_trace,omitempty"`

	// 阶段失败。前置失败时后置阶段绝不演算，故二者永不同时非空。
	PreFailures  []PhaseFailure `json:"pre_failures,omitempty"`
	PostFailures []PhaseFailure `json:"post_failures,omitempty"`

	// 仅当 Verdict == allowed 时非空：状态改变意图（未落地）。
	Effects []Effect `json:"effects,omitempty"`

	// 重建成本统计：版本记录探测次数（独立验证非线形成本用）。
	Probes int `json:"probes"`

	AuditID string `json:"audit_id,omitempty"`
}

// IsError 报告该结果是否属于四类历史性输入错误之一。
func (r *PrecheckResult) IsError() bool { return r.Verdict == VerdictError }

// Allowed 报告该动作在假设时刻是否会被允许。
func (r *PrecheckResult) Allowed() bool { return r.Verdict == VerdictAllowed }
