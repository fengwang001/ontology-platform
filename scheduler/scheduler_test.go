package scheduler

import (
	"errors"
	"testing"
)

func int64Equal(t *testing.T, got, want int64, name string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %d, want %d; 判定依据: 应与规范定义一致", name, got, want)
	}
}

func intsEqual(t *testing.T, got, want []int, name string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s len = %d (%v), want %d (%v); 判定依据: 编号列表长度和顺序", name, len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s = %v, want %v; 判定依据: 编号列表应升序且逐项一致", name, got, want)
		}
	}
}

func buildExample(t *testing.T, deadline int64) *Scheduler {
	t.Helper()
	s, err := New(10, 10, deadline)
	if err != nil {
		t.Fatal(err)
	}
	for _, dur := range []int64{3, 2, 4, 1} {
		if _, _, err := s.AddTask(dur); err != nil {
			t.Fatal(err)
		}
	}
	for _, edge := range [][3]int64{{0, 1, 0}, {0, 2, 1}, {1, 3, 0}, {2, 3, -1}} {
		if _, err := s.AddDep(int(edge[0]), int(edge[1]), edge[2]); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func TestExampleSchedule(t *testing.T) {
	s := buildExample(t, 10)
	t.Logf("输入: D=10, durations=[3 2 4 1], deps=(0,1,0),(0,2,1),(1,3,0),(2,3,-1)")

	wantES := []int64{0, 3, 4, 7}
	wantEF := []int64{3, 5, 8, 8}
	wantLF := []int64{5, 9, 10, 10}
	wantLS := []int64{2, 7, 6, 9}
	wantTF := []int64{2, 4, 2, 2}
	wantFF := []int64{0, 2, 0, 0}
	for v := 0; v < 4; v++ {
		es, _ := s.ES(v)
		ef, _ := s.EF(v)
		ls, _ := s.LS(v)
		lf, _ := s.LF(v)
		tf, _ := s.TF(v)
		ff, _ := s.FF(v)
		int64Equal(t, es, wantES[v], "ES")
		int64Equal(t, ef, wantEF[v], "EF")
		int64Equal(t, ls, wantLS[v], "LS")
		int64Equal(t, lf, wantLF[v], "LF")
		int64Equal(t, tf, wantTF[v], "TF")
		int64Equal(t, ff, wantFF[v], "FF")
	}
	int64Equal(t, s.PF(), 8, "PF")
	intsEqual(t, s.CriticalTasks(), []int{0, 2, 3}, "critical")
	intsEqual(t, s.CriticalPath(), []int{0, 2, 3}, "path")
	t.Logf("输出: ES=%v EF=%v LS=%v LF=%v TF=%v FF=%v PF=%d path=%v",
		wantES, wantEF, wantLS, wantLF, wantTF, wantFF, 8, []int{0, 2, 3})
}

func TestExampleUpdatesBaselineAndDrivingEdge(t *testing.T) {
	s := buildExample(t, 10)
	s.SetBaseline()

	r, err := s.SetDuration(1, 6)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("输入: SetBaseline 后 SetDuration(1,6); 输出: %+v", *r)
	intsEqual(t, r.ChangedES, []int{3}, "ChangedES")
	intsEqual(t, r.ChangedLF, []int{0}, "ChangedLF")
	int64Equal(t, r.OldPF, 8, "OldPF")
	int64Equal(t, r.NewPF, 10, "NewPF")
	intsEqual(t, r.CritAdded, []int{1}, "CritAdded")
	intsEqual(t, r.CritRemoved, []int{2}, "CritRemoved")
	intsEqual(t, s.CriticalTasks(), []int{0, 1, 3}, "critical")
	intsEqual(t, s.CriticalPath(), []int{0, 1, 3}, "path")
	v1, _ := s.Variance(1)
	v3, _ := s.Variance(3)
	int64Equal(t, v1, 4, "variance 1")
	int64Equal(t, v3, 2, "variance 3")

	r, err = s.SetConstraint(2, 6, -1)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("输入: SetConstraint(2,6,-1); 输出: %+v", *r)
	intsEqual(t, r.ChangedES, []int{2}, "ChangedES")
	intsEqual(t, r.ChangedLF, []int{}, "ChangedLF")
	intsEqual(t, r.CritAdded, []int{2}, "CritAdded")
	intsEqual(t, r.CritRemoved, []int{}, "CritRemoved")
	intsEqual(t, s.CriticalPath(), []int{0, 1, 3}, "path remains via driving edge")
}

func TestNegativeLagAndSnetDriving(t *testing.T) {
	s, _ := New(3, 2, 20)
	s.AddTask(10)
	s.AddTask(2)
	s.AddDep(0, 1, -4)
	es, _ := s.ES(1)
	ef0, _ := s.EF(0)
	int64Equal(t, es, 6, "negative-lag ES")
	if es >= ef0 {
		t.Fatalf("后继 ES %d 应小于前驱 EF %d", es, ef0)
	}

	s, _ = New(3, 2, 1000)
	s.AddTask(10)
	s.AddTask(2)
	s.AddDep(0, 1, -4)
	r, _ := s.SetConstraint(1, 9, -1)
	es, _ = s.ES(1)
	int64Equal(t, es, 9, "snet-raised ES")
	intsEqual(t, r.ChangedES, []int{1}, "ChangedES")
	lf0, _ := s.LF(0)
	lf1, _ := s.LF(1)
	int64Equal(t, lf0, 1000, "deadline remains an attained LF bound")
	int64Equal(t, lf1, 1000, "successor deadline LF")
}

func TestFnltAndTightDeadline(t *testing.T) {
	s, _ := New(2, 1, 5)
	s.AddTask(2)
	s.AddTask(5)
	s.AddDep(0, 1, 0)
	s.SetConstraint(0, 0, 1)
	lf0, _ := s.LF(0)
	int64Equal(t, lf0, 0, "fnlt LF also bounded by successor schedule")
	s2, _ := New(1, 1, 5)
	s2.AddTask(2)
	s2.SetConstraint(0, 0, 1)
	lf0, _ = s2.LF(0)
	int64Equal(t, lf0, 1, "fnlt LF")

	tf0, _ := s.TF(0)
	tf1, _ := s.TF(1)
	if tf0 >= 0 || tf1 >= 0 {
		t.Fatalf("D<PF 时 TF 应全负: TF0=%d TF1=%d", tf0, tf1)
	}
	wantCrit := []int{1}
	if tf0 == tf1 {
		wantCrit = []int{0, 1}
	}
	intsEqual(t, s.CriticalTasks(), wantCrit, "minimum negative TF")
}

func TestLooseDeadlineCriticalPositiveTF(t *testing.T) {
	s, _ := New(1, 1, 100)
	s.AddTask(5)
	tf, _ := s.TF(0)
	int64Equal(t, tf, 95, "positive critical TF")
	intsEqual(t, s.CriticalTasks(), []int{0}, "critical exists")
	intsEqual(t, s.CriticalPath(), []int{0}, "path")
}

func TestCriticalSetSwitchWithoutTimeChanges(t *testing.T) {
	s, _ := New(2, 1, 10)
	s.AddTask(3)
	s.AddTask(5)
	intsEqual(t, s.CriticalTasks(), []int{1}, "initial critical")
	r, _ := s.SetDuration(0, 7)
	intsEqual(t, r.ChangedLF, []int{}, "LF unchanged by nonbinding fnlt")
	intsEqual(t, r.CritAdded, []int{0}, "added critical")
	intsEqual(t, r.CritRemoved, []int{1}, "removed critical")
	intsEqual(t, s.CriticalTasks(), []int{0}, "switched critical")
}

func TestParallelCriticalPathsChooseSmallest(t *testing.T) {
	s, _ := New(5, 4, 6)
	for i := 0; i < 5; i++ {
		s.AddTask(2)
	}
	for _, edge := range [][2]int{{0, 1}, {0, 2}, {1, 3}, {2, 4}} {
		if _, err := s.AddDep(edge[0], edge[1], 0); err != nil {
			t.Fatal(err)
		}
	}
	path := s.CriticalPath()
	intsEqual(t, path, []int{0, 1, 3}, "smallest parallel path")
}

func TestFreeFloatSuccessorAndSink(t *testing.T) {
	s, _ := New(3, 2, 20)
	s.AddTask(2)
	s.AddTask(1)
	s.AddTask(1)
	s.AddDep(0, 1, 0)
	s.AddDep(0, 2, 3)
	ff0, _ := s.FF(0)
	ff1, _ := s.FF(1)
	ff2, _ := s.FF(2)
	int64Equal(t, ff0, 0, "minimum successor FF")
	int64Equal(t, ff1, 3, "sink FF uses PF")
	int64Equal(t, ff2, 0, "sink FF")
}

func TestRemoveDepFallsBackAndRejectedNoStateChange(t *testing.T) {
	s, _ := New(3, 4, 10)
	s.AddTask(5)
	s.AddTask(0)
	s.AddTask(0)
	s.AddDep(0, 1, 2)
	s.AddDep(0, 2, 1)
	s.RemoveDep(0, 1)
	es1, _ := s.ES(1)
	int64Equal(t, es1, 0, "ES falls back to snet")

	if _, _, err := s.AddTask(0); !errors.Is(err, ErrTaskLimit) {
		t.Fatalf("task limit error = %v", err)
	}
	if _, err := s.AddDep(0, 2, 0); !errors.Is(err, ErrDepExists) {
		t.Fatalf("duplicate error = %v", err)
	}
	if _, err := s.AddDep(2, 0, 0); !errors.Is(err, ErrCycle) {
		t.Fatalf("cycle error = %v", err)
	}
	if _, err := s.RemoveDep(0, 1); !errors.Is(err, ErrDepNotFound) {
		t.Fatalf("missing dep error = %v", err)
	}
	if s.PF() != 6 {
		t.Fatalf("rejected operations changed PF to %d", s.PF())
	}
}

func TestBaselineNewTaskAndNoBaseline(t *testing.T) {
	s, _ := New(3, 1, 10)
	s.AddTask(2)
	if _, err := s.Variance(0); !errors.Is(err, ErrNoBaseline) {
		t.Fatalf("no baseline error = %v", err)
	}
	s.SetBaseline()
	s.AddTask(4)
	if _, err := s.Variance(1); !errors.Is(err, ErrNoBaseline) {
		t.Fatalf("new-task baseline error = %v", err)
	}
	s.SetDuration(0, 3)
	v, _ := s.Variance(0)
	int64Equal(t, v, 1, "baseline variance")
}

func TestInvalidErrorPriority(t *testing.T) {
	if _, err := New(0, 1, 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal(err)
	}
	s, _ := New(1, 1, 10)
	if _, _, err := s.AddTask(-1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal(err)
	}
	_, err := s.AddDep(0, 1, 2_000_000)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("AddDep invalid first, got %v", err)
	}
	s.AddTask(1)
	_, err = s.AddDep(0, 1, 2_000_000)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("AddDep invalid before existence, got %v", err)
	}
}
