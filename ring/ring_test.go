package ring

import "testing"

func TestRingAddEvictCounts(t *testing.T) {
	r := New(2)
	r.Add(Entry{})
	if r.Count() != 1 || r.Failed() != 0 || r.Slow() != 0 {
		t.Fatalf("unexpected counts after add: %d %d %d", r.Count(), r.Failed(), r.Slow())
	}
	r.Add(Entry{Failed: true, Slow: true})
	if r.Count() != 2 || r.Failed() != 1 || r.Slow() != 1 {
		t.Fatalf("unexpected counts: %d %d %d", r.Count(), r.Failed(), r.Slow())
	}
	ev, ok := r.Add(Entry{Failed: true})
	if !ok || ev != (Entry{}) {
		t.Fatalf("expected oldest success evicted, got %+v %v", ev, ok)
	}
	if r.Count() != 2 || r.Failed() != 2 || r.Slow() != 1 {
		t.Fatalf("unexpected counts after evict: %d %d %d", r.Count(), r.Failed(), r.Slow())
	}
	r.Reset()
	if r.Count() != 0 || r.Failed() != 0 || r.Slow() != 0 || r.Touches() != 0 {
		t.Fatal("reset did not clear")
	}
}

func TestRingTouchesConstantPerOperation(t *testing.T) {
	mk := func(n int) (*Ring, int, int) {
		r := New(n)
		for i := 0; i < 500; i++ {
			r.Add(Entry{Failed: i%3 == 0, Slow: i%5 == 0})
		}
		return r, r.Count(), r.Touches()
	}
	_, c10, t10 := mk(10)
	_, c1000, t1000 := mk(1000)
	// 同序列 500 次 Add：N=10 时保留 10 条，N=1000 时保留 500 条，
	// 但触碰次数只取决于操作数（非满 1 次/满 2 次），与 N 无渐增关系。
	if c10 != 10 || c1000 != 500 {
		t.Fatalf("unexpected sizes: %d %d", c10, c1000)
	}
	if want := 10 + 490*2; t10 != want {
		t.Fatalf("N=10 touches=%d want %d", t10, want)
	}
	if t1000 != 500 {
		t.Fatalf("N=1000 touches=%d want 500 (one per op, never scans)", t1000)
	}
	// 关键不变量：每次操作考察的环元素为常数，N 增大不产生额外遍历。
	if t1000 > t10 {
		t.Fatalf("larger window must not cost more touches per op: %d vs %d", t1000, t10)
	}
}
