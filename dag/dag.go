// Package dag 校验节点定义、检测环、计算层级与消费者表。不依赖其他包。
package dag

import (
	"errors"
	"fmt"
)

var (
	ErrInvalidDef  = errors.New("dag: invalid definition")
	ErrUnknownNode = errors.New("dag: unknown node")
	ErrCycle       = errors.New("dag: dependency cycle")
)

type Def struct {
	Name   string
	Kind   string // src | scale | add | sum
	Inputs []string
	K      int64
}

type Graph struct {
	defs  map[string]Def
	order []string            // 定义顺序
	level map[string]int      // 层级
	cons  map[string][]string // 消费者，按定义顺序
}

// New 按「定义非法 → 节点不存在 → 依赖成环」的顺序检查，只报第一条。
func New(defs []Def) (*Graph, error) {
	g := &Graph{defs: map[string]Def{}, level: map[string]int{}, cons: map[string][]string{}}
	// 阶段一：定义非法
	for _, d := range defs {
		if d.Name == "" {
			return nil, fmt.Errorf("%w: empty name", ErrInvalidDef)
		}
		if _, dup := g.defs[d.Name]; dup {
			return nil, fmt.Errorf("%w: duplicate name %q", ErrInvalidDef, d.Name)
		}
		seen := map[string]bool{}
		for _, in := range d.Inputs {
			if seen[in] {
				return nil, fmt.Errorf("%w: duplicate input %q in %q", ErrInvalidDef, in, d.Name)
			}
			seen[in] = true
		}
		switch d.Kind {
		case "src":
			if len(d.Inputs) != 0 {
				return nil, fmt.Errorf("%w: src %q takes no inputs", ErrInvalidDef, d.Name)
			}
		case "scale", "add":
			if len(d.Inputs) != 1 {
				return nil, fmt.Errorf("%w: %s %q takes exactly 1 input", ErrInvalidDef, d.Kind, d.Name)
			}
		case "sum":
			if len(d.Inputs) < 1 {
				return nil, fmt.Errorf("%w: sum %q takes >=1 inputs", ErrInvalidDef, d.Name)
			}
		default:
			return nil, fmt.Errorf("%w: unknown kind %q", ErrInvalidDef, d.Kind)
		}
		g.defs[d.Name] = d
		g.order = append(g.order, d.Name)
	}
	// 阶段二：节点不存在
	for _, d := range defs {
		for _, in := range d.Inputs {
			if _, ok := g.defs[in]; !ok {
				return nil, fmt.Errorf("%w: %q input %q", ErrUnknownNode, d.Name, in)
			}
		}
	}
	// 阶段三：依赖成环（含自环），顺带算层级
	const (
		white = iota
		gray
		black
	)
	color := map[string]int{}
	var visit func(n string) (int, error)
	visit = func(n string) (int, error) {
		switch color[n] {
		case gray:
			return 0, fmt.Errorf("%w: at %q", ErrCycle, n)
		case black:
			return g.level[n], nil
		}
		color[n] = gray
		max := -1
		for _, in := range g.defs[n].Inputs {
			l, err := visit(in)
			if err != nil {
				return 0, err
			}
			if l > max {
				max = l
			}
		}
		color[n] = black
		g.level[n] = max + 1
		return g.level[n], nil
	}
	for _, n := range g.order {
		if _, err := visit(n); err != nil {
			return nil, err
		}
	}
	for _, d := range defs { // 消费者表按定义顺序
		for _, in := range d.Inputs {
			g.cons[in] = append(g.cons[in], d.Name)
		}
	}
	return g, nil
}

func (g *Graph) Has(name string) bool        { _, ok := g.defs[name]; return ok }
func (g *Graph) IsSrc(name string) bool      { return g.defs[name].Kind == "src" }
func (g *Graph) Def(name string) Def         { return g.defs[name] }
func (g *Graph) Level(name string) int       { return g.level[name] }
func (g *Graph) Names() []string             { return append([]string(nil), g.order...) }
func (g *Graph) Consumers(n string) []string { return g.cons[n] }
