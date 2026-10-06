package exports

// TargetKind 目标的三种形态。
type TargetKind int

const (
	// TargetString 相对目标字符串，如 "./dist/a.js"。
	TargetString TargetKind = iota
	// TargetForbidden 显式禁止。
	TargetForbidden
	// TargetConditions 有序条件映射。
	TargetConditions
)

// Condition 是条件映射中的一项：条件名 + 目标（可再嵌套条件映射）。
type Condition struct {
	Name   string
	Target *Target
}

// Target 表示一个导出目标。请用构造函数创建。
type Target struct {
	kind  TargetKind
	value string
	conds []Condition
}

// StringTarget 构造相对目标字符串。
func StringTarget(s string) Target {
	return Target{kind: TargetString, value: s}
}

// ForbiddenTarget 构造显式禁止目标。
func ForbiddenTarget() Target {
	return Target{kind: TargetForbidden}
}

// ConditionsTarget 构造有序条件映射。conds 的顺序即匹配优先级。
func ConditionsTarget(conds ...Condition) Target {
	cp := make([]Condition, len(conds))
	copy(cp, conds)
	return Target{kind: TargetConditions, conds: cp}
}

// Cond 构造一个条件项。
func Cond(name string, t Target) Condition {
	return Condition{Name: name, Target: &t}
}

// Kind 返回目标形态。
func (t Target) Kind() TargetKind { return t.kind }

// String 返回字符串目标的原始（未替换）内容；非字符串目标返回 ""。
func (t Target) String() string { return t.value }

// Conditions 返回条件映射的副本。
func (t Target) Conditions() []Condition {
	cp := make([]Condition, len(t.conds))
	copy(cp, t.conds)
	return cp
}
