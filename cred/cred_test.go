package cred

import (
	"errors"
	"sync"
	"testing"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRotateFlow(t *testing.T) {
	s := NewStore()
	must(t, s.Rotate("T", "k1", "sec", "r", "svc", 100, 0))
	if s.Epoch() != 1 {
		t.Fatalf("epoch=%d want 1", s.Epoch())
	}
	c1, _ := s.Snapshot("k1")
	if c1.Status != StatusActive {
		t.Fatalf("k1 status=%d", c1.Status)
	}

	must(t, s.Rotate("T", "k2", "sec", "r", "svc", 100, 50))
	c1, _ = s.Snapshot("k1")
	c2, _ := s.Snapshot("k2")
	if c1.Status != StatusRetiring || c1.ValidUntil != 150 {
		t.Fatalf("k1 = status %d until %d", c1.Status, c1.ValidUntil)
	}
	if c2.Status != StatusActive {
		t.Fatalf("k2 status=%d", c2.Status)
	}

	if err := s.Rotate("T", "k3", "sec", "r", "svc", 100, 60); !errors.Is(err, ErrLimit) {
		t.Fatalf("err=%v want limit", err)
	}
	if s.Epoch() != 2 {
		t.Fatalf("epoch=%d, rejected op must not bump", s.Epoch())
	}

	// t=150: k1 恰等于 validUntil，已不计入，可签发。
	must(t, s.Rotate("T", "k3", "sec", "r", "svc", 100, 150))
	c2, _ = s.Snapshot("k2")
	if c2.Status != StatusRetiring || c2.ValidUntil != 250 {
		t.Fatalf("k2 = status %d until %d", c2.Status, c2.ValidUntil)
	}
}

func TestRotateRejectOrder(t *testing.T) {
	cases := []struct {
		name                                   string
		tenant, keyID, secret, region, service string
		g, now                                 int64
		seed                                   func(s *Store)
		want                                   error
	}{
		{"bad grace", "T", "k", "s", "r", "v", 0, 0, nil, ErrInvalidParam},
		{"grace too big", "T", "k", "s", "r", "v", maxGrace + 1, 0, nil, ErrInvalidParam},
		{"empty secret", "T", "k", "", "r", "v", 1, 0, nil, ErrInvalidParam},
		{"bad now", "T", "k", "s", "r", "v", 1, -1, nil, ErrInvalidParam},
		{"clock back", "T", "k", "s", "r", "v", 1, 1,
			func(s *Store) { must(t, s.Rotate("T", "a", "s", "r", "v", 1, 5)) }, ErrClockBack},
		{"exists", "T", "k", "s", "r", "v", 1, 5,
			func(s *Store) { must(t, s.Rotate("T", "k", "s", "r", "v", 1, 0)) }, ErrExists},
		{"limit", "T", "k3", "s", "r", "v", 100, 100,
			func(s *Store) {
				must(t, s.Rotate("T", "k1", "s", "r", "v", 100, 0))
				must(t, s.Rotate("T", "k2", "s", "r", "v", 100, 1))
			}, ErrLimit},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewStore()
			if tc.seed != nil {
				tc.seed(s)
			}
			err := s.Rotate(tc.tenant, tc.keyID, tc.secret, tc.region, tc.service, tc.g, tc.now)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err=%v want %v", err, tc.want)
			}
		})
	}
}

func TestDisable(t *testing.T) {
	s := NewStore()
	if err := s.Disable("nope", 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v", err)
	}
	must(t, s.Rotate("T", "k", "s", "r", "v", 100, 10))
	if err := s.Disable("k", 5); !errors.Is(err, ErrClockBack) {
		t.Fatalf("err=%v", err)
	}
	must(t, s.Disable("k", 10))
	if s.Epoch() != 2 {
		t.Fatalf("epoch=%d want 2", s.Epoch())
	}
	c, _ := s.Snapshot("k")
	if c.Status != StatusDisabled {
		t.Fatalf("status=%d", c.Status)
	}
	// 重复 Disable 为空操作：不耗纪元。
	must(t, s.Disable("k", 10))
	must(t, s.Disable("k", 20))
	if s.Epoch() != 2 {
		t.Fatalf("epoch=%d, no-op must not bump", s.Epoch())
	}
	// 时钟被空操作推进后，更小的 now 仍算回退。
	if err := s.Rotate("T2", "k2", "s", "r", "v", 1, 19); !errors.Is(err, ErrClockBack) {
		t.Fatalf("err=%v", err)
	}
}

func TestRevokeBefore(t *testing.T) {
	s := NewStore()
	if err := s.RevokeBefore("ghost", 1, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v", err)
	}
	must(t, s.Rotate("T", "k", "s", "r", "v", 100, 0))
	if err := s.RevokeBefore("T", -1, 0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("err=%v", err)
	}
	must(t, s.RevokeBefore("T", 120, 10))
	c, _ := s.Snapshot("k")
	if c.RevokeBefore != 120 {
		t.Fatalf("rb=%d", c.RevokeBefore)
	}
	if s.Epoch() != 2 {
		t.Fatalf("epoch=%d", s.Epoch())
	}
	// t 不增为空操作。
	must(t, s.RevokeBefore("T", 120, 10))
	must(t, s.RevokeBefore("T", 50, 10))
	if s.Epoch() != 2 {
		t.Fatalf("epoch=%d, monotone no-op must not bump", s.Epoch())
	}
	c, _ = s.Snapshot("k")
	if c.RevokeBefore != 120 {
		t.Fatalf("rb=%d must stay 120", c.RevokeBefore)
	}
}

func TestCredProbesOnce(t *testing.T) {
	s := NewStore()
	must(t, s.Rotate("T", "k", "s", "r", "v", 100, 0))
	for i := 0; i < 5; i++ {
		s.Snapshot("k")
		if got := s.CredProbes(); got != uint64(i+1) {
			t.Fatalf("probes=%d want %d", got, i+1)
		}
	}
	s.Snapshot("missing")
	if got := s.CredProbes(); got != 6 {
		t.Fatalf("missing probe not counted: %d", got)
	}
}

func TestConcurrent(t *testing.T) {
	s := NewStore()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = s.Rotate("T", keyName(i), "s", "r", "v", 100, int64(i))
			_, _ = s.Snapshot("k0")
		}(i)
	}
	wg.Wait()
}

func keyName(i int) string {
	const digits = "0123456789"
	if i == 0 {
		return "k0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{digits[i%10]}, b...)
		i /= 10
	}
	return "k" + string(b)
}
