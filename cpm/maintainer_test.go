package cpm

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func requireTime(t *testing.T, m *Maintainer, v int, wantES, wantEF, wantLS, wantLF, wantTF, wantFF int64, wantCritical bool) {
	t.Helper()
	es, ef, ls, lf, tf, ff, critical, err := m.Time(v)
	if err != nil {
		t.Fatalf("Time(%d): %v", v, err)
	}
	got := []int64{es, ef, ls, lf, tf, ff}
	want := []int64{wantES, wantEF, wantLS, wantLF, wantTF, wantFF}
	if !reflect.DeepEqual(got, want) || critical != wantCritical {
		t.Fatalf("task %d = %v critical=%v, want %v critical=%v", v, got, critical, want, wantCritical)
	}
}

func TestSpecExampleAndUpdates(t *testing.T) {
	m, err := NewMaintainer(10, 10, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, duration := range []int64{3, 2, 4, 1} {
		if _, _, err := m.AddTask(duration); err != nil {
			t.Fatal(err)
		}
	}
	for _, edge := range []struct {
		u, v int
		lag  int64
	}{
		{0, 1, 0}, {0, 2, 1}, {1, 3, 0}, {2, 3, -1},
	} {
		if _, err := m.AddDep(edge.u, edge.v, edge.lag); err != nil {
			t.Fatal(err)
		}
	}

	requireTime(t, m, 0, 0, 3, 2, 5, 2, 0, true)
	requireTime(t, m, 1, 3, 5, 7, 9, 4, 2, false)
	requireTime(t, m, 2, 4, 8, 6, 10, 2, 0, true)
	requireTime(t, m, 3, 7, 8, 9, 10, 2, 0, true)
	if got, want := m.CriticalPath(), []int{0, 2, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("path=%v want %v", got, want)
	}

	m.SetBaseline()
	report, err := m.SetDuration(1, 6)
	if err != nil {
		t.Fatal(err)
	}
	wantReport := &UpdateReport{ChangedES: []int{3}, ChangedLF: []int{0}, OldPF: 8, NewPF: 10, CritAdded: []int{1}, CritRemoved: []int{2}}
	if !reflect.DeepEqual(report, wantReport) {
		t.Fatalf("report=%+v want %+v", report, wantReport)
	}
	if got, want := m.CriticalPath(), []int{0, 1, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("path=%v want %v", got, want)
	}
	variance, _ := m.Variance(1)
	if variance != 4 {
		t.Fatalf("variance task 1=%d", variance)
	}
	variance, _ = m.Variance(3)
	if variance != 2 {
		t.Fatalf("variance task 3=%d", variance)
	}

	report, err = m.SetConstraint(2, 6, -1)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(report, &UpdateReport{ChangedES: []int{2}, ChangedLF: []int{}, OldPF: 10, NewPF: 10, CritAdded: []int{2}, CritRemoved: []int{}}) {
		t.Fatalf("unexpected report %+v", report)
	}
	if got, want := m.CriticalPath(), []int{0, 1, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("path=%v want %v", got, want)
	}
}

func TestRejectionOrderAndAtomicity(t *testing.T) {
	m, _ := NewMaintainer(1, 1, 10)
	v, _, _ := m.AddTask(1)

	if _, _, err := m.AddTask(1); !errors.Is(err, ErrCapacityExceeded) {
		t.Fatalf("capacity error=%v", err)
	}
	if _, _, err := m.AddTask(1 << 40); !errors.Is(err, ErrInvalidParameter) {
		t.Fatalf("duration error=%v", err)
	}
	if _, err := m.AddDep(v, v, 0); !errors.Is(err, ErrCycle) {
		t.Fatalf("self cycle error=%v", err)
	}
	if _, err := m.AddDep(0, 9, 0); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("task error=%v", err)
	}
	if _, err := m.AddDep(0, 0, 1<<60); !errors.Is(err, ErrInvalidParameter) {
		t.Fatalf("lag validation must precede cycle: %v", err)
	}
	if _, err := m.RemoveDep(0, 9); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("remove task error=%v", err)
	}
	if _, err := m.RemoveDep(0, 0); !errors.Is(err, ErrDependencyNotFound) {
		t.Fatalf("remove dependency error=%v", err)
	}
	if _, err := m.Variance(0); !errors.Is(err, ErrNoBaseline) {
		t.Fatalf("variance baseline=%v", err)
	}
	m.SetBaseline()
	if tasks, deps := m.Counts(); tasks != 1 || deps != 0 {
		t.Fatalf("counts=(%d,%d)", tasks, deps)
	}
}

func TestNegativeLagConstraintFinishDeadlineAndFloat(t *testing.T) {
	m, _ := NewMaintainer(10, 10, 10)
	for range 3 {
		if _, _, err := m.AddTask(5); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.AddDep(0, 1, -4); err != nil {
		t.Fatal(err)
	}
	// ES(1)=EF(0)-4=1, which is earlier than EF(0)=5.
	requireTime(t, m, 1, 1, 6, 5, 10, 4, 0, true)

	// A start constraint can raise ES and make the predecessor edge non-driving.
	if _, err := m.SetConstraint(1, 3, -1); err != nil {
		t.Fatal(err)
	}
	requireTime(t, m, 1, 3, 8, 5, 10, 2, 0, true)

	// A finish constraint lowers LF.
	if _, err := m.SetConstraint(2, 0, 4); err != nil {
		t.Fatal(err)
	}
	requireTime(t, m, 2, 0, 5, -1, 4, -1, 3, true)

	// D=10 is below PF=8? no; add long task to force negative floats and verify minimum-TF criticality.
	if _, _, err := m.AddTask(12); err != nil {
		t.Fatal(err)
	}
	requireTime(t, m, 2, 0, 5, -1, 4, -1, 7, false)
	requireTime(t, m, 3, 0, 12, -2, 10, -2, 0, true)
	if got := m.CriticalPath(); len(got) == 0 || got[0] != 3 {
		t.Fatalf("critical path under deadline overflow=%v", got)
	}
}

func TestLooseDeadlineCriticalFloatIsPositive(t *testing.T) {
	m, _ := NewMaintainer(2, 1, 100)
	m.AddTask(2)
	m.AddTask(3)
	m.AddDep(0, 1, 0)
	es, ef, ls, lf, tf, _, critical, _ := m.Time(1)
	if es != 2 || ef != 5 || ls != 97 || lf != 100 || tf != 95 || !critical {
		t.Fatalf("unexpected loose task times es=%d ef=%d ls=%d lf=%d tf=%d critical=%v", es, ef, ls, lf, tf, critical)
	}
	if got := m.CriticalPath(); !reflect.DeepEqual(got, []int{0, 1}) {
		t.Fatalf("path=%v", got)
	}
}

func TestRemoveDependencyAllowsESFallBack(t *testing.T) {
	m, _ := NewMaintainer(3, 2, 10)
	m.AddTask(6)
	m.AddTask(1)
	m.AddTask(1)
	m.AddDep(0, 2, 0)
	m.AddDep(1, 2, 0)
	if es, _, _, _, _, _, _, _ := m.Time(2); es != 6 {
		t.Fatalf("ES before removal=%d", es)
	}
	report, err := m.RemoveDep(0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(report.ChangedES, []int{2}) {
		t.Fatalf("changed=%v", report.ChangedES)
	}
	if es, _, _, _, _, _, _, _ := m.Time(2); es != 1 {
		t.Fatalf("ES after removal=%d", es)
	}
}

func TestBaselineNewTaskHasNoBaseline(t *testing.T) {
	m, _ := NewMaintainer(3, 1, 10)
	m.AddTask(2)
	m.SetBaseline()
	v, _, _ := m.AddTask(3)
	if _, err := m.Variance(v); !errors.Is(err, ErrNoBaseline) {
		t.Fatalf("new task variance=%v", err)
	}
}

func TestTerminalConstraintCountsDoNotGrow(t *testing.T) {
	for _, n := range []int{1000, 100000} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			m := buildChain(t, n)
			report, err := m.SetConstraint(n-1, 5_000_000, -1)
			if err != nil {
				t.Fatal(err)
			}
			fwd, bwd := m.EvaluationCounts()
			if fwd != 1 || bwd > 1 {
				t.Fatalf("n=%d counts=(%d,%d)", n, fwd, bwd)
			}
			if !reflect.DeepEqual(report.ChangedES, []int{n - 1}) {
				t.Fatalf("changed ES=%v", report.ChangedES)
			}
			assertEvaluationBounds(t, m, []int{n - 1}, []int{n - 1}, report)
		})
	}
}

func buildChain(t *testing.T, n int) *Maintainer {
	t.Helper()
	m, err := NewMaintainer(n, n-1, 1_000_000_000_000)
	if err != nil {
		t.Fatal(err)
	}
	for range n {
		if _, _, err := m.AddTask(0); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i < n; i++ {
		if _, err := m.AddDep(i-1, i, 0); err != nil {
			t.Fatal(err)
		}
	}
	return m
}

func assertEvaluationBounds(t *testing.T, m *Maintainer, xFwd, xBwd []int, report *UpdateReport) {
	t.Helper()
	snapshot := m.Snapshot()
	fwd, bwd := m.EvaluationCounts()
	outdegree := make([]int, len(snapshot.ES))
	indegree := make([]int, len(snapshot.ES))
	for _, edge := range snapshot.Edges {
		outdegree[edge.From]++
		indegree[edge.To]++
	}
	fwdSet := map[int]struct{}{}
	for _, groups := range [][]int{xFwd, report.ChangedES} {
		for _, v := range groups {
			fwdSet[v] = struct{}{}
		}
	}
	bwdSet := map[int]struct{}{}
	for _, groups := range [][]int{xBwd, report.ChangedLF} {
		for _, v := range groups {
			bwdSet[v] = struct{}{}
		}
	}
	fwdBound, bwdBound := 0, 0
	for v := range fwdSet {
		fwdBound += 1 + outdegree[v]
	}
	for v := range bwdSet {
		bwdBound += 1 + indegree[v]
	}
	if fwd > fwdBound || bwd > bwdBound {
		t.Fatalf("evaluations=(%d,%d) bounds=(%d,%d)", fwd, bwd, fwdBound, bwdBound)
	}
}
