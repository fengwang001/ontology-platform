// Package abac 实现基于属性的访问控制（Attribute-Based Access Control）。
//
// 判定依据来自三类属性：主体（subject）、资源（resource）与环境（environment）。
// 多策略采用 deny-overrides（拒绝优先）组合；策略引用的属性缺失时策略不匹配；
// 对调用方的最终答复不泄露资源是否存在。
package abac

// Attributes 是属性名到属性值的映射。
// 属性值支持 Go 基础类型：string、bool、int64、float64、time.Time。
type Attributes map[string]any

// Op 是单个属性条件的比较算子。
type Op string

const (
	OpEqual          Op = "eq"
	OpNotEqual       Op = "ne"
	OpExists         Op = "exists"
	OpNotExists      Op = "not_exists"
	OpStringContains Op = "contains"
	OpStringPrefix   Op = "prefix"
	OpStringSuffix   Op = "suffix"
	OpLess           Op = "lt"
	OpLessEqual      Op = "le"
	OpGreater        Op = "gt"
	OpGreaterEqual   Op = "ge"
	OpIn             Op = "in"
)

// Condition 描述对单个属性的条件。
// Key 缺失时：exists/not_exists 是合法判定，其它算子导致条件无法求值。
type Condition struct {
	Key   string
	Op    Op
	Value any
}

// Effect 是策略的效果。
type Effect string

const (
	EffectAllow Effect = "allow"
	EffectDeny  Effect = "deny"
)

// Policy 是一条 ABAC 策略：当主体/资源/环境条件全部匹配时产生其效果。
type Policy struct {
	ID          string
	Effect      Effect
	Description string
	Subject     []Condition
	Resource    []Condition
	Environment []Condition
}

// Request 是一次访问请求的输入。
// Resource 为 nil 表示目标资源不存在或不可见，引擎不区分该情形与“资源存在但隐藏”。
type Request struct {
	Subject     Attributes
	Resource    Attributes
	Environment Attributes
	Action      string
}

// Reason 是拒绝（或允许）的内部原因码，仅出现在审计日志中，不直接返回给调用方。
type Reason string

const (
	ReasonDenyByPolicy   Reason = "deny_by_policy"
	ReasonMissingAttr    Reason = "missing_referenced_attribute"
	ReasonNoApplicable   Reason = "no_applicable_policy"
	ReasonInvalidRequest Reason = "invalid_request"
)

// Decision 是返回给调用方的判定结果。
// 所有拒绝原因对外统一为固定文案，不携带策略、属性或资源存在性信息。
type Decision struct {
	Allowed bool
	Action  string
	Message string
}

// AuditDecision 是供服务端审计使用的完整判定，包含可区分的原因与依据。
type AuditDecision struct {
	Decision
	Reason      Reason
	DenyPolicy  string
	AllowPolicy string
	// Matched 是实际参与组合的策略 ID，按字典序排列，保证确定性。
	Matched []string
	// Indeterminate 是因引用属性缺失而无法求值的策略 ID，按字典序排列。
	Indeterminate []string
	// ResourcePresent 仅用于审计，不进入对外答复。
	ResourcePresent bool
}

// PublicDenyMessage 是所有拒绝对外暴露的统一文案。
const PublicDenyMessage = "access denied"
