package ontology

// VisibilityEffect 是可见性策略的结论。
type VisibilityEffect int

const (
	Allow VisibilityEffect = iota
	Deny
)

// CondOp 是可见性判定条件的比较操作。
type CondOp int

const (
	OpEq CondOp = iota
	OpNeq
)

// Condition 引用同一实例上另一属性的原始值进行判定。
type Condition struct {
	Attr  string
	Op    CondOp
	Value Value
}

// VisibilityPolicy 对某属性给出允许/拒绝读取的结论。
type VisibilityPolicy struct {
	ID      string
	Subject string // 精确主体名或 "*"
	Attr    string
	Effect  VisibilityEffect
	Cond    *Condition // 可为 nil，表示无条件生效
}

// RuleKind 是脱敏派生规则的类别。
type RuleKind int

const (
	RuleRedact   RuleKind = iota // 替换为固定掩码 "***"
	RuleHash                     // 替换为 sha256 前 8 位十六进制
	RuleTruncate                 // 截断到 Param 个字符
	RuleConstant                 // 替换为常量 ParamValue
	RuleFromAttr                 // 取 InputAttr 脱敏后的派生值
)

// MaskingRule 描述一条派生规则。
type MaskingRule struct {
	Kind       RuleKind
	Param      int    // RuleTruncate 的截断长度
	ParamValue Value  // RuleConstant 的常量
	InputAttr  string // RuleFromAttr 依赖的属性（其脱敏后派生值）
}

// MaskingPolicy 对某属性给出脱敏强度与派生规则。
type MaskingPolicy struct {
	ID       string
	Subject  string // 精确主体名或 "*"
	Attr     string
	Strength int
	Rule     MaskingRule
}
