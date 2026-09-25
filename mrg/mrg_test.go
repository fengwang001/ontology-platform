package mrg

import (
	"math/rand"
	"sort"
	"testing"

	"ontology/run"
)

// TestRunStructure 钉住不变量 3：除最后一 run 外每个恰好 M 条，
// 最后一 run ≤ M 条，且每个 run 内部升序。
func TestRunStructure(t *testing.T) {
	cases := []struct {
		name string
		keys []run.Key
		m    int
	}{
		{"M3-题面R", []run.Key{5, 1, 3, 7, 3, 2}, 3},
		{"M3-题面S", []run.Key{3, 6, 3, 2}, 3},
		{"整除", []run.Key{9, 8, 7, 6, 5, 4}, 2},
		{"单元素", []run.Key{42}, 5},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			runs, err := run.MakeRuns(c.keys, c.m)
			if err != nil {
				t.Fatal(err)
			}
			total := 0
			for i, r := range runs {
				if i < len(runs)-1 && len(r.Keys) != c.m {
					t.Fatalf("run %d size=%d want %d", i, len(r.Keys), c.m)
				}
				if i == len(runs)-1 && len(r.Keys) > c.m {
					t.Fatalf("last run size %d > M %d", len(r.Keys), c.m)
				}
				if sort.SliceIsSorted(r.Keys, func(a, b int) bool { return r.Keys[a] < r.Keys[b] }) == false {
					t.Fatalf("run %d not sorted: %v", i, r.Keys)
				}
				total += len(r.Keys)
			}
			if total != len(c.keys) {
				t.Fatalf("total %d != input %d", total, len(c.keys))
			}
		})
	}
}

// TestMergeSorted 钉住不变量 2：归并序列是输入的排序（多重集相同）且非降。
func TestMergeSorted(t *testing.T) {
	cases := [][]run.Key{
		{5, 1, 3, 7, 3, 2},
		{3, 6, 3, 2},
		{1, 1, 1, 1},
		{0},
	}
	for seed := int64(0); seed < 40; seed++ {
		r := rand.New(rand.NewSource(seed))
		n := r.Intn(200) + 1
		ks := make([]run.Key, n)
		for i := range ks {
			ks[i] = run.Key(r.Intn(20)) // 小值域制造大量重复与并列
		}
		cases = append(cases, ks)
	}
	for idx, keys := range cases {
		runs, err := run.MakeRuns(keys, 3)
		if err != nil {
			t.Fatal(err)
		}
		fanIn := len(runs)
		if fanIn < 2 {
			fanIn = 2
		}
		got, err := Merge(runs, fanIn) // run 数即扇入，强制走多路堆归并
		if err != nil {
			t.Fatal(err)
		}
		want := append([]run.Key(nil), keys...)
		sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
		if len(got) != len(want) {
			t.Fatalf("case %d len %d want %d", idx, len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("case %d pos %d got %d want %d", idx, i, got[i], want[i])
			}
		}
	}
}

// TestJoinTrace 验证题面六步与重复键整段笛卡尔积。
func TestJoinTrace(t *testing.T) {
	rR, _ := run.MakeRuns([]run.Key{5, 1, 3, 7, 3, 2}, 3)
	rS, _ := run.MakeRuns([]run.Key{3, 6, 3, 2}, 3)
	pairs, steps, err := Join(rR, rS, 2)
	if err != nil {
		t.Fatal(err)
	}
	wantAct := []string{"推进R", "相等成对", "相等成对", "推进R", "推进S", "结束"}
	if len(steps) != 6 {
		t.Fatalf("steps=%d want 6", len(steps))
	}
	for i, w := range wantAct {
		if steps[i].Action != w {
			t.Fatalf("step %d action=%s want %s", i+1, steps[i].Action, w)
		}
	}
	if len(steps[1].Out) != 1 || len(steps[2].Out) != 4 || len(pairs) != 5 {
		t.Fatalf("step2=%d step3=%d total=%d want 1/4/5",
			len(steps[1].Out), len(steps[2].Out), len(pairs))
	}
}

// TestProbeCountLogarithmic 白盒读非导出计数器：每次取最小的首元素检查
// 个数必须 ≤ 2⌈log₂ m⌉+2，不随 m 线性增长。
func TestProbeCountLogarithmic(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		runs := make([]run.Run, m)
		for i := range runs {
			// 交错值域，保证归并过程中各 run 交替出队、堆持续下沉。
			runs[i] = run.Run{Keys: []run.Key{run.Key((i * 7) % m), run.Key((i*7)%m + m)}}
		}
		n := newNode(runs)
		h := 0
		for 1<<h < m {
			h++
		}
		bound := 2*h + 2
		for c := 0; c < 2*m; c++ {
			if _, ok := n.popMin(); !ok {
				t.Fatalf("m=%d early drain at %d", m, c)
			}
			if n.probe > bound {
				t.Fatalf("m=%d probe=%d exceeds bound %d (linear scan?)", m, n.probe, bound)
			}
		}
	}
}
