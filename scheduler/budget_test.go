package scheduler

import (
	"fmt"
	"testing"
)

func buildChain(t *testing.T, n int) *Scheduler {
	t.Helper()
	s, err := New(int64(n), int64(n), 0)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if _, _, err := s.AddTask(0); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i+1 < n; i++ {
		s.preds[i+1] = append(s.preds[i+1], i)
		s.succs[i] = append(s.succs[i], i+1)
		s.edges[s.edgeKey(i, i+1)] = 0
		s.edgeCount++
	}
	s.order = makeSequence(n)
	for i := range s.pos {
		s.pos[i] = i
	}
	return s
}

func makeSequence(n int) []int {
	values := make([]int, n)
	for i := range values {
		values[i] = i
	}
	return values
}

func TestLongChainTerminalConstraintConstantReevaluation(t *testing.T) {
	for _, n := range []int{1000, 100000} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			s := buildChain(t, n)
			end := n - 1
			r, err := s.SetConstraint(end, int64(n+10), -1)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("输入: n=%d 长链末端 SetConstraint(snet=%d,fnlt=-1); 输出: ChangedES=%v FwdEval=%d BwdEval=%d",
				n, n+10, r.ChangedES, s.FwdEval(), s.BwdEval())
			if s.FwdEval() != 1 {
				t.Fatalf("FwdEval=%d, want 1; 判定依据: X={末端}, ChangedES={末端}, (1+出度)=1", s.FwdEval())
			}
			if s.BwdEval() != 0 {
				t.Fatalf("BwdEval=%d, want 0; 判定依据: fnlt 未改变，不触发后向重算", s.BwdEval())
			}
		})
	}
}

func TestDurationBackwardStopsAtConstraint(t *testing.T) {
	s, _ := New(5, 4, 100)
	for i := 0; i < 5; i++ {
		s.AddTask(2)
	}
	for i := 0; i < 4; i++ {
		s.AddDep(i, i+1, 0)
	}
	s.SetConstraint(1, 0, 20)
	r, _ := s.SetDuration(4, 5)
	t.Logf("输入: 0-1(fnlt=20)-2-3-4 后 SetDuration(4,5); 输出: ChangedLF=%v BwdEval=%d",
		r.ChangedLF, s.BwdEval())
	intsEqual(t, r.ChangedLF, []int{2, 3}, "LF propagation stops at fnlt")
	if s.BwdEval() > 5 {
		t.Fatalf("BwdEval=%d exceeds seed and changed-LF neighborhood", s.BwdEval())
	}
	lf1, _ := s.LF(1)
	int64Equal(t, lf1, 20, "fnlt truncates backward propagation")
}
