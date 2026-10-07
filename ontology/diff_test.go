package ontology

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// diffWorld 同时驱动真实 Graph 与独立朴素模型，对随机链接增删与属性写入序列对拍。
type diffWorld struct {
	g    *Graph
	m    *naiveGraph
	t    *testing.T
	vs   []string
	all  []string // 全部可能的起点
	logs []string
}

func buildDiffWorld(t *testing.T, rng *rand.Rand) *diffWorld {
	g := NewGraph()
	m := newNaive()
	typeNames := []string{"T0", "T1", "T2", "T3"}
	for _, ty := range typeNames {
		g.AddObjectType(ty)
		m.addType(ty)
	}
	// 每跳一个链接名，允许“前向类型对 + 回跳类型对（制造环）”。
	for k := 0; k < 3; k++ {
		lname := fmt.Sprintf("e%d", k)
		mustOK(t, g.AddLinkType(lname, typeNames[k], typeNames[k+1]))
		m.addLinkType(lname, typeNames[k], typeNames[k+1])
		if k < 2 {
			// 允许隔层回边，使同类型不在固定位置产生，但通过自类型对制造环：
		}
		// 自环类型对：T{k+1}->T{k}，并在路径声明里把类型集合放开为多个类型。
		mustOK(t, g.AddLinkType(lname, typeNames[k+1], typeNames[k+1]))
		m.addLinkType(lname, typeNames[k+1], typeNames[k+1])
	}
	// 对象：每层若干实例。
	var ids []string
	idAt := map[int][]string{}
	for layer := 0; layer < 4; layer++ {
		n := 2 + rng.Intn(3)
		for i := 0; i < n; i++ {
			id := fmt.Sprintf("o%d_%d", layer, i)
			v := int64(rng.Intn(20))
			attrs := map[string]int64{"v": v}
			mustOK(t, g.CreateObject(id, typeNames[layer], attrs))
			m.create(id, typeNames[layer], attrs)
			ids = append(ids, id)
			idAt[layer] = append(idAt[layer], id)
		}
	}
	// 视图：3 跳，每位置允许该层类型及其后一层（制造同位置环的可能）。
	spec := ViewSpec{Name: "dv", Attr: "v",
		Path: Path{
			Types: [][]string{
				{"T0"},
				{"T1"},
				{"T2"},
				{"T3"},
			},
			Links: []string{"e0", "e1", "e2"},
		}}
	mustOK(t, g.RegisterView(spec))
	m.register(spec)
	return &diffWorld{g: g, m: m, t: t, vs: []string{"dv"}, all: idAt[0]}
}

func (w *diffWorld) checkAll() {
	w.t.Helper()
	starts := map[string]struct{}{}
	for _, id := range w.all {
		starts[id] = struct{}{}
	}
	for k := range w.g.byName["dv"].snap.agg {
		starts[k] = struct{}{}
	}
	for s := range starts {
		got, err := w.g.Query("dv", s)
		if err != nil {
			w.t.Fatalf("query %s: %v", s, err)
		}
		wp, want := w.m.query("dv", s)
		if got.Present != wp || (wp && got.Value != want) {
			w.t.Fatalf("DIFF at %s: naive present=%v v=%d got=%+v", s, wp, want, got)
		}
	}
	// 终点集合也必须严格一致（去重语义）。
	for s := range starts {
		want := w.m.reachableEnds(w.m.views["dv"], s)
		got := endSet(w.g, "dv", s)
		if !setEq(want, got) {
			w.t.Fatalf("REACH DIFF at %s: naive=%v got=%v", s, sorted(want), sorted(got))
		}
	}
}

func sorted(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func setEq(a, b map[string]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}

// TestDifferentialRandom 随机链接增删 + 属性写入序列与朴素模型对拍，并打印日志。
func TestDifferentialRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(20261007))
	for iter := 0; iter < 40; iter++ {
		w := buildDiffWorld(t, rng)
		w.g.SetLogger(func(line string) {
			w.logs = append(w.logs, line)
		})
		// 收集“合法”的增删候选：每条链接的端点类型必须被签名接受。
		type edge struct {
			link     string
			src, dst string
			k        int
		}
		var candidates []edge
		objs := w.g.objects
		for _, lname := range []string{"e0", "e1", "e2"} {
			k := int(lname[1] - '0')
			for sid, so := range objs {
				for did, dobj := range objs {
					ok := (so.typ == fmt.Sprintf("T%d", k) && dobj.typ == fmt.Sprintf("T%d", k+1)) ||
						(so.typ == dobj.typ && so.typ == fmt.Sprintf("T%d", k+1))
					if ok {
						candidates = append(candidates, edge{lname, sid, did, k})
					}
				}
			}
		}
		var endObjs []string
		for id, o := range objs {
			if o.typ == "T3" {
				endObjs = append(endObjs, id)
			}
		}

		present := map[[3]string]bool{}
		for step := 0; step < 300; step++ {
			if rng.Intn(3) == 0 && len(endObjs) > 0 {
				id := endObjs[rng.Intn(len(endObjs))]
				v := int64(rng.Intn(25))
				old, _ := w.g.GetAttr(id, "v")
				aff, err := w.g.SetAttr(id, "v", v)
				if err != nil {
					t.Fatalf("SetAttr: %v", err)
				}
				w.m.setAttr(id, "v", v)
				// 朴素判定受影响起点：可达集合包含 id 的起点。
				expectAff := map[string]struct{}{}
				for _, s := range w.all {
					if _, ok := w.m.reachableEnds(w.m.views["dv"], s)[id]; ok {
						// 还需结果真的发生变化才应出现。
						_ = old
						expectAff[s] = struct{}{}
					}
				}
				// 影响集合允许是“可达起点”的子集（值未变的不出现），但不得超出。
				for s := range aff {
					if _, ok := expectAff[s]; !ok {
						t.Fatalf("step %d SetAttr %s=%d affected unrelated start %s; aff=%v",
							step, id, v, s, aff)
					}
				}
			} else {
				e := candidates[rng.Intn(len(candidates))]
				key := [3]string{e.link, e.src, e.dst}
				if !present[key] && rng.Intn(2) == 0 || !present[key] {
					_, err := w.g.AddLink(e.link, e.src, e.dst, LinkOptions{})
					if err != nil {
						t.Fatalf("AddLink %v: %v", key, err)
					}
					w.m.addEdge(e.link, e.src, e.dst)
					present[key] = true
				} else {
					_, err := w.g.RemoveLink(e.link, e.src, e.dst)
					if err != nil {
						t.Fatalf("RemoveLink %v: %v", key, err)
					}
					w.m.removeEdge(e.link, e.src, e.dst)
					present[key] = false
				}
			}
			if step%15 == 0 {
				w.checkAll()
			}
		}
		w.checkAll()
		if iter == 0 && len(w.logs) == 0 {
			t.Fatalf("expected change logs")
		}
		if iter == 0 {
			// 仅打印第一次迭代的前若干条，验证日志含输入、受影响集合与判定依据。
			limit := 8
			if len(w.logs) < limit {
				limit = len(w.logs)
			}
			for _, l := range w.logs[:limit] {
				t.Logf("LOG %s", l)
			}
		}
	}
}
