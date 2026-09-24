package fanout

import (
	"context"
	"errors"
	"testing"
	"time"

	"ontology/shard"
)

func mk(id string, n int) shard.Shard {
	recs := make([]shard.Record, n)
	for i := range recs {
		recs[i] = shard.Record{ID: id + "-r", Score: int64(i + 1)}
	}
	return shard.New(shard.Config{ShardID: id, Records: recs})
}

func TestRunTable(t *testing.T) {
	cases := []struct {
		name       string
		shards     []shard.Shard
		maxConc    int
		timeout    time.Duration
		wantErr    error
		wantOK     int
		wantMiss   int
		peakMax    int
		startedMax int
	}{
		{"no shards", nil, 4, 0, ErrNoShards, 0, 0, 0, 0},
		{"all success", []shard.Shard{mk("a", 2), mk("b", 3), mk("", 1)}, 4, time.Second,
			nil, 3, 0, 4, 3},
		{"one timeout partial", []shard.Shard{mk("a", 2),
			shard.New(shard.Config{ShardID: "z", Timeout: true})}, 4, 80 * time.Millisecond,
			nil, 1, 1, 4, 2},
		{"one corrupt partial", []shard.Shard{mk("a", 2),
			shard.New(shard.Config{ShardID: "c", Records: []shard.Record{{ID: "x"}}, Claimed: 5})},
			4, time.Second, nil, 1, 1, 4, 2},
		{"all failed", []shard.Shard{
			shard.New(shard.Config{ShardID: "a", Timeout: true}),
			shard.New(shard.Config{ShardID: "b", Timeout: true})}, 4, 60 * time.Millisecond,
			ErrAllShardsFailed, 0, 2, 4, 2},
		{"all empty but success", []shard.Shard{mk("a", 0), mk("b", 0)}, 4, time.Second,
			nil, 2, 0, 4, 2},
		{"duplicate dedup", []shard.Shard{
			shard.New(shard.Config{ShardID: "a", Records: []shard.Record{{ID: "r", Score: 7}},
				Duplicate: true})}, 1, time.Second, nil, 1, 0, 1, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Now()
			out, err := Run(context.Background(), tc.shards, tc.maxConc, tc.timeout)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if len(out.Success) != tc.wantOK || len(out.Missing) != tc.wantMiss {
				t.Fatalf("ok=%d miss=%d, want %d/%d", len(out.Success), len(out.Missing),
					tc.wantOK, tc.wantMiss)
			}
			if out.Peak > tc.peakMax {
				t.Fatalf("peak = %d > %d", out.Peak, tc.peakMax)
			}
			if out.Started > tc.startedMax {
				t.Fatalf("started = %d > %d", out.Started, tc.startedMax)
			}
			if tc.timeout > 0 && tc.wantErr != ErrNoShards &&
				time.Since(start) > tc.timeout+200*time.Millisecond {
				t.Fatalf("did not return near deadline")
			}
		})
	}
}

func TestPeakAndDeadline(t *testing.T) {
	shards := make([]shard.Shard, 200)
	for i := range shards {
		shards[i] = mk(idOf(i), 1)
	}
	out, err := Run(context.Background(), shards, 8, time.Second)
	if err != nil || out.Peak != 8 {
		t.Fatalf("err=%v peak=%d, want peak 8", err, out.Peak)
	}

	hanging := make([]shard.Shard, 200)
	for i := range hanging {
		hanging[i] = shard.New(shard.Config{ShardID: idOf(i), Timeout: true})
	}
	start := time.Now()
	out, err = Run(context.Background(), hanging, 8, 60*time.Millisecond)
	if !errors.Is(err, ErrAllShardsFailed) {
		t.Fatalf("err = %v, want ErrAllShardsFailed", err)
	}
	if out.Started != 8 {
		t.Fatalf("started after deadline = %d, want 8", out.Started)
	}
	if d := time.Since(start); d > 200*time.Millisecond {
		t.Fatalf("Wait took %v after deadline", d)
	}
	if out.Status[hanging[9].ID()] != shard.StatusCanceled {
		t.Fatalf("unlaunched shard status = %v, want canceled", out.Status[hanging[9].ID()])
	}
}

func idOf(i int) string {
	return string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + string(rune('a'+i/676))
}
