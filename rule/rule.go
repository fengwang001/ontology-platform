// Package rule 编译脱敏规则为路径 trie，并提供结构匹配驱动。
package rule

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync/atomic"
)

var (
	// ErrConflict 表示同一路径被配置了互相矛盾的脱敏方式。
	ErrConflict = errors.New("rule: conflicting actions on same path")
	ErrPattern  = errors.New("rule: invalid value pattern")
	ErrAction   = errors.New("rule: invalid action/parameter")
)

type Kind string

const (
	Replace  Kind = "replace"
	Hash     Kind = "hash"
	Truncate Kind = "truncate"
)

// Action 是命中后的脱敏方式。N 仅 truncate 使用，Replacement 仅 replace 使用。
type Action struct {
	Kind        Kind
	Replacement string
	N           int
}

type Rule struct {
	Path        string
	ValueRegexp string
	Action      Action
}

type alias struct {
	target *node
	segs   []string
}

type node struct {
	action   *Action
	ruleID   int
	children map[string]*node
	literals map[string]*alias
}

// Set 是编译后的只读规则集，可被多协程并发使用；计数器为原子量。
type Set struct {
	root         *node
	values       []valueRule
	MatchLookups atomic.Int64
}

type valueRule struct {
	re     *regexp.Regexp
	action Action
}

func (v valueRule) Regexp() *regexp.Regexp { return v.re }
func (v valueRule) Action() Action         { return v.action }

func (s *Set) Root() *node { return s.root }

func (s *Set) ValueRules() []valueRule { return s.values }

func (s *Set) MatchKey(n *node, key string) (child *node, isLiteral bool) {
	if n == nil {
		return nil, false
	}
	s.MatchLookups.Add(1)
	if a, ok := n.literals[key]; ok {
		return a.target, true
	}
	return n.children[key], false
}

type Node = node

func (n *node) Action() *Action { return n.action }
func (n *node) RuleID() int     { return n.ruleID }
func (n *node) Child(seg string) *node {
	if n == nil {
		return nil
	}
	return n.children[seg]
}

func (n *node) Aliases() map[string]*alias {
	if n == nil {
		return nil
	}
	return n.literals
}

type AliasView = alias

func (a *alias) Target() *node  { return a.target }
func (a *alias) Segs() []string { return a.segs }

// ArrayEnter 返回数组第 i 个元素的规则节点：优先精确 "[i]"，其次通配 "[*]"。
func (s *Set) ArrayEnter(n *node, i int) *node {
	if n == nil || len(n.children) == 0 {
		return nil
	}
	s.MatchLookups.Add(1)
	if c, ok := n.children[fmt.Sprintf("[%d]", i)]; ok {
		return c
	}
	return n.children["[*]"]
}

// Compile 校验并预编译规则集：检出同路径动作冲突、非法正则与非法参数。
func Compile(rules []Rule) (*Set, error) {
	s := &Set{root: &node{children: map[string]*node{}, literals: map[string]*alias{}}}
	type placed struct {
		path string
		act  Action
	}
	done := map[string]placed{}
	for idx, r := range rules {
		switch r.Action.Kind {
		case Replace:
			if r.Action.Replacement == "" {
				return nil, fmt.Errorf("%w: replace needs replacement (#%d %q)", ErrAction, idx, r.Path)
			}
		case Truncate:
			if r.Action.N <= 0 {
				return nil, fmt.Errorf("%w: truncate needs N>0 (#%d %q)", ErrAction, idx, r.Path)
			}
		case Hash:
		default:
			return nil, fmt.Errorf("%w: unknown kind %q", ErrAction, r.Action.Kind)
		}
		if r.ValueRegexp != "" {
			re, err := regexp.Compile(r.ValueRegexp)
			if err != nil {
				return nil, fmt.Errorf("%w: %q: %v", ErrPattern, r.ValueRegexp, err)
			}
			s.values = append(s.values, valueRule{re: re, action: r.Action})
			continue
		}
		segs, err := ParsePath(r.Path)
		if err != nil {
			return nil, err
		}
		if prev, ok := done[r.Path]; ok && prev.act.Kind != r.Action.Kind {
			return nil, fmt.Errorf("%w: %q has %s and %s", ErrConflict, r.Path, prev.act.Kind, r.Action.Kind)
		}
		done[r.Path] = placed{r.Path, r.Action}
		insert(s.root, segs, r.Action, idx)
	}
	return s, nil
}

func insert(root *node, segs []string, act Action, id int) {
	cur := root
	for _, seg := range segs {
		child, ok := cur.children[seg]
		if !ok {
			child = &node{children: map[string]*node{}, literals: map[string]*alias{}}
			cur.children[seg] = child
		}
		cur = child
	}
	cur.action, cur.ruleID = &act, id
	// 多段路径在根登记整键别名：记录里若键名就叫 "a.b"，按字面量优先解释。
	if len(segs) > 1 {
		segsCopy := append([]string(nil), segs...)
		root.literals[strings.Join(segs, ".")] = &alias{target: cur, segs: segsCopy}
		// 每个含点的中间层也登记别名（如 a.b.c 在节点 a 下登记键 "b.c"）。
		for end := 2; end < len(segs); end++ {
			parent := root
			for k := 0; k < end-1; k++ {
				parent = parent.children[segs[k]]
			}
			key := strings.Join(segs[end-1:], ".")
			parent.literals[key] = &alias{target: cur, segs: append([]string(nil), segs[end-1:]...)}
		}
	}
}

// ParsePath 按 '.' 分段，支持 `\.`（字面点）与 `\\`（字面反斜杠）转义。
func ParsePath(p string) ([]string, error) {
	var segs []string
	var cur strings.Builder
	for i := 0; i < len(p); i++ {
		switch p[i] {
		case '.':
			segs = append(segs, cur.String())
			cur.Reset()
		case '\\':
			i++
			if i >= len(p) {
				return nil, fmt.Errorf("%w: dangling escape in %q", ErrAction, p)
			}
			cur.WriteByte(p[i])
		default:
			cur.WriteByte(p[i])
		}
	}
	segs = append(segs, cur.String())
	for _, seg := range segs {
		if seg == "" {
			return nil, fmt.Errorf("%w: empty segment in %q", ErrAction, p)
		}
	}
	return segs, nil
}
