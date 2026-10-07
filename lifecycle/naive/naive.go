// Package naive 提供一个独立实现的朴素逐步推进模型，用于与 lifecycle
// 引擎对拍随机生成的迁移请求序列与链式结构。朴素模型用一把全局大锁把
// 整个系统串行化；它与正式引擎共享语义但不共享任何实现代码。
package naive

import (
	"fmt"
	"sync"
)

type State string
type LinkType string
type AttrKey string
type AttrValue any
type InstanceID string

const (
	ErrUndeclared   = 1
	ErrPrecondition = 2
	ErrMutex        = 3
	ErrCardinality  = 4
	ErrHook         = 5
	ErrCycle        = 6
	ErrTerminal     = 7
)

type AttrCheck struct {
	Key   AttrKey
	Op    string
	Value AttrValue
}

type LinkCountCheck struct {
	Link LinkType
	Min  int
	Max  int
}

type LinkStateCheck struct {
	Link          LinkType
	AllowedStates []State
	RequireAll    bool
}

type Precondition struct {
	Attr      *AttrCheck
	LinkCount *LinkCountCheck
	LinkState *LinkStateCheck
}

type AttrBound struct {
	Key           AttrKey
	Min           *int64
	Max           *int64
	AllowedValues []AttrValue
}

type CardinalityBound struct {
	Link LinkType
	Min  int
	Max  int
}

type Hook struct {
	Link          LinkType
	RequireStates []State
}

type Cascade struct {
	Link   LinkType
	ToRule string
}

type Rule struct {
	Name          string
	From          []State
	To            State
	Preconditions []Precondition
	AttrBounds    []AttrBound
	Cardinality   []CardinalityBound
	Hooks         []Hook
	Cascades      []Cascade
	MutexGroup    string
}

type Type struct {
	Name      string
	Initial   State
	Terminals []State
	Rules     map[string]*Rule
}

type AttrOp struct {
	Key   AttrKey
	Op    string
	Value AttrValue
}

type LinkOp struct {
	Link   LinkType
	Target InstanceID
	Op     string
}

type Request struct {
	Instance InstanceID
	Rule     string
	Priority int
	Attrs    []AttrOp
	Links    []LinkOp
}

type Outcome struct {
	Instance InstanceID
	Rule     string
	Code     int
	Detail   string
}

func (o Outcome) OK() bool { return o.Code == 0 }

type inst struct {
	typ   string
	state State
	attrs map[AttrKey]AttrValue
	clock uint64
}

// Model 是朴素逐步推进模型：一把全局锁串行化一切；每次 Execute 在世界
// 快照副本上模拟整个批次，任一环节失败就丢弃副本（天然整体撤销），全部
// 通过才用副本替换真实状态。
type Model struct {
	mu    sync.Mutex
	types map[string]*Type
	insts map[InstanceID]*inst
	links map[InstanceID]map[LinkType]map[InstanceID]bool
	clock uint64

	Log    func(line string)
	logSeq uint64
}

func NewModel() *Model {
	return &Model{
		types: map[string]*Type{},
		insts: map[InstanceID]*inst{},
		links: map[InstanceID]map[LinkType]map[InstanceID]bool{},
	}
}

func (m *Model) RegisterType(t *Type) { m.types[t.Name] = t }

func (m *Model) Create(id InstanceID, typ string) error {
	t, ok := m.types[typ]
	if !ok {
		return fmt.Errorf("naive: unknown type %q", typ)
	}
	m.insts[id] = &inst{typ: typ, state: t.Initial, attrs: map[AttrKey]AttrValue{}}
	return nil
}
