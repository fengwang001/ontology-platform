package lifecycle

import (
	"fmt"

	"ontology/hooks"
	"ontology/lcerr"
)

// Phase 是生命周期阶段标识（hooks.Phase 的别名，便于调用方书写）。
type Phase = hooks.Phase

// Edge 是一条允许的直接转移关系。转移关系构成有向图，允许存在环，
// 也允许 From == To 的自转移（必须显式声明才允许）。
type Edge struct {
	From Phase
	To   Phase
}

// SchemaConfig 描述一个对象类型的生命周期定义。
type SchemaConfig struct {
	// Initial 是实例创建时所处的阶段，必须在 Phases 中声明。
	Initial Phase
	// Phases 是有限的阶段集合。
	Phases []Phase
	// Edges 是允许的直接转移关系；未声明的转移一律拒绝。
	Edges []Edge
	// Terminals 是终态阶段集合；终态不允许任何转出（含转到自身）。
	Terminals []Phase
}

// Schema 是校验后的生命周期定义，所有查询均为 O(1) 哈希查找。
type Schema struct {
	initial   Phase
	phases    map[Phase]struct{}
	edges     map[Edge]struct{}
	terminals map[Phase]struct{}
}

const opNewSchema = "lifecycle.NewSchema"

// NewSchema 校验并构造生命周期定义。
func NewSchema(cfg SchemaConfig) (*Schema, error) {
	s := &Schema{
		initial:   cfg.Initial,
		phases:    make(map[Phase]struct{}, len(cfg.Phases)),
		edges:     make(map[Edge]struct{}, len(cfg.Edges)),
		terminals: make(map[Phase]struct{}, len(cfg.Terminals)),
	}
	if len(cfg.Phases) == 0 {
		return nil, lcerr.New(lcerr.KindInvalidArgument, opNewSchema, "phases must not be empty")
	}
	for _, p := range cfg.Phases {
		if _, dup := s.phases[p]; dup {
			return nil, lcerr.New(lcerr.KindInvalidArgument, opNewSchema,
				fmt.Sprintf("duplicate phase %q", p))
		}
		s.phases[p] = struct{}{}
	}
	if !s.HasPhase(cfg.Initial) {
		return nil, lcerr.New(lcerr.KindInvalidArgument, opNewSchema,
			fmt.Sprintf("initial phase %q not declared", cfg.Initial))
	}
	for _, e := range cfg.Edges {
		if !s.HasPhase(e.From) || !s.HasPhase(e.To) {
			return nil, lcerr.New(lcerr.KindInvalidArgument, opNewSchema,
				fmt.Sprintf("edge %q -> %q references undeclared phase", e.From, e.To))
		}
		if _, dup := s.edges[e]; dup {
			return nil, lcerr.New(lcerr.KindInvalidArgument, opNewSchema,
				fmt.Sprintf("duplicate edge %q -> %q", e.From, e.To))
		}
		s.edges[e] = struct{}{}
	}
	for _, t := range cfg.Terminals {
		if !s.HasPhase(t) {
			return nil, lcerr.New(lcerr.KindInvalidArgument, opNewSchema,
				fmt.Sprintf("terminal phase %q not declared", t))
		}
		s.terminals[t] = struct{}{}
	}
	return s, nil
}

// HasPhase 报告阶段是否已声明。
func (s *Schema) HasPhase(p Phase) bool {
	_, ok := s.phases[p]
	return ok
}

// IsTerminal 报告阶段是否为终态。
func (s *Schema) IsTerminal(p Phase) bool {
	_, ok := s.terminals[p]
	return ok
}

// Allows 报告 from -> to 是否为声明的允许转移。
func (s *Schema) Allows(from, to Phase) bool {
	_, ok := s.edges[Edge{From: from, To: to}]
	return ok
}
