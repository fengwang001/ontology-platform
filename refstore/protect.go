package refstore

import "strings"

// Rule 是一条受保护规则。Pattern 按分隔符分段匹配引用名，
// 分段 "*" 匹配任意单个分段，其余分段为字面量。
// 四种限制相互独立；多条规则命中同一引用时各限制取并集，
// 名单限制取所有命中规则名单的并集。
type Rule struct {
	Pattern   string
	NoCreate  bool     // 禁止创建
	NoDelete  bool     // 禁止删除
	NoNonFF   bool     // 禁止非快进
	AllowOnly bool     // 仅限名单内用户更新
	Users     []string // 名单，仅 AllowOnly 为 true 时有效
}

// Restrictions 是命中某引用的全部规则的限制并集。
type Restrictions struct {
	NoCreate  bool
	NoDelete  bool
	NoNonFF   bool
	AllowOnly bool
	allowed   map[string]bool
}

// Allows 报告用户是否被允许更新（未开启名单限制时人人允许）。
func (r Restrictions) Allows(user string) bool {
	if !r.AllowOnly {
		return true
	}
	return r.allowed[user]
}

func (r *Restrictions) merge(rule Rule) {
	r.NoCreate = r.NoCreate || rule.NoCreate
	r.NoDelete = r.NoDelete || rule.NoDelete
	r.NoNonFF = r.NoNonFF || rule.NoNonFF
	if rule.AllowOnly {
		r.AllowOnly = true
		if r.allowed == nil {
			r.allowed = make(map[string]bool, len(rule.Users))
		}
		for _, u := range rule.Users {
			r.allowed[u] = true
		}
	}
}

type trieNode struct {
	children map[string]*trieNode
	star     *trieNode
	rules    []int // 终止于该节点的规则下标
}

// RuleIndex 用分段 trie 索引规则：匹配时沿引用名的分段同时走
// 字面孩子与 "*" 孩子，访问的节点数只取决于命中路径，
// 不随规则总数线性增长。
type RuleIndex struct {
	root  *trieNode
	rules []Rule
	// LastVisited 记录最近一次匹配访问的 trie 节点数，用于性能验证。
	LastVisited int
}

// NewRuleIndex 由规则快照构建索引。规则更新时整体重建，
// 匹配开销与规则总数无关，重建开销不在裁决路径上被放大。
func NewRuleIndex(rules []Rule) *RuleIndex {
	ix := &RuleIndex{root: &trieNode{}, rules: rules}
	for i, r := range rules {
		n := ix.root
		for _, seg := range strings.Split(r.Pattern, "/") {
			if seg == "*" {
				if n.star == nil {
					n.star = &trieNode{}
				}
				n = n.star
			} else {
				if n.children == nil {
					n.children = make(map[string]*trieNode)
				}
				c, ok := n.children[seg]
				if !ok {
					c = &trieNode{}
					n.children[seg] = c
				}
				n = c
			}
		}
		n.rules = append(n.rules, i)
	}
	return ix
}

// Match 返回命中 ref 的全部规则的限制并集。
func (ix *RuleIndex) Match(ref string) Restrictions {
	ix.LastVisited = 0
	segs := strings.Split(ref, "/")
	var res Restrictions
	var walk func(n *trieNode, depth int)
	walk = func(n *trieNode, depth int) {
		ix.LastVisited++
		if depth == len(segs) {
			for _, ri := range n.rules {
				res.merge(ix.rules[ri])
			}
			return
		}
		if c, ok := n.children[segs[depth]]; ok {
			walk(c, depth+1)
		}
		if n.star != nil {
			walk(n.star, depth+1)
		}
	}
	walk(ix.root, 0)
	return res
}
