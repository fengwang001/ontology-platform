package groupcommit

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestCountBoundary: five preloaded entries, MaxEntries=2 -> batches 2,2,1
// with strictly contiguous sequence runs assigned per batch.
func TestCountBoundary(t *testing.T) {
	p := newFakePersister()
	gc, release := newGatedCommitter(t, Config{MaxEntries: 2, MaxBytes: 1 << 20, Persister: p})

	const n = 5
	reqs := make([]*pending, n)
	for i := range reqs {
		reqs[i] = &pending{payload: []byte("x"), result: make(chan batchResult, 1)}
		gc.submit <- reqs[i]
	}
	release()

	for i := range reqs {
		select {
		case r := <-reqs[i].result:
			if r.failed {
				t.Fatalf("entry %d failed: %v", i, r.err)
			}
			if r.seq != int64(i+1) {
				t.Fatalf("entry %d seq = %d, want %d", i, r.seq, i+1)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("entry %d no result", i)
		}
	}

	got := p.snapshot()
	if len(got) != 3 {
		t.Fatalf("batches = %d, want 3", len(got))
	}
	if len(got[0]) != 2 || len(got[1]) != 2 || len(got[2]) != 1 {
		t.Fatalf("batch sizes = %d,%d,%d want 2,2,1", len(got[0]), len(got[1]), len(got[2]))
	}
}

// TestByteBoundary: MaxBytes=10, payloads of 4,4,4 -> batch1 holds two
// (8+4>10 at the third), batch2 holds the last one. The boundary decision is
// "adding one more would exceed the byte cap", never splitting an accepted
// payload.
func TestByteBoundary(t *testing.T) {
	p := newFakePersister()
	gc, release := newGatedCommitter(t, Config{MaxEntries: 100, MaxBytes: 10, Persister: p})

	sizes := []int{4, 4, 4}
	reqs := make([]*pending, len(sizes))
	for i, sz := range sizes {
		reqs[i] = &pending{payload: make([]byte, sz), result: make(chan batchResult, 1)}
		gc.submit <- reqs[i]
	}
	release()

	for i := range reqs {
		select {
		case r := <-reqs[i].result:
			if r.failed {
				t.Fatalf("entry %d failed: %v", i, r.err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("entry %d no result", i)
		}
	}

	got := p.snapshot()
	if len(got) != 2 {
		t.Fatalf("batches = %d, want 2", len(got))
	}
	if len(got[0]) != 2 || len(got[1]) != 1 {
		t.Fatalf("batch sizes = %d,%d want 2,1", len(got[0]), len(got[1]))
	}

	// Exactly-at-limit payload fits; limit+1 must be rejected up front.
	p2 := newFakePersister()
	gc2 := newTestCommitter(t, Config{MaxEntries: 100, MaxBytes: 10, Persister: p2})
	if _, err := gc2.Submit(context.Background(), make([]byte, 10)); err != nil {
		t.Fatalf("payload == MaxBytes rejected: %v", err)
	}
	if _, err := gc2.Submit(context.Background(), make([]byte, 11)); err != ErrPayloadTooLarge {
		t.Fatalf("payload > MaxBytes: got %v, want ErrPayloadTooLarge", err)
	}
}

// TestCloseDrainsInflight: entries admitted before Close must all complete,
// while Close blocks until then and later submits are rejected.
func TestCloseDrainsInflight(t *testing.T) {
	p := newFakePersister()
	gc, release := newGatedCommitter(t, Config{MaxEntries: 3, MaxBytes: 1 << 20, Persister: p})
	proceed := make(chan struct{})
	p.proceed = proceed
	p.started = make(chan struct{}, 16)

	const n = 7
	reqs := make([]*pending, n)
	for i := range reqs {
		reqs[i] = &pending{payload: []byte{byte('a' + i)}, result: make(chan batchResult, 1)}
		gc.submit <- reqs[i]
	}

	closed := make(chan struct{})
	release()
	<-p.started // batch 1 is mid-persistence when shutdown is requested
	go func() {
		_ = gc.Close()
		close(closed)
	}()

	// While persistence is blocked, Close must wait for in-flight requests.
	select {
	case <-closed:
		t.Fatal("Close returned before in-flight requests completed")
	case <-time.After(20 * time.Millisecond):
	}
	close(proceed) // let all batches finish

	var wg sync.WaitGroup
	for i, req := range reqs {
		wg.Add(1)
		go func(i int, req *pending) {
			defer wg.Done()
			select {
			case r := <-req.result:
				if r.failed {
					t.Errorf("entry %d failed: %v", i, r.err)
				}
				if r.seq != int64(i+1) {
					t.Errorf("entry %d seq = %d, want %d", i, r.seq, i+1)
				}
			case <-time.After(5 * time.Second):
				t.Errorf("entry %d never completed after Close", i)
			}
		}(i, req)
	}
	wg.Wait()

	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return after drain")
	}

	// After close, new submits are rejected and nothing is enqueued.
	if _, err := gc.Submit(context.Background(), []byte("late")); err != ErrClosed {
		t.Fatalf("post-close submit: got %v, want ErrClosed", err)
	}
	if count := p.batchCount(); count != 3 {
		t.Fatalf("persist attempts after close = %d, want 3", count)
	}

	// Close is idempotent.
	if err := gc.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// TestDeterministic: identical enqueue order plus identical failure injection
// produces identical batch cuts and sequence assignment across runs.
func TestDeterministic(t *testing.T) {
	runOnce := func() ([][][]byte, []int64) {
		p := newFakePersister()
		p.failOn[1] = true
		gc, release := newGatedCommitter(t, Config{MaxEntries: 3, MaxBytes: 5, Persister: p})

		// Payloads: 3,3,2,2 bytes -> cuts at byte boundary: [3],[3],[2,2];
		// first batch fails and is removed durably.
		names := []string{"aaa", "bbb", "cc", "dd"}
		reqs := make([]*pending, len(names))
		for i, name := range names {
			reqs[i] = &pending{payload: []byte(name), result: make(chan batchResult, 1)}
			gc.submit <- reqs[i]
		}
		release()

		seqs := make([]int64, len(reqs))
		for i, req := range reqs {
			r := <-req.result
			seqs[i] = r.seq
		}
		_ = gc.Close()
		return p.snapshot(), seqs
	}

	cuts1, seqs1 := runOnce()
	cuts2, seqs2 := runOnce()
	if len(cuts1) != len(cuts2) {
		t.Fatalf("batch count differs: %d vs %d", len(cuts1), len(cuts2))
	}
	for i := range cuts1 {
		if len(cuts1[i]) != len(cuts2[i]) {
			t.Fatalf("batch %d size differs: %d vs %d", i, len(cuts1[i]), len(cuts2[i]))
		}
		for j := range cuts1[i] {
			if string(cuts1[i][j]) != string(cuts2[i][j]) {
				t.Fatalf("batch %d entry %d differs: %q vs %q", i, j, cuts1[i][j], cuts2[i][j])
			}
		}
	}
	for i := range seqs1 {
		if seqs1[i] != seqs2[i] {
			t.Fatalf("seq %d differs: %d vs %d", i, seqs1[i], seqs2[i])
		}
	}
}

// TestOneBatchAtATime: a slow persister must never observe overlap while
// requests keep arriving.
func TestOneBatchAtATime(t *testing.T) {
	p := newFakePersister()
	gc, release := newGatedCommitter(t, Config{MaxEntries: 1, MaxBytes: 1 << 20, Persister: p})

	const n = 20
	reqs := make([]*pending, n)
	for i := range reqs {
		reqs[i] = &pending{payload: []byte("z"), result: make(chan batchResult, 1)}
		gc.submit <- reqs[i]
	}
	release()
	for _, req := range reqs {
		<-req.result
	}
	if p.maxActive == 2 {
		t.Fatal("persister observed concurrent batches")
	}
	if p.batchCount() != n {
		t.Fatalf("attempts = %d, want %d", p.batchCount(), n)
	}
}

// TestCloseDrainsBufferedChannel: when shutdown starts with requests still
// sitting in the submit channel buffer, none may be lost.
func TestCloseDrainsBufferedChannel(t *testing.T) {
	p := newFakePersister()
	gc, release := newGatedCommitter(t, Config{MaxEntries: 10, MaxBytes: 1 << 20, Persister: p})

	const n = 6
	reqs := make([]*pending, n)
	reqs[0] = &pending{payload: []byte("first"), result: make(chan batchResult, 1)}
	gc.submit <- reqs[0] // wakes the worker; it blocks on the gate

	for i := 1; i < n; i++ {
		reqs[i] = &pending{payload: []byte{byte('a' + i)}, result: make(chan batchResult, 1)}
		gc.submit <- reqs[i] // buffered, not yet picked up by the worker
	}

	closed := make(chan struct{})
	go func() { _ = gc.Close(); close(closed) }()
	release()

	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return after draining buffered requests")
	}

	for i, req := range reqs {
		select {
		case r := <-req.result:
			if r.failed || r.seq != int64(i+1) {
				t.Fatalf("entry %d got %+v, want seq %d success", i, r, i+1)
			}
		default:
			t.Fatalf("buffered entry %d lost on shutdown", i)
		}
	}
}
