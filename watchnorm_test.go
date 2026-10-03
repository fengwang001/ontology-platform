package watchnorm

import (
	"errors"
	"sync"
	"testing"
)

func call(t *testing.T, n *Normalizer, got *[]Event, op func() ([]Event, error), want error) {
	t.Helper()
	events, err := op()
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
	*got = append(*got, events...)
}

func TestSpecBudgetRenameExample(t *testing.T) {
	n, err := New(2, 10)
	if err != nil {
		t.Fatal(err)
	}

	var events []Event
	call(t, n, &events, func() ([]Event, error) { return n.Created(0, "a", true) }, nil)
	call(t, n, &events, func() ([]Event, error) { return n.Created(0, "b", true) }, nil)
	call(t, n, &events, func() ([]Event, error) { return n.Deleted(1, "a") }, nil)
	call(t, n, &events, func() ([]Event, error) { return n.MovedFrom(2, "b", 7, true) }, nil)
	call(t, n, &events, func() ([]Event, error) { return n.MovedTo(11, "x", 7, true) }, nil)

	wantEvents := []Event{
		CreatedEvent("a"),
		CreatedEvent("b"),
		DeletedEvent("a"),
		RescanEvent("b"),
		RenamedEvent("b", "x"),
	}
	assertEvents(t, events, wantEvents)
	if _, ok := n.entries["x"]; !ok {
		t.Fatalf("x is not known: %#v", n.entries)
	}
	if _, ok := n.watched["x"]; !ok {
		t.Fatalf("x is not watched: %#v", n.watched)
	}
	if len(n.watched) != 2 {
		t.Fatalf("watched count = %d, want 2", len(n.watched))
	}
}

func TestPairExpiresExactlyAtWindowAndBecomesCreate(t *testing.T) {
	n, _ := New(2, 10)
	var events []Event
	call(t, n, &events, func() ([]Event, error) { return n.Created(0, "a", true) }, nil)
	call(t, n, &events, func() ([]Event, error) { return n.MovedFrom(2, "a", 7, true) }, nil)
	call(t, n, &events, func() ([]Event, error) { return n.MovedTo(12, "x", 7, true) }, nil)

	assertEvents(t, events, []Event{
		CreatedEvent("a"),
		DeletedEvent("a"),
		CreatedEvent("x"),
		RescanEvent("x"),
	})
	if _, ok := n.watched["x"]; !ok {
		t.Fatalf("x was not watched after expiration: %#v", n.watched)
	}
}

func TestRecreatedPathWhilePendingAndPairRejected(t *testing.T) {
	n, _ := New(2, 10)
	var events []Event
	call(t, n, &events, func() ([]Event, error) { return n.Created(0, "a", true) }, nil)
	call(t, n, &events, func() ([]Event, error) { return n.MovedFrom(1, "a", 7, true) }, nil)
	call(t, n, &events, func() ([]Event, error) { return n.Created(2, "a", true) }, nil)
	call(t, n, &events, func() ([]Event, error) { return n.MovedTo(3, "a", 7, true) }, ErrExists)

	if _, ok := n.pending[7]; !ok {
		t.Fatal("pending record was not retained after destination collision")
	}
	if !n.entries["a"] {
		t.Fatal("recreated a disappeared")
	}
	if len(n.watched) != 2 {
		t.Fatalf("watched count = %d, want root plus pending old subtree", len(n.watched))
	}
}

func TestRenamePrefixReplacesCompleteSegments(t *testing.T) {
	n, _ := New(5, 10)
	var events []Event
	for _, path := range []string{"a", "a/b", "a/b/c", "a/bx"} {
		call(t, n, &events, func() ([]Event, error) { return n.Created(0, path, true) }, nil)
	}
	call(t, n, &events, func() ([]Event, error) { return n.MovedFrom(1, "a/b", 7, true) }, nil)
	call(t, n, &events, func() ([]Event, error) { return n.MovedTo(2, "a/bc", 7, true) }, nil)

	assertEvents(t, events[len(events)-1:], []Event{RenamedEvent("a/b", "a/bc")})
	for _, path := range []string{"a", "a/bc", "a/bc/c", "a/bx"} {
		if _, ok := n.entries[path]; !ok {
			t.Fatalf("expected %q to exist after rename; entries=%#v", path, n.entries)
		}
	}
	if _, ok := n.entries["a/b"]; ok {
		t.Fatalf("old path a/b still exists: %#v", n.entries)
	}
	if _, ok := n.watched["a/bc"]; !ok {
		t.Fatalf("watched state was not renamed: %#v", n.watched)
	}
}

func TestMultipleReleasesFillInByteOrder(t *testing.T) {
	n, _ := New(3, 10)
	var events []Event
	for _, path := range []string{"a", "b", "c", "d"} {
		call(t, n, &events, func() ([]Event, error) { return n.Created(0, path, true) }, nil)
	}
	call(t, n, &events, func() ([]Event, error) { return n.MovedFrom(1, "a", 2, true) }, nil)
	call(t, n, &events, func() ([]Event, error) { return n.MovedFrom(1, "b", 1, true) }, nil)
	events = nil
	call(t, n, &events, func() ([]Event, error) { return n.Tick(11) }, nil)

	assertEvents(t, events, []Event{
		DeletedEvent("b"),
		DeletedEvent("a"),
		RescanEvent("c"),
		RescanEvent("d"),
	})
}

func TestOverflowDropsPendingThenFills(t *testing.T) {
	n, _ := New(2, 10)
	var events []Event
	call(t, n, &events, func() ([]Event, error) { return n.Created(0, "a", true) }, nil)
	call(t, n, &events, func() ([]Event, error) { return n.Created(0, "b", true) }, nil)
	call(t, n, &events, func() ([]Event, error) { return n.MovedFrom(1, "a", 7, true) }, nil)
	events = nil
	call(t, n, &events, func() ([]Event, error) { return n.Overflow(2) }, nil)

	assertEvents(t, events, []Event{
		RescanEvent(""),
		RescanEvent("b"),
	})
	if len(n.pending) != 0 || len(n.watched) != 2 {
		t.Fatalf("pending=%#v watched=%#v", n.pending, n.watched)
	}
}

func TestEntryEffectsSurviveStateRejection(t *testing.T) {
	n, _ := New(2, 10)
	var events []Event
	call(t, n, &events, func() ([]Event, error) { return n.Created(0, "a", true) }, nil)
	call(t, n, &events, func() ([]Event, error) { return n.MovedFrom(1, "a", 7, true) }, nil)
	events = nil
	call(t, n, &events, func() ([]Event, error) { return n.Deleted(11, "missing") }, ErrUnknown)

	assertEvents(t, events, []Event{
		DeletedEvent("a"),
	})
	if _, ok := n.entries["a"]; ok {
		t.Fatal("expiration effects must remain committed even when the operation is rejected")
	}
}

func TestMovedToPairMismatchHasPriority(t *testing.T) {
	n, _ := New(2, 10)
	var events []Event
	call(t, n, &events, func() ([]Event, error) { return n.Created(0, "a", true) }, nil)
	call(t, n, &events, func() ([]Event, error) { return n.MovedFrom(1, "a", 7, true) }, nil)
	call(t, n, &events, func() ([]Event, error) { return n.MovedTo(2, "missing/a", 7, false) }, ErrMismatch)
	if _, ok := n.pending[7]; !ok {
		t.Fatal("mismatched pending record must be retained")
	}
}

func TestErrorPrioritiesAndPathValidation(t *testing.T) {
	n, _ := New(2, 10)
	n.lastNow = 5
	invalidPaths := []string{"", "/a", "a/", "a//b", ".", "..", "a/../b", "a/./b"}
	for _, path := range invalidPaths {
		if _, err := n.Created(3, path, true); !errors.Is(err, ErrClock) {
			t.Fatalf("Created(%q) error=%v, want ErrClock", path, err)
		}
	}
	for _, path := range invalidPaths {
		if _, err := n.Created(6, path, true); !errors.Is(err, ErrBadArg) {
			t.Fatalf("Created(%q) error=%v, want ErrBadArg", path, err)
		}
	}
	fresh, _ := New(2, 10)
	if _, err := fresh.Created(-1, "a", true); !errors.Is(err, ErrBadArg) {
		t.Fatalf("negative now error=%v, want ErrBadArg", err)
	}
}

func TestConcurrentCalls(t *testing.T) {
	n, _ := New(4, 10)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			path := string(rune('a' + i%10))
			_, _ = n.Created(int64(i), path, true)
			_, _ = n.Tick(int64(i + 1))
		}(i)
	}
	wg.Wait()
	if len(n.watched) > 4 {
		t.Fatalf("watched count = %d, exceeds limit", len(n.watched))
	}
}

func assertEvents(t *testing.T, got, want []Event) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("events = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("event[%d] = %#v, want %#v; all=%#v", i, got[i], want[i], got)
		}
	}
}
