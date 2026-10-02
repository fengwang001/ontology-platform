package scheduler_test

import (
	"errors"
	"testing"

	"ontology/scheduler"
)

func TestSpecWalkthrough(t *testing.T) {
	s := scheduler.New()
	checkW := func(want int64) {
		t.Helper()
		if got := s.Watermark(); got != want {
			t.Fatalf("W = %d, want %d", got, want)
		}
	}

	id, err := s.Add(10, 20)
	if err != nil || id != 1 {
		t.Fatalf("Add = %d,%v", id, err)
	}
	checkW(10)

	k, lo, hi, b, err := s.Process(1, 3)
	if err != nil || k != 3 || lo != 10 || hi != 13 || b != 1 {
		t.Fatalf("Process = %d,%d,%d,%d,%v", k, lo, hi, b, err)
	}
	checkW(10)

	if id, err = s.Add(11, 15); err != nil || id != 2 {
		t.Fatalf("Add = %d,%v", id, err)
	}
	checkW(10)

	if err = s.Ack(1); err != nil {
		t.Fatalf("Ack1: %v", err)
	}
	checkW(11)

	k, lo, hi, b, err = s.Process(2, 4)
	if err != nil || k != 4 || lo != 11 || hi != 15 || b != 2 {
		t.Fatalf("Process2 = %d,%d,%d,%d,%v", k, lo, hi, b, err)
	}
	checkW(11)

	nid, err := s.Split(1, 1, 2)
	if err != nil || nid != 3 {
		t.Fatalf("Split = %d,%v", nid, err)
	}
	if p, _ := s.Progress(1); p.From != 10 || p.To != 17 || p.Next != 13 {
		t.Fatalf("iv1 = %+v", p)
	}
	if p, _ := s.Progress(3); p.From != 17 || p.To != 20 || p.Next != 17 {
		t.Fatalf("iv3 = %+v", p)
	}

	k, lo, hi, b, err = s.Process(1, 10)
	if err != nil || k != 4 || lo != 13 || hi != 17 || b != 3 {
		t.Fatalf("Process1 = %d,%d,%d,%d,%v", k, lo, hi, b, err)
	}
	checkW(11)

	if err = s.Ack(3); err != nil {
		t.Fatalf("Ack3: %v", err)
	}
	checkW(11)

	_, err = s.Split(2, 1, 2)
	if !errors.Is(err, scheduler.ErrExhausted) {
		t.Fatalf("Split exhausted = %v", err)
	}

	if err = s.Ack(2); err != nil {
		t.Fatalf("Ack2: %v", err)
	}
	checkW(17)

	if _, err = s.Add(15, 16); !errors.Is(err, scheduler.ErrBelowWatermark) {
		t.Fatalf("Add below W = %v", err)
	}
	id, err = s.Add(17, 18)
	if err != nil || id != 4 {
		t.Fatalf("Add(17,18) = %d,%v", id, err)
	}
	checkW(17)

	k, lo, hi, b, err = s.Process(3, 3)
	if err != nil || k != 3 || lo != 17 || hi != 20 || b != 4 {
		t.Fatalf("Process3 = %d,%d,%d,%d,%v", k, lo, hi, b, err)
	}
	if err = s.Ack(4); err != nil {
		t.Fatalf("Ack4: %v", err)
	}
	checkW(17)

	k, lo, hi, b, err = s.Process(4, 1)
	if err != nil || k != 1 || lo != 17 || hi != 18 || b != 5 {
		t.Fatalf("Process4 = %d,%d,%d,%d,%v", k, lo, hi, b, err)
	}
	if err = s.Ack(5); err != nil {
		t.Fatalf("Ack5: %v", err)
	}
	checkW(17)

	if id, err = s.Add(17, 25); err != nil || id != 5 {
		t.Fatalf("final Add = %d,%v", id, err)
	}
}

func TestSplitZeroAndOne(t *testing.T) {
	s := scheduler.New()
	if _, err := s.Add(0, 0); !errors.Is(err, scheduler.ErrInvalid) {
		t.Fatalf("Add(0,0) = %v", err)
	}
	id, _ := s.Add(17, 20)

	// 先领取 1 个，next=18，rem=2；num=0 时 keep=max(1,0)=1，sp=19。
	if _, _, _, _, err := s.Process(id, 1); err != nil {
		t.Fatal(err)
	}
	nid, err := s.Split(id, 0, 1)
	if err != nil || nid != 2 {
		t.Fatalf("Split(0,1) = %d,%v", nid, err)
	}
	if p, _ := s.Progress(id); p.To != 19 {
		t.Fatalf("iv1.To = %d", p.To)
	}
	if p, _ := s.Progress(2); p.From != 19 || p.To != 20 {
		t.Fatalf("iv2 = %+v", p)
	}

	// 新区间 rem=1：keep=1，sp==to => 无法拆分，不改状态。
	if _, err = s.Split(2, 0, 1); !errors.Is(err, scheduler.ErrCannotSplit) {
		t.Fatalf("Split rem1 = %v", err)
	}
	if _, err = s.Split(2, 1, 1); !errors.Is(err, scheduler.ErrCannotSplit) {
		t.Fatalf("Split rem1 full = %v", err)
	}
	p, _ := s.Progress(2)
	if p.From != 19 || p.To != 20 {
		t.Fatalf("cannot-split changed state: %+v", p)
	}
}
