package ontology

import (
	"sort"
	"testing"
)

// naiveScenario 是朴素参照模型直接消费的场景描述。
// 它刻意不依赖生产代码的快照结构，以保证两份实现相互独立。
type naiveScenario struct {
	objects  []string
	links    []naiveLink
	exist    map[string]bool // 存在性权限
	traverse map[string]bool // 遍历权限
}

type naiveLink struct {
	id    string
	from  string
	to    string
	bidir bool
}

// naiveHasCycle 独立朴素实现：
//  1. 先按存在性权限过滤对象，并连带剔除端点不可见的链接；
//  2. 再按遍历权限过滤链接；
//  3. 在得到的有向邻接（平行边/双向边折叠为集合）上穷举全部简单环；
//  4. 自环长度 1、u<->v 长度 2 自然从邻接集合中产生。
//
// 复杂度为朴素指数级，仅供小规模随机场景下与生产实现做结果对照。
func naiveHasCycle(s naiveScenario) (bool, []string) {
	visible := map[string]bool{}
	var verts []string
	for _, o := range s.objects {
		if s.exist[o] {
			visible[o] = true
			verts = append(verts, o)
		}
	}
	sort.Strings(verts)
	if len(verts) == 0 {
		return false, nil
	}
	adj := map[string]map[string]bool{}
	for _, v := range verts {
		adj[v] = map[string]bool{}
	}
	for _, l := range s.links {
		if !visible[l.from] || !visible[l.to] { // 阶段 1 连带剔除
			continue
		}
		if !s.traverse[l.id] { // 阶段 2
			continue
		}
		adj[l.from][l.to] = true
		if l.bidir {
			adj[l.to][l.from] = true
		}
	}

	var best []string
	take := func(cyc []string) {
		cp := append([]string(nil), cyc...)
		sort.Strings(cp)
		if best == nil || len(cp) < len(best) || (len(cp) == len(best) && lexLess(cp, best)) {
			best = cp
		}
	}

	// 穷举：自每个顶点出发的所有自避路径，回到起点即得到一个简单环。
	// 用“起点必须是环上最小顶点”来去重，避免同一环被重复报告。
	for _, start := range verts {
		var dfs func(cur string, path []string, onPath map[string]bool)
		dfs = func(cur string, path []string, onPath map[string]bool) {
			nexts := make([]string, 0, len(adj[cur]))
			for n := range adj[cur] {
				nexts = append(nexts, n)
			}
			sort.Strings(nexts)
			for _, n := range nexts {
				if n == start {
					take(path)
					continue
				}
				if n < start || onPath[n] {
					continue
				}
				onPath[n] = true
				dfs(n, append(path, n), onPath)
				delete(onPath, n)
			}
		}
		dfs(start, []string{start}, map[string]bool{start: true})
	}
	return best != nil, best
}

// verifyEvidenceIsRealCycle 独立验证生产实现返回的证据集合本身确实构成一个环，
// 且不含与环无关的对象（证据即环上全部顶点，不多不少）。
func verifyEvidenceIsRealCycle(t *testing.T, s naiveScenario, ev []string) {
	t.Helper()
	want := map[string]bool{}
	for _, v := range ev {
		want[v] = true
	}
	adj := map[string]map[string]bool{}
	for _, l := range s.links {
		if !s.exist[l.from] || !s.exist[l.to] || !s.traverse[l.id] {
			continue
		}
		if adj[l.from] == nil {
			adj[l.from] = map[string]bool{}
		}
		adj[l.from][l.to] = true
		if l.bidir {
			if adj[l.to] == nil {
				adj[l.to] = map[string]bool{}
			}
			adj[l.to][l.from] = true
		}
	}
	if len(ev) == 1 {
		v := ev[0]
		if !adj[v][v] {
			t.Fatalf("evidence %v claimed self-loop but none exists", ev)
		}
		return
	}
	// 长度 k>=2：证据中每个顶点在证据内部都必须有入边与出边，
	// 且整体可沿方向走成一条回到起点的闭合序列（存在通过全部证据顶点的环）。
	if len(ev) < 2 {
		t.Fatalf("bad evidence %v", ev)
	}
	used := permutationCycle(ev, adj)
	if used == nil {
		t.Fatalf("evidence %v does not form a directed cycle", ev)
	}
	for _, v := range used {
		if !want[v] {
			t.Fatalf("cycle used non-evidence vertex %v", v)
		}
	}
	if len(used) != len(ev) {
		t.Fatalf("evidence contains unrelated objects: %v vs %v", used, ev)
	}
}

// permutationCycle 在给定顶点集合上尝试找到一条有向哈密顿环（证据顶点数
// 很少，朴素回溯即可），用于独立校验“证据集合本身构成一个环”。
func permutationCycle(ev []string, adj map[string]map[string]bool) []string {
	used := make([]string, 0, len(ev))
	onPath := map[string]bool{}
	var dfs func(v string) bool
	dfs = func(v string) bool {
		used = append(used, v)
		onPath[v] = true
		if len(used) == len(ev) {
			if adj[v][ev[0]] {
				return true
			}
		} else {
			for _, n := range ev {
				if !onPath[n] && adj[v][n] && dfs(n) {
					return true
				}
			}
		}
		used = used[:len(used)-1]
		delete(onPath, v)
		return false
	}
	if dfs(ev[0]) {
		return append([]string(nil), used...)
	}
	return nil
}
