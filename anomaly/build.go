package anomaly

import (
	"fmt"
	"sort"
)

// addEdge 合并同一对事务之间的边，why 保留首次出现的人类可读说明。
func (g *graph) addEdge(from, to int, mask EdgeMask, why string) {
	if g.adj[from] == nil {
		g.adj[from] = make(map[int]EdgeMask)
	}
	if _, exists := g.adj[from][to]; !exists {
		g.edges = append(g.edges, edge{from: from, to: to, mask: mask, why: why})
	} else {
		for i := range g.edges {
			if g.edges[i].from == from && g.edges[i].to == to {
				g.edges[i].mask |= mask
				break
			}
		}
	}
	g.adj[from][to] |= mask
}

type badRead struct {
	category string // G1a 或 G1b
	reader   int
	key      string
	version  [2]int
	writer   int
}

// buildGraph 依据版本次序与读操作，在已提交事务之间构建三类边。
// 只有版本次序内的版本参与建边；同时按确定顺序（读者编号、键、
// 版本、写者）收集 G1a/G1b 现象。
func buildGraph(h History, m *model) (*graph, []badRead) {
	g := &graph{adj: make(map[int]map[int]EdgeMask)}
	for id := range m.committed {
		if m.committed[id] {
			g.nodes = append(g.nodes, id)
		}
	}
	sort.Ints(g.nodes)

	var bad []badRead

	// 先收集坏读现象，保证 G1a 先于 G1b 且结果与输入排列无关。
	for _, r := range m.reads {
		if !m.committed[r.reader] {
			continue
		}
		if r.version == [2]int{0, 0} {
			continue
		}
		writer := m.writers[r.key][r.version]
		if !m.committed[writer] {
			bad = append(bad, badRead{
				category: "G1a", reader: r.reader, key: r.key,
				version: r.version, writer: writer,
			})
			continue
		}
		if writer != r.reader {
			last := m.lastWrite[r.key][writer]
			if r.version[1] != last {
				bad = append(bad, badRead{
					category: "G1b", reader: r.reader, key: r.key,
					version: r.version, writer: writer,
				})
			}
		}
	}
	sort.Slice(bad, func(i, j int) bool {
		if bad[i].category != bad[j].category {
			return bad[i].category < bad[j].category
		}
		if bad[i].reader != bad[j].reader {
			return bad[i].reader < bad[j].reader
		}
		if bad[i].key != bad[j].key {
			return bad[i].key < bad[j].key
		}
		if bad[i].version != bad[j].version {
			return bad[i].version[0] < bad[j].version[0] ||
				bad[i].version[0] == bad[j].version[0] && bad[i].version[1] < bad[j].version[1]
		}
		return bad[i].writer < bad[j].writer
	})

	// 按键的字典序处理，边说明因此稳定可复现。
	keys := make([]string, 0, len(m.order))
	for k := range m.order {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		versions := m.order[k]

		// 写写边：次序中相邻的两个具体版本，写者不同且都已提交。
		for i := 1; i+1 < len(versions); i++ {
			a, b := versions[i], versions[i+1]
			ta, tb := a[0], b[0]
			if ta != tb && m.committed[ta] && m.committed[tb] {
				g.addEdge(ta, tb, EdgeWW, fmt.Sprintf(
					"key %q: WW 相邻版本 (%d,%d)->(%d,%d)", k, a[0], a[1], b[0], b[1]))
			}
		}

		// 建一个「版本 -> 其后继写者」的索引，供读写边使用。
		succWriter := make(map[[2]int]int)
		for i := 0; i+1 < len(versions); i++ {
			succWriter[versions[i]] = versions[i+1][0]
		}

		// 对本键的每个已提交读者处理写读边与读写边。
		for _, r := range m.reads {
			if r.key != k || !m.committed[r.reader] {
				continue
			}
			reader := r.reader
			if r.version != [2]int{0, 0} {
				writer := m.writers[k][r.version]
				if m.committed[writer] && writer != reader {
					g.addEdge(writer, reader, EdgeWR, fmt.Sprintf(
						"key %q: WR T%d 读到 T%d 的版本 (%d,%d)",
						k, reader, writer, r.version[0], r.version[1]))
				}
			}
			sw, ok := succWriter[r.version]
			if ok && m.committed[sw] && sw != reader {
				g.addEdge(reader, sw, EdgeRW, fmt.Sprintf(
					"key %q: RW T%d 读版本后 T%d 的版本紧邻其后", k, reader, sw))
			}
		}
	}

	sort.Slice(g.edges, func(i, j int) bool {
		if g.edges[i].from != g.edges[j].from {
			return g.edges[i].from < g.edges[j].from
		}
		return g.edges[i].to < g.edges[j].to
	})
	return g, bad
}

func edgeName(mask EdgeMask) string {
	var s []byte
	if mask&EdgeWW != 0 {
		s = append(s, 'W', 'W')
	}
	if mask&EdgeWR != 0 {
		if len(s) > 0 {
			s = append(s, '+')
		}
		s = append(s, 'W', 'R')
	}
	if mask&EdgeRW != 0 {
		if len(s) > 0 {
			s = append(s, '+')
		}
		s = append(s, 'R', 'W')
	}
	return string(s)
}
