package matcher

import (
	"fmt"
	"strings"
)

func (m *Matcher) logf(format string, args ...any) {
	if m.log != nil {
		m.log.Printf(format, args...)
	}
}

// describePattern 以稳定的文本形式打印模式的节点类型、边类型与约束。
func describePattern(p *Pattern) string {
	var b strings.Builder
	b.WriteString("nodes=[")
	for i, name := range orderedNames(p) {
		if i > 0 {
			b.WriteString(", ")
		}
		node := p.node(name)
		fmt.Fprintf(&b, "%s:%s", name, orAny(node.Type))
		for _, constraint := range node.Constraints {
			fmt.Fprintf(&b, "{%s %s %v}", constraint.Attr, constraint.Op, constraint.Value)
		}
	}
	b.WriteString("] edges=[")
	for i, edge := range p.sortedEdges() {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s -%s-> %s", edge.Source, edge.Type, edge.Target)
	}
	b.WriteString("]")
	return b.String()
}

func orAny(s string) string {
	if s == "" {
		return "*"
	}
	return s
}

func (p *Pattern) node(name string) NodeVar {
	for _, node := range p.Nodes {
		if node.Name == name {
			return node
		}
	}
	return NodeVar{}
}

// sortedEdges 返回按 (源, 边类型, 目标) 排序的边，保证日志输出确定。
func (p *Pattern) sortedEdges() []EdgePat {
	edges := append([]EdgePat(nil), p.Edges...)
	sortEdges(edges)
	return edges
}

func sortEdges(edges []EdgePat) {
	for i := 1; i < len(edges); i++ {
		for j := i; j > 0 && edgeLess(edges[j], edges[j-1]); j-- {
			edges[j], edges[j-1] = edges[j-1], edges[j]
		}
	}
}

func edgeLess(a, b EdgePat) bool {
	if a.Source != b.Source {
		return a.Source < b.Source
	}
	if a.Type != b.Type {
		return a.Type < b.Type
	}
	return a.Target < b.Target
}

func describeEmbedding(p *Pattern, emb map[string]string) string {
	var b strings.Builder
	for i, name := range orderedNames(p) {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s=%s", name, emb[name])
	}
	return b.String()
}

func (m *Matcher) logPatternStart(p *Pattern, s *searchState) {
	m.logf("match START pattern={%s} candidates=%v order=%v",
		describePattern(p), s.stats.CandidateCount, s.order)
}

func (m *Matcher) logMatch(p *Pattern, emb map[string]string, key string) {
	m.logf("match ACCEPT binding={%s} reason=node-types+constraints+all-edges-satisfied canonical=%s",
		describeEmbedding(p, emb), key)
}

func (m *Matcher) logDuplicate(p *Pattern, emb map[string]string, key string) {
	m.logf("match DEDUP  binding={%s} reason=isomorphic-canonical-key-seen canonical=%s",
		describeEmbedding(p, emb), key)
}

func (m *Matcher) logPatternRejected(p *Pattern, err error) {
	m.logf("match REJECT pattern={%s} reason=%v", describePattern(p), err)
}

func (m *Matcher) logSummary(p *Pattern, s *searchState) {
	m.logf("match DONE  pattern={%s} accepted=%d embeddings=%d isomorphic-duplicates=%d "+
		"pruned-constraint=%d pruned-edge=%d backtracks=%d",
		describePattern(p), len(s.matches), s.stats.Embeddings, s.stats.IsomorphicDuplicates,
		s.stats.PrunedByConstraint, s.stats.PrunedByEdge, s.stats.Backtracks)
}
