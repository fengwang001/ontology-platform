package replay

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// logStep prints every required piece of evidence for one operation:
// operation, clock/tokens before and after, emitted sequence, and the
// judgment basis (reason, including the expectation).
func logStep(t *testing.T, op string, before, after Snapshot, emitted []int64, reason string) {
	t.Helper()
	t.Logf("op=%-18s clock:%d->%d tokens:%d->%d pending:%d emitted=%v verdict=%s",
		op, before.Clock, after.Clock, before.Tokens, after.Tokens, after.Pending, emitted, reason)
}

func seqString(seq []int64) string {
	parts := make([]string, len(seq))
	for i, v := range seq {
		parts[i] = fmt.Sprintf("%d", v)
	}
	return "[" + strings.Join(parts, " ") + "]"
}

// TestStepwiseReplayTriples replays a script of
// (operation@clock, events, expected-emission) triples against one fixed
// configuration and checks every triple against the actual player state.
//
//	Config: base=1/tick, catch-up=2/tick, bucket=5, queue limit=10.
func TestStepwiseReplayTriples(t *testing.T) {
	r, err := New(Config{BaseRate: 1, CatchupRate: 2, BucketCap: 5, QueueLimit: 10})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	type triple struct {
		op     string
		at     int64
		events []int64
		want   []int64 // expected drain output (enqueue/advance emit nothing)
		reason string
	}
	script := []triple{
		{"Enqueue", 0, []int64{1, 2, 3}, nil, "backlog of 3 at t=0; tokens start at 0"},
		{"Drain", 0, nil, []int64{}, "elapsed=0, 0 tokens -> nothing emitted"},
		{"Drain", 1, nil, []int64{1, 2}, "1 tick backlog refill at catch-up 2/tick -> 2 emitted"},
		{"Enqueue", 1, []int64{4, 5, 6, 7, 8, 9, 10}, nil, "elapsed=0, no refill; 7 pending"},
		{"Drain", 2, nil, []int64{3, 4}, "1 more tick -> 2 tokens, FIFO continues"},
		{"Drain", 5, nil, []int64{5, 6, 7, 8, 9}, "3 idle ticks backlogged: 2*3=6 capped at 5"},
		{"Drain", 6, nil, []int64{10}, "2 new tokens, 1 pending -> emitted, 1 token banked"},
		{"Advance", 10, nil, nil, "queue empty: base rate 1/tick, 1+4 capped at bucket 5"},
	}

	for _, st := range script {
		before := r.Snapshot()
		var got []int64
		var opErr error
		switch st.op {
		case "Enqueue":
			opErr = r.Enqueue(st.at, st.events)
		case "Drain":
			got, opErr = r.Drain(st.at)
		case "Advance":
			opErr = r.Advance(st.at)
		}
		after := r.Snapshot()
		if opErr != nil {
			t.Fatalf("%s@%d: unexpected error: %v", st.op, st.at, opErr)
		}
		logStep(t, fmt.Sprintf("%s@%d", st.op, st.at), before, after, got,
			st.reason+" | want="+seqString(st.want))
		if fmt.Sprint(got) != fmt.Sprint(st.want) {
			t.Fatalf("%s@%d: got %v, want %v", st.op, st.at, got, st.want)
		}
		if err := r.SelfCheck(); err != nil {
			t.Fatalf("SelfCheck after %s@%d: %v", st.op, st.at, err)
		}
	}

	snap := r.Snapshot()
	logStep(t, "FinalCheck", snap, snap, nil,
		"enqueued(10) == drained(10) + pending(0); tokens capped at 5")
	if snap.EnqueuedTotal != 10 || snap.DrainedTotal != 10 || snap.Pending != 0 {
		t.Fatalf("final totals wrong: %+v", snap)
	}
	if snap.Tokens != 5 {
		t.Fatalf("tokens should be capped at 5, got %d", snap.Tokens)
	}
}

// TestCatchupRefillCapped verifies that while a backlog exists, refill runs
// at the (higher) catch-up rate but never raises tokens above BucketCap.
func TestCatchupRefillCapped(t *testing.T) {
	cfg := Config{BaseRate: 1, CatchupRate: 10, BucketCap: 4, QueueLimit: 100}
	r, _ := New(cfg)

	burst := make([]int64, 50)
	for i := range burst {
		burst[i] = int64(i + 1)
	}
	before := r.Snapshot()
	if err := r.Enqueue(0, burst); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// 1000s * 10/s would be 10000 raw tokens, but the bucket holds at most 4,
	// so the burst is sliced at the bucket capacity.
	out, err := r.Drain(1000)
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	after := r.Snapshot()
	logStep(t, "Drain@1000", before, after, out,
		"catch-up 10/s*1000s saturated at cap=4 -> first slice has exactly 4 events")
	if len(out) != 4 || out[0] != 1 || out[3] != 4 {
		t.Fatalf("catch-up burst should emit 4 FIFO events 1..4, got %v", out)
	}

	// Still backlogged, refill again saturates at the cap.
	before = r.Snapshot()
	out, err = r.Drain(2000)
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	after = r.Snapshot()
	logStep(t, "Drain@2000", before, after, out,
		"still backlogged: refill again saturates at cap=4, events 5..8")
	if len(out) != 4 || out[0] != 5 || out[3] != 8 {
		t.Fatalf("second slice wrong: %v", out)
	}
	if err := r.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

// TestClockBackwardsRejected verifies every mutating op refuses a past
// timestamp and leaves state untouched.
func TestClockBackwardsRejected(t *testing.T) {
	r, _ := New(Config{BaseRate: 1, CatchupRate: 2, BucketCap: 2, QueueLimit: 10})
	if err := r.Enqueue(5, []int64{1}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	ops := map[string]func(int64) error{
		"Advance": r.Advance,
		"Enqueue": func(now int64) error { return r.Enqueue(now, []int64{2}) },
		"Drain": func(now int64) error {
			_, err := r.Drain(now)
			return err
		},
	}

	for name, fn := range ops {
		before := r.Snapshot()
		err := fn(4)
		after := r.Snapshot()
		logStep(t, name+"@4", before, after, nil,
			fmt.Sprintf("clock 5 -> 4 backwards, err=%v; state unchanged", err))
		if !errors.Is(err, ErrClockBackwards) {
			t.Fatalf("%s: want ErrClockBackwards, got %v", name, err)
		}
		if before != after {
			t.Fatalf("%s mutated state on backwards clock", name)
		}
	}
}

// TestInvalidConfig verifies every illegal configuration dimension is
// rejected with ErrInvalidConfig.
func TestInvalidConfig(t *testing.T) {
	bad := []Config{
		{BaseRate: 0, CatchupRate: 2, BucketCap: 2, QueueLimit: 10},
		{BaseRate: 2, CatchupRate: -1, BucketCap: 2, QueueLimit: 10},
		{BaseRate: 5, CatchupRate: 2, BucketCap: 2, QueueLimit: 10}, // catch-up < base
		{BaseRate: 2, CatchupRate: 2, BucketCap: 0, QueueLimit: 10},
		{BaseRate: 2, CatchupRate: 2, BucketCap: 2, QueueLimit: 0},
		{BaseRate: -3, CatchupRate: 2, BucketCap: 2, QueueLimit: 10},
	}
	for i, cfg := range bad {
		r, err := New(cfg)
		if !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("case %d: want ErrInvalidConfig, got %v (replayer=%v)", i, err, r)
		}
		t.Logf("op=New case=%d cfg=%+v verdict=rejected cause=%v", i, cfg, err)
	}
}

// TestFIFOOrder interleaves several enqueues and drains and checks the global
// emission sequence exactly matches enqueue order.
func TestFIFOOrder(t *testing.T) {
	r, _ := New(Config{BaseRate: 1, CatchupRate: 3, BucketCap: 5, QueueLimit: 100})

	var emitted []int64
	next := int64(1)
	enqueue := func(at int64, n int) {
		evs := make([]int64, n)
		for i := range evs {
			evs[i] = next
			next++
		}
		if err := r.Enqueue(at, evs); err != nil {
			t.Fatalf("Enqueue@%d: %v", at, err)
		}
	}
	drain := func(at int64) {
		before := r.Snapshot()
		out, err := r.Drain(at)
		if err != nil {
			t.Fatalf("Drain@%d: %v", at, err)
		}
		after := r.Snapshot()
		logStep(t, fmt.Sprintf("Drain@%d", at), before, after, out,
			"interleaved replay keeps global FIFO order")
		emitted = append(emitted, out...)
	}

	enqueue(0, 3)
	drain(1)
	enqueue(2, 4)
	drain(2)
	drain(3)
	enqueue(5, 2)
	drain(6)

	want := []int64{1, 2, 3, 4, 5, 6, 7, 8, 9}
	if seqString(emitted) != seqString(want) {
		t.Fatalf("FIFO broken: got %v, want %v", emitted, want)
	}
	if err := r.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

// TestConcurrentReadsAndSelfCheck drives one writer goroutine while many
// readers concurrently take snapshots and run SelfCheck. Every snapshot must
// be internally self-consistent; run with -race to exercise locking.
func TestConcurrentReadsAndSelfCheck(t *testing.T) {
	cfg := Config{BaseRate: 1, CatchupRate: 2, BucketCap: 4, QueueLimit: 50}
	r, _ := New(cfg)

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Readers: every observed snapshot must satisfy the totals invariant and
	// field ranges, and the per-field accessors must stay within range.
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				s := r.Snapshot()
				if s.EnqueuedTotal != s.DrainedTotal+s.Pending {
					t.Errorf("inconsistent snapshot under concurrency: %+v", s)
					return
				}
				if s.Tokens < 0 || s.Tokens > cfg.BucketCap || s.Pending < 0 || s.Pending > cfg.QueueLimit {
					t.Errorf("out-of-range snapshot under concurrency: %+v", s)
					return
				}
				if err := r.SelfCheck(); err != nil {
					t.Errorf("SelfCheck under concurrency: %v", err)
					return
				}
				if r.Len() < 0 || r.Tokens() < 0 || r.Clock() < 0 {
					t.Error("negative scalar read under concurrency")
					return
				}
			}
		}()
	}

	// Writer: one serialized timeline. Failures from queue overflow and
	// clock assertions are expected parts of the workload.
	seq := int64(0)
	for tick := int64(0); tick < 300; tick++ {
		evs := []int64{seq, seq + 1, seq + 2}
		seq += 3
		_ = r.Enqueue(tick, evs)
		out, err := r.Drain(tick)
		if err != nil {
			t.Fatalf("writer Drain@%d: %v", tick, err)
		}
		if len(out) > 0 {
			t.Logf("op=Drain@%d emitted=%v verdict=ordered drain under concurrent reads", tick, out)
		}
	}

	close(stop)
	wg.Wait()

	s := r.Snapshot()
	logStep(t, "FinalCheck", s, s, nil,
		"enqueued == drained + pending after concurrent workload")
	if s.EnqueuedTotal != s.DrainedTotal+s.Pending {
		t.Fatalf("final invariant broken: %+v", s)
	}
}

// TestBurstSmoothing verifies a burst larger than the bucket is flattened:
// the first slice is bounded by BucketCap, after which events leave at the
// steady catch-up rate, in strict FIFO order.
func TestBurstSmoothing(t *testing.T) {
	cfg := Config{BaseRate: 2, CatchupRate: 3, BucketCap: 5, QueueLimit: 100}
	r, _ := New(cfg)

	burst := []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	// Pre-charge the bucket while the queue is idle: base rate 2/s for 3s
	// yields 5 tokens, capped at BucketCap.
	if err := r.Advance(3); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if err := r.Enqueue(3, burst); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	var all []int64
	steps := []struct {
		at   int64
		want int
	}{
		{3, 5}, // banked burst: bucket cap 5 flattens the front of the burst
		{4, 3}, // backlog refill 3/s -> steady slice
		{5, 2}, // tail; one extra token remains banked
	}
	for _, st := range steps {
		before := r.Snapshot()
		out, err := r.Drain(st.at)
		if err != nil {
			t.Fatalf("Drain@%d: %v", st.at, err)
		}
		after := r.Snapshot()
		logStep(t, fmt.Sprintf("Drain@%d", st.at), before, after, out,
			fmt.Sprintf("burst split flat: slice want=%d, cumulative=%d", st.want, len(all)+len(out)))
		if len(out) != st.want {
			t.Fatalf("Drain@%d: got %d events, want %d (%v)", st.at, len(out), st.want, out)
		}
		all = append(all, out...)
	}
	if seqString(all) != seqString(burst) {
		t.Fatalf("smoothing reordered/dropped events: got %v, want %v", all, burst)
	}
}

// TestQueueFullRejectsWholeBatch verifies over-limit enqueues are rejected
// wholesale with ErrQueueFull and change no state.
func TestQueueFullRejectsWholeBatch(t *testing.T) {
	cfg := Config{BaseRate: 1, CatchupRate: 2, BucketCap: 2, QueueLimit: 3}
	r, _ := New(cfg)

	if err := r.Enqueue(0, []int64{1, 2}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	before := r.Snapshot()
	err := r.Enqueue(0, []int64{3, 4})
	after := r.Snapshot()
	logStep(t, "Enqueue@0(overflow)", before, after, nil,
		fmt.Sprintf("pending=2 + batch=2 > limit=3, err=%v; state unchanged", err))
	if !errors.Is(err, ErrQueueFull) {
		t.Fatalf("want ErrQueueFull, got %v", err)
	}
	if before != after {
		t.Fatalf("rejected enqueue mutated state: before=%+v after=%+v", before, after)
	}

	// One event exactly fills the queue and must succeed.
	before = r.Snapshot()
	if err := r.Enqueue(0, []int64{3}); err != nil {
		t.Fatalf("exact-fill Enqueue: %v", err)
	}
	after = r.Snapshot()
	logStep(t, "Enqueue@0(exact)", before, after, nil, "2 + 1 == limit 3: accepted")

	// Even on an empty queue, a batch larger than the limit is invalid.
	empty, _ := New(cfg)
	err = empty.Enqueue(0, []int64{1, 2, 3, 4})
	logStep(t, "Enqueue@0(oversized)", empty.Snapshot(), empty.Snapshot(), nil,
		fmt.Sprintf("batch=4 > limit=3, err=%v", err))
	if !errors.Is(err, ErrQueueFull) {
		t.Fatalf("want ErrQueueFull for oversized batch, got %v", err)
	}
	if err := r.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}
