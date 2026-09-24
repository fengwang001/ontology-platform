package api

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"
)

func gen(n int) ([]float64, []Item) {
	us, it := make([]float64, n), make([]Item, n)
	for i := range us {
		us[i] = 0.01 + 0.98*float64(i)/float64(n)
		it[i] = Item{ID: fmt.Sprintf("i%d", i), W: 1 + float64(i%4)}
	}
	return us, it
}

// 不变量 1、2、3：任意合法序列、任意批划分，结果等于批量参照；规模、消耗守恒、ID 互异；键相等先到者保留。
func TestBatchReference(t *testing.T) {
	for _, tc := range []struct{ k, n int }{{1, 1}, {1, 9}, {3, 50}, {7, 200}, {100, 100}, {5, 4}} {
		us, items := gen(tc.n)
		for _, bs := range []int{1, 3, tc.n} {
			sm, _ := New(tc.k, us)
			for i := 0; i < tc.n; i += bs {
				if err := sm.Offer(items[i:min(i+bs, tc.n)]); err != nil {
					t.Fatalf("k=%d n=%d bs=%d: %v", tc.k, tc.n, bs, err)
				}
			}
			want, got := reference(items, us, tc.k), sm.Sample()
			if sm.Consumed() != tc.n || len(got) != min(tc.k, tc.n) || len(got) != len(want) {
				t.Fatalf("k=%d n=%d bs=%d: consumed=%d len=%d", tc.k, tc.n, bs, sm.Consumed(), len(got))
			}
			seen := map[string]bool{}
			for i := range want {
				if got[i] != want[i] || seen[got[i].ID] {
					t.Errorf("k=%d n=%d bs=%d [%d]: got %v want %v", tc.k, tc.n, bs, i, got[i], want[i])
				}
				seen[got[i].ID] = true
			}
		}
	}
	sm, _ := New(1, []float64{0.5, 0.5}) // 键相等时先到者保留
	sm.Offer([]Item{{ID: "first", W: 1}, {ID: "second", W: 1}})
	if got := sm.Sample(); len(got) != 1 || got[0].ID != "first" {
		t.Errorf("tie: got %v", got)
	}
}

// 四类可判定错误：权重 0、权重非法、随机源用尽、输入非法；按行序与行内顺序报错。
func TestErrorsDistinct(t *testing.T) {
	sm, _ := New(2, []float64{0.5, 0.5, 0.5})
	cases := []struct {
		batch []Item
		want  error
	}{
		{[]Item{{ID: "z", W: 0}}, ErrZeroWeight},
		{[]Item{{ID: "n", W: -1}}, ErrBadWeight},
		{[]Item{{ID: "n", W: math.NaN()}}, ErrBadWeight},
		{[]Item{{ID: "n", W: math.Inf(1)}}, ErrBadWeight},
		{[]Item{{ID: "", W: 1}}, ErrBadInput},
		{[]Item{{ID: "d", W: 1}, {ID: "d", W: 1}}, ErrBadInput},
		{[]Item{{ID: "a", W: 1}, {ID: "b", W: 1}, {ID: "c", W: 1}, {ID: "e", W: 1}}, ErrExhausted},
		{[]Item{{ID: "", W: 0}}, ErrBadInput},                    // 行内先查元素
		{[]Item{{ID: "q", W: 0}, {ID: "", W: 1}}, ErrZeroWeight}, // 按行序报第一行
	}
	for i, tc := range cases {
		if err := sm.Offer(tc.batch); !errors.Is(err, tc.want) {
			t.Errorf("case %d: got %v want %v", i, err, tc.want)
		}
	}
	sm.Offer([]Item{{ID: "ok", W: 1}})
	if err := sm.Offer([]Item{{ID: "ok", W: 1}}); !errors.Is(err, ErrBadInput) {
		t.Errorf("dup accepted id: got %v", err)
	}
	if _, err := New(0, nil); !errors.Is(err, ErrBadInput) {
		t.Errorf("k=0: got %v", err)
	}
	for _, u := range []float64{0, 1, math.NaN()} {
		if _, err := New(1, []float64{u}); !errors.Is(err, ErrBadInput) {
			t.Errorf("u=%v: got %v", u, err)
		}
	}
}

// 不变量 4：任何被拒的批都不留痕，之后仍可正常使用。
func TestRejectionNoTrace(t *testing.T) {
	bads := [][]Item{
		{{ID: "a", W: 1}, {ID: "b", W: 0}},
		{{ID: "a", W: 1}, {ID: "b", W: math.NaN()}},
		{{ID: "a", W: 1}, {ID: "a", W: 1}},
		{{ID: "a", W: 1}, {ID: "b", W: 1}, {ID: "c", W: 1}},
	}
	for i, batch := range bads {
		sm, _ := New(2, []float64{0.3, 0.49})
		if err := sm.Offer(batch); err == nil {
			t.Fatalf("case %d: want error", i)
		}
		if sm.Consumed() != 0 || len(sm.Sample()) != 0 {
			t.Errorf("case %d: state changed after rejection", i)
		}
		if err := sm.Offer([]Item{{ID: "ok", W: 1}}); err != nil {
			t.Errorf("case %d: unusable after rejection: %v", i, err)
		}
	}
}

// 并发：N 个 goroutine 各 Offer 一批，随机数守恒、样本合法、键与 u 一一对应、SelfCheck 通过。
func TestConcurrentOffer(t *testing.T) {
	const G, B = 8, 16
	us, _ := gen(G * B)
	sm, _ := New(7, us)
	inUS := map[float64]bool{}
	for _, u := range us {
		inUS[u] = true
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // 并发读：Sample/Consumed/SelfCheck
		defer wg.Done()
		for i := 0; i < 200; i++ {
			sm.Sample()
			sm.SelfCheck()
		}
	}()
	for g := 0; g < G; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < B; i++ {
				sm.Offer([]Item{{ID: fmt.Sprintf("g%di%d", g, i), W: 1}})
			}
		}(g)
	}
	wg.Wait()
	if sm.Consumed() != G*B || len(sm.Sample()) != 7 {
		t.Fatalf("consumed=%d len=%d", sm.Consumed(), len(sm.Sample()))
	}
	seenID, seenKey := map[string]bool{}, map[float64]bool{}
	for _, s := range sm.Sample() {
		if seenID[s.ID] || !inUS[s.Key] || seenKey[s.Key] {
			t.Errorf("bad sample entry %v", s)
		}
		seenID[s.ID], seenKey[s.Key] = true, true
	}
	if err := sm.SelfCheck(); err != nil {
		t.Error(err)
	}
}
