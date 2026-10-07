package ontology

// OpKind 是动作对对象实例的直接操作种类。
type OpKind int

const (
	// OpCreate 创建一个新实例。
	OpCreate OpKind = iota + 1
	// OpModify 修改一个已存在实例的属性。
	OpModify
)

// InvisibleMode 决定级联触及的实例对主体不可见时的处理模式，两种模式互斥。
type InvisibleMode int

const (
	// InvisibleDeny 整体拒绝该动作，且错误中不泄露不可见实例的存在。
	InvisibleDeny InvisibleMode = iota + 1
	// InvisibleSkip 允许动作继续执行，但跳过对不可见实例的级联影响。
	InvisibleSkip
)

// MergeMode 声明多层级授权结论的合并规则。
// 两种规则都是可交换、可结合的折叠，因此合并结果与访问各层级的顺序无关。
type MergeMode int

const (
	// MergeAll 整条路径逐层全部放行方可通过（逻辑与）。
	MergeAll MergeMode = iota + 1
	// MergeAny 路径上任一层级放行即可通过（逻辑或）。
	MergeAny
)

// CascadeRule 声明级联传播的边界。
type CascadeRule struct {
	// MaxDepth 是传播深度上限（按链接跳数计，0 表示不级联）。
	// 若实际关系图要求更深的传播才能覆盖全部级联影响，
	// 引擎以 ErrDepthExceeded 拒绝，而不是静默截断。
	MaxDepth int
	// ExcludeLinkTypes 声明沿这些链接类型不传播。
	ExcludeLinkTypes []LinkTypeID
}

// ActionDecl 是一个动作类型的声明。
type ActionDecl struct {
	ID ActionTypeID
	// AllowedOps 声明该动作允许对哪些对象类型执行哪些操作。
	AllowedOps map[ObjectTypeID][]OpKind
	Cascade    CascadeRule
	Invisible  InvisibleMode
	Merge      MergeMode
}

// allows 报告声明是否允许对给定对象类型执行给定操作。
func (d ActionDecl) allows(t ObjectTypeID, k OpKind) bool {
	for _, ok := range d.AllowedOps[t] {
		if ok == k {
			return true
		}
	}
	return false
}

// DirectOp 是动作调用中对一个目标实例的直接操作。
type DirectOp struct {
	Kind   OpKind
	Type   ObjectTypeID
	Target InstanceID        // OpCreate 时为要分配的 ID；OpModify 时为已存在实例
	Props  map[string]string // 要设置的属性
}

// Invocation 是一次动作调用的具体参数。
type Invocation struct {
	Action ActionTypeID
	Ops    []DirectOp
}
