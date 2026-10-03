package sla

import (
	"errors"
	"testing"

	"ontology/cal"
)

func setup(t *testing.T) (*Manager, *cal.Calendar) {
	t.Helper()
	c, err := cal.New(540, 1080, 0b0011111)
	if err != nil {
		t.Fatal(err)
	}
	return NewManager(c), c
}

func TestExampleNoPause(t *testing.T) {
	m, c := setup(t)
	id := []byte("s")
	if err := m.Start(id, 600, 600); err != nil {
		t.Fatal(err)
	}
	dl, ok, _ := m.Deadline(id, 600)
	if !ok || dl != 2100 {
		t.Fatalf("Deadline=(%d,%v) want 2100,true", dl, ok)
	}
	if el, _ := m.Elapsed(id, 1000); el != 400 {
		t.Fatalf("Elapsed(1000)=%d want 400", el)
	}
	want := map[uint8]int64{50: 900, 80: 1080, 100: 2100}
	for q, at := range want {
		got, due, err := m.Trigger(id, Threshold(600, q), 2100)
		if err != nil || !due || got != at {
			t.Errorf("q=%d Trigger=(%d,%v,%v) want %d", q, got, due, err, at)
		}
	}
	_ = c
}

func TestExamplePauseResume(t *testing.T) {
	m, _ := setup(t)
	id := []byte("s")
	if err := m.Start(id, 600, 600); err != nil {
		t.Fatal(err)
	}
	if err := m.Pause(id, 1000); err != nil {
		t.Fatal(err)
	}
	if el, _ := m.Elapsed(id, 1500); el != 400 {
		t.Fatalf("暂停段内 Elapsed=%d want 400", el)
	}
	if _, ok, _ := m.Deadline(id, 1500); ok {
		t.Fatal("暂停态 Deadline 应为无")
	}
	if err := m.Pause(id, 1600); !errors.Is(err, ErrState) {
		t.Fatalf("重复 Pause err=%v want ErrState", err)
	}
	if err := m.Resume(id, 2000); err != nil {
		t.Fatal(err)
	}
	if err := m.Resume(id, 2010); !errors.Is(err, ErrState) {
		t.Fatalf("重复 Resume err=%v want ErrState", err)
	}
	want := map[uint8]int64{50: 900, 80: 2080, 100: 2200}
	for q, at := range want {
		got, due, err := m.Trigger(id, Threshold(600, q), 3000)
		if err != nil || !due || got != at {
			t.Errorf("q=%d Trigger=(%d,%v,%v) want %d", q, got, due, err, at)
		}
	}
}

func TestPauseOutsideWindow(t *testing.T) {
	m, _ := setup(t)
	id := []byte("s")
	if err := m.Start(id, 600, 600); err != nil {
		t.Fatal(err)
	}
	if err := m.Pause(id, 1080); err != nil {
		t.Fatal(err)
	}
	if err := m.Resume(id, 1980); err != nil {
		t.Fatal(err)
	}
	at, due, _ := m.Trigger(id, 600, 5000)
	if !due || at != 2100 {
		t.Fatalf("Trigger=(%d,%v) want 2100", at, due)
	}
}

func TestHolidayAddedBeforeResume(t *testing.T) {
	m, c := setup(t)
	id := []byte("s")
	if err := m.Start(id, 600, 600); err != nil {
		t.Fatal(err)
	}
	if err := m.Pause(id, 1000); err != nil {
		t.Fatal(err)
	}
	if err := c.AddHoliday(1, 1000); err != nil {
		t.Fatal(err)
	}
	if err := m.Resume(id, 2000); err != nil {
		t.Fatal(err)
	}
	want := map[uint8]int64{50: 900, 80: 3500, 100: 3620}
	for q, w := range want {
		got, due, err := m.Trigger(id, Threshold(600, q), 4000)
		if err != nil || !due || got != w {
			t.Errorf("q=%d=(%d,%v,%v) want %d", q, got, due, err, w)
		}
	}
}

func TestErrorsAndClock(t *testing.T) {
	m, _ := setup(t)
	if err := m.Start(nil, 10, 10); !errors.Is(err, cal.ErrArgument) {
		t.Fatalf("空 id err=%v", err)
	}
	if err := m.Start([]byte("a"), 0, 10); !errors.Is(err, cal.ErrArgument) {
		t.Fatalf("B=0 err=%v", err)
	}
	if err := m.Start([]byte("a"), 10, 10); err != nil {
		t.Fatal(err)
	}
	if err := m.Start([]byte("a"), 10, 20); !errors.Is(err, ErrExists) {
		t.Fatalf("重复 Start err=%v", err)
	}
	if err := m.Pause([]byte("b"), 30); !errors.Is(err, ErrNotFound) {
		t.Fatalf("NotFound err=%v", err)
	}
	if err := m.Pause([]byte("a"), 5); !errors.Is(err, cal.ErrClock) {
		t.Fatalf("时钟倒退 err=%v", err)
	}
	if err := m.Pause([]byte("a"), 30); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Elapsed([]byte("a"), 20); !errors.Is(err, cal.ErrClock) {
		t.Fatalf("暂停后时钟倒退 err=%v", err)
	}
}
