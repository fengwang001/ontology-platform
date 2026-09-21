package batcher

import (
	"errors"
	"sync"
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

type sink struct {
	mu        sync.Mutex
	batches   []Batch
	failNext  int
	deliverN  int
	onDeliver func()
}

var errDeliver = errors.New("deliver failed")

func (s *sink) Deliver(b Batch) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deliverN++
	if s.onDeliver != nil {
		s.onDeliver()
	}
	s.batches = append(s.batches, b)
	if s.failNext > 0 {
		s.failNext--
		return errDeliver
	}
	return nil
}

func (s *sink) all() []Batch {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Batch, len(s.batches))
	copy(out, s.batches)
	return out
}

func TestCountTrigger(t *testing.T) {
	s := &sink{}
	b := New(s, 3, 1<<20, time.Hour, nil)
	for _, it := range []string{"a", "b", "c"} {
		if err := b.Add(it); err != nil {
			t.Fatal(err)
		}
	}
	got := s.all()
	if len(got) != 1 || got[0].Seq != 1 || len(got[0].Items) != 3 || got[0].Bytes != 3 {
		t.Fatalf("unexpected batches: %+v", got)
	}
	if n, by := b.Pending(); n != 0 || by != 0 {
		t.Fatalf("pending = %d, %d", n, by)
	}
}

func TestByteTrigger(t *testing.T) {
	s := &sink{}
	b := New(s, 100, 5, time.Hour, nil)
	_ = b.Add("ab")
	_ = b.Add("cd")
	if len(s.all()) != 0 {
		t.Fatal("batch sealed before byte limit")
	}
	_ = b.Add("e")
	got := s.all()
	if len(got) != 1 || got[0].Bytes != 5 {
		t.Fatalf("unexpected batches: %+v", got)
	}
}

func TestTooLarge(t *testing.T) {
	s := &sink{}
	b := New(s, 10, 3, time.Hour, nil)
	if err := b.Add("abcd"); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v", err)
	}
	if n, by := b.Pending(); n != 0 || by != 0 {
		t.Fatalf("oversized item entered buffer: %d, %d", n, by)
	}
	if len(s.all()) != 0 {
		t.Fatal("oversized item triggered a batch")
	}
	if err := b.Add("ab"); err != nil {
		t.Fatal(err)
	}
}

func TestAgeTriggerFromFirstItem(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	s := &sink{}
	b := New(s, 100, 1<<20, 10*time.Second, c.now)
	b.Tick() // empty buffer: no batch
	_ = b.Add("x")
	c.advance(9 * time.Second)
	_ = b.Add("y") // must not reset the timer
	b.Tick()
	if len(s.all()) != 0 {
		t.Fatal("sealed before maxAge from first item")
	}
	c.advance(time.Second)
	b.Tick()
	got := s.all()
	if len(got) != 1 || len(got[0].Items) != 2 {
		t.Fatalf("unexpected batches: %+v", got)
	}
	b.Tick() // buffer empty again: no empty batch
	if len(s.all()) != 1 {
		t.Fatal("empty tick produced a batch")
	}
}

func TestSeqStrictAndFailedPreserved(t *testing.T) {
	s := &sink{failNext: 1}
	b := New(s, 2, 1<<20, time.Hour, nil)
	_ = b.Add("a")
	_ = b.Add("b") // seq 1, delivery fails
	_ = b.Add("c")
	_ = b.Add("d") // seq 2, succeeds
	got := s.all()
	if len(got) != 2 || got[0].Seq != 1 || got[1].Seq != 2 {
		t.Fatalf("seq not strict: %+v", got)
	}
	failed := b.Failed()
	if len(failed) != 1 || failed[0].Seq != 1 || failed[0].Bytes != 2 ||
		failed[0].Items[0] != "a" || failed[0].Items[1] != "b" {
		t.Fatalf("failed batch not preserved: %+v", failed)
	}
	if n, _ := b.Pending(); n != 0 {
		t.Fatal("failed items flowed back into buffer")
	}
}

func TestFlushAndClose(t *testing.T) {
	s := &sink{}
	b := New(s, 100, 1<<20, time.Hour, nil)
	if err := b.Flush(); err != nil {
		t.Fatal(err)
	}
	if len(s.all()) != 0 {
		t.Fatal("empty flush produced a batch")
	}
	_ = b.Add("x")
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	got := s.all()
	if len(got) != 1 || got[0].Seq != 1 {
		t.Fatalf("close did not flush: %+v", got)
	}
	if err := b.Add("y"); !errors.Is(err, ErrClosed) {
		t.Fatalf("add after close = %v", err)
	}
	if err := b.Flush(); !errors.Is(err, ErrClosed) {
		t.Fatalf("flush after close = %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("second close = %v", err)
	}
	if len(s.all()) != 1 {
		t.Fatal("second close re-delivered")
	}
}

func TestDeliverVisibility(t *testing.T) {
	s := &sink{}
	b := New(s, 2, 1<<20, time.Hour, nil)
	var pendN, pendB, failedN = -1, -1, -1
	s.onDeliver = func() {
		pendN, pendB = b.Pending()
		failedN = len(b.Failed())
	}
	_ = b.Add("a")
	_ = b.Add("b")
	if pendN != 0 || pendB != 0 || failedN != 0 {
		t.Fatalf("during deliver: pending=%d,%d failed=%d", pendN, pendB, failedN)
	}
}
