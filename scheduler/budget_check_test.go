package scheduler

import "testing"

func assertBudget(t *testing.T, s *Scheduler, x, changedES, changedLF []int, op string) {
	t.Helper()
	fwdNodes := append(append([]int{}, x...), changedES...)
	bwdNodes := append(append([]int{}, x...), changedLF...)
	wantFwd := 0
	wantBwd := 0
	for _, v := range uniqueSorted(fwdNodes) {
		wantFwd += 1 + len(s.succs[v])
	}
	for _, v := range uniqueSorted(bwdNodes) {
		wantBwd += 1 + len(s.preds[v])
	}
	if s.FwdEval() > wantFwd {
		t.Fatalf("%s FwdEval=%d > budget %d", op, s.FwdEval(), wantFwd)
	}
	if s.BwdEval() > wantBwd {
		t.Fatalf("%s BwdEval=%d > budget %d", op, s.BwdEval(), wantBwd)
	}
	t.Logf("%s 判定依据: FwdEval=%d<=%d, BwdEval=%d<=%d", op, s.FwdEval(), wantFwd, s.BwdEval(), wantBwd)
}

func TestExampleUpdateBudget(t *testing.T) {
	s := buildExample(t, 10)
	r, _ := s.SetDuration(1, 6)
	assertBudget(t, s, []int{1}, r.ChangedES, r.ChangedLF, "SetDuration(1,6)")
	r, _ = s.SetConstraint(2, 6, -1)
	assertBudget(t, s, []int{2}, r.ChangedES, r.ChangedLF, "SetConstraint(2,6,-1)")
	r, _ = s.RemoveDep(0, 2)
	assertBudget(t, s, []int{0, 2}, r.ChangedES, r.ChangedLF, "RemoveDep(0,2)")
	v, _, _ := s.AddTask(2)
	assertBudget(t, s, []int{v}, nil, nil, "AddTask")
}
