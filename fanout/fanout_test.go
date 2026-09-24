package fanout

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"ontology/shard"
)

func fakeShards(n int, fill func(i int) shard.Fake) []shard.Shard {
	out := make([]shard.Shard, n)
	for i := range out {
		f := fill(i)
		out[i] = f
	}
	return out
}

func TestRun(t *testing.T) {
	cases := []struct {
		name      string
		n         int
		limit     int
		delay     time.Duration
		wantCount int
		wantPeak  int64 // 0 means "only check <= limit"
	}{
		{"zero shards", 0, 8, 0, 0, 0},
		{"single shard", 1, 8, 0, 1, 1},
		{"under limit", 4, 8, time.Millisecond, 4, 4},
		{"200 shards cap 8", 200, 8, 2 * time.Millisecond, 200, 8},
		{"limit 1 serializes", 20, 1, time.Millisecond, 20, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			shards := fakeShards(tc.n, func(i int) shard.Fake {
				return shard.Fake{ShardID: fmt.Sprintf("s%03d", i), Delay: tc.delay,
					Records: []shard.Record{{ID: "r", Value: 1}}}
			})
			fo := New(tc.limit, 10*time.Second)
			res := fo.Run(context.Background(), shards)
			if len(res) != tc.wantCount {
				t.Fatalf("got %d results, want %d", len(res), tc.wantCount)
			}
			for i, r := range res {
				if r.Err != nil {
					t.Fatalf("result %d unexpected err: %v", i, r.Err)
				}
				if tc.n > 0 && r.ShardID != fmt.Sprintf("s%03d", i) {
					t.Fatalf("result %d out of order: %s", i, r.ShardID)
				}
			}
			if peak := fo.Peak(); peak > int64(tc.limit) {
				t.Fatalf("peak %d exceeds limit %d", peak, tc.limit)
			} else if tc.wantPeak > 0 && peak != tc.wantPeak {
				t.Fatalf("peak %d, want %d", peak, tc.wantPeak)
			}
		})
	}
}

func TestDeadline(t *testing.T) {
	var started atomic.Int64
	shards := fakeShards(200, func(i int) shard.Fake {
		return shard.Fake{ShardID: fmt.Sprintf("h%03d", i), Bound: 1, Hang: true,
			OnQuery: func() { started.Add(1) }}
	})
	fo := New(8, 50*time.Millisecond)
	begin := time.Now()
	res := fo.Run(context.Background(), shards)
	elapsed := time.Since(begin)
	if got := started.Load(); got > 8 {
		t.Fatalf("launched %d queries after deadline, want <= 8", got)
	}
	if elapsed > 50*time.Millisecond+2*time.Second {
		t.Fatalf("Wait returned after %v, want a short window past deadline", elapsed)
	}
	if len(res) != 200 {
		t.Fatalf("got %d results, want 200", len(res))
	}
	for i, r := range res {
		if !errors.Is(r.Err, context.DeadlineExceeded) {
			t.Fatalf("result %d err = %v, want DeadlineExceeded", i, r.Err)
		}
		if r.Bound != 1 {
			t.Fatalf("result %d lost its declared bound", i)
		}
	}
}
