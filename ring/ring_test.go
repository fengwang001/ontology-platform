package ring

import (
	"fmt"
	"testing"
)

// TestSuccessorQueryConstant 证明后继查询靠指针直接定位：
// 无论环多大，每次查询检查的节点条目个数恒为 1，不随 m 线性增长。
func TestSuccessorQueryConstant(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		r := New()
		for i := 0; i < m; i++ {
			if err := r.Add(fmt.Sprintf("n%05d", i)); err != nil {
				t.Fatalf("m=%d add: %v", m, err)
			}
		}
		for i := 0; i < 50; i++ {
			next, err := r.Successor("n00042")
			if err != nil {
				t.Fatalf("m=%d successor: %v", m, err)
			}
			if next != "n00043" {
				t.Fatalf("m=%d: successor = %q, want n00043", m, next)
			}
			if got := r.succChecks.Load(); got != 1 {
				t.Fatalf("m=%d: successor query checked %d entries, want 1", m, got)
			}
		}
	}
}

// TestRingClosure 钉住不变量 3：增删后从任意节点沿后继走，
// 恰好访问每个节点一次后回到自身。
func TestRingClosure(t *testing.T) {
	cases := []struct {
		name   string
		n      int
		remove []int // 要删除的下标
	}{
		{"single", 1, nil},
		{"small-no-remove", 4, nil},
		{"remove-head", 8, []int{0}},
		{"remove-tail", 8, []int{7}},
		{"remove-middle", 8, []int{3, 4}},
		{"remove-down-to-one", 5, []int{0, 1, 2, 3}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := New()
			ids := make([]string, 0, tc.n)
			for i := 0; i < tc.n; i++ {
				id := fmt.Sprintf("n%03d", i)
				ids = append(ids, id)
				if err := r.Add(id); err != nil {
					t.Fatalf("add %q: %v", id, err)
				}
			}
			kept := map[string]bool{}
			for _, id := range ids {
				kept[id] = true
			}
			for _, i := range tc.remove {
				if err := r.Remove(ids[i]); err != nil {
					t.Fatalf("remove %q: %v", ids[i], err)
				}
				delete(kept, ids[i])
			}
			if r.Size() != len(kept) {
				t.Fatalf("size = %d, want %d", r.Size(), len(kept))
			}
			if len(kept) == 0 {
				return
			}
			for start := range kept {
				seen, cur := map[string]bool{}, start
				for i := 0; i < r.Size(); i++ {
					if seen[cur] {
						t.Fatalf("from %q: revisited %q before full lap", start, cur)
					}
					seen[cur] = true
					next, err := r.Successor(cur)
					if err != nil {
						t.Fatalf("successor %q: %v", cur, err)
					}
					cur = next
				}
				if cur != start {
					t.Fatalf("from %q: lap ended at %q, want back to start", start, cur)
				}
				if len(seen) != r.Size() {
					t.Fatalf("from %q: visited %d nodes, want %d", start, len(seen), r.Size())
				}
			}
		})
	}
}
