// Package prune 持有投影定义并执行列裁剪：保留集取所有输出列 refs 的并集，
// 逐行投影时只读取保留集中的源列。它只依赖 col 包。
package prune

import (
	"errors"

	"ontology/col"
)

// refs 引用了未知源列（哨兵错误）。
var ErrUnknownRef = errors.New("prune: projection references unknown source column")

// 输出列名重复（哨兵错误）。
var ErrDuplicateOutput = errors.New("prune: duplicate output column name")

// Def 是一个输出列：Out 为下游可见列名，Refs 为其依赖的源列（只能是源列）。
type Def struct {
	Out  string
	Refs []string
}

// Projection 是一份已校验、不可变的投影定义；lastRead 是非导出的读取计数器。
type Projection struct {
	schema   *col.Schema
	defs     []Def
	retained []string // refs 并集，按源列模式顺序排列
	keep     map[string]bool
	lastRead int // 最近一次 Project 为求投影实际读取的源列个数
}

// New 校验并构造投影：输出名不得重复，每个 ref 必须是已知源列。
// 任一校验失败即返回对应哨兵错误，不产生任何可用定义。
func New(schema *col.Schema, defs []Def) (*Projection, error) {
	seen := make(map[string]bool, len(defs))
	for _, d := range defs {
		if seen[d.Out] {
			return nil, ErrDuplicateOutput
		}
		seen[d.Out] = true
		for _, r := range d.Refs {
			if !schema.Has(r) {
				return nil, ErrUnknownRef
			}
		}
	}
	keep := make(map[string]bool)
	for _, d := range defs {
		for _, r := range d.Refs {
			keep[r] = true
		}
	}
	retained := make([]string, 0, len(keep))
	for _, n := range schema.Names() {
		if keep[n] {
			retained = append(retained, n)
		}
	}
	cp := make([]Def, len(defs))
	for i, d := range defs {
		cp[i] = Def{Out: d.Out, Refs: append([]string(nil), d.Refs...)}
	}
	return &Projection{schema: schema, defs: cp, retained: retained, keep: keep}, nil
}

// Names 按投影定义顺序返回输出列名。
func (p *Projection) Names() []string {
	out := make([]string, len(p.defs))
	for i, d := range p.defs {
		out[i] = d.Out
	}
	return out
}

// Retained 返回裁剪后保留的源列（refs 并集，按源列模式顺序）。
func (p *Projection) Retained() []string { return append([]string(nil), p.retained...) }

// Project 对一个完整变更行做投影：只把保留集内的源列读入裁剪行，
// 再严格按定义顺序逐输出列现算。被裁掉的源列在此处根本不会被索引。
func (p *Projection) Project(row map[string]int) []int {
	kept := make(map[string]int, len(p.retained))
	p.lastRead = 0
	for _, c := range p.retained {
		kept[c] = row[c] // 每个保留源列只读一次，与它被几个输出列引用无关
		p.lastRead++
	}
	r := col.NewRow(kept)
	out := make([]int, len(p.defs))
	for i, d := range p.defs {
		out[i] = col.Eval(r, d.Refs)
	}
	return out
}
