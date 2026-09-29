package groupcommit

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

// fakePersister records every batch it persisted and can inject failures.
type fakePersister struct {
	mu        sync.Mutex
	batches   [][][]byte     // every persist attempt
	durable   [][][]byte     // only batches that returned nil
	failOn    map[int64]bool // 1-based batch ordinals that must fail
	failErr   error
	started   chan struct{} // signalled at each Persist entry
	proceed   chan struct{} // each Persist blocks until closed/sent
	persistMu sync.Mutex
	active    bool
	maxActive int
}

func newFakePersister() *fakePersister {
	return &fakePersister{
		failOn:  map[int64]bool{},
		failErr: fmt.Errorf("injected disk fault"),
	}
}

func (p *fakePersister) Persist(_ context.Context, entries [][]byte) error {
	p.persistMu.Lock()
	if p.active {
		p.maxActive = 2
	}
	p.active = true
	p.persistMu.Unlock()

	p.mu.Lock()
	no := int64(len(p.batches)) + 1
	stored := make([][]byte, len(entries))
	copy(stored, entries)
	p.batches = append(p.batches, stored)
	shouldFail := p.failOn[no]
	if p.started != nil {
		select {
		case p.started <- struct{}{}:
		default:
		}
	}
	p.mu.Unlock()

	if p.proceed != nil {
		<-p.proceed
	}

	p.persistMu.Lock()
	p.active = false
	p.persistMu.Unlock()

	if shouldFail {
		return fmt.Errorf("%w: batch %d", p.failErr, no)
	}
	p.mu.Lock()
	p.durable = append(p.durable, stored)
	p.mu.Unlock()
	return nil
}

func (p *fakePersister) snapshot() [][][]byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([][][]byte, len(p.batches))
	for i, b := range p.batches {
		out[i] = append([][]byte(nil), b...)
	}
	return out
}

func (p *fakePersister) batchCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.batches)
}

// testLogger writes decision lines into the test output, making inputs,
// outputs and the decision basis visible in -v logs.
type testLogger struct{ t *testing.T }

func (l testLogger) Printf(format string, args ...any) {
	l.t.Logf(format, args...)
}

func payload(n int) []byte {
	return []byte(fmt.Sprintf("%0*d", n, n))
}

func newTestCommitter(t *testing.T, cfg Config) *GroupCommitter {
	t.Helper()
	cfg.Log = testLogger{t: t}
	gc, err := NewGroupCommitter(cfg)
	if err != nil {
		t.Fatalf("NewGroupCommitter: %v", err)
	}
	t.Cleanup(func() { _ = gc.Close() })
	return gc
}

func TestSmoke(t *testing.T) {
	p := newFakePersister()
	gc := newTestCommitter(t, Config{MaxEntries: 2, MaxBytes: 100, Persister: p})

	res, err := gc.Submit(context.Background(), []byte("hello"))
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if res.Seq != 1 || res.Batch != 1 {
		t.Fatalf("got seq=%d batch=%d, want 1/1", res.Seq, res.Batch)
	}
}
