package hook

import (
	"fmt"
	"testing"

	"ontology/snapshot"
)

func pass(snapshot.Snapshot) (bool, string) { return true, "" }

// 按类型匹配 + 注册序号递增。
func TestRegistryMatch(t *testing.T) {
	cases := []struct {
		name     string
		types    []string // 依次注册的 appliesTo
		query    string
		wantHits int
	}{
		{"单类型命中", []string{"Task", "Task", "User"}, "Task", 2},
		{"无命中", []string{"User", "User"}, "Task", 0},
		{"全部同类型", []string{"Task", "Task", "Task"}, "Task", 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := NewRegistry()
			for i, ty := range tc.types {
				h := r.Register(Hook{Name: fmt.Sprintf("h%d", i), AppliesTo: ty, Phase: Pre, Check: pass})
				if h.Seq() != i {
					t.Fatalf("注册序号 = %d, 期望 %d", h.Seq(), i)
				}
			}
			got := r.Match(tc.query)
			if len(got) != tc.wantHits {
				t.Fatalf("命中 %d 条, 期望 %d", len(got), tc.wantHits)
			}
			for _, h := range got {
				if h.AppliesTo != tc.query {
					t.Fatalf("匹配到错误类型 %q", h.AppliesTo)
				}
			}
			if r.Len() != len(tc.types) {
				t.Fatalf("Len = %d, 期望 %d", r.Len(), len(tc.types))
			}
		})
	}
}

// 匹配效率：访问数不随已注册钩子总数线性增长（按 appliesTo 索引）。
func TestMatchVisitsIndependentOfTotal(t *testing.T) {
	visits := map[int]int{}
	for _, total := range []int{100, 10000} {
		r := NewRegistry()
		r.Register(Hook{Name: "target", AppliesTo: "Task", Phase: Pre, Check: pass})
		for i := 0; i < total-1; i++ {
			r.Register(Hook{Name: fmt.Sprintf("noise-%d", i), AppliesTo: "Other", Phase: Post, Check: pass})
		}
		r.Match("Task")
		visits[total] = r.MatchVisits()
	}
	if visits[100] != visits[10000] {
		t.Fatalf("匹配访问数随总量增长: 100→%d, 10000→%d", visits[100], visits[10000])
	}
}

// Phase 字符串化。
func TestPhaseString(t *testing.T) {
	for _, tc := range []struct {
		p    Phase
		want string
	}{{Pre, "pre"}, {Post, "post"}} {
		if tc.p.String() != tc.want {
			t.Fatalf("Phase(%d).String() = %q, 期望 %q", tc.p, tc.p.String(), tc.want)
		}
	}
}
