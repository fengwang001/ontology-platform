package groupcommit

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func newGatedCommitter(t *testing.T, cfg Config) (*GroupCommitter, func()) {
	t.Helper()
	cfg.Log = testLogger{t: t}
	gc, err := NewGroupCommitter(cfg)
	if err != nil {
		t.Fatalf("NewGroupCommitter: %v", err)
	}
	gate := make(chan struct{})
	gc.setBatchGate(gate)
	t.Cleanup(func() { _ = gc.Close() })
	return gc, func() { close(gate) }
}

func timeAfter() <-chan time.Time { return time.After(5 * time.Second) }

func TestInvalidConfig(t *testing.T) {
	p := newFakePersister()
	cases := []struct {
		name string
		cfg  Config
		want error
	}{
		{"zero max entries", Config{MaxEntries: 0, MaxBytes: 10, Persister: p}, ErrInvalidMaxEntries},
		{"negative max entries", Config{MaxEntries: -1, MaxBytes: 10, Persister: p}, ErrInvalidMaxEntries},
		{"zero max bytes", Config{MaxEntries: 1, MaxBytes: 0, Persister: p}, ErrInvalidMaxBytes},
		{"negative max bytes", Config{MaxEntries: 1, MaxBytes: -9, Persister: p}, ErrInvalidMaxBytes},
		{"nil persister", Config{MaxEntries: 1, MaxBytes: 10, Persister: nil}, ErrNilPersister},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewGroupCommitter(tc.cfg)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestImmediateRejections(t *testing.T) {
	p := newFakePersister()
	gc := newTestCommitter(t, Config{MaxEntries: 4, MaxBytes: 8, Persister: p})

	// Empty payload is rejected immediately and consumes nothing.
	if _, err := gc.Submit(context.Background(), nil); !errors.Is(err, ErrEmptyPayload) {
		t.Fatalf("empty: got %v, want ErrEmptyPayload", err)
	}
	if _, err := gc.Submit(context.Background(), []byte{}); !errors.Is(err, ErrEmptyPayload) {
		t.Fatalf("empty slice: got %v, want ErrEmptyPayload", err)
	}

	// Oversized payload is rejected immediately and consumes nothing.
	big := []byte("0123456789") // 10 > MaxBytes 8
	if _, err := gc.Submit(context.Background(), big); !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("oversized: got %v, want ErrPayloadTooLarge", err)
	}

	// Nothing was queued or persisted; a normal submit still starts at seq 1.
	res, err := gc.Submit(context.Background(), []byte("ab"))
	if err != nil {
		t.Fatalf("normal submit: %v", err)
	}
	if res.Seq != 1 {
		t.Fatalf("seq after rejections = %d, want 1", res.Seq)
	}

	// Submit after Close is rejected with a distinct reason.
	if err := gc.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := gc.Submit(context.Background(), []byte("cd")); !errors.Is(err, ErrClosed) {
		t.Fatalf("post-close submit: got %v, want ErrClosed", err)
	}
	if p.batchCount() != 1 {
		t.Fatalf("rejected requests persisted: attempts=%d, want 1", p.batchCount())
	}
}

func Test200Concurrent(t *testing.T) {
	const callers = 200
	p := newFakePersister()
	gc := newTestCommitter(t, Config{MaxEntries: 16, MaxBytes: 1 << 20, Persister: p})

	var wg sync.WaitGroup
	results := make([]Result, callers)
	errs := make([]error, callers)
	start := make(chan struct{})
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			r, err := gc.Submit(context.Background(), []byte(fmt.Sprintf("writer-%03d", i)))
			results[i] = r
			errs[i] = err
		}(i)
	}
	close(start)
	wg.Wait()

	seen := make(map[int64]int, callers)
	for i, err := range errs {
		if err != nil {
			t.Fatalf("writer %d: %v", i, err)
		}
		if results[i].Seq < 1 || results[i].Seq > callers {
			t.Fatalf("writer %d out-of-range seq %d", i, results[i].Seq)
		}
		if results[i].Batch < 1 {
			t.Fatalf("writer %d bad batch %d", i, results[i].Batch)
		}
		// A writer must only ever receive its own payload's result; verify via
		// the durable record indexed by the returned sequence number.
		seen[results[i].Seq] = i
	}
	if len(seen) != callers {
		t.Fatalf("unique seqs = %d, want %d (loss or cross-talk)", len(seen), callers)
	}

	durable := p.durable
	var count int
	var prev int64
	for bi, batch := range durable {
		count += len(batch)
		for _, raw := range batch {
			s := string(raw)
			if !strings.HasPrefix(s, "writer-") {
				t.Fatalf("unexpected payload %q", s)
			}
			var idx int
			if _, err := fmt.Sscanf(s, "writer-%03d", &idx); err != nil {
				t.Fatalf("parse %q: %v", s, err)
			}
			got := results[idx].Seq
			prev++
			if got != prev {
				t.Fatalf("durable position %d (batch %d) got seq %d", prev, bi+1, got)
			}
			if results[idx].Batch != int64(bi+1) {
				t.Fatalf("writer %d batch = %d, durable batch %d", idx, results[idx].Batch, bi+1)
			}
		}
	}
	if count != callers {
		t.Fatalf("durable entries = %d, want %d", count, callers)
	}
	if p.maxActive == 2 {
		t.Fatal("two batches persisted concurrently")
	}
}

func TestFailureRecyclesSequence(t *testing.T) {
	p := newFakePersister()
	p.failOn[2] = true // the second persist attempt fails
	gc, release := newGatedCommitter(t, Config{MaxEntries: 2, MaxBytes: 1 << 20, Persister: p})

	names := []string{"a1", "a2", "b1", "b2", "c1", "c2"}
	reqs := make([]*pending, len(names))
	for i, name := range names {
		reqs[i] = &pending{
			payload: []byte(name),
			result:  make(chan batchResult, 1),
		}
		gc.submit <- reqs[i] // buffered channel: deterministic FIFO order
	}
	// All six requests are queued in exact order before any batch is cut.
	release()

	get := func(i int) batchResult {
		select {
		case o := <-reqs[i].result:
			return o
		case <-timeAfter():
			t.Fatalf("writer %s never got a result", names[i])
			return batchResult{}
		}
	}

	o1, o2 := get(0), get(1)
	if o1.failed || o1.seq != 1 || o1.batch != 1 {
		t.Fatalf("a1 = %+v", o1)
	}
	if o2.failed || o2.seq != 2 || o2.batch != 1 {
		t.Fatalf("a2 = %+v", o2)
	}

	// Batch 2 fails: both callers get the identical reason; seqs recycled.
	o3, o4 := get(2), get(3)
	if !o3.failed || !o4.failed {
		t.Fatal("batch 2 entries not marked failed")
	}
	if !errors.Is(o3.err, ErrBatchFailed) || !errors.Is(o4.err, ErrBatchFailed) {
		t.Fatalf("failed batch errors: %v / %v", o3.err, o4.err)
	}
	if o3.err.Error() != o4.err.Error() {
		t.Fatalf("failure reason differs within batch: %q vs %q", o3.err, o4.err)
	}

	// Batch 3 resumes at the recycled seq 3: durable seqs stay 1..N contiguous.
	o5, o6 := get(4), get(5)
	if o5.failed || o5.seq != 3 || o5.batch != 3 {
		t.Fatalf("c1 = %+v, want seq 3 batch 3", o5)
	}
	if o6.failed || o6.seq != 4 || o6.batch != 3 {
		t.Fatalf("c2 = %+v, want seq 4 batch 3", o6)
	}

	var flat []string
	for _, b := range p.durable {
		for _, raw := range b {
			flat = append(flat, string(raw))
		}
	}
	want := []string{"a1", "a2", "c1", "c2"}
	if len(flat) != len(want) {
		t.Fatalf("durable = %v, want %v", flat, want)
	}
	for i := range want {
		if flat[i] != want[i] {
			t.Fatalf("durable[%d] = %q, want %q (gap after recycle)", i, flat[i], want[i])
		}
	}
	joined := strings.Join(flat, ",")
	if strings.Contains(joined, "b1") || strings.Contains(joined, "b2") {
		t.Fatalf("failed batch leaked into durable storage: %v", flat)
	}

	// Exactly three persist attempts, only two durable.
	if p.batchCount() != 3 {
		t.Fatalf("attempts = %d, want 3", p.batchCount())
	}
}
