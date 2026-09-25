package alloc

import (
	"fmt"
	"math/rand"
	"testing"

	"ontology/drf"
)

var z = drf.Frac{N: 0, D: 1}

func q(n, d int64) drf.Frac { return drf.Ratio(n, d) }
func fs(x drf.Frac) string  { return fmt.Sprintf("%d/%d", x.N, x.D) }

// naive 是独立朴素参照：首轮所有任务楼层皆 0、同步逐单位推进，
// 主导份额抬到先耗尽的约束为止：s=min(Ccpu/ΣwCPU, Cmem/ΣwMem)，a_i=s/r_i。
func naive(capC, capM int64, ts []Task) map[string]drf.Frac {
	out := map[string]drf.Frac{}
	if len(ts) == 0 {
		return out
	}
	bC, bM, rs := z, z, map[string]drf.Frac{}
	for _, t := range ts {
		r := drf.DominantRatio(t.CPU, t.Mem, capC, capM)
		rs[t.ID] = r
		bC = drf.Add(bC, drf.Div(q(t.CPU, 1), r))
		bM = drf.Add(bM, drf.Div(q(t.Mem, 1), r))
	}
	s := drf.Div(q(capC, 1), bC)
	if dM := drf.Div(q(capM, 1), bM); drf.Cmp(dM, s) < 0 {
		s = dM
	}
	for id, r := range rs {
		out[id] = drf.Div(s, r)
	}
	return out
}
func plan(capC, capM int64, ts []Task) map[string]drf.Frac {
	p := New(capC, capM)
	for _, t := range ts {
		p.Add(t)
	}
	return p.Allocate()
}

// TestAllocateMatchesNaiveReference 不变量1：手工+随机算例与朴素参照逐 id 相等。
func TestAllocateMatchesNaiveReference(t *testing.T) {
	cases := []struct {
		cC, cM int64
		ts     []Task
	}{
		{60, 60, []Task{{"A", 1, 6}, {"B", 4, 1}}},
		{1, 1, []Task{{"only", 1, 1}}},
		{60, 60, []Task{{"pc", 1, 0}, {"pm", 0, 1}, {"mix", 1, 1}}},
	}
	rng := rand.New(rand.NewSource(20260926))
	for m := 0; m <= 30; m++ {
		cC, cM, ts := int64(1+rng.Intn(40)), int64(1+rng.Intn(40)), []Task{}
		for i := 0; i < m; i++ {
			c, mm := int64(rng.Intn(21)), int64(rng.Intn(21))
			if c == 0 && mm == 0 {
				c = 1
			}
			ts = append(ts, Task{fmt.Sprintf("t%d", i), c, mm})
		}
		cases = append(cases, struct {
			cC, cM int64
			ts     []Task
		}{cC, cM, ts})
	}
	for ci, tc := range cases {
		got, want := plan(tc.cC, tc.cM, tc.ts), naive(tc.cC, tc.cM, tc.ts)
		for id, w := range want {
			if g, ok := got[id]; !ok || drf.Cmp(g, w) != 0 {
				t.Fatalf("case %d id=%s got=%s want=%s", ci, id, fs(g), fs(w))
			}
		}
		if ci == 0 && (drf.Cmp(got["A"], q(8, 1)) != 0 || drf.Cmp(got["B"], q(12, 1)) != 0) {
			t.Fatalf("主算例 = A%s B%s, want A8 B12", fs(got["A"]), fs(got["B"]))
		}
	}
}

// TestHardConstraintsAndBinding 不变量2：两约束不突破，且 mem 恰达上界（绑定）。
func TestHardConstraintsAndBinding(t *testing.T) {
	ts := []Task{{"A", 1, 6}, {"B", 4, 1}}
	a, uC, uM := plan(60, 60, ts), z, z
	for _, x := range ts {
		uC = drf.Add(uC, drf.Mul(a[x.ID], q(x.CPU, 1)))
		uM = drf.Add(uM, drf.Mul(a[x.ID], q(x.Mem, 1)))
	}
	if drf.Cmp(uC, q(60, 1)) > 0 || drf.Cmp(uM, q(60, 1)) != 0 {
		t.Fatalf("硬约束/绑定失败 cpu=%s mem=%s（want mem=60 绑定）", fs(uC), fs(uM))
	}
}

// TestDominantSharesAreFair 不变量3：首轮主导份额全相等且为 12/35。
func TestDominantSharesAreFair(t *testing.T) {
	ts := []Task{{"A", 1, 6}, {"B", 4, 1}, {"C", 2, 3}, {"D", 3, 2}}
	a, s := plan(60, 60, ts), z
	for i, x := range ts {
		if sh := drf.Mul(a[x.ID], drf.DominantRatio(x.CPU, x.Mem, 60, 60)); i == 0 {
			s = sh
		} else if drf.Cmp(sh, s) != 0 {
			t.Fatalf("%s 份额=%s 其他=%s 不等", x.ID, fs(sh), fs(s))
		}
	}
	if drf.Cmp(s, q(12, 35)) != 0 {
		t.Fatalf("主导份额=%s want 12/35", fs(s))
	}
}

// TestExaminedCountDoesNotScaleWithM 复杂度：仅 k 个零楼层新任务被考察。
func TestExaminedCountDoesNotScaleWithM(t *testing.T) {
	const k = 3
	for _, m := range []int{100, 1000, 10000} {
		p, n1 := New(120, 120), (m-k)/2+1
		for i := 0; i < m-k; i++ {
			t := Task{ID: fmt.Sprintf("t%d", i)}
			if i < n1 {
				t.CPU, t.Mem = 4, 2
			} else {
				t.CPU, t.Mem = 2, 4
			}
			p.Add(t)
		}
		p.Allocate()
		for j := 0; j < k; j++ {
			p.Add(Task{ID: fmt.Sprintf("new%d", j), Mem: 1})
		}
		a := p.Allocate()
		for j := 0; j < k; j++ {
			if drf.Cmp(a[fmt.Sprintf("new%d", j)], z) <= 0 {
				t.Fatalf("m=%d 新任务未被拉平", m)
			}
		}
		if p.examined != k {
			t.Fatalf("m=%d examined=%d want %d（不随 m 线性增长）", m, p.examined, k)
		}
	}
}
