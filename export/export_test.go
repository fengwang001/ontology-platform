package export

import (
	"context"
	"testing"
)

// A clean cycle confirms exactly once and advances the checkpoint.
func TestCleanCycle(t *testing.T) {
	cp, h := NewMemCheckpoint(), NewMemHistory()
	m := NewManager(cp, h)
	sink := newRecSink()

	c, err := m.Begin(context.Background(), "L", Range{0, 3})
	if err != nil {
		t.Fatal(err)
	}
	acceptAll(t, c, Write{1, "a"}, Write{2, "b"}, Write{3, "c"})
	if _, err := c.Deliver(context.Background(), sink); err != nil {
		t.Fatal(err)
	}
	if err := c.Confirm(); err != nil {
		t.Fatal(err)
	}
	if got, _ := cp.Load("L"); got != 3 {
		t.Fatalf("checkpoint = %d, want 3", got)
	}
	if got := idsStr(sink.Emitted()); got != "[a b c]" {
		t.Fatalf("emitted = %v", got)
	}
}

// The end cannot be confirmed until every write of the interval is out.
func TestConfirmRejectsIncomplete(t *testing.T) {
	cp, h := NewMemCheckpoint(), NewMemHistory()
	m := NewManager(cp, h)
	c, _ := m.Begin(context.Background(), "L", Range{0, 3})
	acceptAll(t, c, Write{1, "a"}, Write{2, "b"})
	if _, err := c.Deliver(context.Background(), newRecSink()); err != nil {
		t.Fatal(err)
	}
	if err := c.Confirm(); errKind(err) != KindResourceExhausted {
		t.Fatalf("confirm err = %v, want ResourceExhausted", err)
	}
	// Checkpoint untouched: a restart must see start 0.
	if got, _ := m.ResolvedStart("L", 0); got != 0 {
		t.Fatalf("start moved to %d before confirmation", got)
	}
}

// A new cycle must start at the last confirmed end; arbitrary starts are lies.
func TestStartMustEqualConfirmedEnd(t *testing.T) {
	cp, h := NewMemCheckpoint(), NewMemHistory()
	m := NewManager(cp, h)
	c0 := mustBegin(t, m, "L", 0, 2)
	acceptAll(t, c0, Write{1, "a"}, Write{2, "b"})
	commitCycle(t, m, c0, newRecSink())
	_, err := m.Begin(context.Background(), "L", Range{1, 4})
	if errKind(err) != KindStartMismatch {
		t.Fatalf("err = %v, want StartMismatch", err)
	}
}

// Interrupt at every phase before confirmation, then restart: accumulated
// output must equal one uninterrupted run from the confirmed start.
func TestInterruptRestartEquivalence(t *testing.T) {
	for _, phase := range []string{"afterAccept", "midDeliver", "afterDeliver", "failedConfirm"} {
		t.Run(phase, func(t *testing.T) {
			cp, h := NewMemCheckpoint(), NewMemHistory()
			m := NewManager(cp, h)
			sink := newRecSink()

			c1 := mustBegin(t, m, "L", 0, 4)
			acceptAll(t, c1, Write{1, "a"}, Write{2, "b"})
			switch phase {
			case "afterAccept":
				c1.Abandon()
			case "midDeliver":
				sink.failAt["b"] = true
				if _, err := c1.Deliver(context.Background(), sink); errKind(err) != KindResourceExhausted {
					t.Fatalf("want resource error, got %v", err)
				}
				c1.Abandon()
			case "afterDeliver":
				if _, err := c1.Deliver(context.Background(), sink); err != nil {
					t.Fatal(err)
				}
				c1.Abandon()
			case "failedConfirm":
				if _, err := c1.Deliver(context.Background(), sink); err != nil {
					t.Fatal(err)
				}
				cp.Corrupt["L"] = true
				if err := c1.Confirm(); errKind(err) != KindResourceExhausted {
					t.Fatalf("want resource error, got %v", err)
				}
				c1.Abandon()
				cp.Repair("L", 0)
			}

			c2 := mustBegin(t, m, "L", 0, 4)
			acceptAll(t, c2, Write{1, "a"}, Write{2, "b"}, Write{3, "c"}, Write{4, "d"})
			if _, err := c2.Deliver(context.Background(), sink); err != nil {
				t.Fatal(err)
			}
			if err := c2.Confirm(); err != nil {
				t.Fatal(err)
			}
			if got := idsStr(sink.Emitted()); got != "[a b c d]" {
				t.Fatalf("phase %s emitted %v", phase, got)
			}
			if got, _ := cp.Load("L"); got != 4 {
				t.Fatalf("checkpoint = %d", got)
			}
		})
	}
}

// Retries interleaved with other writes keep the first acceptance slot.
func TestRetryKeepsFirstSlot(t *testing.T) {
	m := NewManager(NewMemCheckpoint(), NewMemHistory())
	c := mustBegin(t, m, "L", 0, 5)
	steps := []Write{
		{1, "a"}, {2, "b"},
		{1, "a"}, // retry after b: must not move a
		{3, "c"},
		{2, "b"}, {2, "b"},
		{4, "d"}, {5, "e"},
		{5, "e"},
	}
	counts := map[string]int{}
	for _, w := range steps {
		fresh, err := c.Accept(context.Background(), w)
		if err != nil {
			t.Fatal(err)
		}
		if counts[w.ID] > 0 && fresh {
			t.Fatalf("retry of %s reported fresh", w.ID)
		}
		counts[w.ID]++
	}
	if got := idsStr(c.dedup.Merged()); got != "[a b c d e]" {
		t.Fatalf("merged = %v", got)
	}
	sink := newRecSink()
	commitCycle(t, m, c, sink)
	if got := idsStr(sink.Emitted()); got != "[a b c d e]" {
		t.Fatalf("emitted = %v", got)
	}
}

// Direct deduper check: a late retry leaves no position trace.
func TestDeduperOrdering(t *testing.T) {
	d := NewDeduper()
	d.Accept(Write{10, "x"})
	d.Accept(Write{11, "y"})
	d.Accept(Write{10, "x"})
	d.Accept(Write{12, "z"})
	if got := idsStr(d.Merged()); got != "[x y z]" {
		t.Fatalf("got %v", got)
	}
}

// Fixed priority when several errors coexist.
func TestErrorPriorityFixed(t *testing.T) {
	errs := []error{
		mkErr(KindResourceExhausted, "x"),
		mkErr(KindHistoryGap, "x"),
		mkErr(KindCheckpointUnreadable, "x"),
		mkErr(KindStartMismatch, "x"),
	}
	if k := Classify(errs...); k != KindStartMismatch {
		t.Fatalf("priority = %s", k)
	}
	if k := Classify(errs[1], errs[2]); k != KindCheckpointUnreadable {
		t.Fatalf("priority = %s", k)
	}
	if k := Classify(errs[0], errs[1]); k != KindHistoryGap {
		t.Fatalf("priority = %s", k)
	}
	if k := Classify(nil, errs[0]); k != KindResourceExhausted {
		t.Fatalf("priority = %s", k)
	}
}
