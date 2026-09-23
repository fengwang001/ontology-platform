package fanout

import (
	"context"
	"errors"
	"testing"
	"time"

	"ontology/shard"
)

// gateShard blocks until release so concurrency can be pinned.
type gateShard struct {
	id      string
	gate    chan struct{}
	started chan<- string
}

func (g gateShard) ID() string { return g.id }

func (g gateShard) Fetch(ctx context.Context, deliver func(shard.Frame)) error {
	select {
	case g.started <- g.id:
	default:
	}
	select {
	case <-g.gate:
		deliver(shard.Frame{ShardID: g.id, Claimed: 0})
	case <-ctx.Done():
		return shard.ErrTimeout
	}
	return nil
}

func TestConcurrencyPeak(t *testing.T) {
	const n, limit = 200, 8
	gate := make(chan struct{})
	started := make(chan string, n)
	shards := make([]shard.Shard, n)
	for i := range shards {
		shards[i] = gateShard{id: "s" + string(rune('a'+i%26)) + string(rune('a'+i/26)), gate: gate, started: started}
	}
	done := make(chan struct{})
	var r *Result
	go func() {
		var err error
		r, err = Run(context.Background(), shards, limit, 0)
		_ = err
		close(done)
	}()
	peak := 0
	cur := 0
	deadline := time.After(2 * time.Second)
	for cur < limit {
		select {
		case <-started:
			cur++
			if cur > peak {
				peak = cur
			}
		case <-deadline:
			t.Fatalf("only %d started, want %d", cur, limit)
		}
	}
	select {
	case <-started:
		t.Fatalf("peak exceeded limit: %d in flight", limit+1)
	case <-time.After(30 * time.Millisecond):
	}
	close(gate)
	<-done
	if r.PeakInflight() > limit {
		t.Fatalf("PeakInflight = %d, want <= %d", r.PeakInflight(), limit)
	}
	if r.PeakInflight() < limit {
		t.Fatalf("PeakInflight = %d, want %d (limit must be reached)", r.PeakInflight(), limit)
	}
}

func TestDeadlineStopsNewRequests(t *testing.T) {
	const n, limit = 40, 2
	shards := make([]shard.Shard, n)
	for i := range shards {
		shards[i] = shard.NewFake("hang"+string(rune('a'+i)), nil, 0, shard.WithHang())
	}
	start := time.Now()
	r, err := Run(context.Background(), shards, limit, 40*time.Millisecond)
	elapsed := time.Since(start)
	if !errors.Is(err, shard.ErrNoResults) {
		t.Fatalf("err = %v, want ErrNoResults", err)
	}
	if r.Started() > limit {
		t.Fatalf("started after deadline = %d, want <= %d", r.Started(), limit)
	}
	if elapsed > 300*time.Millisecond {
		t.Fatalf("Wait took %v after deadline, want short window", elapsed)
	}
	for i, st := range r.States {
		if st.Status != shard.StatusTimeout {
			t.Fatalf("state[%d]=%s, want timeout", i, st.Status)
		}
	}
}
