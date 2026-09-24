package combine

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"ontology/shard"
)

func okResult(id string, bound float64, recs ...shard.Record) shard.Result {
	return shard.Result{ShardID: id, Claimed: len(recs), Records: recs, Bound: bound}
}

func TestMerge(t *testing.T) {
	boom := errors.New("boom")
	cases := []struct {
		name    string
		results []shard.Result
		k       int
		wantErr error
		check   func(m Merged) error
	}{
		{"zero shards", nil, 3, ErrNoShards, nil},
		{"all failed", []shard.Result{
			{ShardID: "a", Err: boom}, {ShardID: "b", Err: boom}}, 3, ErrAllFailed,
			func(m Merged) error {
				if len(m.Failed) != 2 || m.Failed[0].Status != StatusError {
					return fmt.Errorf("bad failures: %+v", m.Failed)
				}
				return nil
			}},
		{"all empty is not all failed", []shard.Result{
			okResult("a", 1), okResult("b", 1)}, 3, nil,
			func(m Merged) error {
				if m.Count != 0 || m.HasMinMax || len(m.Succeeded) != 2 {
					return fmt.Errorf("got %+v", m)
				}
				return nil
			}},
		{"five aggregations", []shard.Result{
			okResult("a", 9, shard.Record{ID: "x", Value: 3}, shard.Record{ID: "y", Value: 5}),
			okResult("b", 9, shard.Record{ID: "z", Value: 4})}, 2, nil,
			func(m Merged) error {
				if m.Count != 3 || m.Sum != 12 || m.Min != 3 || m.Max != 5 {
					return fmt.Errorf("got %+v", m)
				}
				if len(m.TopK) != 2 || m.TopK[0].ID != "y" || m.TopK[1].ID != "z" {
					return fmt.Errorf("bad topk %+v", m.TopK)
				}
				return nil
			}},
		{"topk ties broken by id asc", []shard.Result{
			okResult("a", 9, shard.Record{ID: "b", Value: 1}, shard.Record{ID: "a", Value: 1})}, 2, nil,
			func(m Merged) error {
				if m.TopK[0].ID != "a" || m.TopK[1].ID != "b" {
					return fmt.Errorf("bad tie break %+v", m.TopK)
				}
				return nil
			}},
		{"k larger than total", []shard.Result{
			okResult("a", 9, shard.Record{ID: "x", Value: 1})}, 10, nil,
			func(m Merged) error {
				if len(m.TopK) != 1 {
					return fmt.Errorf("want 1 entry, got %+v", m.TopK)
				}
				return nil
			}},
		{"empty shard id is legal", []shard.Result{
			okResult("", 9, shard.Record{ID: "x", Value: 1})}, 1, nil,
			func(m Merged) error {
				if m.Count != 1 || m.Succeeded[0] != "" {
					return fmt.Errorf("got %+v", m)
				}
				return nil
			}},
		{"corrupt excluded", []shard.Result{
			{ShardID: "bad", Claimed: 5, Records: []shard.Record{{ID: "x", Value: 100}}, Bound: 7},
			okResult("ok", 9, shard.Record{ID: "y", Value: 2})}, 5, nil,
			func(m Merged) error {
				if m.Count != 1 || m.Sum != 2 {
					return fmt.Errorf("corrupt data merged: %+v", m)
				}
				if len(m.Failed) != 1 || m.Failed[0].Status != StatusCorrupt ||
					!errors.Is(m.Failed[0].Err, ErrCorrupt) {
					return fmt.Errorf("bad failure %+v", m.Failed)
				}
				if m.MissingBound != 7 {
					return fmt.Errorf("missing bound %v", m.MissingBound)
				}
				return nil
			}},
		{"timeout classified", []shard.Result{
			{ShardID: "slow", Err: context.DeadlineExceeded, Bound: 3},
			okResult("ok", 9, shard.Record{ID: "y", Value: 2})}, 5, nil,
			func(m Merged) error {
				if len(m.Failed) != 1 || m.Failed[0].Status != StatusTimeout {
					return fmt.Errorf("bad failure %+v", m.Failed)
				}
				return nil
			}},
		{"duplicate delivery counted once", []shard.Result{
			okResult("a", 9, shard.Record{ID: "x", Value: 3}),
			okResult("a", 9, shard.Record{ID: "x", Value: 3}),
			okResult("b", 9, shard.Record{ID: "y", Value: 4})}, 5, nil,
			func(m Merged) error {
				if m.Count != 2 || m.Sum != 7 {
					return fmt.Errorf("double counted: %+v", m)
				}
				return nil
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := Merge(tc.results, tc.k)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if tc.check != nil {
				if cerr := tc.check(m); cerr != nil {
					t.Fatal(cerr)
				}
			}
		})
	}
}

func TestDuplicateBitwiseIdentical(t *testing.T) {
	single := []shard.Result{
		okResult("a", 9, shard.Record{ID: "x", Value: 0.1}),
		okResult("b", 9, shard.Record{ID: "y", Value: 0.2}),
	}
	dup := append(append([]shard.Result{}, single...), single[0])
	m1, _ := Merge(single, 5)
	m2, _ := Merge(dup, 5)
	if m1.Count != m2.Count || m1.Sum != m2.Sum {
		t.Fatalf("duplicate changed result: %+v vs %+v", m1, m2)
	}
}

func TestDeterministicAcrossOrders(t *testing.T) {
	var base []shard.Result
	for i := 0; i < 6; i++ {
		id := fmt.Sprintf("d%d", i)
		base = append(base, okResult(id, 5,
			shard.Record{ID: id + "a", Value: float64(i) + 0.1},
			shard.Record{ID: id + "b", Value: float64(i) * 1.5}))
	}
	base[2].Err = context.DeadlineExceeded // one missing shard in the mix
	want, _ := Merge(base, 4)
	for p := 0; p < 20; p++ {
		perm := make([]shard.Result, len(base))
		for j := range base {
			dst := (j + p) % len(base)
			if p%2 == 1 {
				dst = len(base) - 1 - dst
			}
			perm[dst] = base[j]
		}
		got, _ := Merge(perm, 4)
		if fmt.Sprintf("%#v", got) != fmt.Sprintf("%#v", want) {
			t.Fatalf("order %d differs:\n%#v\n%#v", p, got, want)
		}
	}
}
