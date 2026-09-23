// Package set 持有多条通配规则，按「最后命中者胜」给出包含/排除结论，
// 支持原子热替换与无锁并发查询。
package set

import (
	"errors"
	"sync"
	"sync/atomic"

	"ontology/engine"
	"ontology/syntax"
)

// Verdict 是查询结论；Undecided 与 Excluded 必须可区分。
type Verdict int

const (
	Undecided Verdict = iota
	Include
	Exclude
)

// ErrTooManyRules：规则条数超限。
var ErrTooManyRules = errors.New("set: too many rules")

// DefaultMaxRules 是规则集默认条数上限。
const DefaultMaxRules = 10000

// Options 为可选上限；零值字段采用默认值。
type Options struct {
	MaxRules      int
	MaxBytes      int
	MaxDoubleStar int
}

// Rule 是一条已编译规则。
type Rule struct {
	Raw     string
	Exclude bool // 原文以 '!' 开头
	Pat     *syntax.Pattern
}

type version struct {
	n     int
	rules []Rule
}

// Set 是可热替换的规则集。
type Set struct {
	cur atomic.Pointer[version]
	mu  sync.Mutex
	opt Options
}

// New 编译初始规则集；失败时返回错误且不产生部分状态。
func New(raws []string, opt *Options) (*Set, error) {
	s := &Set{}
	if opt != nil {
		s.opt = *opt
	}
	v, err := s.compile(raws, 1)
	if err != nil {
		return nil, err
	}
	s.cur.Store(v)
	return s, nil
}

// Explanation 是 Explain 的结果；字段全部来自同一个版本快照。
type Explanation struct {
	Verdict Verdict
	Index   int    // 决定结论的规则序号（从 0 起）；未决时为 -1
	Rule    string // 该规则原文；未决时为空
	Version int    // 本次查询读到的版本号
}

// Replace 原子替换规则集：任一规则编译失败则保留旧版本并返回错误。
func (s *Set) Replace(raws []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	nv := s.cur.Load().n + 1
	v, err := s.compile(raws, nv)
	if err != nil {
		return err
	}
	s.cur.Store(v)
	return nil
}

// Version 返回当前版本号。
func (s *Set) Version() int { return s.cur.Load().n }

// Decide 只返回结论。
func (s *Set) Decide(path string) Verdict { return s.Explain(path).Verdict }

// Explain 返回结论及决定它的规则原文、序号、版本；一次查询只读一次快照。
func (s *Set) Explain(path string) Explanation {
	v := s.cur.Load()
	e := Explanation{Verdict: Undecided, Index: -1, Version: v.n}
	for i, r := range v.rules {
		if engine.Match(r.Pat, path) {
			e.Index = i
			e.Rule = r.Raw
			if r.Exclude {
				e.Verdict = Exclude
			} else {
				e.Verdict = Include
			}
		}
	}
	return e
}

func (s *Set) compile(raws []string, n int) (*version, error) {
	maxRules := DefaultMaxRules
	if s.opt.MaxRules > 0 {
		maxRules = s.opt.MaxRules
	}
	if len(raws) > maxRules {
		return nil, ErrTooManyRules
	}
	lim := syntax.Limits{MaxBytes: s.opt.MaxBytes, MaxDoubleStar: s.opt.MaxDoubleStar}
	rules := make([]Rule, 0, len(raws))
	for _, raw := range raws {
		body, exclude := raw, false
		if len(raw) > 0 && raw[0] == '!' {
			body, exclude = raw[1:], true
		}
		pat, err := syntax.Compile(body, &lim)
		if err != nil {
			return nil, err
		}
		rules = append(rules, Rule{Raw: raw, Exclude: exclude, Pat: pat})
	}
	return &version{n: n, rules: rules}, nil
}
