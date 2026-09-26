package cbf

import "testing"

// TestQueryVisitsExactlyK 证明 Query 只访问 k 个计数器，不随 m 增长。
// 白盒：直接读非导出字段 lastVisits（不经过任何导出函数）。
func TestQueryVisitsExactlyK(t *testing.T) {
	cases := []struct {
		m, k int
	}{
		{100, 3}, {500, 4}, {1000, 5}, {5000, 6}, {10000, 7},
	}
	for _, tc := range cases {
		f := New(tc.m, tc.k)
		for x := int64(0); x < int64(tc.m); x++ {
			f.Add(x)
		}
		f.Query(42)
		if f.lastVisits != tc.k {
			t.Fatalf("m=%d k=%d: Query visited %d counters, want %d",
				tc.m, tc.k, f.lastVisits, tc.k)
		}
		if !f.QueryLocalityOK() {
			t.Fatalf("m=%d k=%d: QueryLocalityOK = false", tc.m, tc.k)
		}
	}
}

// TestRemoveUnderflowGuard 钉住下溢保护：拒绝时不改变任何计数器。
func TestRemoveUnderflowGuard(t *testing.T) {
	f := New(8, 3)
	f.Add(3)
	f.Add(5)
	f.Add(7)
	if err := f.Remove(5); err != nil {
		t.Fatalf("first Remove(5): %v", err)
	}
	before := f.Snapshot()
	if err := f.Remove(5); err != ErrNotPresent {
		t.Fatalf("second Remove(5) err = %v, want ErrNotPresent", err)
	}
	after := f.Snapshot()
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("counter %d changed by rejected Remove: %d -> %d", i, before[i], after[i])
		}
		if after[i] < 0 {
			t.Fatalf("counter %d underflowed to %d", i, after[i])
		}
	}
}
