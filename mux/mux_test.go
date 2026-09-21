package mux

import (
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Unix(1000, 0)}
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

func waitPending(t *testing.T, m *Mux, n int) {
	t.Helper()
	for i := 0; i < 1000; i++ {
		if m.Stats().Pending == n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("pending never reached %d", n)
}

func TestOneToOneDelivery(t *testing.T) {
	m := New(newFakeClock().Now)
	ch, err := m.Register("a")
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("hello")
	m.Deliver("a", payload)
	payload[0] = 'X' // mutating after Deliver must not affect the waiter
	if got := <-ch; string(got) != "hello" {
		t.Fatalf("got %q, want %q", got, "hello")
	}
	s := m.Stats()
	if s.Pending != 0 || s.Delivered != 1 {
		t.Fatalf("stats %+v, want Pending=0 Delivered=1", s)
	}
}

func TestDuplicateIDRejected(t *testing.T) {
	m := New(newFakeClock().Now)
	ch, err := m.Register("a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Register("a"); !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("second Register err=%v, want ErrDuplicateID", err)
	}
	m.Deliver("a", []byte("first"))
	if got := <-ch; string(got) != "first" {
		t.Fatalf("original waiter got %q", got)
	}
	// The id completed, so it can be registered again.
	ch2, err := m.Register("a")
	if err != nil {
		t.Fatalf("re-register after completion: %v", err)
	}
	m.Deliver("a", []byte("second"))
	if got := <-ch2; string(got) != "second" {
		t.Fatalf("re-registered waiter got %q", got)
	}
	if s := m.Stats(); s.Delivered != 2 {
		t.Fatalf("Delivered=%d, want 2", s.Delivered)
	}
}

func TestOrphanResponseDropped(t *testing.T) {
	m := New(newFakeClock().Now)
	m.Deliver("ghost", []byte("old"))
	s := m.Stats()
	if s.Orphans != 1 || s.Pending != 0 || s.Late != 0 {
		t.Fatalf("stats %+v, want Orphans=1 Pending=0 Late=0", s)
	}
	// The orphan must not have created a slot: a later Register for the
	// same id receives only the next payload.
	ch, err := m.Register("ghost")
	if err != nil {
		t.Fatal(err)
	}
	m.Deliver("ghost", []byte("new"))
	if got := <-ch; string(got) != "new" {
		t.Fatalf("got %q, want %q", got, "new")
	}
}

func TestLateResponseCountedSeparately(t *testing.T) {
	clk := newFakeClock()
	m := New(clk.Now)
	done := make(chan error, 1)
	go func() {
		_, err := m.Wait("w", clk.Now().Add(time.Second))
		done <- err
	}()
	waitPending(t, m, 1)
	clk.Advance(2 * time.Second)
	m.Tick()
	if err := <-done; !errors.Is(err, ErrTimedOut) {
		t.Fatalf("Wait err=%v, want ErrTimedOut", err)
	}
	m.Deliver("w", []byte("late"))
	s := m.Stats()
	if s.Late != 1 || s.Orphans != 0 || s.Delivered != 0 || s.Pending != 0 {
		t.Fatalf("stats %+v, want Late=1 Orphans=0 Delivered=0 Pending=0", s)
	}
}

func TestTimeoutAccounting(t *testing.T) {
	clk := newFakeClock()
	m := New(clk.Now)
	done := make(chan error, 1)
	go func() {
		_, err := m.Wait("t", clk.Now().Add(time.Minute))
		done <- err
	}()
	waitPending(t, m, 1)
	clk.Advance(time.Minute)
	m.Tick()
	if err := <-done; !errors.Is(err, ErrTimedOut) {
		t.Fatalf("Wait err=%v, want ErrTimedOut", err)
	}
	s := m.Stats()
	if s.Pending != 0 || s.Delivered != 0 || s.Orphans != 0 {
		t.Fatalf("stats %+v, want all zero after timeout", s)
	}
}
