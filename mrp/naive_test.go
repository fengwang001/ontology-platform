package mrp

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// 朴素参照实现：沿父→子边递归重算，任一父项投放变化即向子项传播，
// 共用件会被多次访问（路径条数级），仅用于对照，不代表生产算法。
type naiveEdge struct {
	parent, child string
	per, scrap    uint64
}

type naiveSpec struct {
	T       int
	params  map[string][5]uint64 // onHand, ss, lead, lotMin, lotMult
	edges   []naiveEdge
	demands map[string][]uint64
	scheds  map[string][]uint64
}

type naiveMRP struct {
	spec    *naiveSpec
	out     map[string][]naiveEdge
	g       map[string][]uint64
	contrib map[string]map[string][]uint64 // parent -> child -> 各期贡献
	rec     map[string][]uint64
	rel     map[string][]uint64
	avail   map[string][]uint64
	exc     map[string][]Exception
}

func newNaiveMRP(spec *naiveSpec) *naiveMRP {
	n := &naiveMRP{
		spec:    spec,
		out:     make(map[string][]naiveEdge),
		g:       make(map[string][]uint64),
		contrib: make(map[string]map[string][]uint64),
		rec:     make(map[string][]uint64),
		rel:     make(map[string][]uint64),
		avail:   make(map[string][]uint64),
		exc:     make(map[string][]Exception),
	}
	for id := range spec.params {
		g := make([]uint64, spec.T+1)
		if d := spec.demands[id]; d != nil {
			copy(g, d)
		}
		n.g[id] = g
	}
	for _, e := range spec.edges {
		n.out[e.parent] = append(n.out[e.parent], e)
		if n.contrib[e.parent] == nil {
			n.contrib[e.parent] = make(map[string][]uint64)
		}
		n.contrib[e.parent][e.child] = make([]uint64, spec.T+1)
	}
	return n
}

func (n *naiveMRP) run() {
	for id := range n.spec.params {
		n.replan(id)
	}
}

func (n *naiveMRP) replan(id string) {
	p := n.spec.params[id]
	onHand, ss, lead, lotMin, lotMult := p[0], p[1], p[2], p[3], p[4]
	T := n.spec.T
	g := n.g[id]
	s := n.spec.scheds[id]
	rec := make([]uint64, T+1)
	rel := make([]uint64, T+1)
	a := make([]uint64, T+1)
	a[0] = onHand
	var excs []Exception
	for tt := 1; tt <= T; tt++ {
		var sched uint64
		if s != nil {
			sched = s[tt]
		}
		x := int64(a[tt-1]) + int64(sched) - int64(g[tt])
		if x < int64(ss) {
			net := uint64(int64(ss) - x)
			r := lotSize(net, lotMin, lotMult)
			rec[tt] = r
			a[tt] = uint64(x + int64(r))
		} else {
			a[tt] = uint64(x)
		}
		if rec[tt] > 0 {
			rt := tt - int(lead)
			if rt < 1 {
				excs = append(excs, Exception{Item: id, T: uint64(tt), Short: uint64(1 - rt)})
				rt = 1
			}
			rel[rt] += rec[tt]
		}
	}
	n.rec[id], n.rel[id], n.avail[id] = rec, rel, a
	n.exc[id] = excs
	for _, e := range n.out[id] {
		old := n.contrib[e.parent][e.child]
		next := make([]uint64, T+1)
		denom := uint64(1000) - e.scrap
		for tt := 1; tt <= T; tt++ {
			if rel[tt] > 0 {
				next[tt] = (rel[tt]*e.per*1000 + denom - 1) / denom
			}
		}
		equal := true
		for tt := 1; tt <= T; tt++ {
			if old[tt] != next[tt] {
				equal = false
				break
			}
		}
		if equal {
			continue
		}
		cg := n.g[e.child]
		for tt := 1; tt <= T; tt++ {
			cg[tt] += next[tt] - old[tt]
		}
		n.contrib[e.parent][e.child] = next
		n.replan(e.child)
	}
}

func (n *naiveMRP) exceptions() []Exception {
	var out []Exception
	for _, es := range n.exc {
		out = append(out, es...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Item != out[j].Item {
			return out[i].Item < out[j].Item
		}
		return out[i].T < out[j].T
	})
	return out
}

// genRandomSpec 生成随机无环清单：按随机拓扑序只允许前驱指向后继。
func genRandomSpec(rng *rand.Rand) *naiveSpec {
	T := 1 + rng.Intn(5)
	n := 2 + rng.Intn(7)
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("I%d", i)
	}
	spec := &naiveSpec{
		T:       T,
		params:  make(map[string][5]uint64),
		demands: make(map[string][]uint64),
		scheds:  make(map[string][]uint64),
	}
	for _, id := range ids {
		spec.params[id] = [5]uint64{
			uint64(rng.Intn(31)),     // onHand
			uint64(rng.Intn(9)),      // ss
			uint64(rng.Intn(4)),      // lead
			uint64(1 + rng.Intn(15)), // lotMin
			uint64(1 + rng.Intn(15)), // lotMult
		}
	}
	perm := rng.Perm(n)
	for j := 0; j < n; j++ {
		for k := j + 1; k < n; k++ {
			if rng.Float64() < 0.35 {
				spec.edges = append(spec.edges, naiveEdge{
					parent: ids[perm[j]],
					child:  ids[perm[k]],
					per:    uint64(1 + rng.Intn(5)),
					scrap:  uint64(rng.Intn(401)),
				})
			}
		}
	}
	for _, id := range ids {
		for _, m := range []map[string][]uint64{spec.demands, spec.scheds} {
			entries := rng.Intn(3)
			for e := 0; e < entries; e++ {
				if m[id] == nil {
					m[id] = make([]uint64, T+1)
				}
				m[id][1+rng.Intn(T)] += uint64(1 + rng.Intn(25))
			}
		}
	}
	return spec
}

func buildEngineFromSpec(t *testing.T, spec *naiveSpec, ids []string) *Engine {
	t.Helper()
	eng, err := New(uint64(spec.T))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		p := spec.params[id]
		if err := eng.AddItem([]byte(id), p[0], p[1], p[2], p[3], p[4]); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range spec.edges {
		if err := eng.AddComponent([]byte(e.parent), []byte(e.child), e.per, e.scrap); err != nil {
			t.Fatal(err)
		}
	}
	for id, d := range spec.demands {
		for tt := 1; tt <= spec.T; tt++ {
			if d[tt] > 0 {
				if err := eng.Demand([]byte(id), uint64(tt), d[tt]); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	for id, s := range spec.scheds {
		for tt := 1; tt <= spec.T; tt++ {
			if s[tt] > 0 {
				if err := eng.Scheduled([]byte(id), uint64(tt), s[tt]); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	return eng
}

func checkInvariants(t *testing.T, spec *naiveSpec, res *Result) {
	t.Helper()
	for id, p := range spec.params {
		got := res.Items[id]
		s := spec.scheds[id]
		var sumRec, sumRel uint64
		for tt := 1; tt <= spec.T; tt++ {
			var sched uint64
			if s != nil {
				sched = s[tt]
			}
			want := int64(got.A[tt-1]) + int64(sched) + int64(got.Rec[tt]) - int64(got.G[tt])
			if int64(got.A[tt]) != want {
				t.Fatalf("不变式 A[t]=A[t-1]+Sched+Rec-G 失败: %s t=%d A=%d want=%d", id, tt, got.A[tt], want)
			}
			if got.Rec[tt] > 0 && got.A[tt] < p[1] {
				t.Fatalf("不变式 有订货期 A>=ss 失败: %s t=%d A=%d ss=%d", id, tt, got.A[tt], p[1])
			}
			var pegSum uint64
			for _, pe := range res.pegs[id][tt] {
				pegSum += pe.Qty
			}
			pegSum += res.independent[id][tt]
			if pegSum != got.G[tt] {
				t.Fatalf("不变式 Peg之和=G 失败: %s t=%d sum=%d G=%d", id, tt, pegSum, got.G[tt])
			}
			sumRec += got.Rec[tt]
			sumRel += got.Rel[tt]
		}
		if sumRec != sumRel {
			t.Fatalf("不变式 ΣRel=ΣRec 失败: %s rec=%d rel=%d", id, sumRec, sumRel)
		}
	}
}

func TestRandomVsNaive(t *testing.T) {
	const groups = 1500
	rng := rand.New(rand.NewSource(20261005))
	for gi := 0; gi < groups; gi++ {
		spec := genRandomSpec(rng)
		ids := make([]string, 0, len(spec.params))
		for id := range spec.params {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		eng := buildEngineFromSpec(t, spec, ids)
		res, err := eng.Run()
		if err != nil {
			t.Fatalf("组 %d Run 失败: %v", gi, err)
		}
		naive := newNaiveMRP(spec)
		naive.run()
		// 判定依据：G/Rec/Rel/A 逐物料逐期相等，例外清单相等，Peg 逐来源相等。
		for _, id := range ids {
			got := res.Items[id]
			if !reflect.DeepEqual(got.G, naive.g[id]) {
				t.Fatalf("组 %d %s.G = %v, 朴素实现 %v", gi, id, got.G, naive.g[id])
			}
			if !reflect.DeepEqual(got.Rec, naive.rec[id]) {
				t.Fatalf("组 %d %s.Rec = %v, 朴素实现 %v", gi, id, got.Rec, naive.rec[id])
			}
			if !reflect.DeepEqual(got.Rel, naive.rel[id]) {
				t.Fatalf("组 %d %s.Rel = %v, 朴素实现 %v", gi, id, got.Rel, naive.rel[id])
			}
			if !reflect.DeepEqual(got.A, naive.avail[id]) {
				t.Fatalf("组 %d %s.A = %v, 朴素实现 %v", gi, id, got.A, naive.avail[id])
			}
			for tt := 1; tt <= spec.T; tt++ {
				var want []PegEntry
				for parent, children := range naive.contrib {
					if c := children[id]; c != nil && c[tt] > 0 {
						want = append(want, PegEntry{Parent: parent, Qty: c[tt]})
					}
				}
				sort.Slice(want, func(i, j int) bool { return want[i].Parent < want[j].Parent })
				gotPegs := res.pegs[id][tt]
				if len(gotPegs) == 0 {
					gotPegs = nil
				}
				if !reflect.DeepEqual(gotPegs, want) {
					t.Fatalf("组 %d %s t=%d Peg = %+v, 朴素实现 %+v", gi, id, tt, gotPegs, want)
				}
			}
		}
		if !reflect.DeepEqual(res.Exceptions, naive.exceptions()) {
			t.Fatalf("组 %d 例外 = %+v, 朴素实现 %+v", gi, res.Exceptions, naive.exceptions())
		}
		checkInvariants(t, spec, res)
		if gi < 2 {
			t.Logf("组 %d 输入: T=%d 参数=%v 边=%v 需求=%v 在途=%v", gi, spec.T, spec.params, spec.edges, spec.demands, spec.scheds)
			t.Logf("组 %d 输出: G/Rec/Rel/A 见 res.Items, 例外=%v, 判定=与朴素路径递归实现逐期逐项一致且四条不变式成立", gi, res.Exceptions)
		} else {
			t.Logf("组 %d: items=%d edges=%d 判定=与朴素实现一致且不变式成立", gi, len(ids), len(spec.edges))
		}
	}
}

// 菱形链：每层一个物料经两个中间件汇入下一层共用件，路径数 2^k。
// 验证 visitedItems/visitedEdges 与路径数无关。
func TestVisitedCountersDiamond(t *testing.T) {
	for _, k := range []int{5, 20} {
		t.Run(fmt.Sprintf("k=%d", k), func(t *testing.T) {
			eng, err := New(1)
			if err != nil {
				t.Fatal(err)
			}
			s := func(i int) string { return fmt.Sprintf("S%d", i) }
			m := func(i, j int) string { return fmt.Sprintf("M%d_%d", i, j) }
			for i := 1; i <= k+1; i++ {
				if err := eng.AddItem([]byte(s(i)), 0, 0, 0, 1, 1); err != nil {
					t.Fatal(err)
				}
				if i <= k {
					for j := 1; j <= 2; j++ {
						if err := eng.AddItem([]byte(m(i, j)), 0, 0, 0, 1, 1); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			for i := 1; i <= k; i++ {
				for j := 1; j <= 2; j++ {
					if err := eng.AddComponent([]byte(s(i)), []byte(m(i, j)), 1, 0); err != nil {
						t.Fatal(err)
					}
					if err := eng.AddComponent([]byte(m(i, j)), []byte(s(i+1)), 1, 0); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := eng.Demand([]byte(s(1)), 1, 1); err != nil {
				t.Fatal(err)
			}
			res, err := eng.Run()
			if err != nil {
				t.Fatal(err)
			}
			wantItems, wantEdges := 3*k+1, 4*k
			if res.visitedItems != wantItems || res.visitedEdges != wantEdges {
				t.Fatalf("k=%d: visitedItems=%d visitedEdges=%d, 期望 %d/%d（路径数 2^%d=%d 不影响计数）",
					k, res.visitedItems, res.visitedEdges, wantItems, wantEdges, k, 1<<k)
			}
			if got := res.Items[s(k+1)].G[1]; got != uint64(1)<<k {
				t.Fatalf("k=%d: 末端毛需求 = %d, 期望 2^k=%d", k, got, 1<<k)
			}
		})
	}
}
