package epoll

import (
	"errors"
	"testing"
)

func wantError(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error = %v, want %v", got, want)
	}
}

func TestONESHOTBlocksERRUntilRearm(t *testing.T) {
	p, _ := New(100, 1, 100)
	noError(t, p.Add(0, 1, IN, ONESHOT))
	noError(t, p.SetState(1, IN))
	mustWait(t, p, 0, 1, []Event{{1, IN}})

	noError(t, p.SetState(1, 0))
	noError(t, p.SetState(1, ERR))
	mustQueue(t, p, 0, nil)

	noError(t, p.Mod(0, 1, IN, ONESHOT))
	mustQueue(t, p, 0, []int{1})
	mustWait(t, p, 0, 1, []Event{{1, ERR}})
	watches, _ := p.Watches(0)
	if !watches[0].Disabled {
		t.Fatal("watch was not disabled after ONESHOT delivery")
	}
}

func TestModPositionDelAndIndependence(t *testing.T) {
	p, _ := New(100, 2, 100)
	noError(t, p.SetState(1, IN|OUT|ERR|HUP))
	noError(t, p.SetState(2, IN))
	noError(t, p.Add(0, 1, IN, 0))
	noError(t, p.Add(0, 2, IN, 0))
	noError(t, p.Add(1, 1, IN, 0))
	mustQueue(t, p, 0, []int{1, 2})

	noError(t, p.Mod(0, 1, OUT, ET))
	mustQueue(t, p, 0, []int{1, 2})
	watches, _ := p.Watches(0)
	if watches[0].Eff != OUT|ERR|HUP || watches[0].Disabled {
		t.Fatalf("Mod did not replace state: %#v", watches[0])
	}

	noError(t, p.Del(0, 2))
	mustQueue(t, p, 0, []int{1})
	mustQueue(t, p, 1, []int{1})

	noError(t, p.Del(1, 1))
	mustQueue(t, p, 1, nil)
	mustQueue(t, p, 0, []int{1})
}

func TestExclusiveMixing(t *testing.T) {
	p, _ := New(100, 4, 100)
	noError(t, p.Add(0, 1, IN, EXCL))
	noError(t, p.Add(1, 1, IN, EXCL))
	noError(t, p.Add(2, 1, IN, 0))
	noError(t, p.Add(3, 1, IN, ET|EXCL))

	noError(t, p.SetState(1, IN))
	mustQueue(t, p, 0, []int{1})
	mustQueue(t, p, 1, nil)
	mustQueue(t, p, 2, []int{1})
	mustQueue(t, p, 3, nil)

	noError(t, p.SetState(1, 0))
	noError(t, p.SetState(1, OUT))
	mustQueue(t, p, 0, []int{1})
	mustQueue(t, p, 2, []int{1})
	mustQueue(t, p, 3, nil)

	if err := p.Mod(0, 1, IN, 0); !errors.Is(err, ErrExclChange) {
		t.Fatalf("Mod removing EXCL error = %v", err)
	}
	if err := p.Mod(0, 1, IN, EXCL); err != nil {
		t.Fatalf("Mod preserving EXCL error = %v", err)
	}
}

func TestEdgeExclusiveAddModAndIndependence(t *testing.T) {
	p, _ := New(100, 2, 100)
	noError(t, p.SetState(1, IN))
	noError(t, p.Add(0, 1, IN, ET|EXCL))
	noError(t, p.Add(1, 1, IN, ET|EXCL))
	mustQueue(t, p, 0, []int{1})
	mustQueue(t, p, 1, []int{1})

	mustWait(t, p, 0, 1, []Event{{1, IN}})
	mustWait(t, p, 1, 1, []Event{{1, IN}})
	noError(t, p.SetState(1, 0))
	noError(t, p.SetState(1, IN))
	mustQueue(t, p, 0, []int{1})
	mustQueue(t, p, 1, nil)

	noError(t, p.Del(1, 1))
	noError(t, p.SetState(1, 0))
	noError(t, p.SetState(1, IN))
	mustQueue(t, p, 0, []int{1})

	noError(t, p.Add(0, 3, IN, ET|EXCL))
	noError(t, p.Add(1, 3, IN, ET|EXCL))
	mustWait(t, p, 0, 1, []Event{{1, IN}})
	noError(t, p.SetState(3, IN))
	mustQueue(t, p, 0, []int{3})
	mustQueue(t, p, 1, nil)
	noError(t, p.SetState(3, 0))
	noError(t, p.SetState(3, ERR))
	mustQueue(t, p, 0, []int{3})
	noError(t, p.Mod(1, 3, IN, ET|EXCL))
	mustQueue(t, p, 1, []int{3})
	mustWait(t, p, 0, 1, []Event{{3, ERR}})
	mustWait(t, p, 1, 1, []Event{{3, ERR}})

	noError(t, p.Add(1, 2, IN, ONESHOT))
	noError(t, p.Add(0, 2, IN, ONESHOT))
	noError(t, p.SetState(2, IN))
	mustWait(t, p, 0, 1, []Event{{2, IN}})
	mustQueue(t, p, 0, nil)
	mustQueue(t, p, 1, []int{2})
	watches0, _ := p.Watches(0)
	watches1, _ := p.Watches(1)
	var watch0, watch1 Watch
	for _, w := range watches0 {
		if w.FD == 2 {
			watch0 = w
		}
	}
	for _, w := range watches1 {
		if w.FD == 2 {
			watch1 = w
		}
	}
	if !watch0.Disabled || watch1.Disabled {
		t.Fatal("ONESHOT disabled state leaked between instances")
	}
}

func TestInvalidArgumentsAndRejection(t *testing.T) {
	p, _ := New(100, 2, 100)
	wantError(t, p.Add(2, 1, IN, 0), ErrInvalid)
	wantError(t, p.Add(0, -1, IN, 0), ErrInvalid)
	wantError(t, p.Add(0, 1, 16, 0), ErrInvalid)
	wantError(t, p.Add(0, 1, IN, 8), ErrInvalid)
	wantError(t, p.Add(0, 1, IN, EXCL|ONESHOT), ErrInvalid)
	wantError(t, p.Add(0, 1, ERR, EXCL), ErrInvalid)
	wantError(t, p.Add(0, 1, HUP, EXCL), ErrInvalid)

	noError(t, p.Add(0, 1, IN, 0))
	wantError(t, p.Add(0, 1, IN, 0), ErrExists)
	wantError(t, p.Mod(1, 1, IN, 0), ErrNoEnt)
	wantError(t, p.Del(1, 1), ErrNoEnt)
	wantError(t, p.Del(2, 1), ErrInvalid)
	wantError(t, p.SetState(-1, 0), ErrInvalid)
	wantError(t, p.SetState(1, 16), ErrInvalid)
	if _, err := p.Close(-1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Close(-1) error = %v", err)
	}
	_, err := p.Wait(2, 1)
	wantError(t, err, ErrInvalid)
	_, err = p.Wait(0, 0)
	wantError(t, err, ErrInvalid)
	_, err = p.Wait(0, 1_000_001)
	wantError(t, err, ErrInvalid)

	state, _ := p.State(1)
	if state != 0 {
		t.Fatal("rejected SetState changed file state")
	}
	mustQueue(t, p, 0, nil)
}

func TestLimits(t *testing.T) {
	p, _ := New(1, 2, 2)
	noError(t, p.Add(0, 1, IN, 0))
	wantError(t, p.Add(0, 2, IN, 0), ErrNoSpace)
	noError(t, p.Add(1, 2, IN, 0))
	wantError(t, p.Add(1, 3, IN, 0), ErrNoSpace)

	noError(t, p.Del(0, 1))
	noError(t, p.Add(0, 3, IN, 0))
	watches0, _ := p.Watches(0)
	watches1, _ := p.Watches(1)
	if len(watches0) != 1 || len(watches1) != 1 {
		t.Fatalf("watch counts = %d, %d", len(watches0), len(watches1))
	}

	globalFull, _ := New(2, 2, 2)
	noError(t, globalFull.Add(0, 1, IN, 0))
	noError(t, globalFull.Add(1, 2, IN, 0))
	wantError(t, globalFull.Add(1, 3, IN, 0), ErrTooMany)
}

func TestCloseMultipleInstances(t *testing.T) {
	p, _ := New(100, 3, 100)
	noError(t, p.SetState(1, IN))
	noError(t, p.SetState(2, IN))
	for ep := 0; ep < 3; ep++ {
		if ep == 2 {
			continue
		}
		noError(t, p.Add(ep, 1, IN, 0))
		noError(t, p.Add(ep, 2, IN, 0))
	}

	removed, err := p.Close(1)
	if err != nil || removed != 2 {
		t.Fatalf("Close = (%d, %v), want 2", removed, err)
	}
	mustQueue(t, p, 0, []int{2})
	mustQueue(t, p, 1, []int{2})
	mustQueue(t, p, 2, nil)
	state, _ := p.State(1)
	if state != 0 {
		t.Fatalf("state = %d, want 0", state)
	}
	removed, err = p.Close(1)
	if err != nil || removed != 0 {
		t.Fatalf("second Close = (%d, %v), want 0", removed, err)
	}
}

func TestWaitDiscardsStaleWithoutMax(t *testing.T) {
	p, _ := New(100, 1, 100)
	noError(t, p.SetState(1, IN))
	noError(t, p.SetState(2, IN))
	noError(t, p.Add(0, 1, IN, 0))
	noError(t, p.Add(0, 2, IN, 0))
	mustWait(t, p, 0, 1, []Event{{1, IN}})
	mustQueue(t, p, 0, []int{2, 1})
	noError(t, p.SetState(1, 0))
	noError(t, p.SetState(2, 0))

	examinedBefore := p.examined
	events, err := p.Wait(0, 1)
	if err != nil || len(events) != 0 {
		t.Fatalf("Wait = %#v, %v; want no events", events, err)
	}
	if p.examined-examinedBefore != 2 {
		t.Fatalf("examined = %d, want 2", p.examined-examinedBefore)
	}
	mustQueue(t, p, 0, nil)
}

func TestIdleExaminedCounter(t *testing.T) {
	p, _ := New(100_000, 1, 100_000)
	for fd := 0; fd < 100_000; fd++ {
		noError(t, p.Add(0, fd, IN, 0))
	}
	before := p.examined
	events, err := p.Wait(0, 10)
	if err != nil || len(events) != 0 {
		t.Fatalf("Wait = %#v, %v; want no events", events, err)
	}
	if p.examined-before != 0 {
		t.Fatalf("idle Wait examined %d queue entries", p.examined-before)
	}
}
