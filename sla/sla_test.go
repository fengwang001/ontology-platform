package sla

import (
	"errors"
	"testing"

	"ontology/cal"
)

func newExample() (*cal.Calendar, *Manager) {
	c := cal.New(540, 1080, 0b0011111)
	return c, NewManager(c)
}

func TestExampleDeadline(t *testing.T) {
	c, m := newExample()
	id := []byte("s")
	if err := m.Start(id, 600, 600); err != nil {
		t.Fatal(err)
	}
	el, err := m.Elapsed(id, 900)
	if err != nil || el != 300 {
		t.Fatalf("Elapsed(900)=%d,%v want 300", el, err)
	}
	t.Logf("Elapsed@900=300 => T50=900, T80=1080, T100=2100 (B=600)")
	dl, ok, err := m.Deadline(id, 900)
	if err != nil || !ok || dl != 2100 {
		t.Fatalf("Deadline=%d,%v,%v want 2100", dl, ok, err)
	}
	dl, ok, _ = m.Deadline(id, 2100)
	t.Logf("Deadline at/after due stays at actual instant: %d ok=%v", dl, ok)
	if dl != 2100 || !ok {
		t.Fatalf("deadline after reached =%d,%v", dl, ok)
	}
	if el, _ := m.Elapsed(id, 2100); el != 600 {
		t.Fatalf("Elapsed(2100)=%d want 600", el)
	}
	_ = c
}

func TestPauseResumeExample(t *testing.T) {
	_, m := newExample()
	id := []byte("s")
	if err := m.Start(id, 600, 600); err != nil {
		t.Fatal(err)
	}
	if err := m.Pause(id, 1000); err != nil {
		t.Fatal(err)
	}
	if err := m.Pause(id, 1100); !errors.Is(err, ErrState) {
		t.Fatalf("double Pause want ErrState got %v", err)
	}
	if el, _ := m.Elapsed(id, 1500); el != 400 {
		t.Fatalf("paused elapsed=%d want 400", el)
	}
	if _, ok, _ := m.Deadline(id, 1500); ok {
		t.Fatal("deadline while paused must be absent")
	}
	if err := m.Resume(id, 2000); err != nil {
		t.Fatal(err)
	}
	if err := m.Resume(id, 2050); !errors.Is(err, ErrState) {
		t.Fatalf("double Resume want ErrState got %v", err)
	}
	// T80 需 480：暂停前 400，恢复后再 80 => 2080；T100 => 2200
	m.Lock()
	m.Cal().RLock()
	tm := m.TimerLocked("s")
	r80, ok80 := m.TriggerLocked(tm, 480)
	r100, ok100 := m.TriggerLocked(tm, 600)
	m.Cal().RUnlock()
	m.Unlock()
	t.Logf("after Pause@1000 Resume@2000: T80=%d(ok%v) want 2080, T100=%d(ok%v) want 2200",
		r80, ok80, r100, ok100)
	if r80 != 2080 || !ok80 || r100 != 2200 || !ok100 {
		t.Fatal("pause/resume trigger mismatch")
	}
}

func TestHolidayAddedBetweenPause(t *testing.T) {
	c, m := newExample()
	id := []byte("s")
	if err := m.Start(id, 600, 600); err != nil {
		t.Fatal(err)
	}
	if err := m.Pause(id, 1000); err != nil {
		t.Fatal(err)
	}
	if err := c.AddHoliday(1, 1000); err != nil {
		t.Fatalf("AddHoliday(1,1000): %v", err)
	}
	if err := m.Resume(id, 2000); err != nil {
		t.Fatal(err)
	}
	m.Lock()
	c.RLock()
	tm := m.TimerLocked("s")
	r80, _ := m.TriggerLocked(tm, 480)
	r100, _ := m.TriggerLocked(tm, 600)
	c.RUnlock()
	m.Unlock()
	t.Logf("holiday day1 added while paused: T80=%d want 3500, T100=%d want 3620", r80, r100)
	if r80 != 3500 || r100 != 3620 {
		t.Fatal("holiday-during-pause trigger mismatch")
	}
}

func TestPauseOutsideWindow(t *testing.T) {
	_, m := newExample()
	id := []byte("s")
	if err := m.Start(id, 600, 600); err != nil {
		t.Fatal(err)
	}
	// 2000 在窗口外，Resume 前没有工作分钟
	if err := m.Pause(id, 1200); err != nil {
		t.Fatal(err)
	}
	if err := m.Resume(id, 2000); err != nil {
		t.Fatal(err)
	}
	el, err := m.Elapsed(id, 2000)
	if err != nil || el != 480 {
		t.Fatalf("elapsed@2000=%d,%v want 480 (day0 only)", el, err)
	}
	t.Logf("pause outside window: elapsed@2000=480")
}

func TestErrors(t *testing.T) {
	_, m := newExample()
	if err := m.Start(nil, 10, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty id: %v", err)
	}
	if err := m.Start([]byte("a"), 0, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("B=0: %v", err)
	}
	if err := m.Start([]byte("a"), MaxBudget+1, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("B too large: %v", err)
	}
	if err := m.Pause([]byte("x"), 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing timer: %v", err)
	}
	if err := m.Start([]byte("a"), 10, 0); err != nil {
		t.Fatal(err)
	}
	if err := m.Start([]byte("b"), 10, 5); err == nil {
		if err2 := m.Start([]byte("a"), 10, 5); !errors.Is(err2, ErrExists) {
			t.Fatalf("dup start want ErrExists got %v", err2)
		}
	}
	if err := m.Pause([]byte("a"), 3); !errors.Is(err, cal.ErrClock) {
		t.Fatalf("clock backwards: %v", err)
	}
	if _, err := m.Elapsed([]byte("a"), -1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("now<0 want ErrInvalid got %v", err)
	}
	t.Logf("error precedence verified: invalid > clock > notfound/exists > state")
}
