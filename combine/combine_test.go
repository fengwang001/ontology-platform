package combine

import (
	"math/rand"
	"reflect"
	"slices"
	"testing"

	"ontology/shard"
)

func resp(id string, recs ...shard.Record) shard.Response {
	return shard.Response{ShardID: id, Records: recs, Claimed: len(recs)}
}

func TestMerge(t *testing.T) {
	cases := []struct {
		name  string
		resps []shard.Response
		k     int
		want  Result
	}{
		{
			name: "five-aggregations",
			resps: []shard.Response{
				resp("s1", shard.Record{ID: "a", Value: 5}, shard.Record{ID: "b", Value: 3}),
				resp("s2", shard.Record{ID: "a", Value: 2}, shard.Record{ID: "c", Value: 9}),
			},
			k: 2,
			want: Result{
				Count: 4, Sum: 19, Min: 2, Max: 9,
				TopK: []TopEntry{{ID: "c", Score: 9}, {ID: "a", Score: 7}},
			},
		},
		{
			name:  "single-shard",
			resps: []shard.Response{resp("only", shard.Record{ID: "x", Value: 4})},
			k:     5,
			want: Result{
				Count: 1, Sum: 4, Min: 4, Max: 4,
				TopK: []TopEntry{{ID: "x", Score: 4}},
			},
		},
		{
			name:  "all-empty-is-not-failure",
			resps: []shard.Response{resp("s1"), resp("s2")},
			k:     3,
			want:  Result{Empty: true},
		},
		{
			name: "k-exceeds-total",
			resps: []shard.Response{
				resp("s1", shard.Record{ID: "a", Value: 1}, shard.Record{ID: "b", Value: 2}),
			},
			k: 100,
			want: Result{
				Count: 2, Sum: 3, Min: 1, Max: 2,
				TopK: []TopEntry{{ID: "b", Score: 2}, {ID: "a", Score: 1}},
			},
		},
		{
			name: "tie-break-by-id-asc",
			resps: []shard.Response{
				resp("s1", shard.Record{ID: "b", Value: 5}, shard.Record{ID: "a", Value: 5}),
			},
			k: 2,
			want: Result{
				Count: 2, Sum: 10, Min: 5, Max: 5,
				TopK: []TopEntry{{ID: "a", Score: 5}, {ID: "b", Score: 5}},
			},
		},
		{
			name: "partial-fields-zero-value-and-empty-id",
			resps: []shard.Response{
				resp("s1", shard.Record{ID: "", Value: 0}, shard.Record{ID: "a", Value: 0}),
			},
			k: 2,
			want: Result{
				Count: 2, Sum: 0, Min: 0, Max: 0,
				TopK: []TopEntry{{ID: "", Score: 0}, {ID: "a", Score: 0}},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Merge(tc.resps, tc.k); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestMergeDeterministicAcrossOrders(t *testing.T) {
	base := []shard.Response{
		resp("s1", shard.Record{ID: "a", Value: 5}, shard.Record{ID: "b", Value: 3}),
		resp("s2", shard.Record{ID: "b", Value: 4}, shard.Record{ID: "c", Value: 1}),
		resp("s3", shard.Record{ID: "a", Value: 2}, shard.Record{ID: "d", Value: 6}),
		resp("", shard.Record{ID: "e", Value: 2}),
	}
	want := Merge(base, 4)
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 20; trial++ {
		shuffled := slices.Clone(base)
		rng.Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})
		if got := Merge(shuffled, 4); !reflect.DeepEqual(got, want) {
			t.Fatalf("trial %d: got %+v, want %+v", trial, got, want)
		}
	}
}
