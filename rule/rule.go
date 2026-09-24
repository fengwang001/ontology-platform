// Package rule 实现脱敏规则定义、转义路径解析与规则集预编译（trie）。
package rule

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync/atomic"
)

// Action 是脱敏动作。
type Action int

const (
	Replace   Action = iota // 固定串替换
	Hash                    // SHA-256 哈希
	Truncate                // 保留前 KeepN 个 rune
)

// Spec 是一条外部规则描述。Path 与 Pattern 二选一非空。
type Spec struct {
	Path    string
	Pattern string
	Action  Action
	KeepN   int
}

// Rule 是编译后的规则。
type Rule struct {
	Segments []string
	Pattern  *regexp.Regexp
	Action   Action
	KeepN    int
}

var (
	// ErrConflict 同一路径配置了互斥动作（replace 与 hash）。
	ErrConflict = errors.New("rule: conflicting rules on same path")
	// ErrPattern 值模式正则非法。
	ErrPattern = errors.New("rule: invalid pattern")
)

type node struct {
	children map[string]*node
	rules    []*Rule
}

// Set 是编译后的规则集，路径规则进 trie，值规则进 regexp 列表。
type Set struct {
	root        *node
	valueRules  []*Rule
	pathLookups atomic.Int64
}

// ParsePath 按 . 分层，\. 为字面点，\\ 为字面反斜杠。
func ParsePath(p string) []string {
	var segs []string
	var cur strings.Builder
	esc := false
	for _, r := range p {
		switch {
		case esc:
			cur.WriteRune(r)
			esc = false
		case r == '\\':
			esc = true
		case r == '.':
			segs = append(segs, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	if p != "" {
		segs = append(segs, cur.String())
	}
	return segs
}

// Compile 预编译规则集；同路径 replace 与 hash 冲突即报 ErrConflict。
func Compile(specs []Spec) (*Set, error) {
	s := &Set{root: &node{children: map[string]*node{}}}
	byPath := map[string]Spec{}
	index := map[string]int{}
	for i, sp := range specs {
		switch {
		case sp.Path != "":
			key := strings.Join(ParsePath(sp.Path), "\x00")
			if prev, ok := byPath[key]; ok {
				if (prev.Action == Replace && sp.Action == Hash) ||
					(prev.Action == Hash && sp.Action == Replace) {
					return nil, fmt.Errorf("%w: path=%q rule#%d(replace/hash) vs rule#%d",
						ErrConflict, sp.Path, index[key], i)
				}
			} else {
				byPath[key] = sp
				index[key] = i
			}
			r := &Rule{Segments: ParsePath(sp.Path), Action: sp.Action, KeepN: sp.KeepN}
			insert(s.root, r)
		case sp.Pattern != "":
			re, err := regexp.Compile(sp.Pattern)
			if err != nil {
				return nil, fmt.Errorf("%w: %q: %v", ErrPattern, sp.Pattern, err)
			}
			s.valueRules = append(s.valueRules, &Rule{Pattern: re, Action: sp.Action, KeepN: sp.KeepN})
		}
	}
	return s, nil
}

func insert(n *node, r *Rule) {
	for _, seg := range r.Segments {
		c := n.children[seg]
		if c == nil {
			c = &node{children: map[string]*node{}}
			n.children[seg] = c
		}
		n = c
	}
	n.rules = append(n.rules, r)
}

// LookupLookups 返回每条记录路径匹配累计计数（非导出计数器的只读快照）。
func (s *Set) LookupLookups() int64 { return s.pathLookups.Load() }

// ResetLookups 清零计数器。
func (s *Set) ResetLookups() { s.pathLookups.Store(0) }

// PathHit 是一条路径规则在记录中的命中点。
type PathHit struct {
	Segments []string
	Rule     *Rule
}

// ValueRules 返回值模式规则。
func (s *Set) ValueRules() []*Rule { return s.valueRules }

// Walk 仅沿映射下钻遍历记录，产出路径规则命中。
// 每个被访问字段恰计 1 次 trie 查表，故计数 = 字段数 + O(1)，与规则数无关。
// 路径中某层不是映射或路径不存在均静默不命中。数组不进入路径规则。
func (s *Set) Walk(fields map[string]any) []PathHit {
	var hits []PathHit
	type stFrame struct {
		m    map[string]any
		n    *node
		path []string
	}
	stack := []stFrame{{m: fields, n: s.root}}
	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for k, v := range f.m {
			s.pathLookups.Add(1)
			cn, ok := f.n.children[k]
			if !ok {
				continue
			}
			path := append(append([]string{}, f.path...), k)
			for _, r := range cn.rules {
				hits = append(hits, PathHit{Segments: append([]string{}, path...), Rule: r})
			}
			if cm, ok := v.(map[string]any); ok {
				stack = append(stack, stFrame{m: cm, n: cn, path: path})
			}
		}
	}
	return hits
}
