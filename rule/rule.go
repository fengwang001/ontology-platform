// Package rule 负责脱敏规则的定义与预编译。
package rule

import (
	"errors"
	"fmt"
	"strings"
)

// ErrConflict 表示同一目标配置了互相冲突的脱敏方式。
var ErrConflict = errors.New("rule: conflicting masking rules")

// Method 是脱敏方式。
type Method int

const (
	Replace Method = iota + 1
	Hash
	Truncate
)

func (m Method) String() string {
	switch m {
	case Replace:
		return "replace"
	case Hash:
		return "hash"
	case Truncate:
		return "truncate"
	default:
		return "invalid"
	}
}

// Rule 是一条脱敏规则：Path 与 Value 二选一（Path 非空时按路径）。
type Rule struct {
	Name    string
	Path    string // 支持 a.b 与转义 a\.b
	Value   string // 非空表示按标量值精确匹配
	Method  Method
	Replace string // Method==Replace 时的替换串
	Keep    int    // Method==Truncate 时保留的字符数
}

type node struct {
	children map[string]*node
	action   *Rule
}

// Set 是编译后的规则集。
type Set struct {
	root  *node
	value map[string]*Rule

	// MatchCount 为导出测试用的路径匹配计数；每次 Trie 查询计一次。
	MatchCount uint64
}

// Compile 编译规则并检测冲突。
func Compile(rules []Rule) (*Set, error) {
	s := &Set{root: &node{children: map[string]*node{}}, value: map[string]*Rule{}}
	for i := range rules {
		r := rules[i]
		if err := s.add(r); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *Set) add(r Rule) error {
	if r.Method < Replace || r.Method > Truncate {
		return errors.New("rule: unknown method")
	}
	if r.Value != "" {
		if old, ok := s.value[r.Value]; ok && old.Method != r.Method {
			return conflictErr(old, &r)
		}
		s.value[r.Value] = &r
		return nil
	}
	segs := SplitPath(r.Path)
	cur := s.root
	for _, seg := range segs {
		next := cur.children[seg]
		if next == nil {
			next = &node{children: map[string]*node{}}
			cur.children[seg] = next
		}
		cur = next
	}
	if cur.action != nil && cur.action.Method != r.Method {
		return conflictErr(cur.action, &r)
	}
	cur.action = &r
	return nil
}

// Lookup 按路径段序列查询动作；无规则时返回 nil。
func (s *Set) Lookup(segs []string) *Rule {
	s.MatchCount++
	cur := s.root
	for _, seg := range segs {
		next := cur.children[seg]
		if next == nil {
			return nil
		}
		cur = next
	}
	return cur.action
}

// LookupValue 查询按值匹配的动作。
func (s *Set) LookupValue(v string) *Rule {
	return s.value[v]
}

// SplitPath 将规则路径切分为段，`\.` 表示键内字面点号。
func SplitPath(p string) []string {
	var segs []string
	var cur strings.Builder
	for i := 0; i < len(p); i++ {
		if p[i] == '\\' && i+1 < len(p) && p[i+1] == '.' {
			cur.WriteByte('.')
			i++
			continue
		}
		if p[i] == '.' {
			segs = append(segs, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteByte(p[i])
	}
	segs = append(segs, cur.String())
	return segs
}

func conflictErr(a, b *Rule) error {
	return fmt.Errorf("%w: %s(%s) vs %s(%s)", ErrConflict,
		a.Name, a.Method.String(), b.Name, b.Method.String())
}
