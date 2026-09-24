package fanout

import (
	"context"
	"errors"
	"testing"
	"time"

	"ontology/shard"
)

type errShard struct{ id string }

func (e errShard) ID() string     { return e.id }
func (e errShard) Bound() float64 { return 1 }
func (e errShard) Query(context.Context) (shard.Response, error) {
	return shard.Response{}, errors.New("boom")
}

func TestStatusClassification(t *testing.T) {
	cases := []struct {
		name  string
		shard shard.Shard
		want  Status
	}{
		{"ok", shard.NewFake("a", 1, []shard.Record{{ID: "x", Value: 1}}), StatusOK},
		{"timeout", shard.NewFake("b", 1, nil, shard.WithHang()), StatusTimeout},
		{"corrupt", shard.NewFake("c", 1, []shard.Record{{ID: "y", Value: 2}}, shard.WithCorrupt()), StatusCorrupt},
		{"error", errShard{"d"}, StatusError},
		{"empty-id-ok", shard.NewFake("", 1, nil), StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := New(2).Run(context.Background(), []shard.Shard{tc.shard}, 50*time.Millisecond)
			if len(got) != 1 || got[0].Status != tc.want {
				t.Fatalf("got %+v, want status %v", got, tc.want)
			}
		})
	}
}

func TestConcurrencyLimit(t *testing.T) {
	const total, limit = 200, 8
	shards := make([]shard.Shard, total)
	for i := range shards {
		shards[i] = shard.NewFake(string(rune('a'+i%26))+string(rune('A'+i/26)), 1,
			[]shard.Record{{ID: "r", Value: 1}}, shard.WithDelay(2*time.Millisecond))
	}
	f := New(limit)
	results := f.Run(context.Background(), shards, 10*time.Second)
	if f.Peak() > limit {
		t.Fatalf("peak %d exceeds limit %d", f.Peak(), limit)
	}
	if f.Started() != total {
		t.Fatalf("started %d, want %d", f.Started(), total)
	}
	for _, r := range results {
		if r.Status != StatusOK {
			t.Fatalf("shard %s status %v, want ok", r.ID, r.Status)
		}
	}
}

func TestDeadlineStopsNewRequests(t *testing.T) {
	const total, limit = 40, 4
	shards := make([]shard.Shard, total)
	for i := range shards {
		shards[i] = shard.NewFake(string(rune('a'+i)), 1, nil, shard.WithHang())
	}
	f := New(limit)
	deadline := 80 * time.Millisecond
	start := time.Now()
	results := f.Run(context.Background(), shards, deadline)
	elapsed := time.Since(start)
	if elapsed > deadline+500*time.Millisecond {
		t.Fatalf("Run took %v, want to return shortly after %v", elapsed, deadline)
	}
	if got := f.Started(); got > limit {
		t.Fatalf("started %d after deadline, want <= %d", got, limit)
	}
	counts := map[Status]int{}
	for _, r := range results {
		counts[r.Status]++
	}
	if counts[StatusTimeout] != limit || counts[StatusCancelled] != total-limit {
		t.Fatalf("status counts %v, want %d timeout + %d cancelled", counts, limit, total-limit)
	}
}

func TestDuplicateShardIDDeduped(t *testing.T) {
	s := shard.NewFake("dup", 1, []shard.Record{{ID: "x", Value: 3}})
	results := New(4).Run(context.Background(), []shard.Shard{s, s, s}, time.Second)
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1 (deduped)", len(results))
	}
	if results[0].Status != StatusOK || len(results[0].Resp.Records) != 1 {
		t.Fatalf("unexpected result %+v", results[0])
	}
}
