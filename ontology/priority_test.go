package ontology

import "testing"

// 优先级分数必须对等待时间与失败次数单调不减。
func TestScoreMonotonic(t *testing.T) {
	base := Ticket{ArrivalSeq: 10, Failures: 3}
	prev := base.Score(20)
	for now := uint64(21); now < 100; now++ {
		if got := base.Score(now); got < prev {
			t.Fatalf("分数随等待时间降低: now=%d got=%d prev=%d", now, got, prev)
		}
		prev = base.Score(now)
	}
	prev = base.Score(50)
	for f := uint64(4); f < 100; f++ {
		got := (Ticket{ArrivalSeq: 10, Failures: f}).Score(50)
		if got < prev {
			t.Fatalf("分数随失败次数降低: failures=%d got=%d prev=%d", f, got, prev)
		}
		prev = got
	}
}

// 全序比较：分数优先，其次到达时刻，最后请求 ID，结果确定且可复现。
func TestTotalOrder(t *testing.T) {
	a := priorityKey{score: 5, arrival: 1, id: "a"}
	b := priorityKey{score: 5, arrival: 1, id: "b"}
	c := priorityKey{score: 5, arrival: 2, id: "a"}
	d := priorityKey{score: 6, arrival: 9, id: "z"}

	if !b.less(a) || a.less(b) {
		t.Fatal("同分同到达时应按请求 ID 字典序裁定")
	}
	if !c.less(a) {
		t.Fatal("同分时先到达者优先")
	}
	if !a.less(d) {
		t.Fatal("分数高者优先")
	}
	// 反对称性：任意两个不同键不可能互相小于。
	keys := []priorityKey{a, b, c, d}
	for i, x := range keys {
		for j, y := range keys {
			if i != j && x.less(y) && y.less(x) {
				t.Fatalf("键 %d 与键 %d 互相小于，全序被破坏", i, j)
			}
		}
	}
}

// 关键不变式：任何已失败过的等待者严格优先于任何更晚到达的新请求，
// 与逻辑时钟取值无关。这是 O(1) 准入判定与反饥饿保证的基础。
func TestWaiterAlwaysOutranksNewcomer(t *testing.T) {
	for arrival := uint64(0); arrival < 50; arrival++ {
		for failures := uint64(1); failures < 5; failures++ {
			waiter := Ticket{ArrivalSeq: arrival, Failures: failures}
			for later := arrival + 1; later < arrival+50; later++ {
				newcomer := Ticket{ArrivalSeq: later, Failures: 0}
				for now := later; now < later+50; now++ {
					wk := keyOf("w", waiter, now)
					nk := keyOf("n", newcomer, now)
					if !nk.less(wk) {
						t.Fatalf("等待者 %+v 未优先于新到达 %+v (now=%d)", waiter, newcomer, now)
					}
				}
			}
		}
	}
}
