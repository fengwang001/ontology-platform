package fanout

import (
	"context"
	"errors"
	"testing"
	"time"

	"ontology/shard"
)

func rec(n, score int64, id string) shard.Record {
	return shard.Record{ID: id, N: shard.I64(n), Score: shard.I64(score)}
}

func TestFailureClasses(t *testing.T) {
	cases := []struct {
		name   string
		shards []shard.Shard
		errIs  error
		status shard.Status
		accept bool
	}{
		{"ok", []shard.Shard{shard.NewFake("s", []shard.Record{rec(1, 1, "a")}, 1)}, nil, shard.StatusOK, true},
		{"corrupt", []shard.Shard{shard.NewFake("s", []shard.Record{rec(1, 1, "a")}, 1, shard.WithCorrupt())}, shard.ErrCorrupt, shard.StatusCorrupt, false},
		{"duplicate", []shard.Shard{shard.NewFake("s", []shard.Record{rec(1, 1, "a")}, 1, shard.WithRepeats(2))}, shard.ErrDuplicate, shard.StatusDuplicate, true},
		{"timeout", []shard.Shard{shard.NewFake("s", nil, 0, shard.WithHang())}, shard.ErrTimeout, shard.StatusTimeout, false},
		{"failed", []shard.Shard{shard.NewFake("s", nil, 0, shard.WithFail(errors.New("boom")))}, nil, shard.StatusFailed, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := Run(context.Background(), tc.shards, 4, 100*time.Millisecond)
			if len(r.States) != 1 {
				t.Fatalf("states = %d", len(r.States))
			}
			st := r.States[0]
			if st.Status != tc.status || st.Accepted() != tc.accept {
				t.Fatalf("status=%s accepted=%v, want %s/%v", st.Status, st.Accepted(), tc.status, tc.accept)
			}
			if tc.errIs != nil && !errors.Is(st.Err, tc.errIs) {
				t.Fatalf("err = %v, want %v", st.Err, tc.errIs)
			}
			if tc.name != "ok" && tc.name != "duplicate" {
				if !errors.Is(err, shard.ErrNoResults) {
					t.Fatalf("overall err = %v, want ErrNoResults", err)
				}
			}
		})
	}
}

func TestEdgeCases(t *testing.T) {
	cases := []struct {
		name    string
		shards  []shard.Shard
		wantErr error
	}{
		{"zero_shards", nil, shard.ErrNoResults},
		{"all_fail", []shard.Shard{
			shard.NewFake("", nil, 0, shard.WithHang()),
			shard.NewFake("b", nil, 0, shard.WithCorrupt()),
		}, shard.ErrNoResults},
		{"all_empty_ok", []shard.Shard{
			shard.NewFake("", nil, 0),
			shard.NewFake("b", nil, 0),
		}, nil},
		{"single_ok", []shard.Shard{
			shard.NewFake("only", []shard.Record{rec(3, 9, "x")}, 9),
		}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := Run(context.Background(), tc.shards, 4, 100*time.Millisecond)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if tc.name == "all_empty_ok" {
				if len(r.Missing) != 0 || len(r.Frames()) != 2 {
					t.Fatalf("empty results must stay ok: missing=%v frames=%d", r.Missing, len(r.Frames()))
				}
			}
		})
	}
}
