package schedule

import (
	"testing"

	"ontology/hook"
	"ontology/snapshot"
)

func pass(snapshot.Snapshot) (bool, string) { return true, "" }

func mk(t *testing.T, defs ...hook.Hook) []hook.Hook {
	t.Helper()
	r := hook.NewRegistry()
	for _, d := range defs {
		d.Check = pass
		r.Register(d)
	}
	return r.Match("T")
}

func names(hs []hook.Hook) []string {
	out := make([]string, len(hs))
	for i, h := range hs {
		out[i] = h.Name
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// 分组与组内排序：pre 全先于 post；组内 (priority, name) 升序。
func TestPlanOrdering(t *testing.T) {
	cases := []struct {
		name string
		defs []hook.Hook
		want []string
	}{
		{
			"pre先于post",
			[]hook.Hook{
				{Name: "z-post", AppliesTo: "T", Phase: hook.Post},
				{Name: "a-pre", AppliesTo: "T", Phase: hook.Pre},
			},
			[]string{"a-pre", "z-post"},
		},
		{
			"组内priority升序",
			[]hook.Hook{
				{Name: "p-low", AppliesTo: "T", Phase: hook.Pre, Priority: 10},
				{Name: "p-high", AppliesTo: "T", Phase: hook.Pre, Priority: -1},
			},
			[]string{"p-high", "p-low"},
		},
		{
			"同priority按name",
			[]hook.Hook{
				{Name: "b", AppliesTo: "T", Phase: hook.Post},
				{Name: "a", AppliesTo: "T", Phase: hook.Post},
			},
			[]string{"a", "b"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := names(Plan(mk(t, tc.defs...)))
			if !equal(got, tc.want) {
				t.Fatalf("执行序列 = %v, 期望 %v", got, tc.want)
			}
		})
	}
}

// 确定性：同一钩子集合、任意注册顺序，执行序列逐字节相同。
func TestPlanDeterministicAcrossRegistrationOrders(t *testing.T) {
	orders := [][]string{
		{"p1", "q1", "p2", "q2", "p3"},
		{"q2", "p3", "q1", "p1", "p2"},
		{"p3", "p2", "p1", "q2", "q1"},
	}
	var first []string
	for _, order := range orders {
		r := hook.NewRegistry()
		for _, n := range order {
			phase := hook.Pre
			if n[0] == 'q' {
				phase = hook.Post
			}
			r.Register(hook.Hook{Name: n, AppliesTo: "T", Phase: phase, Check: pass})
		}
		got := names(Plan(r.Match("T")))
		if first == nil {
			first = got
			continue
		}
		if !equal(got, first) {
			t.Fatalf("注册顺序 %v 得到 %v, 期望 %v", order, got, first)
		}
	}
}
