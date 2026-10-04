package idmap

import "testing"

// tid 从 1 连续分配、永不改变也不回收；映射单射。
func TestAssignContinuousInjective(t *testing.T) {
	m := New()
	keys := []Key{{1, 10}, {2, 10}, {1, 11}, {3, 7}}
	seen := map[int64]Key{}
	for i, k := range keys {
		tid := m.Assign(k)
		if tid != int64(i+1) {
			t.Fatalf("Assign(%v)=%d, want %d", k, tid, i+1)
		}
		if prev, dup := seen[tid]; dup {
			t.Fatalf("tid %d assigned to both %v and %v", tid, prev, k)
		}
		seen[tid] = k
	}
	// 重复 Assign 沿用原 tid。
	for i, k := range keys {
		if tid := m.Assign(k); tid != int64(i+1) {
			t.Fatalf("re-Assign(%v)=%d, want %d", k, tid, i+1)
		}
	}
	if m.Next() != 5 || m.Len() != 4 {
		t.Fatalf("Next=%d Len=%d, want 5,4", m.Next(), m.Len())
	}
	if tid, ok := m.Tid(Key{1, 10}); !ok || tid != 1 {
		t.Fatalf("Tid(1,10)=%d,%v, want 1,true", tid, ok)
	}
	if _, ok := m.Tid(Key{9, 9}); ok {
		t.Fatal("Tid(9,9) should be unmapped")
	}
}
