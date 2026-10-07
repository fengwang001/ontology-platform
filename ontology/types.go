// ontology 包实现多租户命名空间权限继承覆盖模块。
//
// 对象类型定义在全局命名空间，实例归属租户命名空间。
// 权限规则分两层：全局默认规则与租户覆盖规则。
package ontology

// Effect 表示一条规则声明的效果。
type Effect int

const (
	Deny Effect = iota
	Allow
)

func (e Effect) String() string {
	if e == Allow {
		return "allow"
	}
	return "deny"
}

// ScopeKind 表示规则声明覆盖范围的粒度。
type ScopeKind int

const (
	// ScopeAttr 精确到单个属性。
	ScopeAttr ScopeKind = iota
	// ScopeAttrSet 覆盖一组属性（成员枚举）。
	ScopeAttrSet
	// ScopeAttrWildcard 覆盖对象类型的全部属性。
	ScopeAttrWildcard
	// ScopePredicate 精确到单个行级谓词条件。
	ScopePredicate
	// ScopePredicateSet 覆盖一组谓词条件。
	ScopePredicateSet
	// ScopePredicateWildcard 覆盖全部谓词条件。
	ScopePredicateWildcard
)

// Scope 描述一条声明的作用范围。
// Name 用于 ScopeAttr / ScopePredicate；Members 用于集合粒度。
type Scope struct {
	Kind    ScopeKind
	Name    string
	Members []string
}

// IsRow 报告该范围是否属于行级（谓词）维度。
func (s Scope) IsRow() bool {
	return s.Kind == ScopePredicate || s.Kind == ScopePredicateSet || s.Kind == ScopePredicateWildcard
}

// Statement 是一条权限声明：在某个范围上给出允许或拒绝。
// Basis 为授权依据；当系统要求放宽必须声明依据时，放宽声明必须携带非空 Basis。
type Statement struct {
	ID     string
	Scope  Scope
	Effect Effect
	Basis  string
}

// Predicate 是对象类型上登记的行级谓词条件。
type Predicate struct {
	ID    string
	Field string
	Op    string // eq, ne, lt, le, gt, ge, contains
	Value any
}

// ObjectTypeDef 是全局命名空间中的对象类型定义。
type ObjectTypeDef struct {
	Name       string
	Attrs      []string
	Predicates []Predicate
}

// Instance 是归属某个租户命名空间的具体实例。
type Instance struct {
	ID    string
	Attrs map[string]any
}

// AccessRequest 描述一次访问判定查询。
// 判定依据 InstanceTenant（实例归属租户）的覆盖规则，
// 而不是 SubjectTenant（发起访问主体所属租户）的覆盖规则。
type AccessRequest struct {
	SubjectTenant  string
	SubjectClass   string
	Type           string
	InstanceTenant string
	Instance       Instance
}

// Trace 记录一次判定的可观测依据。
type Trace struct {
	// DefaultEntriesExamined 是判定过程中考察的全局默认规则条目数。
	DefaultEntriesExamined int
	// OverrideEntriesExamined 是判定过程中考察的、实例归属租户实际登记的覆盖规则条目数。
	// 该数量与系统租户总数及其他租户的覆盖规则数量无关。
	OverrideEntriesExamined int
	// Basis 记录每个判定点的规则层级依据：
	// "default:<stmtID>" / "override:<stmtID>" / "implicit-deny"。
	Basis map[string]string
}

// Decision 是一次访问判定的结果。
type Decision struct {
	// RowAllowed 表示行级判定：实例是否命中任一被允许的谓词条件。
	RowAllowed bool
	// Attrs 是属性级判定结果。
	Attrs map[string]bool
	Trace Trace
}

// AuditRecord 是审计日志条目，追加后不可变。
type AuditRecord struct {
	Seq    uint64
	Op     string
	Input  string
	Output string
	// RuleBasis 记录据以裁决的规则层级依据（仅判定类调用）。
	RuleBasis map[string]string
}
