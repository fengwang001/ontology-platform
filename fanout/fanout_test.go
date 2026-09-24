package fanout_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync/atomic"
	"testing"
	"time"

	"ontology/combine"
	"ontology/fanout"
	"ontology/report"
	"ontology/shard"
)

func recs(base string, n int) []shard.Record {
	rs := make([]shard.Record, n)
	for i := range rs {
		rs[i] = shard.Record{ID: fmt.Sprintf("%s%d", base, i), V: float64(i + 1)}
	}
	return rs
}

func run(cfgs []shard.Config, cap int, d time.Duration) (*fanout.Result, error) {
	ss := make([]shard.Shard, len(cfgs))
	for i := range cfgs {
		ss[i] = shard.New(cfgs[i])
	}
	return fanout.Run(context.Background(), ss, cap, d)
}

func TestStatuses(t *testing.T) {
	cases := []struct {
		name   string
		cfgs   []shard.Config
		err    error
		status map[string]fanout.Status
	}{
		{"timeout", []shard.Config{{ShardID: "a", Records: recs("a", 2)}, {ShardID: "b", Hang: true}},
			nil, map[string]fanout.Status{"a": fanout.StatusOK, "b": fanout.StatusTimeout}},
		{"corrupt", []shard.Config{{ShardID: "a", Records: recs("a", 2)}, {ShardID: "b", Records: recs("b", 2), Corrupt: true}},
			nil, map[string]fanout.Status{"a": fanout.StatusOK, "b": fanout.StatusCorrupt}},
		{"duplicate", []shard.Config{{ShardID: "a", Records: recs("a", 2), Duplicate: true}},
			nil, map[string]fanout.Status{"a": fanout.StatusOK}},
		{"error", []shard.Config{{ShardID: "a", Records: recs("a", 1)}, {ShardID: "b", Fail: true}},
			nil, map[string]fanout.Status{"a": fanout.StatusOK, "b": fanout.StatusError}},
		{"all-failed", []shard.Config{{ShardID: "a", Hang: true}, {ShardID: "b", Fail: true}},
			fanout.ErrAllFailed, map[string]fanout.Status{"a": fanout.StatusTimeout, "b": fanout.StatusError}},
		{"empty-success", []shard.Config{{ShardID: "a"}, {ShardID: "b"}},
			nil, map[string]fanout.Status{"a": fanout.StatusOK, "b": fanout.StatusOK}},
		{"empty-id", []shard.Config{{ShardID: "", Records: recs("e", 1)}},
			nil, map[string]fanout.Status{"": fanout.StatusOK}},
		{"partial-fields", []shard.Config{{ShardID: "a", Records: []shard.Record{{ID: "x"}, {V: 3}}}},
			nil, map[string]fanout.Status{"a": fanout.StatusOK}},
	}
	for _, tc := range cases {
		res, err := run(tc.cfgs, 4, 150*time.Millisecond)
		if !errors.Is(err, tc.err) {
			t.Errorf("%s: err=%v want %v", tc.name, err, tc.err)
		}
		if res == nil {
			continue
		}
		for _, o := range res.Outcomes {
			if want := tc.status[o.ShardID]; o.Status != want {
				t.Errorf("%s: shard %q status=%v want %v", tc.name, o.ShardID, o.Status, want)
			}
		}
	}
	if _, err := run(nil, 4, time.Second); !errors.Is(err, fanout.ErrNoShards) {
		t.Errorf("zero shards: err=%v want ErrNoShards", err)
	}
}

func TestDuplicateNotCounted(t *testing.T) {
	dup, _ := run([]shard.Config{{ShardID: "a", Records: recs("a", 3), Duplicate: true}}, 4, time.Second)
	once, _ := run([]shard.Config{{ShardID: "a", Records: recs("a", 3)}}, 4, time.Second)
	d, o := combine.All(dup, 5), combine.All(once, 5)
	if d.Count != o.Count || d.Sum != o.Sum {
		t.Errorf("dup Count=%d Sum=%v want Count=%d Sum=%v", d.Count, d.Sum, o.Count, o.Sum)
	}
}

func TestConcurrencyCap(t *testing.T) {
	cfgs := make([]shard.Config, 200)
	for i := range cfgs {
		cfgs[i] = shard.Config{ShardID: fmt.Sprintf("s%03d", i), Records: recs("x", 1)}
	}
	res, err := run(cfgs, 8, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if p := res.PeakInFlight(); p > 8 || p == 0 {
		t.Errorf("peak=%d want in (0,8]", p)
	}
}

type counting struct {
	shard.Shard
	started *atomic.Int32
}

func (c counting) Query(ctx context.Context, out chan<- []shard.Record) error {
	c.started.Add(1)
	return c.Shard.Query(ctx, out)
}

func TestDeadlineStopsNewRequests(t *testing.T) {
	var started atomic.Int32
	ss := make([]shard.Shard, 20)
	for i := range ss {
		ss[i] = counting{shard.New(shard.Config{ShardID: fmt.Sprint(i), Hang: true}), &started}
	}
	begin := time.Now()
	deadline := 60 * time.Millisecond
	_, err := fanout.Run(context.Background(), ss, 3, deadline)
	if !errors.Is(err, fanout.ErrAllFailed) {
		t.Fatalf("err=%v want ErrAllFailed", err)
	}
	if elapsed := time.Since(begin); elapsed > deadline+250*time.Millisecond {
		t.Errorf("Run returned after %v, want within deadline window", elapsed)
	}
	if got := started.Load(); got != 3 {
		t.Errorf("started=%d want exactly cap=3 (no new requests after deadline)", got)
	}
}

func TestDeterministicAcrossOrders(t *testing.T) {
	base := []shard.Config{
		{ShardID: "a", Records: recs("a", 3)},
		{ShardID: "b", Records: recs("b", 3)},
		{ShardID: "c", Records: recs("c", 3)},
		{ShardID: "d", Hang: true},
	}
	var want string
	for i := 0; i < 20; i++ {
		perm := rand.New(rand.NewSource(int64(i))).Perm(len(base))
		cfgs := make([]shard.Config, len(base))
		for j, p := range perm {
			cfgs[j] = base[p]
		}
		res, err := run(cfgs, 2, 200*time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		got := report.Build(res, 3).String()
		if i == 0 {
			want = got
		} else if got != want {
			t.Fatalf("order %d: report differs\n%s\n---\n%s", i, got, want)
		}
	}
}
