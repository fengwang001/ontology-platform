// Package rule 编译脱敏规则：路径 trie + 值正则模式。
package rule

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync/atomic"
)

// ErrConflict 表示同一路径配置了互相矛盾的动作。
var ErrConflict = errors.New("rule: conflicting actions on same path")

// Action 为脱敏方式。
type Action int

const (
	Replace Action = iota + 1
	Hash
	Truncate
)

// Rule 是一条脱敏规则。Path 为空表示按值模式匹配。
type Rule struct {
	Name    string
	Path    string
	Pattern string
	Action  Action
	Arg     int // truncate 的保留 rune 数
	Repl    string
}

// Set 是编译后的规则集。
type Set struct {
	root    *trieNode
	values  []valueRule
	pathCnt atomic.Int64 // 每条记录路径匹配次数（trie 边访问次数）
}

type valueRule struct {
	re     *regexp.Regexp
	action Action
	arg    int
	repl   string
}

type trieNode struct {
	children map[string]*trieNode
	action   Action
	arg      int
	repl     string
	hasRule  bool
}

// Compile 编译规则并检出同路径冲突。
func Compile(rules []Rule) (*Set, error) {
	s := &Set{root: &trieNode{children: map[string]*trieNode{}}}
	paths := map[string]Rule{}
	for _, r := range rules {
		if r.Path == "" {
			re, err := regexp.Compile(r.Pattern)
			if err != nil {
				return nil, fmt.Errorf("rule: bad pattern %q: %w", r.Pattern, err)
			}
			s.values = append(s.values, valueRule{re: re, action: r.Action, arg: r.Arg, repl: r.Repl})
			continue
		}
		if old, ok := paths[r.Path]; ok && old.Action != r.Action {
			return nil, fmt.Errorf("%w: path %q rules %q(%d) vs %q(%d)",
				ErrConflict, r.Path, old.Name, old.Action, r.Name, r.Action)
		}
		paths[r.Path] = r
		s.insert(r)
	}
	return s, nil
}

// insert 同时挂两条边：字面全名键（字段名可含点）与嵌套首段。
// 命中歧义由 mask 遍历时的查找顺序消解：先嵌套、后字面。
func (s *Set) insert(r Rule) {
	s.root.children[r.Path] = mkTerminal(s.root.children[r.Path], r)
	node := s.root
	for _, seg := range strings.Split(r.Path, ".") {
		next := node.children[seg]
		if next == nil {
			next = &trieNode{children: map[string]*trieNode{}}
			node.children[seg] = next
		}
		node = next
	}
	node.action, node.arg, node.repl, node.hasRule = r.Action, r.Arg, r.Repl, true
}

func mkTerminal(n *trieNode, r Rule) *trieNode {
	if n == nil {
		n = &trieNode{children: map[string]*trieNode{}}
	}
	n.action, n.arg, n.repl, n.hasRule = r.Action, r.Arg, r.Repl, true
	return n
}

// MatchCount 返回自上次重置以来的路径匹配计数。
func (s *Set) MatchCount() int64 { return s.pathCnt.Load() }

// ResetCount 清零计数器。
func (s *Set) ResetCount() { s.pathCnt.Store(0) }

// Lookup 查询单个路径段序列命中的动作（用于 mask 遍历）。
// segs 为数据树当前位置到根的段序列（反向），此处按正向传入更方便。
type Hit struct {
	Action Action
	Arg    int
	Repl   string
}

// Trie 暴露根节点供 mask 同步下钻。
func (s *Set) Trie() *TrieNode { return s.root }

// TrieNode 是编译后路径 trie 的节点。
type TrieNode = trieNode

// Child 取 trie 子节点（通配 "*" 用于数组），未命中返回 nil。
func (n *TrieNode) Child(seg string) *TrieNode {
	if n == nil {
		return nil
	}
	return n.children[seg]
}

// Terminal 返回该节点是否挂有动作。
func (n *TrieNode) Terminal() (Hit, bool) {
	if n != nil && n.hasRule {
		return Hit{Action: n.action, Arg: n.arg, Repl: n.repl}, true
	}
	return Hit{}, false
}

// ValueRules 返回值模式规则。
func (s *Set) ValueRules() []valueRule { return s.values }

// Match 暴露给同包测试使用的值匹配判定。
func (vr valueRule) Match(v string) (Hit, bool) {
	if vr.re.MatchString(v) {
		return Hit{Action: vr.action, Arg: vr.arg, Repl: vr.repl}, true
	}
	return Hit{}, false
}

// MatchValue 对外的值模式匹配。
func (s *Set) MatchValue(v string) (Hit, bool) {
	for _, vr := range s.values {
		if h, ok := vr.Match(v); ok {
			return h, true
		}
	}
	return Hit{}, false
}

// Edge 记录一次 trie 边访问（mask 遍历时调用）。
func (s *Set) Edge() { s.pathCnt.Add(1) }
