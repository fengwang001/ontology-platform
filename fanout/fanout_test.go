package fanout_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"ontology/fanout"
	"ontology/shard"
)

func rec(id string, score int64) shard.Record { return shard.Record{ID: id, Score: score, Valid: true} }

func TestShardFake(t *testing.T) {
	recs := []shard.Record{rec("a", 3), {ID: "", Valid: false}}
	cases := []struct {
		name    string
		cfg     shard.Config
		timeout time.Duration
		retry   bool
		wantN   int
		wantErr error
	}{
		{"ok", shard.Config{ID: "s1", Records: recs, Bound: 3}, 0, false, 2, nil},
		{"empty-id", shard.Config{ID: "", Records: recs[:1]}, 0, false, 1, nil},
		{"corrupt-count", shard.Config{ID: "s", Corrupt: shard.CorruptCount}, 0, false, 0, shard.ErrCorrupt},
		{"corrupt-notok", shard.Config{ID: "s", Records: recs[:1], Corrupt: shard.CorruptNotOK}, 0, false, 1, shard.ErrCorrupt},
		{"hang", shard.Config{ID: "s", Hang: true}, 10 * time.Millisecond, false, 0, context.DeadlineExceeded},
		{"delay", shard.Config{ID: "s", Delay: time.Second}, 10 * time.Millisecond, false, 0, context.DeadlineExceeded},
		{"replay", shard.Config{ID: "s", Records: recs[:1], Replay: true}, 0, true, 1, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := shard.NewFake(tc.cfg)
			ctx := context.Background()
			if tc.timeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, tc.timeout)
				defer cancel()
			}
			resp, err := f.Fetch(ctx, 1)
			if tc.retry {
				if !errors.Is(err, shard.ErrRetryable) {
					t.Fatalf("want ErrRetryable, got %v", err)
				}
				resp, err = f.Fetch(ctx, 1)
			}
			if tc.wantErr == shard.ErrCorrupt {
				err = shard.Validate(resp)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr == nil && (err != nil || len(resp.Records) != tc.wantN) {
				t.Fatalf("resp err=%v n=%d, want n=%d", err, len(resp.Records), tc.wantN)
			}
		})
	}
}

func TestFanoutStatuses(t *testing.T) {
	cases := []struct {
		name   string
		shards []shard.Shard
		want   map[fanout.Status]int
	}{
		{"all-ok", []shard.Shard{
			shard.NewFake(shard.Config{ID: "a", Records: []shard.Record{rec("1", 1)}, Bound: 1}),
			shard.NewFake(shard.Config{ID: "b", Records: []shard.Record{rec("2", 2)}, Bound: 2}),
		}, map[fanout.Status]int{fanout.StatusOK: 2}},
		{"timeout-partial", []shard.Shard{
			shard.NewFake(shard.Config{ID: "a", Records: []shard.Record{rec("1", 1)}, Bound: 1}),
			shard.NewFake(shard.Config{ID: "b", Hang: true}),
		}, map[fanout.Status]int{fanout.StatusOK: 1, fanout.StatusTimeout: 1}},
		{"corrupt", []shard.Shard{
			shard.NewFake(shard.Config{ID: "a", Corrupt: shard.CorruptCount}),
		}, map[fanout.Status]int{fanout.StatusCorrupt: 1}},
		{"duplicate", []shard.Shard{
			shard.NewFake(shard.Config{ID: "a", Records: []shard.Record{rec("1", 5)}, Replay: true}),
		}, map[fanout.Status]int{fanout.StatusDuplicate: 1}},
		{"all-failed", []shard.Shard{
			shard.NewFake(shard.Config{ID: "a", Hang: true}),
			shard.NewFake(shard.Config{ID: "b", Corrupt: shard.CorruptNotOK}),
		}, map[fanout.Status]int{fanout.StatusTimeout: 1, fanout.StatusCorrupt: 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, err := fanout.New(tc.shards, 4)
			if err != nil {
				t.Fatal(err)
			}
			o := f.Run(context.Background(), 100*time.Millisecond)
			got := map[fanout.Status]int{}
			for _, r := range o.Results {
				got[r.Status]++
			}
			if len(got) != len(tc.want) {
				t.Fatalf("statuses = %v, want %v", got, tc.want)
			}
			for st, n := range tc.want {
				if got[st] != n {
					t.Fatalf("status %s = %d, want %d", st, got[st], n)
				}
			}
		})
	}
}

func TestNoShards(t *testing.T) {
	if _, err := fanout.New(nil, 4); !errors.Is(err, fanout.ErrNoShards) {
		t.Fatalf("err = %v, want ErrNoShards", err)
	}
}

func TestPeakConcurrency(t *testing.T) {
	shards := make([]shard.Shard, 200)
	for i := range shards {
		shards[i] = shard.NewFake(shard.Config{ID: "s", Delay: 2 * time.Millisecond})
	}
	f, _ := fanout.New(shards, 8)
	f.Run(context.Background(), 5*time.Second)
	if p := f.PeakInFlight(); p != 8 {
		t.Fatalf("peak = %d, want 8", p)
	}
}

func TestDeadlineStopsDispatch(t *testing.T) {
	shards := make([]shard.Shard, 40)
	for i := range shards {
		shards[i] = shard.NewFake(shard.Config{ID: "s", Hang: true})
	}
	f, _ := fanout.New(shards, 2)
	start := time.Now()
	o := f.Run(context.Background(), 30*time.Millisecond)
	if f.StartedAfterDeadline() {
		t.Fatal("a fetch started after the deadline")
	}
	for _, r := range o.Results {
		if r.Status != fanout.StatusTimeout {
			t.Fatalf("status = %s, want timeout", r.Status)
		}
	}
	if d := time.Since(start); d > 300*time.Millisecond {
		t.Fatalf("Wait returned after %v, want quick return", d)
	}
}
