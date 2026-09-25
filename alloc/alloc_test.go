package alloc

import (
	"math/rand"
	"strconv"
	"testing"

	"ontology/drf"
)

func frac(n, d int64) drf.Frac {
	f, err := drf.NewFrac(n, d)
	if err != nil {
		panic(err)
	}
	return f
}

func mapsEq(x, y map[string]drf.Frac) bool {
	if len(x) != len(y) {
		return false
	}
	for k, v := range x {
		if w, ok := y[k]; !ok || drf.Cmp(v, w) != 0 {
			return false
		}
	}
	return true
}

func usage(cc, cm int64, tasks []Task, al map[string]drf.Frac) (uC, uM drf.Frac) {
	uC, uM = frac(0, 1), frac(0, 1)
	for _, tk := range tasks {
		uC = drf.Add(uC, drf.Mul(al[tk.ID], frac(tk.CPU, 1)))
		uM = drf.Add(uM, drf.Mul(al[tk.ID], frac(tk.Mem, 1)))
	}
	return
}

type caseT struct {
	cc, cm int64
	tasks  []Task
}

// I1：分组水位推进必须与逐任务重扫的朴素参照逐 id、精确分数相同；含 NOTES 原题钉死 A=8、B=12。
func TestNaiveEquivalence(t *testing.T) {
	cases := []caseT{
		{60, 60, []Task{{ID: "A", CPU: 1, Mem: 6}, {ID: "B", CPU: 4, Mem: 1}}},
		{60, 60, []Task{{ID: "A", CPU: 1, Mem: 6}, {ID: "B", CPU: 4, Mem: 1}, {ID: "C", CPU: 2, Mem: 3}, {ID: "D", CPU: 5, Mem: 0}, {ID: "E", CPU: 0, Mem: 2}}},
		{7, 13, []Task{{ID: "only", CPU: 3, Mem: 2}}},
		{60, 60, []Task{{ID: "tie", CPU: 2, Mem: 2}, {ID: "x", CPU: 1, Mem: 1}}}, // 比例相等→主导资源取 CPU
	}
	r := rand.New(rand.NewSource(42))
	for n := 0; n < 64; n++ { // 循环生成随机小规模用例（含 0 需求坐标）
		tasks := make([]Task, 1+r.Intn(7))
		for i := range tasks {
			tasks[i] = Task{ID: strconv.Itoa(i), CPU: int64(r.Intn(5)), Mem: int64(r.Intn(5))}
			if tasks[i].CPU == 0 && tasks[i].Mem == 0 {
				tasks[i].CPU = 1
			}
		}
		cases = append(cases, caseT{int64(8 + r.Intn(40)), int64(8 + r.Intn(40)), tasks})
	}
	for i, c := range cases {
		a := New(c.cc, c.cm)
		for _, tk := range c.tasks {
			a.Add(tk)
		}
		got, want := a.Allocate(), NaiveAllocate(c.cc, c.cm, c.tasks)
		if !mapsEq(got, want) {
			t.Fatalf("case %d: cc=%d cm=%d tasks=%v grouped %v != naive %v", i, c.cc, c.cm, c.tasks, got, want)
		}
		if i == 0 && (drf.Cmp(got["A"], frac(8, 1)) != 0 || drf.Cmp(got["B"], frac(12, 1)) != 0) {
			t.Fatalf("derivation values wrong: A=%v B=%v", got["A"], got["B"])
		}
	}
}

// I2：两条硬约束不得越界，且至少一条达到上界。
func TestHardConstraints(t *testing.T) {
	cases := []caseT{
		{60, 60, []Task{{ID: "A", CPU: 1, Mem: 6}, {ID: "B", CPU: 4, Mem: 1}}},
		{30, 50, []Task{{ID: "p", CPU: 3, Mem: 0}, {ID: "q", CPU: 0, Mem: 4}, {ID: "r", CPU: 2, Mem: 7}}},
		{9, 9, []Task{{ID: "s", CPU: 1, Mem: 1}}},
	}
	for i, c := range cases {
		al := NaiveAllocate(c.cc, c.cm, c.tasks)
		uC, uM := usage(c.cc, c.cm, c.tasks, al)
		if drf.Cmp(uC, frac(c.cc, 1)) > 0 || drf.Cmp(uM, frac(c.cm, 1)) > 0 {
			t.Fatalf("case %d: capacity exceeded cpu=%v mem=%v", i, uC, uM)
		}
		if drf.Cmp(uC, frac(c.cc, 1)) < 0 && drf.Cmp(uM, frac(c.cm, 1)) < 0 {
			t.Fatalf("case %d: neither resource binds", i)
		}
	}
}

// I3：主导份额严格更小的任务，其主导资源必已整体耗尽（否则它还能继续增加）。
func TestFairShares(t *testing.T) {
	cases := []caseT{
		{60, 60, []Task{{ID: "A", CPU: 1, Mem: 6}, {ID: "B", CPU: 4, Mem: 1}}},
		{60, 60, []Task{{ID: "p", CPU: 3, Mem: 0}, {ID: "q", CPU: 0, Mem: 4}, {ID: "r", CPU: 2, Mem: 7}, {ID: "s", CPU: 0, Mem: 2}}},
	}
	for i, c := range cases {
		al := NaiveAllocate(c.cc, c.cm, c.tasks)
		uC, uM := usage(c.cc, c.cm, c.tasks, al)
		maxS := frac(-1, 1)
		for _, tk := range c.tasks {
			if s := drf.DominantShare(al[tk.ID], tk.CPU, tk.Mem, c.cc, c.cm); drf.Cmp(s, maxS) > 0 {
				maxS = s
			}
		}
		for _, tk := range c.tasks {
			s := drf.DominantShare(al[tk.ID], tk.CPU, tk.Mem, c.cc, c.cm)
			if drf.Cmp(s, maxS) >= 0 {
				continue
			}
			if drf.Dominant(tk.CPU, tk.Mem, c.cc, c.cm) == drf.CPU {
				if drf.Cmp(uC, frac(c.cc, 1)) != 0 {
					t.Fatalf("case %d: %s low share, cpu not exhausted (%v)", i, tk.ID, uC)
				}
			} else if drf.Cmp(uM, frac(c.cm, 1)) != 0 {
				t.Fatalf("case %d: %s low share, mem not exhausted (%v)", i, tk.ID, uM)
			}
		}
	}
}

// 复杂度：m-k 个同需求向量任务聚合为 1 个组代表，加 k 个异向量组，共 k+1；
// 考察个数恒为 k+1，不随 m（100..10000）线性增长。计数器为非导出字段，仅本包可读。
func TestExaminedSublinear(t *testing.T) {
	const k = 3
	for _, m := range []int{100, 1000, 5000, 10000} {
		a := New(60, 60)
		for i := 0; i < m-k; i++ {
			a.Add(Task{ID: "g" + strconv.Itoa(i), CPU: 0, Mem: 1})
		}
		for j := int64(1); j <= k; j++ {
			a.Add(Task{ID: "o" + strconv.FormatInt(j, 10), CPU: j, Mem: 0})
		}
		a.Allocate()
		if a.examined != k+1 {
			t.Fatalf("m=%d examined=%d want %d", m, a.examined, k+1)
		}
	}
}
