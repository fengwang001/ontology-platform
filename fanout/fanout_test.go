package fanout

import (
	"context"
	"errors"
	"testing"
	"time"

	"ontology/shard"
)

type gateShard struct {
	id      string
	entered chan<- int
	release <-chan struct{}
}

func (s gateShard) ID() string { return s.id }
func (s gateShard) UpperBound() int64 { return 0 }
func (s gateShard) Query(ctx context.Context) ([]shard.Response, error) {
	s.entered <- 1
	select {
	case <-s.release:
		return []shard.Response{{}}, nil
	case <-ctx.Done():
		return nil, shard.ErrTimeout
	}
}

func TestFanout(t *testing.T) {
	t.Run("concurrency cap", func(t *testing.T) {
		const n, limit = 200, 8
		entered := make(chan int, n)
		release := make(chan struct{})
		shards := make([]shard.Shard, n)
		for i := range shards {
			shards[i] = gateShard{id: "s", entered: entered, release: release}
		}
		f, err := New(shards, limit)
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan []shard.Attempt, 1)
		go func() { done <- f.Run(context.Background()) }()
		active := 0
		peak := 0
		for i := 0; i < n; i++ {
			<-entered
			active++
			if active > peak {
				peak = active
			}
			if active >= limit {
				active--
				release <- struct{}{}
			}
		}
		for active > 0 {
			active--
			release <- struct{}{}
		}
		if got := f.peakInFlight(); got > limit || peak > limit {
			t.Fatalf("peak = %d/%d, want <= %d", got, peak, limit)
		}
		<-done
	})

	cases := []struct {
		name     string
		shards   func() []shard.Shard
		cancel   func(context.CancelFunc)
		launched int
		status   shard.Status
		wait     time.Duration
	}{
		{"zero", func() []shard.Shard { return nil }, func(context.CancelFunc) {}, 0, shard.StatusUnknown, 0},
		{"deadline no launch", func() []shard.Shard {
			return []shard.Shard{shard.NewFake("x", nil, 0).WithHang()}
		}, func(cancel context.CancelFunc) { cancel() }, 0, shard.StatusTimeout, 20 * time.Millisecond},
		{"corrupt", func() []shard.Shard {
			return []shard.Shard{shard.NewFake("x", []shard.Record{{ID: "a"}}, 0).WithCorrupt(1)}
		}, func(context.CancelFunc) {}, 1, shard.StatusCorrupt, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "zero" {
				if _, err := New(tc.shards(), 1); !errors.Is(err, ErrNoShards) {
					t.Fatalf("err = %v", err)
				}
				return
			}
			f, err := New(tc.shards(), 1)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			tc.cancel(cancel)
			defer cancel()
			start := time.Now()
			got := f.Run(ctx)
			if tc.wait > 0 && time.Since(start) > tc.wait {
				t.Fatalf("wait took %v", time.Since(start))
			}
			launched := 0
			for _, attempt := range got {
				if attempt.Launched {
					launched++
				}
				if attempt.Status != tc.status {
					t.Fatalf("status = %s, want %s", attempt.Status, tc.status)
				}
			}
			if launched != tc.launched {
				t.Fatalf("launched = %d, want %d", launched, tc.launched)
			}
		})
	}
}
