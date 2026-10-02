package epoll

import (
	"errors"
	"reflect"
	"testing"
)

func mustWait(t *testing.T, p *Poll, ep, max int, want []Event) {
	t.Helper()
	got, err := p.Wait(ep, max)
	if err != nil {
		t.Fatalf("Wait(%d, %d) error = %v", ep, max, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Wait(%d, %d) = %#v, want %#v", ep, max, got, want)
	}
}

func mustQueue(t *testing.T, p *Poll, ep int, want []int) {
	t.Helper()
	got, err := p.Queue(ep)
	if err != nil {
		t.Fatalf("Queue(%d) error = %v", ep, err)
	}
	if len(got) == 0 {
		got = nil
	}
	if len(want) == 0 {
		want = nil
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Queue(%d) = %v, want %v", ep, got, want)
	}
}

func noError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestConstruction(t *testing.T) {
	if _, err := New(1, 1, 1); err != nil {
		t.Fatalf("New() error = %v", err)
	}
	for _, args := range [][3]int{{0, 1, 1}, {1_000_001, 1, 1}, {1, 0, 1}, {1, 5, 1}, {1, 1, 0}, {1, 1, 1_000_001}} {
		if _, err := New(args[0], args[1], args[2]); !errors.Is(err, ErrInvalid) {
			t.Fatalf("New(%v) error = %v, want ErrInvalid", args, err)
		}
	}
}

func TestExampleOne(t *testing.T) {
	p, err := New(100, 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	noError(t, p.Add(0, 5, IN, 0))
	noError(t, p.Add(0, 6, IN, ET))
	noError(t, p.Add(0, 7, IN|OUT, 0))
	noError(t, p.SetState(5, IN))
	noError(t, p.SetState(6, IN))
	noError(t, p.SetState(7, OUT))
	mustQueue(t, p, 0, []int{5, 6, 7})

	mustWait(t, p, 0, 2, []Event{{5, IN}, {6, IN}})
	mustQueue(t, p, 0, []int{7, 5})

	noError(t, p.SetState(6, 0))
	noError(t, p.SetState(6, IN))
	mustQueue(t, p, 0, []int{7, 5, 6})

	mustWait(t, p, 0, 10, []Event{{7, OUT}, {5, IN}, {6, IN}})
	mustQueue(t, p, 0, []int{7, 5})

	noError(t, p.SetState(5, 0))
	mustWait(t, p, 0, 10, []Event{{7, OUT}})
	mustQueue(t, p, 0, []int{7})
}

func TestExampleTwo(t *testing.T) {
	p, err := New(100, 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	noError(t, p.Add(0, 8, IN, ONESHOT))
	noError(t, p.SetState(8, IN))
	mustWait(t, p, 0, 1, []Event{{8, IN}})

	noError(t, p.SetState(8, 0))
	noError(t, p.SetState(8, IN))
	mustQueue(t, p, 0, nil)

	noError(t, p.Mod(0, 8, IN, ONESHOT))
	mustQueue(t, p, 0, []int{8})
	mustWait(t, p, 0, 1, []Event{{8, IN}})
}

func TestExampleThree(t *testing.T) {
	p, err := New(100, 2, 100)
	if err != nil {
		t.Fatal(err)
	}
	noError(t, p.Add(0, 10, IN, EXCL))
	noError(t, p.Add(1, 10, IN, EXCL))
	noError(t, p.Add(0, 11, IN, 0))
	noError(t, p.Add(1, 11, IN, 0))

	noError(t, p.SetState(10, IN))
	mustQueue(t, p, 0, []int{10})
	mustQueue(t, p, 1, nil)
	mustWait(t, p, 0, 1, []Event{{10, IN}})
	mustQueue(t, p, 0, []int{10})

	noError(t, p.SetState(10, 0))
	noError(t, p.SetState(10, IN))
	mustQueue(t, p, 0, []int{10})
	mustQueue(t, p, 1, []int{10})

	noError(t, p.SetState(11, IN))
	mustQueue(t, p, 0, []int{10, 11})
	mustQueue(t, p, 1, []int{10, 11})

	removed, err := p.Close(10)
	if err != nil || removed != 2 {
		t.Fatalf("Close(10) = (%d, %v), want (2, nil)", removed, err)
	}
	mustQueue(t, p, 0, []int{11})
	mustQueue(t, p, 1, []int{11})
	state, err := p.State(10)
	if err != nil || state != 0 {
		t.Fatalf("State(10) = (%d, %v), want 0", state, err)
	}
	if got, _ := p.Watches(0); len(got) != 1 || got[0].FD != 11 {
		t.Fatalf("Watches(0) = %#v, want only fd 11", got)
	}
	if got, _ := p.Watches(1); len(got) != 1 || got[0].FD != 11 {
		t.Fatalf("Watches(1) = %#v, want only fd 11", got)
	}
}

func TestEdgeTrigger(t *testing.T) {
	p, _ := New(100, 1, 100)
	noError(t, p.SetState(1, IN))
	noError(t, p.Add(0, 1, IN, ET))
	mustQueue(t, p, 0, []int{1})
	noError(t, p.Mod(0, 1, IN, ET))
	mustQueue(t, p, 0, []int{1})
	mustWait(t, p, 0, 10, []Event{{1, IN}})

	noError(t, p.SetState(1, IN))
	mustQueue(t, p, 0, nil)
	noError(t, p.SetState(1, 0))
	noError(t, p.SetState(1, IN))
	mustQueue(t, p, 0, []int{1})
	mustWait(t, p, 0, 10, []Event{{1, IN}})

	noError(t, p.SetState(1, IN))
	mustQueue(t, p, 0, nil)
	noError(t, p.SetState(1, IN|ERR))
	mustQueue(t, p, 0, []int{1})
	mustWait(t, p, 0, 10, []Event{{1, IN | ERR}})

	noError(t, p.SetState(1, 0))
	noError(t, p.Add(0, 2, 0, ET|EXCL))
	noError(t, p.SetState(2, ERR|HUP))
	mustQueue(t, p, 0, []int{2})
}

func TestRefillOrderAndMax(t *testing.T) {
	p, _ := New(100, 1, 100)
	noError(t, p.SetState(1, IN))
	noError(t, p.SetState(2, IN))
	noError(t, p.SetState(3, IN))
	noError(t, p.Add(0, 1, IN, 0))
	noError(t, p.Add(0, 2, IN, 0))
	noError(t, p.Add(0, 3, IN, ET))

	mustWait(t, p, 0, 2, []Event{{1, IN}, {2, IN}})
	mustQueue(t, p, 0, []int{3, 1, 2})
	noError(t, p.SetState(3, 0))
	mustWait(t, p, 0, 2, []Event{{1, IN}, {2, IN}})
	mustQueue(t, p, 0, []int{1, 2})
}

func TestDefaultERRHUP(t *testing.T) {
	p, _ := New(100, 1, 100)
	noError(t, p.Add(0, 1, 0, 0))
	noError(t, p.SetState(1, ERR|HUP))
	mustWait(t, p, 0, 10, []Event{{1, ERR | HUP}})
}
