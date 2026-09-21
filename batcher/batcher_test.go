package batcher

import (
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

type recorder struct {
	mu       sync.Mutex
	batches  []Batch
	failNext map[uint64]bool
}

func (r *recorder) Deliver(b Batch) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.batches = append(r.batches, b)
	if r.failNext[b.Seq] {
		return errors.New("delivery failed")
	}
	return nil
}

func (r *recorder) got() []Batch {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Batch, len(r.batches))
	copy(out, r.batches)
	return out
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func TestCountAndByteTriggers(t *testing.T) {
	rec := &recorder{}
	b := New(rec, 3, 10, time.Hour, nil)
	_ = b.Add("ab")  // 2 bytes
	_ = b.Add("cd")  // 4 bytes
	_ = b.Add("efg") // count trigger: 3 items
	_ = b.Add("hi")
	_ = b.Add("jklmno") // 2+6=8 bytes, next hits
	_ = b.Add("pq")     // byte trigger: 10 bytes total
	got := rec.got()
	if len(got) != 2 {
		t.Fatalf("want 2 batches, got %d", len(got))
	}
	if !reflect.DeepEqual(got[0].Items, []string{"ab", "cd", "efg"}) {
		t.Fatalf("batch1 items = %v", got[0].Items)
	}
	if got[1].Bytes != 10 || got[1].Seq != 2 {
		t.Fatalf("batch2 = %+v", got[1])
	}
	if items, _ := b.Pending(); items != 0 {
		t.Fatalf("pending = %d, want 0", items)
	}
}

func TestAgeTriggerFromFirstItem(t *testing.T) {
	rec := &recorder{}
	clk := &fakeClock{t: time.Unix(0, 0)}
	b := New(rec, 100, 1000, 10*time.Second, clk.now)
	b.Tick() // empty buffer: no-op
	_ = b.Add("a")
	clk.advance(9 * time.Second)
	_ = b.Add("b") // does not reset the clock
	clk.advance(2 * time.Second)
	b.Tick() // 11s since first item
	got := rec.got()
	if len(got) != 1 || !reflect.DeepEqual(got[0].Items, []string{"a", "b"}) {
		t.Fatalf("got %v", got)
	}
	b.Tick() // buffer empty again: no empty batch
	if len(rec.got()) != 1 {
		t.Fatal("empty Tick produced a batch")
	}
}

func TestTooLarge(t *testing.T) {
	rec := &recorder{}
	b := New(rec, 10, 4, time.Hour, nil)
	_ = b.Add("ab")
	if err := b.Add("toolong"); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v", err)
	}
	items, bytes := b.Pending()
	if items != 1 || bytes != 2 {
		t.Fatalf("pending = %d/%d", items, bytes)
	}
	if len(rec.got()) != 0 {
		t.Fatal("oversized item triggered a batch")
	}
}

func TestSeqStrictAndFailedKeepSeq(t *testing.T) {
	rec := &recorder{failNext: map[uint64]bool{2: true}}
	b := New(rec, 1, 100, time.Hour, nil)
	for _, s := range []string{"a", "b", "c"} {
		_ = b.Add(s)
	}
	got := rec.got()
	for i, want := range []uint64{1, 2, 3} {
		if got[i].Seq != want {
			t.Fatalf("seq[%d] = %d, want %d", i, got[i].Seq, want)
		}
	}
	failed := b.Failed()
	if len(failed) != 1 || failed[0].Seq != 2 ||
		!reflect.DeepEqual(failed[0].Items, []string{"b"}) || failed[0].Bytes != 1 {
		t.Fatalf("failed = %+v", failed)
	}
	if items, _ := b.Pending(); items != 0 {
		t.Fatal("failed batch flowed back into buffer")
	}
}

func TestFlushAndClose(t *testing.T) {
	rec := &recorder{}
	b := New(rec, 100, 1000, time.Hour, nil)
	if err := b.Flush(); err != nil {
		t.Fatal(err)
	}
	if len(rec.got()) != 0 {
		t.Fatal("empty Flush consumed a seq")
	}
	_ = b.Add("x")
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if got := rec.got(); len(got) != 1 || got[0].Seq != 1 {
		t.Fatalf("got %v", got)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("second Close = %v", err)
	}
	if len(rec.got()) != 1 {
		t.Fatal("second Close delivered again")
	}
	if err := b.Add("y"); !errors.Is(err, ErrClosed) {
		t.Fatalf("Add after close = %v", err)
	}
	if err := b.Flush(); !errors.Is(err, ErrClosed) {
		t.Fatalf("Flush after close = %v", err)
	}
}
