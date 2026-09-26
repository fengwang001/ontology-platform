package api

import (
	"errors"
	"math/rand"
	"sync"
	"testing"
)

func mustNew(t *testing.T, p, g uint64) *Exchange {
	t.Helper()
	x, err := New(p, g)
	if err != nil {
		t.Fatalf("New(%d,%d): %v", p, g, err)
	}
	return x
}

// TestExchangeConsistency pins invariant 1: Secret(a,Pub(b)) == Secret(b,Pub(a)).
func TestExchangeConsistency(t *testing.T) {
	groups := [][2]uint64{{29, 2}, {101, 2}, {104729, 12}}
	rng := rand.New(rand.NewSource(1))
	for _, gp := range groups {
		x := mustNew(t, gp[0], gp[1])
		for i := 0; i < 50; i++ {
			a := 1 + rng.Uint64()%(gp[0]-2)
			b := 1 + rng.Uint64()%(gp[0]-2)
			pa, err := x.PublicKey(a)
			if err != nil {
				t.Fatalf("PublicKey(%d): %v", a, err)
			}
			pb, err := x.PublicKey(b)
			if err != nil {
				t.Fatalf("PublicKey(%d): %v", b, err)
			}
			sa, errA := x.Secret(a, pb)
			sb, errB := x.Secret(b, pa)
			// A public key landing on 1 or p-1 must be rejected by Secret;
			// the exchange invariant applies when both keys are acceptable.
			if errA != nil || errB != nil {
				if errA != nil && !errors.Is(errA, ErrBadPublicKey) {
					t.Fatalf("Secret: %v", errA)
				}
				if errB != nil && !errors.Is(errB, ErrBadPublicKey) {
					t.Fatalf("Secret: %v", errB)
				}
				continue
			}
			if sa != sb {
				t.Errorf("p=%d a=%d b=%d: %d != %d", gp[0], a, b, sa, sb)
			}
		}
	}
}

// TestErrorsDistinct pins the three mutually distinct sentinel errors.
func TestErrorsDistinct(t *testing.T) {
	x := mustNew(t, 29, 2)
	_, errGroup := New(1, 2)
	if _, err := New(29, 1); !errors.Is(err, ErrBadGroupParams) {
		t.Fatalf("g out of range: %v", err)
	}
	_, errPriv := x.PublicKey(0)
	_, errPub := x.Secret(5, 1)
	if _, err := x.Secret(5, 28); !errors.Is(err, ErrBadPublicKey) {
		t.Fatalf("peerPub=p-1 must be rejected: %v", err)
	}
	for _, c := range []struct {
		err  error
		is   error
		isnt []error
	}{
		{errGroup, ErrBadGroupParams, []error{ErrBadPrivateKey, ErrBadPublicKey}},
		{errPriv, ErrBadPrivateKey, []error{ErrBadGroupParams, ErrBadPublicKey}},
		{errPub, ErrBadPublicKey, []error{ErrBadGroupParams, ErrBadPrivateKey}},
	} {
		if !errors.Is(c.err, c.is) {
			t.Errorf("%v not recognized as %v", c.err, c.is)
		}
		for _, other := range c.isnt {
			if errors.Is(c.err, other) {
				t.Errorf("%v confused with %v", c.err, other)
			}
		}
	}
}

// TestRejectionLeavesNoTrace pins invariant 4: rejected operations change
// nothing and the exchange keeps working.
func TestRejectionLeavesNoTrace(t *testing.T) {
	x := mustNew(t, 29, 2)
	before, _ := x.PublicKey(5)
	bad := []func() error{
		func() error { _, err := x.PublicKey(0); return err },
		func() error { _, err := x.PublicKey(28); return err },
		func() error { _, err := x.Secret(5, 0); return err },
		func() error { _, err := x.Secret(5, 1); return err },
		func() error { _, err := x.Secret(5, 28); return err },
		func() error { _, err := x.Secret(0, 18); return err },
	}
	for i, f := range bad {
		if err := f(); err == nil {
			t.Errorf("case %d: expected rejection", i)
		}
	}
	after, _ := x.PublicKey(5)
	s, err := x.Secret(5, 18)
	if after != before || err != nil || s != 15 {
		t.Errorf("state changed or broken after rejections: %d->%d, s=%d, err=%v", before, after, s, err)
	}
}

// TestConcurrentSecret pins concurrency: N goroutines deriving the same
// secret must all agree with the reference value. No sleeps.
func TestConcurrentSecret(t *testing.T) {
	x := mustNew(t, 29, 2)
	const n = 64
	results := make([]uint64, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = x.Secret(5, 18)
		}(i)
	}
	wg.Wait()
	for i := 0; i < n; i++ {
		if errs[i] != nil || results[i] != 15 {
			t.Errorf("goroutine %d: s=%d err=%v, want 15", i, results[i], errs[i])
		}
	}
}

func TestSelfCheck(t *testing.T) {
	if err := SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
