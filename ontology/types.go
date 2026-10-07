package ontology

// ObjectTypeID 唯一标识一个对象类型。
type ObjectTypeID string

// LinkTypeID 唯一标识一个链接类型。
type LinkTypeID string

// SubjectID 唯一标识一个主体（用户、组或服务账号）。
type SubjectID string

// Action 表示一个权限动作，例如 "read"、"write"。
type Action string

// OverrideMode 表示目标对象类型声明的覆盖形态。
type OverrideMode int

const (
	// OverrideNone 表示未声明覆盖。
	OverrideNone OverrideMode = iota
	// OverrideReplace 只替换该类型从上游收到的传播结果，
	// 仅保留其自身的直接授权；替换后的结果继续向下游传播。
	OverrideReplace
	// OverrideReplaceAndBlock 在替换的基础上额外阻断继续向更下游
	// 传播，即便剩余深度仍大于零。
	OverrideReplaceAndBlock
)

func (m OverrideMode) String() string {
	switch m {
	case OverrideReplace:
		return "replace"
	case OverrideReplaceAndBlock:
		return "replace-and-block"
	default:
		return "none"
	}
}

// LinkType 是一条带方向的链接类型。MaxDepth 为 0 表示不参与权限
// 传播，仅用于本类型的直接授权；参与传播时 MaxDepth 为每跳预算的
// 上限：穿越该链接后剩余深度变为 min(剩余, MaxDepth)-1，恰好降到
// 零的那一跳仍允许生效。
type LinkType struct {
	ID         LinkTypeID
	From       ObjectTypeID
	To         ObjectTypeID
	Propagates bool
	MaxDepth   int
}

// participates 报告该链接类型是否实际参与传播。
func (l LinkType) participates() bool {
	return l.Propagates && l.MaxDepth > 0
}

// grantValue 是一条授权记录的值：显式允许或显式否定。
type grantValue int

const (
	grantAllow grantValue = +1
	grantDeny  grantValue = -1
)

// ReasonCode 解释一次权限判定的结论依据，可按拒绝优先级比较。
type ReasonCode int

const (
	// ReasonGranted 判定通过。
	ReasonGranted ReasonCode = iota
	// ReasonExplicitDeny 显式否定项压过了一切传播结果。
	ReasonExplicitDeny
	// ReasonOverrideBlocked 覆盖规则阻断传播导致的无权限。
	ReasonOverrideBlocked
	// ReasonOverrideReplaced 覆盖规则替换掉了全部传播结果，
	// 且目标类型自身没有对应的直接授权。
	ReasonOverrideReplaced
	// ReasonDepthExhausted 传播在到达目标前深度自然耗尽
	// （不视为错误）。
	ReasonDepthExhausted
	// ReasonNoGrant 没有任何授权与传播到达目标。
	ReasonNoGrant
)

func (r ReasonCode) String() string {
	switch r {
	case ReasonGranted:
		return "granted"
	case ReasonExplicitDeny:
		return "explicit-deny"
	case ReasonOverrideBlocked:
		return "override-blocked"
	case ReasonOverrideReplaced:
		return "override-replaced"
	case ReasonDepthExhausted:
		return "depth-exhausted"
	default:
		return "no-grant"
	}
}
