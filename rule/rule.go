// Package rule 编译脱敏规则为路径 trie 与值模式集合。
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

// Action 是脱敏方式。
type Action int

const (
	ActionReplace Action = iota + 1
	ActionHash
	ActionTruncate
)

// Kind 标识规则按路径还是按值模式匹配。
type Kind int

const (
	KindPath Kind = iota + 1
	KindValue
)

// Rule 是一条用户配置的脱敏规则。
type Rule struct {
	Kind    Kind
	Path    string // KindPath；字面键优先于分段下钻
	Pattern string // KindValue 的 Go 正则
	Action  Action
	Keep    int    // ActionTruncate 保留的 rune 数
	Name    string // 规则名，用于冲突报告
}

// Entry 是编译后的命中动作。
type Entry struct {
	Action Action
	Keep   int
	Rule   string
}

// Node 是路径 trie 节点。
type Node struct {
	literals map[string]Entry // 当前层整串键（可能含点）优先
	children map[string]*Node
}

type valueRule struct {
	re    *regexp.Regexp
	entry Entry
}

type owner struct {
	name   string
	action Action
}

// Set 是编译后的规则集。
type Set struct {
	root   *Node
	values []valueRule
	count  atomic.Int64 // 每条记录的路径匹配探测累计
}

// Root 返回 trie 根节点。
func (s *Set) Root() *Node { return s.root }

// Lookup 在当前节点查键：整串字面键命中则终止；否则按分段下钻。
// ok=true 表示该键本身是规则终点。
func (n *Node) Lookup(key string) (entry Entry, child *Node, ok bool) {
	if n == nil {
		return Entry{}, nil, false
	}
	if e, hit := n.literals[key]; hit {
		return e, nil, true
	}
	c := n.children[key]
	if c == nil {
		return Entry{}, nil, false
	}
	e, end := c.literals[""]
	return e, c, end
}

// LookupOn 在节点上完成一次键探测并计数：字面探测 1 次，未命中再分段探测 1 次。
func (s *Set) LookupOn(n *Node, key string) (Entry, *Node, bool) {
	s.count.Add(1)
	e, child, ok := n.Lookup(key)
	if !ok && child == nil {
		s.count.Add(1)
	}
	return e, child, ok
}

// Matches 返回路径匹配探测累计次数。
func (s *Set) Matches() int64 { return s.count.Load() }

// MatchValue 返回所有命中该字符串值的值模式规则。
func (s *Set) MatchValue(v string) []Entry {
	var out []Entry
	for _, vr := range s.values {
		if vr.re.MatchString(v) {
			out = append(out, vr.entry)
		}
	}
	return out
}

// Compile 校验并编译规则集；同路径动作冲突返回 ErrConflict。
func Compile(rules []Rule) (*Set, error) {
	s := &Set{root: &Node{literals: map[string]Entry{}, children: map[string]*Node{}}}
	owners := map[string]owner{}
	for _, r := range rules {
		e := Entry{Action: r.Action, Keep: r.Keep, Rule: r.Name}
		switch r.Kind {
		case KindPath:
			if prev, dup := owners[r.Path]; dup && prev.action != r.Action {
				return nil, fmt.Errorf("%w: rules %q and %q on path %q",
					ErrConflict, prev.name, r.Name, r.Path)
			}
			owners[r.Path] = owner{name: r.Name, action: r.Action}
			s.addPath(r.Path, e)
		case KindValue:
			re, err := regexp.Compile(r.Pattern)
			if err != nil {
				return nil, fmt.Errorf("rule: bad pattern %q: %w", r.Name, err)
			}
			s.values = append(s.values, valueRule{re: re, entry: e})
		default:
			return nil, fmt.Errorf("rule: unknown kind in %q", r.Name)
		}
	}
	return s, nil
}

func (s *Set) addPath(path string, e Entry) {
	parts := strings.Split(path, ".")
	// 在每一级节点插入剩余后缀作为“字面键”，保证嵌套层里含点键优先命中。
	node := s.root
	for i, p := range parts {
		suffix := strings.Join(parts[i:], ".")
		if _, exists := node.literals[suffix]; !exists {
			node.literals[suffix] = e
		}
		child := node.children[p]
		if child == nil {
			child = &Node{literals: map[string]Entry{}, children: map[string]*Node{}}
			node.children[p] = child
		}
		node = child
	}
	node.literals[""] = e
}
