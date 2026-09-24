package report

import (
	"context"
	"errors"
	"math/rand"
	"reflect"
	"slices"
	"testing"
	"time"

	"ontology/confidence"
	"ontology/fanout"
	"ontology/shard"
)

func build(t *testing.T, shards []shard.Shard, k int) (Report, error) {
	t.Helper()
	results := fanout.New(8).Run(context.Background(), shards, 100*time.Millisecond)
	return Build(shards, results, k)
}

func recs(pairs ...any) []shard.Record {
	var out []shard.Record
	for i := 0; i < len(pairs); i += 2 {
		out = append(out, shard.Record{ID: pairs[i].(string), Value: pairs[i+1].(float64)})
	}
	return out
}

func TestBuildOutcomes(t *testing.T) {
	cases := []struct {
		name    string
		shards  []shard.Shard
		wantErr error
		exact   bool
		empty   bool
	}{
		{"zero-shards", nil, ErrNoShards, false, false},
		{"all-fail", []shard.Shard{
			shard.NewFake("h1", 1, nil, shard.WithHang()),
			shard.NewFake("h2", 1, nil, shard.WithHang()),
		}, ErrAllFailed, false, false},
		{"all-empty-not-failure", []shard.Shard{
			shard.NewFake("e1", 1, nil),
			shard.NewFake("e2", 1, nil),
		}, nil, true, true},
		{"all-ok-exact", []shard.Shard{
			shard.NewFake("a", 1, recs("x", 1.0)),
		}, nil, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rep, err := build(t, tc.shards, 3)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr != nil {
				return
			}
			if rep.Combined.Empty != tc.empty {
				t.Fatalf("empty=%v, want %v", rep.Combined.Empty, tc.empty)
			}
			alls := []confidence.Assessment{rep.Count, rep.Sum, rep.Min, rep.Max, rep.TopK}
			for i, a := range alls {
				if tc.exact && a.Kind != confidence.Exact {
					t.Fatalf("assessment %d kind %v, want exact", i, a.Kind)
				}
			}
		})
	}
}

func TestFaultInjection(t *testing.T) {
	good := shard.NewFake("good", 10, recs("a", 5.0, "b", 3.0))
	cases := []struct {
		name       string
		faulty     shard.Shard
		wantStatus fanout.Status
	}{
		{"timeout", shard.NewFake("t", 1, nil, shard.WithHang()), fanout.StatusTimeout},
		{"corrupt", shard.NewFake("c", 1, recs("z", 1.0), shard.WithCorrupt()), fanout.StatusCorrupt},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rep, err := build(t, []shard.Shard{good, tc.faulty}, 2)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(rep.Missing, tc.faulty.ID()) {
				t.Fatalf("missing %v lacks %s", rep.Missing, tc.faulty.ID())
			}
			var st fanout.Status
			for _, s := range rep.Statuses {
				if s.ID == tc.faulty.ID() {
					st = s.Status
				}
			}
			if st != tc.wantStatus {
				t.Fatalf("status %v, want %v", st, tc.wantStatus)
			}
			if rep.Count.Kind != confidence.LowerBound || rep.Combined.Count != 2 {
				t.Fatalf("bad data merged or wrong kind: %+v %+v", rep.Count, rep.Combined)
			}
		})
	}
}

func TestDuplicateReturnNotDoubleCounted(t *testing.T) {
	s := shard.NewFake("dup", 10, recs("a", 5.0, "b", 3.0))
	once, err := build(t, []shard.Shard{s}, 2)
	if err != nil {
		t.Fatal(err)
	}
	twice, err := build(t, []shard.Shard{s, s}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if twice.Combined.Count != once.Combined.Count || twice.Combined.Sum != once.Combined.Sum {
		t.Fatalf("double counted: once=%+v twice=%+v", once.Combined, twice.Combined)
	}
}

func TestTopKTrustedPrefix(t *testing.T) {
	cases := []struct {
		name         string
		missingBound float64
		wantPrefix   int
	}{
		{"small-bound-full-prefix", 5, 3},
		{"large-bound-zero-prefix", 1000, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			shards := []shard.Shard{
				shard.NewFake("s1", 100, recs("a", 60.0, "b", 50.0)),
				shard.NewFake("s2", 100, recs("a", 40.0, "c", 30.0)),
				shard.NewFake("gone", tc.missingBound, nil, shard.WithHang()),
			}
			rep, err := build(t, shards, 3)
			if err != nil {
				t.Fatal(err)
			}
			if rep.TopK.TrustedPrefix != tc.wantPrefix {
				t.Fatalf("prefix %d, want %d (%s)", rep.TopK.TrustedPrefix, tc.wantPrefix, rep.TopK.Note)
			}
		})
	}
}

func TestDeterministicAcrossArrivalOrders(t *testing.T) {
	base := []shard.Shard{
		shard.NewFake("s1", 10, recs("a", 5.0, "b", 3.0)),
		shard.NewFake("s2", 10, recs("b", 4.0, "c", 1.0)),
		shard.NewFake("s3", 10, recs("a", 2.0, "d", 6.0), shard.WithCorrupt()),
		shard.NewFake("", 10, recs("e", 2.0)),
	}
	want, err := build(t, base, 3)
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 20; trial++ {
		shuffled := slices.Clone(base)
		rng.Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})
		got, err := build(t, shuffled, 3)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got.Combined, want.Combined) || !reflect.DeepEqual(got.Missing, want.Missing) {
			t.Fatalf("trial %d differs:\ngot  %+v %v\nwant %+v %v",
				trial, got.Combined, got.Missing, want.Combined, want.Missing)
		}
	}
	if want.Combined.TopK[0].ID != "b" || want.Combined.TopK[0].Score != 7 {
		t.Fatalf("unexpected top: %+v", want.Combined.TopK)
	}
}
