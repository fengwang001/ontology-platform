package alloc

import (
	"math/rand"
	"strconv"
	"testing"

	"ontology/mf"
)

// 考察个数不随 m 线性增长：只有前 k 个被全额满足时，
// examined 不超过 k 加一个小常数（定位到水位点即停）。
func TestExaminedBounded(t *testing.T) {
	const k = 3
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		tasks := make([]Task, 0, m)
		for i := 0; i < m; i++ {
			d := int64(1 << 40)
			if i < k {
				d = 1
			}
			tasks = append(tasks, Task{ID: strconv.Itoa(i), Demand: d})
		}
		var e Engine
		out := e.Allocate(int64(k+5*(m-k)), tasks)
		if e.examined > k+1 {
			t.Fatalf("m=%d: examined=%d, want <= %d", m, e.examined, k+1)
		}
		if mf.Cmp(out["4"], mf.New(5, 1)) != 0 {
			t.Fatalf("m=%d: waterline task got %s, want 5", m, out["4"])
		}
	}
}

// 与朴素参照逐 id 一致且守恒，多档规模、含零需求、随机顺序。
func TestAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	cases := []struct {
		n   int
		cap int64
	}{
		{1, 1}, {4, 30}, {10, 7}, {50, 1000}, {200, 333}, {1000, 999983},
	}
	for _, tc := range cases {
		tasks := make([]Task, 0, tc.n)
		var dsum int64
		for i := 0; i < tc.n; i++ {
			d := rng.Int63n(200) // 含 0 需求
			dsum += d
			tasks = append(tasks, Task{ID: strconv.Itoa(i), Demand: d})
		}
		var e Engine
		got := e.Allocate(tc.cap, tasks)
		want := Naive(tc.cap, tasks)
		if len(got) != len(want) {
			t.Fatalf("n=%d: len %d != %d", tc.n, len(got), len(want))
		}
		sum := mf.Frac{N: 0, D: 1}
		for id, w := range want {
			if mf.Cmp(got[id], w) != 0 {
				t.Fatalf("n=%d cap=%d: id %s got %s, naive %s", tc.n, tc.cap, id, got[id], w)
			}
			sum = mf.Add(sum, w)
		}
		cs := dsum
		if cs > tc.cap {
			cs = tc.cap
		}
		if mf.Cmp(sum, mf.New(cs, 1)) != 0 {
			t.Fatalf("n=%d cap=%d: sum=%s, want %d", tc.n, tc.cap, sum, cs)
		}
	}
}
