package featureflag

// Operator 是定向规则条件支持的比较算子。
type Operator string

const (
	// OpEqual 属性存在且等于期望值。
	OpEqual Operator = "eq"
	// OpNotEqual 属性存在且不等于期望值。
	OpNotEqual Operator = "ne"
	// OpIn 属性存在且属于期望集合。
	OpIn Operator = "in"
)

// Condition 描述对用户某个属性的判定。引用的属性缺失时，整个条件不满足。
type Condition struct {
	Attribute string
	Operator  Operator
	Values    []any
}

// RolloutWeight 是放量中一个变体及其权重（总权重 10000）。
type RolloutWeight struct {
	Variant string
	Weight  int
}

// Rule 是一条有序定向规则：Conditions 全部满足时命中。
// Variant 与 Rollout 恰好指定一个；Rollout 按声明顺序切分桶区间。
type Rule struct {
	Name       string
	Conditions []Condition
	Variant    string
	Rollout    []RolloutWeight
}

// Prerequisite 表示另一个开关对当前用户必须先求出指定变体。
type Prerequisite struct {
	Flag    string
	Variant string
}

// FlagSpec 是单个开关在一次发布中的完整声明。
type FlagSpec struct {
	Enabled       bool
	OffVariant    string
	Variants      []string
	Prerequisites []Prerequisite
	Rules         []Rule
	// DefaultRollout 为 nil 表示没有命中规则时返回 OffVariant。
	DefaultRollout []RolloutWeight
}

// SpecSet 是一次发布包含的完整规则集。
type SpecSet map[string]FlagSpec
