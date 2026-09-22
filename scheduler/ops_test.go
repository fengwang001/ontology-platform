package scheduler

import (
	"errors"
	"testing"

	"ontology/timer"
)

func TestParamErrors(t *testing.T) {
	cases := []struct {
		name string
		op   func(s *Scheduler, tm *timer.Timer) error
		want error
	}{
		{"negative delay", func(s *Scheduler, _ *timer.Timer) error { _, err := s.Add(-1, nil); return err }, ErrNegativeDelay},
		{"delay too large", func(s *Scheduler, _ *timer.Timer) error { _, err := s.Add(101, nil); return err }, ErrDelayTooLarge},
		{"advance zero", func(s *Scheduler, _ *timer.Timer) error { return s.Advance(0) }, ErrInvalidAdvance},
		{"advance negative", func(s *Scheduler, _ *timer.Timer) error { return s.Advance(-5) }, ErrInvalidAdvance},
		{"advance too large", func(s *Scheduler, _ *timer.Timer) error { return s.Advance(11) }, ErrAdvanceTooLarge},
		{"reset negative", func(s *Scheduler, tm *timer.Timer) error {
			return s.Reset(tm, -1)
		}, ErrNegativeDelay},
	}
	for _, c := range cases {
		s := newSched(t, Config{MaxAdvance: 10, MaxDelay: 100})
		tm, err := s.Add(1, nil) // 预先留一个定时器观察状态不变
		if err != nil {
			t.Fatal(err)
		}
		before := s.Pending()
		if err := c.op(s, tm); !errors.Is(err, c.want) {
			t.Fatalf("%s: err = %v, want %v", c.name, err, c.want)
		}
		if s.Pending() != before {
			t.Fatalf("%s: pending changed after rejected op", c.name)
		}
		if err := s.Check(); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
	}
}

func TestLimitsAndRecovery(t *testing.T) {
	s := newSched(t, Config{MaxTimers: 2, MaxAdvance: 10, MaxDelay: 100})
	if _, err := s.Add(1, nil); err != nil {
		t.Fatal(err)
	}
	tm, err := s.Add(2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(3, nil); !errors.Is(err, ErrTooManyTimers) {
		t.Fatalf("third Add = %v, want ErrTooManyTimers", err)
	}
	if err := s.Advance(11); !errors.Is(err, ErrAdvanceTooLarge) {
		t.Fatalf("Advance(11) = %v, want ErrAdvanceTooLarge", err)
	}
	if _, err := s.Add(101, nil); !errors.Is(err, ErrDelayTooLarge) {
		t.Fatalf("Add(101) = %v, want ErrDelayTooLarge", err)
	}
	// 拒绝不是终态：取消一个后还能注册，推进后正常触发。
	if err := s.Cancel(tm); err != nil {
		t.Fatal(err)
	}
	fired := false
	if _, err := s.Add(1, func() { fired = true }); err != nil {
		t.Fatalf("Add after rejection: %v", err)
	}
	if err := s.Advance(2); err != nil {
		t.Fatal(err)
	}
	if !fired {
		t.Fatal("timer added after rejections did not fire")
	}
	if err := s.Check(); err != nil {
		t.Fatal(err)
	}
}

func TestConfigValidation(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{"zero levels ok (default)", Config{Levels: 0}},
	}
	for _, c := range cases {
		if _, err := New(c.cfg); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
	}
	bad := []Config{
		{Levels: -1},
		{SlotSize: 1},
		{MaxAdvance: -1},
		{MaxTimers: -1},
		{MaxDelay: -1},
		{Levels: 2, SlotSize: 4, MaxDelay: 16}, // 量程只有 4^2-1=15
	}
	for _, cfg := range bad {
		if _, err := New(cfg); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("New(%+v) = %v, want ErrInvalidConfig", cfg, err)
		}
	}
}

func TestCheckAfterMixedOps(t *testing.T) {
	for _, start := range []int64{0, 1000, 123456789} {
		s := newSched(t, Config{Start: start})
		for i := int64(0); i < 50; i++ {
			tm, err := s.Add(i*7%300, nil)
			if err != nil {
				t.Fatal(err)
			}
			if i%3 == 0 {
				if err := s.Cancel(tm); err != nil {
					t.Fatal(err)
				}
			}
			if i%5 == 0 {
				if err := s.Reset(tm, i%100+1); err != nil && !errors.Is(err, ErrAlreadyCancelled) {
					t.Fatal(err)
				}
			}
			if err := s.Advance(3); err != nil {
				t.Fatal(err)
			}
			if err := s.Check(); err != nil {
				t.Fatalf("start=%d i=%d: %v", start, i, err)
			}
		}
	}
}
