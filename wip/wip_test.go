package wip

import (
	"errors"
	"testing"

	"ontology/routing"
)

func setupWIP(t *testing.T) (*routing.Manager, *Manager) {
	t.Helper()
	rm := routing.NewManager()
	// n=3, back=[1,2,2]，无检验点（门禁在 hold 包测），R=1。
	if err := rm.Define("r3", 3, []int{1, 2, 2}, []bool{false, false, false}, 1); err != nil {
		t.Fatal(err)
	}
	return rm, NewManager(rm)
}

func q(i, k int) [2]int { return [2]int{i, k} }

func assertConservation(t *testing.T, m *Manager, id string, maxK int) {
	t.Helper()
	s, err := m.State(id)
	if err != nil {
		t.Fatal(err)
	}
	var w int64
	for ck, v := range s.Queue {
		if v < 0 {
			t.Fatalf("negative cell for %s", id)
		}
		if ck[1] > maxK {
			t.Fatalf("cell k>R for %s: %v=%d", id, ck, v)
		}
		w += v
	}
	if s.Q != s.Done+s.Scrapped+w {
		t.Fatalf("conservation broken for %s: Q=%d done=%d scrap=%d wip=%d",
			id, s.Q, s.Done, s.Scrapped, w)
	}
}

func TestOpenAndExampleFlow(t *testing.T) {
	_, m := setupWIP(t)
	if err := m.Open("wo", "r3", 100); err != nil {
		t.Fatal(err)
	}
	s, _ := m.State("wo")
	if s.Queue[q(1, 0)] != 100 || s.Q != 100 {
		t.Fatalf("bad open snapshot %+v", s)
	}

	mustReport := func(wo string, i, k int, g, sc, rw int64) {
		t.Helper()
		if err := m.Report(wo, i, k, g, sc, rw); err != nil {
			t.Fatalf("Report(%d,%d,%d,%d,%d): %v", i, k, g, sc, rw, err)
		}
	}

	mustReport("wo", 1, 0, 100, 0, 0)
	s, _ = m.State("wo")
	if s.Queue[q(2, 0)] != 100 {
		t.Fatalf("after step1: %+v", s.Queue)
	}

	mustReport("wo", 2, 0, 90, 4, 6)
	s, _ = m.State("wo")
	if s.Queue[q(3, 0)] != 90 || s.Scrapped != 4 || s.Queue[q(2, 1)] != 6 {
		t.Fatalf("after step2: %+v scrap=%d", s.Queue, s.Scrapped)
	}

	// k==R 时 rework>0 被拒；良品+报废可报。
	if err := m.Report("wo", 2, 1, 5, 0, 1); !errors.Is(err, routing.ErrReworkCap) {
		t.Fatalf("rework at k=R: %v", err)
	}
	s, _ = m.State("wo")
	if s.Queue[q(2, 1)] != 6 {
		t.Fatalf("rejected report changed state: %d", s.Queue[q(2, 1)])
	}
	mustReport("wo", 2, 1, 5, 1, 0)
	s, _ = m.State("wo")
	if s.Queue[q(3, 1)] != 5 || s.Scrapped != 5 || s.Queue[q(2, 1)] != 0 {
		t.Fatalf("after k=1 good/scrap: %+v", s.Queue)
	}

	// 末工序：完工与回流。
	mustReport("wo", 3, 0, 80, 0, 10)
	s, _ = m.State("wo")
	if s.Done != 80 || s.Queue[q(2, 1)] != 10 {
		t.Fatalf("after last-step report: done=%d q=%+v", s.Done, s.Queue)
	}
	mustReport("wo", 3, 1, 5, 0, 0)
	mustReport("wo", 2, 1, 10, 0, 0)
	mustReport("wo", 3, 1, 9, 1, 0)
	s, _ = m.State("wo")
	if s.Done != 94 || s.Scrapped != 6 {
		t.Fatalf("final: done=%d scrap=%d", s.Done, s.Scrapped)
	}

	res, err := m.Close("wo")
	if err != nil {
		t.Fatal(err)
	}
	if res.Done != 94 || res.Scrapped != 6 || res.Shortfall != 6 {
		t.Fatalf("close result %+v", res)
	}
	s, _ = m.State("wo")
	if !s.Closed {
		t.Fatal("not marked closed")
	}
	// 关闭后拒绝一切操作。
	if err := m.Report("wo", 1, 0, 1, 0, 0); !errors.Is(err, routing.ErrState) {
		t.Fatalf("report after close: %v", err)
	}
	if _, err := m.Close("wo"); !errors.Is(err, routing.ErrState) {
		t.Fatalf("double close: %v", err)
	}
	if err := m.Split("wo", "x", 1, 0, 1); !errors.Is(err, routing.ErrState) {
		t.Fatalf("split after close: %v", err)
	}
}

func TestExactCellAndBounds(t *testing.T) {
	_, m := setupWIP(t)
	if err := m.Open("wo", "r3", 10); err != nil {
		t.Fatal(err)
	}
	// 合计恰等队列格数量，全部报废。
	if err := m.Report("wo", 1, 0, 0, 10, 0); err != nil {
		t.Fatal(err)
	}
	s, _ := m.State("wo")
	if len(s.Queue) != 0 || s.Scrapped != 10 {
		t.Fatalf("cell should be emptied: %+v", s.Queue)
	}
	// 合计超过空格 -> 数量超出。
	if err := m.Report("wo", 1, 0, 1, 0, 0); !errors.Is(err, routing.ErrOverflow) {
		t.Fatalf("report empty cell: %v", err)
	}
	// 数量为负 / 合计 0 -> 参数非法。
	if err := m.Report("wo", 1, 0, -1, 0, 0); !errors.Is(err, routing.ErrInvalid) {
		t.Fatalf("negative: %v", err)
	}
	if err := m.Report("wo", 1, 0, 0, 0, 0); !errors.Is(err, routing.ErrInvalid) {
		t.Fatalf("zero total: %v", err)
	}
	// i/k 越界。
	if err := m.Report("wo", 4, 0, 1, 0, 0); !errors.Is(err, routing.ErrInvalid) {
		t.Fatalf("i out of range: %v", err)
	}
	if err := m.Report("wo", 1, 2, 1, 0, 0); !errors.Is(err, routing.ErrInvalid) {
		t.Fatalf("k>R: %v", err)
	}
	// 工单/路线不存在。
	if err := m.Report("ghost", 1, 0, 1, 0, 0); !errors.Is(err, routing.ErrNotFound) {
		t.Fatalf("missing wo: %v", err)
	}
	if err := m.Open("wo2", "ghost", 1); !errors.Is(err, routing.ErrNotFound) {
		t.Fatalf("missing route: %v", err)
	}
	// Q 边界。
	if err := m.Open("wo3", "r3", 0); !errors.Is(err, routing.ErrInvalid) {
		t.Fatalf("Q=0: %v", err)
	}
	if err := m.Open("wo4", "r3", 1_000_000_001); !errors.Is(err, routing.ErrInvalid) {
		t.Fatalf("Q too big: %v", err)
	}
}

func TestReworkToSelfAndEarlier(t *testing.T) {
	rm := routing.NewManager()
	// 工序1回流自身；工序3回流到1。
	if err := rm.Define("rb", 3, []int{1, 1, 1}, []bool{false, false, false}, 2); err != nil {
		t.Fatal(err)
	}
	m := NewManager(rm)
	if err := m.Open("wo", "rb", 10); err != nil {
		t.Fatal(err)
	}
	if err := m.Report("wo", 1, 0, 0, 0, 10); err != nil {
		t.Fatal(err)
	}
	s, _ := m.State("wo")
	if s.Queue[q(1, 1)] != 10 {
		t.Fatalf("self loop rework: %+v", s.Queue)
	}
	if err := m.Report("wo", 1, 1, 10, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := m.Report("wo", 2, 1, 10, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := m.Report("wo", 3, 1, 0, 0, 10); err != nil {
		t.Fatal(err)
	}
	s, _ = m.State("wo")
	if s.Queue[q(1, 2)] != 10 {
		t.Fatalf("rework to earlier step k=2: %+v", s.Queue)
	}
	assertConservation(t, m, "wo", 2)
}

func TestSplitAndConservation(t *testing.T) {
	_, m := setupWIP(t)
	if err := m.Open("wo", "r3", 100); err != nil {
		t.Fatal(err)
	}
	if err := m.Report("wo", 1, 0, 100, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := m.Split("wo", "wo2", 2, 0, 30); err != nil {
		t.Fatal(err)
	}
	a, _ := m.State("wo")
	b, _ := m.State("wo2")
	if a.Q != 70 || a.Queue[q(2, 0)] != 70 {
		t.Fatalf("parent after split: %+v", a.Queue)
	}
	if b.Q != 30 || b.Queue[q(2, 0)] != 30 || b.Done != 0 || b.Scrapped != 0 || b.Held || b.Closed {
		t.Fatalf("child after split: %+v", b)
	}
	assertConservation(t, m, "wo", 1)
	assertConservation(t, m, "wo2", 1)

	// 合计件数不变。
	var sum int64
	for _, s := range []Snapshot{a, b} {
		var w int64
		for _, v := range s.Queue {
			w += v
		}
		sum += s.Done + s.Scrapped + w
	}
	if sum != 100 {
		t.Fatalf("total pieces changed: %d", sum)
	}

	// qty 越界与重复新工单号。
	if err := m.Split("wo", "wo2", 2, 0, 1); !errors.Is(err, routing.ErrState) {
		t.Fatalf("dup newWo: %v", err)
	}
	if err := m.Split("wo", "wo3", 2, 0, 71); !errors.Is(err, routing.ErrOverflow) {
		t.Fatalf("qty too big: %v", err)
	}
	if err := m.Split("wo", "wo4", 2, 0, 0); !errors.Is(err, routing.ErrInvalid) {
		t.Fatalf("qty 0: %v", err)
	}
	if err := m.Split("ghost", "wo5", 2, 0, 1); !errors.Is(err, routing.ErrNotFound) {
		t.Fatalf("split missing wo: %v", err)
	}
}

func TestCloseWhileWIP(t *testing.T) {
	_, m := setupWIP(t)
	if err := m.Open("wo", "r3", 5); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Close("wo"); !errors.Is(err, routing.ErrState) {
		t.Fatalf("close with wip: %v", err)
	}
	if err := m.Report("wo", 1, 0, 2, 3, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Close("wo"); !errors.Is(err, routing.ErrState) {
		t.Fatalf("close with partial wip: %v", err)
	}
}

func TestRejectionOrder(t *testing.T) {
	_, m := setupWIP(t)
	if err := m.Open("wo", "r3", 10); err != nil {
		t.Fatal(err)
	}
	// 参数非法先于不存在。
	if err := m.Report("", 0, -1, 0, 0, 0); !errors.Is(err, routing.ErrInvalid) {
		t.Fatalf("invalid vs missing: %v", err)
	}
	// 不存在先于数量超出。
	if err := m.Report("ghost", 1, 0, 1<<40, 0, 0); !errors.Is(err, routing.ErrNotFound) {
		t.Fatalf("missing vs overflow: %v", err)
	}
	// 数量超出于返工上限。
	if err := m.Report("wo", 1, 0, 11, 0, 0); !errors.Is(err, routing.ErrOverflow) {
		t.Fatalf("overflow: %v", err)
	}
}

func TestTouchedIndependentOfNAndR(t *testing.T) {
	cases := []struct {
		name string
		n    int
		R    int
		back []int
	}{
		{"n=4,R=2", 4, 2, []int{1, 2, 2, 3}},
		{"n=32,R=1000", 32, 1000, backSelfN(32)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rm := routing.NewManager()
			insp := make([]bool, tc.n)
			if err := rm.Define("big", tc.n, tc.back, insp, tc.R); err != nil {
				t.Fatal(err)
			}
			m := NewManager(rm)
			if err := m.Open("wo", "big", 1_000_000_000); err != nil {
				t.Fatal(err)
			}
			// 良品+报废+返工，全部进入三个不同格：来源、下一工序、回流。
			if err := m.Report("wo", 1, 0, 7, 2, 1); err != nil {
				t.Fatal(err)
			}
			o := m.orders["wo"]
			if o.touched != 3 {
				t.Fatalf("report touched %d cells, want 3", o.touched)
			}
			// 回流到自身工序：来源格与返工格同为 (1,1)？否——来源 (1,0)，
			// 返工格 (back[1],1)=(1,1)，仍为不同格；构造同格场景：
			// 在 back[i]=i 的格上以 k 层报工时，来源 (i,k)、返工 (i,k+1)
			// 仍不同。真正同格是“来源==良品目标”，不可能（i!=i+1）。
			// 因此三向分流恒为 3 格。
			// 仅良品+报废：来源格与良品目标格，共 2 格（报废不进队列）。
			if err := m.Report("wo", 2, 0, 5, 2, 0); err != nil {
				t.Fatal(err)
			}
			if o.touched != 2 {
				t.Fatalf("good+scrap touched %d cells, want 2", o.touched)
			}
			// 把 5 件良品逐级推到末工序。
			for step := 3; step < tc.n; step++ {
				if err := m.Report("wo", step, 0, 5, 0, 0); err != nil {
					t.Fatal(err)
				}
			}
			// 末工序全良品：只触碰来源格（done 不是队列格）。
			if err := m.Report("wo", tc.n, 0, 5, 0, 0); err != nil {
				t.Fatal(err)
			}
			if o.touched != 1 {
				t.Fatalf("last-step good touched %d cells, want 1", o.touched)
			}
			assertConservation(t, m, "wo", tc.R)

			// Close 在在制不为 0 时被拒，且触碰计数应为 0（不遍历队列）。
			if _, err := m.Close("wo"); !errors.Is(err, routing.ErrState) {
				t.Fatalf("close with wip: %v", err)
			}
			if o.touched != 0 {
				t.Fatalf("failed close touched %d cells, want 0", o.touched)
			}
		})
	}
}

func backSelfN(n int) []int {
	b := make([]int, n)
	for i := range b {
		b[i] = i + 1
	}
	return b
}
