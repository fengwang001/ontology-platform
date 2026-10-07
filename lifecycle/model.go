package lifecycle

import "fmt"

// AttrValue 是实例属性值的统一表示。
type AttrValue = any

// PrecondKind 是前置条件的种类。
type PrecondKind int

const (
	// PrecondAttr 要求本实例的某个属性等于给定值（nil 表示属性必须不存在）。
	PrecondAttr PrecondKind = iota + 1
	// PrecondLinkCount 要求本实例经 LinkType 出向连接的实例数量落在闭区间内。
	PrecondLinkCount
	// PrecondPeerState 要求经 LinkType 连接的全部对端实例处于给定状态集合之一。
	PrecondPeerState
)

// Precondition 是一条迁移前置条件。
type Precondition struct {
	Kind     PrecondKind
	Attr     string
	Equals   AttrValue
	LinkType string
	MinCount int
	MaxCount int
	States   []string
	Desc     string
}

// Attr 前置条件：属性 Attr 必须等于 Equals。
func Attr(name string, value AttrValue) Precondition {
	return Precondition{Kind: PrecondAttr, Attr: name, Equals: value,
		Desc: fmt.Sprintf("attr(%s)==%v", name, value)}
}

// LinkCount 前置条件：经 linkType 出向链接数量落在 [min,max] 内（min/max 为 -1 表示不限）。
func LinkCount(linkType string, min, max int) Precondition {
	return Precondition{Kind: PrecondLinkCount, LinkType: linkType, MinCount: min, MaxCount: max,
		Desc: fmt.Sprintf("count(%s) in [%d,%d]", linkType, min, max)}
}

// PeerState 前置条件：经 linkType 连接的每个对端都处于 states 之一。
func PeerState(linkType string, states ...string) Precondition {
	return Precondition{Kind: PrecondPeerState, LinkType: linkType, States: states,
		Desc: fmt.Sprintf("peers(%s) in %v", linkType, states)}
}

// CardinalityRule 是迁移后基数约束：迁移生效后经 LinkType 的出向链接数量不得超过 Max。
type CardinalityRule struct {
	LinkType string
	Max      int
}

// HookRule 是跨实例校验钩子：迁移生效后，经 LinkType 连接的每个对端必须处于 States 之一。
type HookRule struct {
	LinkType string
	States   []string
}

// CascadeRule 描述一次迁移对其关联实例的强制随迁。
// 对经 LinkType 连接、且处于 WhenStates 之一的每个对端，强制触发 Transition 迁移；
// 对端不在 WhenStates 中视为其已就位、无需随迁。
type CascadeRule struct {
	LinkType   string
	WhenStates []string
	Transition string
}

// Transition 声明对象类型上的一条有向迁移。
type Transition struct {
	Name         string
	From         string
	To           string
	Preconds     []Precondition
	MaxCard      []CardinalityRule
	Hooks        []HookRule
	Cascades     []CascadeRule
	MutexGroupID string // 非空时参与同名互斥组
}

// ObjectType 声明一个对象类型的状态集合、终态与迁移规则。
type ObjectType struct {
	Name        string
	States      []string
	FinalStates map[string]bool
	Transitions map[string]*Transition
}

// Schema 是全部对象类型声明的只读集合。
type Schema struct {
	Types map[string]*ObjectType
}

// LookupTransition 按类型与迁移名查找声明。
func (s *Schema) LookupTransition(typeName, transitionName string) *Transition {
	t, ok := s.Types[typeName]
	if !ok {
		return nil
	}
	return t.Transitions[transitionName]
}

// IsFinal 报告某类型的某状态是否为终态。
func (s *Schema) IsFinal(typeName, state string) bool {
	t, ok := s.Types[typeName]
	if !ok {
		return false
	}
	return t.FinalStates[state]
}
