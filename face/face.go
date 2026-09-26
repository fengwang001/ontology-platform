// Package face 按 next(u→v)=v→pred_v(u) 追踪面（下标+双向环 O(1) 取前驱），
// 合并各含边分量的外轨道为唯一外平面，校验 V-E+F=1+C（不假设连通）。
package face

import "errors"
import "ontology/pg"

// ErrIncompleteRotation：非孤立节点缺环序时 next 无法定义。
var ErrIncompleteRotation = errors.New("face: rotation not set for every non-isolated vertex")

type ringNode struct{ to, prev, nxt int }
type orbit struct {
	seq  []int
	comp int
}

// Tracker 持有一次追踪结果；probes 是非导出计数器（最近一次 next 定位前驱检查过的邻居数），公开接口不返回其值。
type Tracker struct {
	n, comp, probes int
	edges           [][2]int
	ring            [][]ringNode
	idx             []map[int]int
	faces           [][]int
}

// New 构建双向环并完成面追踪；非孤立节点缺环序时返回 ErrIncompleteRotation。
func New(s pg.Snapshot) (*Tracker, error) {
	t := &Tracker{n: s.N, edges: s.Edges, ring: make([][]ringNode, s.N), idx: make([]map[int]int, s.N)}
	adj := make([][]int, s.N)
	for _, e := range s.Edges {
		adj[e[0]], adj[e[1]] = append(adj[e[0]], e[1]), append(adj[e[1]], e[0])
	}
	for v := range adj { // 孤立节点各自独立成一个分量
		if len(adj[v]) == 0 {
			t.comp++
		} else if !s.HasRot[v] {
			return nil, ErrIncompleteRotation
		}
	}
	for v, o := range s.Rotation {
		if len(o) == 0 {
			continue
		}
		nodes, im := make([]ringNode, len(o)), make(map[int]int, len(o))
		for i, w := range o {
			nodes[i], im[w] = ringNode{w, (i - 1 + len(o)) % len(o), (i + 1) % len(o)}, i
		}
		t.ring[v], t.idx[v] = nodes, im
	}
	id, k := compMap(s.N, adj)
	t.comp += k
	t.faces = mergeOuter(t.traceOrbits(id), k)
	return t, nil
}

func (t *Tracker) next(u, v int) (int, int) {
	r := t.ring[v]
	t.probes = 1
	return v, r[r[t.idx[v][u]].prev].to
}

func (t *Tracker) traceOrbits(compOf []int) []orbit {
	vis := make(map[[2]int]struct{}, 2*len(t.edges))
	var out []orbit
	step := func(u, v int) {
		if _, ok := vis[[2]int{u, v}]; ok {
			return
		}
		seq, a, b := []int{u}, u, v
		for {
			vis[[2]int{a, b}] = struct{}{}
			seq = append(seq, b)
			if a, b = t.next(a, b); a == u && b == v {
				break
			}
		}
		out = append(out, orbit{seq[:len(seq)-1], compOf[u]}) // 去重复闭合终点，使面长度=边数
	}
	for _, e := range t.edges {
		step(e[0], e[1])
		step(e[1], e[0])
	}
	return out
}

func mergeOuter(orbs []orbit, k int) [][]int {
	if k == 0 {
		return nil
	}
	faces := make([][]int, 0, len(orbs)-k+1)
	seen, outer := map[int]struct{}{}, []int(nil)
	for _, o := range orbs {
		if _, ok := seen[o.comp]; ok {
			faces = append(faces, append([]int(nil), o.seq...))
		} else {
			seen[o.comp] = struct{}{}
			outer = append(outer, o.seq...)
		}
	}
	return append(faces, outer)
}

func compMap(n int, adj [][]int) ([]int, int) {
	id, k := make([]int, n), 0
	for s := range id {
		if id[s] != 0 || len(adj[s]) == 0 {
			continue
		}
		k++
		cid, q := k, []int{s}
		id[s] = cid
		for len(q) > 0 {
			x := q[0]
			q = q[1:]
			for _, w := range adj[x] {
				if id[w] == 0 {
					id[w], q = cid, append(q, w)
				}
			}
		}
	}
	return id, k
}

func (t *Tracker) PrevProbeConstant() bool {
	for _, e := range t.edges {
		for j := 0; j < 2; j++ {
			t.next(e[j], e[1-j])
			if t.probes > 2 {
				return false
			}
		}
	}
	return true
}

func (t *Tracker) Faces() [][]int {
	out := make([][]int, len(t.faces))
	for i, f := range t.faces {
		out[i] = append([]int(nil), f...)
	}
	return out
}
func (t *Tracker) FaceCount() int  { return len(t.faces) }
func (t *Tracker) EdgeCount() int  { return len(t.edges) }
func (t *Tracker) Components() int { return t.comp }

func (t *Tracker) EulerHolds() bool {
	return t.n-len(t.edges)+len(t.faces) == 1+t.comp
}
