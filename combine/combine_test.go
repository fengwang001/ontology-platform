package combine

import (
	"errors"
	"reflect"
	"testing"

	"ontology/shard"
)

func rec(id string, value int64, score int64) shard.Record {
	return shard.Record{ID: id, Value: value, Has: true, Score: score, HasTop: true}
}

func ok(id string, records ...shard.Record) shard.Attempt {
	return shard.Attempt{ShardID: id, Status: shard.StatusOK,
		Response: shard.Response{Records: records, Declared: len(records), UpperBound: 3}}
}

func missing(id string, upper int64) shard.Attempt {
	return shard.Attempt{ShardID: id, Status: shard.StatusTimeout,
		Response: shard.Response{UpperBound: upper}}
}

func TestAggregate(t *testing.T) {
	records := []shard.Record{rec("a", 4, 40), rec("b", 2, 20), rec("c", 8, 10)}
	cases := []struct {
		name      string
		attempts  []shard.Attempt
		k         int
		want      Result
		wantErr   error
		wantDupOK bool
	}{
		{name: "zero", k: 2, wantErr: ErrNoShards},
		{name: "all failed", attempts: []shard.Attempt{missing("", 1)}, k: 2, wantErr: ErrAllShardsFailed},
		{name: "all empty", attempts: []shard.Attempt{ok(""), ok("b")}, k: 2,
			want: Result{AllOK: true, AnyOK: true, Success: []string{"", "b"}, TopK: []Item{}}},
		{name: "aggregate", attempts: []shard.Attempt{ok("a", records...), missing("z", 2)}, k: 2,
			want: Result{Count: 3, Sum: 14, Min: 2, Max: 8,
				TopK: []Item{{"a", 40}, {"b", 20}}, Success: []string{"a"},
				Missing: []string{"z"}, MissingUpper: 2, AnyOK: true}},
		{name: "k larger", attempts: []shard.Attempt{ok("a", records...)}, k: 9,
			want: Result{Count: 3, Sum: 14, Min: 2, Max: 8, AllOK: true, AnyOK: true,
				Success: []string{"a"}, TopK: []Item{{"a", 40}, {"b", 20}, {"c", 10}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Aggregate(tc.attempts, tc.k)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr != nil {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestDedupAndDeterminism(t *testing.T) {
	records := []shard.Record{rec("a", 2, 20), rec("b", 1, 10)}
	one := []shard.Attempt{ok("s", records...), ok("s", records...)}
	once, err := Aggregate(one[:1], 2)
	if err != nil {
		t.Fatal(err)
	}
	twice, err := Aggregate(one, 2)
	if err != nil {
		t.Fatal(err)
	}
	if once.Count != twice.Count || once.Sum != twice.Sum || !reflect.DeepEqual(once.TopK, twice.TopK) {
		t.Fatalf("duplicate changed result: %+v vs %+v", once, twice)
	}
	base := []shard.Attempt{ok("b", rec("tie", 1, 5)), ok("a", rec("a", 1, 5)), missing("c", 1)}
	var first Result
	for order := 0; order < 20; order++ {
		perm := append([]shard.Attempt(nil), base...)
		if order%2 == 1 {
			perm[0], perm[1] = perm[1], perm[0]
		}
		got, err := Aggregate(perm, 2)
		if err != nil {
			t.Fatal(err)
		}
		if order == 0 {
			first = got
			continue
		}
		if !reflect.DeepEqual(got, first) {
			t.Fatalf("order %d changed result", order)
		}
	}
}
