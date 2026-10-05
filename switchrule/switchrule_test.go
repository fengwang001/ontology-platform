package switchrule

import (
	"testing"

	"ontology/plan"
)

type step struct {
	v    Verdict
	d    int
	want plan.Severity
}

func TestTransitions(t *testing.T) {
	cases := []struct {
		name  string
		lr    int
		steps []step
	}{
		{
			name: "Normal 最近 5 批拒收达 2 转 Tightened",
			lr:   2,
			steps: []step{
				{Accept, 0, plan.Normal},
				{Reject, 2, plan.Normal},
				{Accept, 1, plan.Normal},
				{Accept, 0, plan.Normal},
				{Accept, 0, plan.Normal},
				{Reject, 3, plan.Tightened},
			},
		},
		{
			name: "Normal 窗口滑动后旧拒收不再计入",
			lr:   2,
			steps: []step{
				{Accept, 0, plan.Normal},
				{Reject, 2, plan.Normal},
				{Accept, 1, plan.Normal},
				{Accept, 0, plan.Normal},
				{Accept, 0, plan.Normal},
				{Accept, 0, plan.Normal},
				{Reject, 2, plan.Normal}, // 最近 5 批（第 3..7 批）仅 1 批拒收
			},
		},
		{
			name: "Normal 不足 5 批取全部也可转 Tightened",
			lr:   2,
			steps: []step{
				{Reject, 2, plan.Normal},
				{Reject, 2, plan.Tightened},
			},
		},
		{
			name: "Normal 十批全收且 d 之和不超过 Lr 转 Reduced",
			lr:   2,
			steps: []step{
				{Accept, 0, plan.Normal}, {Accept, 0, plan.Normal},
				{Accept, 0, plan.Normal}, {Accept, 0, plan.Normal},
				{Accept, 1, plan.Normal}, {Accept, 0, plan.Normal},
				{Accept, 0, plan.Normal}, {Accept, 0, plan.Normal},
				{Accept, 0, plan.Normal}, {Accept, 1, plan.Reduced},
			},
		},
		{
			name: "Normal 十批 d 之和超过 Lr 不转",
			lr:   2,
			steps: []step{
				{Accept, 1, plan.Normal}, {Accept, 0, plan.Normal},
				{Accept, 0, plan.Normal}, {Accept, 0, plan.Normal},
				{Accept, 1, plan.Normal}, {Accept, 0, plan.Normal},
				{Accept, 0, plan.Normal}, {Accept, 0, plan.Normal},
				{Accept, 0, plan.Normal}, {Accept, 1, plan.Normal},
			},
		},
		{
			name: "Reduced 边缘接收转 Normal",
			lr:   0,
			steps: []step{
				{Accept, 0, plan.Normal}, {Accept, 0, plan.Normal},
				{Accept, 0, plan.Normal}, {Accept, 0, plan.Normal},
				{Accept, 0, plan.Normal}, {Accept, 0, plan.Normal},
				{Accept, 0, plan.Normal}, {Accept, 0, plan.Normal},
				{Accept, 0, plan.Normal}, {Accept, 0, plan.Reduced},
				{Marginal, 1, plan.Normal},
			},
		},
		{
			name: "Reduced 拒收转 Normal",
			lr:   0,
			steps: []step{
				{Accept, 0, plan.Normal}, {Accept, 0, plan.Normal},
				{Accept, 0, plan.Normal}, {Accept, 0, plan.Normal},
				{Accept, 0, plan.Normal}, {Accept, 0, plan.Normal},
				{Accept, 0, plan.Normal}, {Accept, 0, plan.Normal},
				{Accept, 0, plan.Normal}, {Accept, 0, plan.Reduced},
				{Reject, 2, plan.Normal},
			},
		},
		{
			name: "Tightened 连续接收达 5 转 Normal 且计数清零",
			lr:   2,
			steps: []step{
				{Reject, 2, plan.Normal},
				{Reject, 2, plan.Tightened},
				{Accept, 0, plan.Tightened}, {Accept, 0, plan.Tightened},
				{Accept, 0, plan.Tightened}, {Accept, 0, plan.Tightened},
				{Reject, 2, plan.Tightened}, // 连续接收清零，累计拒收 1
				{Accept, 0, plan.Tightened}, {Accept, 0, plan.Tightened},
				{Accept, 0, plan.Tightened}, {Accept, 0, plan.Tightened},
				{Accept, 0, plan.Normal}, // 连续接收达 5
				{Reject, 2, plan.Normal}, // 窗口只有这 1 批，不转
			},
		},
		{
			name: "Tightened 累计拒收达 5 转 Suspended",
			lr:   2,
			steps: []step{
				{Reject, 2, plan.Normal},
				{Reject, 2, plan.Tightened},
				{Reject, 2, plan.Tightened},
				{Accept, 0, plan.Tightened},
				{Reject, 2, plan.Tightened},
				{Reject, 2, plan.Tightened},
				{Accept, 0, plan.Tightened},
				{Reject, 2, plan.Tightened},
				{Reject, 2, plan.Suspended}, // 累计第 5 批拒收
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := New(tc.lr)
			for i, st := range tc.steps {
				if got := s.Record(st.v, st.d); got != st.want {
					t.Fatalf("step %d: severity = %v, want %v", i, got, st.want)
				}
			}
		})
	}
}

func TestResume(t *testing.T) {
	s := New(2)
	for i := 0; i < 2; i++ {
		s.Record(Reject, 2) // Normal -> Tightened
	}
	for i := 0; i < 5; i++ {
		s.Record(Reject, 2)
	}
	if s.Severity() != plan.Suspended {
		t.Fatalf("severity = %v, want Suspended", s.Severity())
	}
	s.Resume()
	if s.Severity() != plan.Tightened {
		t.Fatalf("severity = %v, want Tightened", s.Severity())
	}
	// 计数已清零：4 批拒收不再暂停，第 5 批才暂停。
	for i := 0; i < 4; i++ {
		if got := s.Record(Reject, 2); got != plan.Tightened {
			t.Fatalf("reject %d: severity = %v, want Tightened", i, got)
		}
	}
	if got := s.Record(Reject, 2); got != plan.Suspended {
		t.Fatalf("severity = %v, want Suspended", got)
	}
}

// TestLookedBounded 验证一次 Record 判定转移时考察的历史批记录不超过 10 条，
// 与该流的历史批数无关：历史 100 批与 10000 批两档对照，单次增量相同且有界。
func TestLookedBounded(t *testing.T) {
	delta := func(history int) int {
		s := New(0) // Lr=0，d=1 时十批之和恒大于 Lr，永远停留在 Normal
		for i := 0; i < history; i++ {
			s.Record(Accept, 1)
		}
		before := s.looked
		s.Record(Accept, 1)
		if s.Severity() != plan.Normal {
			t.Fatalf("history %d: severity = %v, want Normal", history, s.Severity())
		}
		return s.looked - before
	}
	d100 := delta(100)
	d10000 := delta(10000)
	t.Logf("looked delta: history=100 -> %d, history=10000 -> %d", d100, d10000)
	if d100 > 10 || d10000 > 10 {
		t.Fatalf("looked 越界: %d, %d", d100, d10000)
	}
	if d100 != d10000 {
		t.Fatalf("looked 与历史批数相关: %d != %d", d100, d10000)
	}
}
