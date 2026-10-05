package cpm

import "testing"

func TestDebugConstraintHeap(t *testing.T) {
	m, _ := NewMaintainer(2, 2, 100)
	m.AddTask(1)
	m.AddTask(0)
	m.SetConstraint(0, 4, 12)
	m.AddDep(1, 0, 2)
	for _, c := range m.inHeaps[0] {
		t.Logf("before value=%d alive=%v kind=%d", c.value, c.alive, c.kind)
	}
	r, err := m.SetConstraint(0, 0, 12)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range m.inHeaps[0] {
		t.Logf("after value=%d alive=%v kind=%d index=%d", c.value, c.alive, c.kind, c.index)
	}
	t.Logf("report=%+v es=%d", r, m.es[0])
}

func TestDebugRemoveFinish(t *testing.T) {
	m, _ := NewMaintainer(5, 5, 100)
	m.AddTask(4)
	m.AddTask(3)
	m.AddDep(1, 0, -1)
	m.SetConstraint(0, 3, 2)
	t.Logf("before lf=%d heap=%+v", m.lf[0], m.outHeaps[0])
	m.RemoveDep(1, 0)
	for _, c := range m.outHeaps[0] {
		t.Logf("after value=%d alive=%v kind=%d index=%d", c.value, c.alive, c.kind, c.index)
	}
	t.Logf("after lf=%d ls=%d", m.lf[0], m.ls[0])
}

func TestDebugExactRemove(t *testing.T) {
	m, _ := NewMaintainer(5, 5, 100)
	for _, d := range []int64{3, 3, 4, 0, 6} {
		m.AddTask(d)
	}
	m.SetDuration(0, 4)
	m.AddDep(1, 0, -1)
	m.RemoveDep(1, 0)
	m.SetConstraint(0, 3, 2)
	t.Logf("after constraint lf=%d", m.lf[0])
	for _, c := range m.outHeaps[0] {
		t.Logf("before reset value=%d alive=%v kind=%d", c.value, c.alive, c.kind)
	}
	m.SetConstraint(0, 0, 12)
	s := m.Snapshot()
	t.Logf("snapshot=%+v", s)
	if s.LF[0] != 2 || s.LS[0] != -2 {
		t.Fatalf("task0 LF=%d LS=%d", s.LF[0], s.LS[0])
	}
}

func TestDebugExactRandomSequence(t *testing.T) {
	m, _ := NewMaintainer(12, 30, 100)
	dump := func(stage string) {
		t.Logf("%s task0 heap:", stage)
		for _, c := range m.outHeaps[0] {
			t.Logf("  v=%d alive=%v kind=%d idx=%d", c.value, c.alive, c.kind, c.index)
		}
	}
	for _, d := range []int64{0, 5, 3, 0, 5, 6, 0, 4, 4, 2, 1, 3} {
		m.AddTask(d)
	}
	m.SetDuration(0, 1)
	m.SetDuration(1, 3)
	m.SetDuration(2, 3)
	m.SetDuration(3, 0)
	m.SetDuration(4, 0)
	m.SetDuration(5, 1)
	m.SetDuration(7, 4)
	m.SetDuration(0, 4)
	m.AddDep(0, 1, -2)
	m.AddDep(2, 1, -2)
	m.AddDep(1, 3, -2)
	m.AddDep(7, 6, -1)
	m.AddDep(7, 4, 0)
	m.AddDep(1, 0, 2)
	m.SetConstraint(5, 3, 13)
	m.SetConstraint(5, 5, 5)
	m.SetConstraint(0, 0, 12)
	dump("after 0,12")
	m.SetConstraint(1, 9, 19)
	m.SetConstraint(0, 7, 4)
	dump("after 7,4")
	m.SetConstraint(0, 7, 5)
	dump("after 7,5")
	m.SetConstraint(0, 4, 17)
	dump("after 4,17")
	m.SetConstraint(0, 0, 19)
	dump("after 0,19")
	m.SetConstraint(0, 3, 2)
	dump("after 3,2")
	m.AddDep(2, 0, 2)
	m.RemoveDep(2, 0)
	m.AddDep(0, 2, 0)
	m.RemoveDep(0, 2)
	m.AddDep(0, 1, 3)
	m.RemoveDep(0, 1)
	m.AddDep(1, 0, -1)
	m.RemoveDep(1, 0)
	m.AddDep(0, 1, 0)
	dump("after last add")
	dump("before remove")
	m.RemoveDep(0, 1)
	s := m.Snapshot()
	t.Logf("snapshot=%+v", s)
	if s.LF[0] != 2 {
		t.Fatalf("LF0=%d", s.LF[0])
	}
}
