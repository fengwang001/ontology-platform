package matcher

import (
	"sort"
	"strconv"
	"strings"
)

// canonicalKey 计算一次嵌入在模式自同构群下的规范化键。
//
// 判定依据：嵌入 f 与 g 是同一个同构匹配，当且仅当存在模式自同构 σ
// （保持节点类型、约束签名与全部有向类型边的变量置换），使得
// 对所有变量 v 有 f(v) = g(σ(v))。规范化键取所有该类表示中的
// 字典序最小值，因此同构嵌入键相同，非同构嵌入键不同。
func canonicalKey(p *Pattern, emb map[string]string) string {
	names := orderedNames(p)
	perms := automorphisms(p, names)
	best := ""
	for _, perm := range perms {
		var b strings.Builder
		for i, name := range names {
			if i > 0 {
				b.WriteByte('|')
			}
			b.WriteString(name)
			b.WriteByte('=')
			b.WriteString(emb[perm[i]])
		}
		key := b.String()
		if best == "" || key < best {
			best = key
		}
	}
	return best
}

// automorphisms 返回模式的全部自同构（以 names 顺序表达的置换）。
// 先用节点签名 + 有向类型边的 WL 风格细化划分自同构候选类，
// 再在类内枚举排列并用完整邻接矩阵判定，避免全 n! 枚举。
func automorphisms(p *Pattern, names []string) [][]string {
	index := make(map[string]int, len(names))
	for i, name := range names {
		index[name] = i
	}

	// 有向带类型邻接矩阵：adj[a][b] 为 a -> b 的边类型集合（排序）。
	adj := make([]map[int][]string, len(names))
	for i := range adj {
		adj[i] = map[int][]string{}
	}
	for _, edge := range p.Edges {
		a, b := index[edge.Source], index[edge.Target]
		adj[a][b] = append(adj[a][b], edge.Type)
	}
	for i := range adj {
		for j := range adj[i] {
			sort.Strings(adj[i][j])
		}
	}

	// 初始标签：节点类型 + 约束签名。
	labels := make([]string, len(names))
	for i, name := range names {
		labels[i] = nodeSignature(p.node(name))
	}

	// WL 风格细化：标签同时包含自身旧标签与按邻居旧标签聚合的
	// 出/入边多重集。细化只可能合并本可区分的自同构候选
	// （过粗不会产生错误自同构，最终仍有邻接矩阵判定）。
	for {
		next := make([]string, len(names))
		for i := range names {
			outSig := neighborSignature(adj, labels, i, true)
			inSig := neighborSignature(adj, labels, i, false)
			next[i] = labels[i] + "|out:" + outSig + "|in:" + inSig
		}
		if samePartition(labels, next) {
			break
		}
		labels = next
	}

	classes := equivalenceClasses(names, labels)

	// 类内枚举排列；不同类的变量绝不互换。
	var result [][]string
	perm := make([]string, len(names))
	classIndices := make([][]int, 0, len(classes))
	for _, members := range classes {
		ids := make([]int, 0, len(members))
		for _, name := range members {
			ids = append(ids, index[name])
		}
		sort.Ints(ids)
		classIndices = append(classIndices, ids)
	}

	var enumerate func(class int)
	enumerate = func(class int) {
		if class == len(classIndices) {
			candidate := make([]string, len(names))
			for i, name := range perm {
				candidate[i] = name
			}
			if isAutomorphism(adj, candidate, names) {
				result = append(result, candidate)
			}
			return
		}
		ids := classIndices[class]
		choices := make([]string, len(ids))
		for i, id := range ids {
			choices[i] = names[id]
		}
		used := make([]bool, len(choices))
		var assign func(pos int)
		assign = func(pos int) {
			if pos == len(ids) {
				enumerate(class + 1)
				return
			}
			for i := range choices {
				if used[i] {
					continue
				}
				used[i] = true
				perm[ids[pos]] = choices[i]
				assign(pos + 1)
				used[i] = false
			}
		}
		assign(0)
	}
	enumerate(0)
	return result
}

func nodeSignature(node NodeVar) string {
	parts := []string{"t:" + node.Type}
	constraints := append([]Constraint(nil), node.Constraints...)
	sort.Slice(constraints, func(i, j int) bool {
		if constraints[i].Attr != constraints[j].Attr {
			return constraints[i].Attr < constraints[j].Attr
		}
		return constraints[i].Op < constraints[j].Op
	})
	for _, constraint := range constraints {
		parts = append(parts,
			constraint.Attr+string(constraint.Op)+valueToken(constraint.Value))
	}
	return strings.Join(parts, ";")
}

func valueToken(v any) string {
	switch n := v.(type) {
	case string:
		return "s:" + n
	case bool:
		if n {
			return "b:true"
		}
		return "b:false"
	case int:
		return "n:" + strconv.FormatInt(int64(n), 10)
	case int64:
		return "n:" + strconv.FormatInt(n, 10)
	case float64:
		return "f:" + strconv.FormatFloat(n, 'g', -1, 64)
	default:
		return "?"
	}
}

func neighborSignature(adj []map[int][]string, labels []string, node int, outgoing bool) string {
	items := make([]string, 0)
	if outgoing {
		neighbors := make([]int, 0, len(adj[node]))
		for neighbor := range adj[node] {
			neighbors = append(neighbors, neighbor)
		}
		sort.Ints(neighbors)
		for _, neighbor := range neighbors {
			for _, typ := range adj[node][neighbor] {
				items = append(items, typ+"->"+labels[neighbor])
			}
		}
	} else {
		others := make([]int, 0, len(adj))
		for other := range adj {
			if _, ok := adj[other][node]; ok {
				others = append(others, other)
			}
		}
		sort.Ints(others)
		for _, other := range others {
			for _, typ := range adj[other][node] {
				items = append(items, typ+"<-"+labels[other])
			}
		}
	}
	sort.Strings(items)
	return strings.Join(items, ",")
}

func samePartition(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	canonicalA := canonicalLabels(a)
	canonicalB := canonicalLabels(b)
	for i := range a {
		if canonicalA[i] != canonicalB[i] {
			return false
		}
	}
	return true
}

// canonicalLabels 把标签替换为“首次出现序号”，只比较划分是否相同，
// 不比较标签字面量。
func canonicalLabels(labels []string) []string {
	seen := map[string]string{}
	next := 0
	out := make([]string, len(labels))
	for i, label := range labels {
		token, ok := seen[label]
		if !ok {
			token = strconv.Itoa(next)
			seen[label] = token
			next++
		}
		out[i] = token
	}
	return out
}

func equivalenceClasses(names, labels []string) map[string][]string {
	groups := map[string][]string{}
	for i, label := range labels {
		groups[label] = append(groups[label], names[i])
	}
	return groups
}

// isAutomorphism 判定候选置换是否保持全部有向类型边
// （含边的不存在）。perm[i] 表示 names[i] 被映射到的变量名。
func isAutomorphism(adj []map[int][]string, perm, names []string) bool {
	index := make(map[string]int, len(names))
	for i, name := range names {
		index[name] = i
	}
	mapped := make([]int, len(names))
	for i, name := range perm {
		mapped[i] = index[name]
	}
	for a := range adj {
		ma := mapped[a]
		for b, typesAB := range adj[a] {
			mb := mapped[b]
			got, ok := adj[ma][mb]
			if !ok || !equalStringSet(got, typesAB) {
				return false
			}
		}
		// 反向检查：被映到的邻接不得多出边。
		for mb, got := range adj[ma] {
			sourceB := -1
			for i, m := range mapped {
				if m == mb {
					sourceB = i
					break
				}
			}
			want, ok := adj[a][sourceB]
			if !ok || !equalStringSet(got, want) {
				return false
			}
		}
	}
	return true
}

func equalStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
