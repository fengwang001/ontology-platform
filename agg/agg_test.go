package agg

import (
	"math/rand"
	"reflect"
	"strconv"
	"testing"

	"ontology/gset"
)

func gp(t *testing.T, dims []int) gset.Group {
	t.Helper()
	g, ok := gset.Parse(dims)
	if !ok {
		t.Fatalf("Parse(%v) unexpectedly invalid", dims)
	}
	return g
}

// gset 表示：组 ID/GROUPING 极性严格按题给 (A)=3、(A,B)=1、()=7；非法 Parse；
// 投影键长度前缀编码无碰撞、忽略被聚合维度、() 恒空键。
func TestGSetRepresentation(t *testing.T) {
	cases := []struct {
		d  []int
		id int
		gr [3]int
	}{
		{nil, 7, [3]int{1, 1, 1}},
		{[]int{0}, 3, [3]int{0, 1, 1}},
		{[]int{0, 1}, 1, [3]int{0, 0, 1}},
		{[]int{0, 2}, 2, [3]int{0, 1, 0}},
		{[]int{1}, 5, [3]int{1, 0, 1}},
		{[]int{1, 2}, 4, [3]int{1, 0, 0}},
		{[]int{2}, 6, [3]int{1, 1, 0}},
		{[]int{0, 1, 2}, 0, [3]int{0, 0, 0}},
	}
	for _, c := range cases {
		g := gp(t, c.d)
		if g.ID() != c.id {
			t.Errorf("ID(%v)=%d want %d", c.d, g.ID(), c.id)
		}
		for d := 0; d < 3; d++ {
			if g.Grouping(d) != c.gr[d] {
				t.Errorf("GROUPING(%d) of %v=%d want %d", d, c.d, g.Grouping(d), c.gr[d])
			}
		}
	}
	for _, d := range [][]int{{-1}, {3}, {0, 0}} {
		if _, ok := gset.Parse(d); ok {
			t.Errorf("Parse(%v) valid, want invalid", d)
		}
	}
	g := gp(t, []int{0, 1})
	seen := map[string]bool{}
	for i, p := range [][3]string{{"1:a", "b", "x"}, {"1", "a:b", "x"}, {"a", "b", "x"}, {"a", "b2", "x"}} {
		if k := g.Key(p); seen[k] {
			t.Fatalf("key collision at row %d: %q", i, k)
		} else {
			seen[k] = true
		}
	}
	if g.Key([3]string{"a", "b", "u"}) != g.Key([3]string{"a", "b", "v"}) {
		t.Fatal("projection must ignore the aggregated-off dimension C")
	}
	if k := gp(t, nil).Key([3]string{"a", "b", "c"}); k != "" {
		t.Fatalf("grand-total key=%q, want empty", k)
	}
}

// batchRef 是独立批量重算：full-tuple 净量投影分组求和，零值不出现。
func batchRef(gs []gset.Group, net map[[3]string]int64) map[int]map[string]int64 {
	out := map[int]map[string]int64{}
	for _, g := range gs {
		out[g.ID()] = map[string]int64{}
	}
	for vals, m := range net {
		for _, g := range gs {
			if m != 0 {
				out[g.ID()][g.Key(vals)] += m
			}
		}
	}
	return out
}

// I1/I3：随机到达顺序（先全部累加、再随机顺序撤回）与批量重算逐 (组,键) 一致，
// 任意前缀不存在负值、且无一条事实被拒。
func TestRandomBatchEquivalence(t *testing.T) {
	gs := []gset.Group{gp(t, []int{0}), gp(t, []int{0, 1}), gp(t, nil)}
	var tris [][3]string // 2x2x2 全部 full-tuple。
	for _, a := range []string{"x", "y"} {
		for _, b := range []string{"x", "y"} {
			for _, c := range []string{"x", "y"} {
				tris = append(tris, [3]string{a, b, c})
			}
		}
	}
	for round := 0; round < 20; round++ {
		r := rand.New(rand.NewSource(int64(round)))
		net := map[[3]string]int64{}
		var pos, neg []Fact
		for _, tri := range tris { // 每 tri 固定一条 +base 与至多一条 -w（w≤base），构造上不可能越界或死循环。
			base := int64(10 + r.Intn(10))
			pos = append(pos, Fact{A: tri[0], B: tri[1], C: tri[2], M: base})
			net[tri] = base
			if w := int64(r.Intn(int(base) + 1)); w > 0 {
				neg = append(neg, Fact{A: tri[0], B: tri[1], C: tri[2], M: -w})
				net[tri] = base - w
			}
		}
		e := New(gs)
		r.Shuffle(len(pos), func(i, j int) { pos[i], pos[j] = pos[j], pos[i] })
		r.Shuffle(len(neg), func(i, j int) { neg[i], neg[j] = neg[j], neg[i] })
		for i, f := range append(pos, neg...) {
			if err := e.Apply(f); err != nil {
				t.Fatalf("round %d fact %d: %v", round, i, err)
			}
			for id, tb := range e.Snapshot() {
				for k, s := range tb {
					if s < 0 {
						t.Fatalf("round %d negative sum group=%d key=%q", round, id, k)
					}
				}
			}
		}
		if got, want := e.Snapshot(), batchRef(gs, net); !reflect.DeepEqual(got, want) {
			t.Fatalf("round %d view %v != batch %v", round, got, want)
		}
	}
}

// I（复杂度）：非导出 probes 记录最近一次 Apply 的现存键检查数；命中既有键时
// 每组恰好一次哈希探测，故恒为组数 1，与现存键规模 m 无关。
func TestProbeCountConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 5000, 10000} {
		e := New([]gset.Group{gp(t, []int{0})})
		for i := 0; i < m; i++ {
			if err := e.Apply(Fact{A: "k" + strconv.Itoa(i), B: "b", C: "c", M: 1}); err != nil {
				t.Fatal(err)
			}
		}
		if err := e.Apply(Fact{A: "k0", B: "b", C: "c", M: 2}); err != nil {
			t.Fatal(err)
		}
		if e.probes != 1 {
			t.Fatalf("m=%d probes=%d, want constant 1 (= group count)", m, e.probes)
		}
	}
}
