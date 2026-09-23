// Package set 多模式规则集：按顺序的包含/排除规则、最后命中者胜、
// 并发查询与整体热替换。每次查询只看到一个版本的规则集。
package set

import (
	"errors"
	"sync/atomic"

	"ontology/engine"
	"ontology/syntax"
)

// ErrTooManyRules 规则数超过配置上限。
var ErrTooManyRules = errors.New("set: too many rules")

// Decision 是一条路径的结论；Undecided 与 Exclude 可区分。
type Decision int

const (
	Undecided Decision = iota // 未决：没有任何规则命中
	Include                   // 包含
	Exclude                   // 排除
)

// Explanation 描述一次查询的结论及其来源，全部字段属于同一版本。
type Explanation struct {
	Decision Decision
	Rule     string // 决定结论的规则原文（含 ! 前缀）；未决为空
	Index    int    // 决定结论的规则序号；未决为 -1
	Version  uint64 // 规则集版本号
}

type rule struct {
	raw     string
	exclude bool
	m       *engine.Matcher
}

type version struct {
	id    uint64
	rules []rule
}

// Set 是并发安全的规则集。
type Set struct {
	lim syntax.Limits
	cur atomic.Pointer[version]
	seq atomic.Uint64
}

// New 创建空规则集（无任何规则，一切未决）。
func New(lim syntax.Limits) *Set {
	s := &Set{lim: lim.Defaults()}
	s.cur.Store(&version{})
	return s
}

// Replace 整体替换规则集，返回新版本号。
// 任一规则编译失败或超上限时保留旧版本不变并返回错误。
func (s *Set) Replace(pats []string) (uint64, error) {
	if len(pats) > s.lim.MaxRules {
		return 0, ErrTooManyRules
	}
	v := &version{id: s.seq.Add(1)}
	for _, p := range pats {
		r := rule{raw: p}
		pat := p
		if len(pat) > 0 && pat[0] == '!' {
			r.exclude = true
			pat = pat[1:]
		}
		cp, err := syntax.Compile(pat, s.lim)
		if err != nil {
			return 0, err
		}
		r.m = engine.New(cp)
		v.rules = append(v.rules, r)
	}
	s.cur.Store(v)
	return v.id, nil
}

// Explain 返回结论与决定它的规则；所有字段来自同一次版本加载。
func (s *Set) Explain(path string) Explanation {
	v := s.cur.Load()
	ex := Explanation{Decision: Undecided, Index: -1, Version: v.id}
	for i, r := range v.rules {
		if r.m.Match(path) {
			ex.Decision, ex.Rule, ex.Index = Include, r.raw, i
			if r.exclude {
				ex.Decision = Exclude
			}
		}
	}
	return ex
}

// Decide 返回路径的结论（最后命中者胜，无命中则未决）。
func (s *Set) Decide(path string) Decision { return s.Explain(path).Decision }
