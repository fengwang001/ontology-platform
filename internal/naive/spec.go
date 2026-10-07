// Package naive 是生命周期状态机的独立“朴素逐步推进”参考实现，
// 与 ontology/lifecycle 的生产实现刻意不共享任何代码，
// 供随机对拍使用：两套实现对相同输入必须产出等价的可观察结果。
package naive

// Value 是属性值。
type Value = any

type PrecondKind int

const (
	PrecondAttr PrecondKind = iota + 1
	PrecondLinkCount
	PrecondPeerState
)

type Precondition struct {
	Kind     PrecondKind
	Attr     string
	Equals   Value
	LinkType string
	Min, Max int
	States   []string
}

type Cardinality struct {
	LinkType string
	Max      int
}
type Hook struct {
	LinkType string
	States   []string
}
type Cascade struct {
	LinkType   string
	WhenStates []string
	Transition string
}

type Transition struct {
	Name         string
	From, To     string
	Preconds     []Precondition
	MaxCard      []Cardinality
	Hooks        []Hook
	Cascades     []Cascade
	MutexGroupID string
}

type ObjectType struct {
	Name        string
	Final       map[string]bool
	Transitions map[string]*Transition
}

type Spec struct{ Types map[string]*ObjectType }

type Instance struct {
	ID, Type, State string
	Attrs           map[string]Value
	Clock           int64
}

type Link struct{ Type, From, To string }

type OpKind int

const (
	OpFire OpKind = iota + 1
	OpSetAttr
	OpAddLink
	OpDelLink
)

type Op struct {
	Kind                   OpKind
	InstanceID, Transition string
	Attr                   string
	Value                  Value
	Link                   Link
}

type ErrCode int

const (
	ErrUnknown ErrCode = iota + 1
	ErrPrecondition
	ErrMutex
	ErrCardinality
	ErrHook
	ErrCycle
	ErrTerminal
)

type OpError struct {
	Code       ErrCode
	InstanceID string
	Detail     string
}

func (e *OpError) Error() string { return e.Detail }

type Fired struct {
	InstanceID, Transition, From, To string
	CascadeOf                        string
}

type OpResult struct {
	OK    bool
	Err   *OpError
	Fired []Fired
}

type BatchResult struct {
	Committed bool
	Ops       []OpResult
}
