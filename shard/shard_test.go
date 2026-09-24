package shard

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestFakeQuery(t *testing.T) {
	recs := []Record{{ID: "a", Score: 3}, {ID: "b", Score: 5}}
	cases := []struct {
		name    string
		cfg     Config
		wantErr error
		wantDup bool
		wantN   int
	}{
		{"normal", Config{Records: recs}, nil, false, 2},
		{"empty id legal", Config{ShardID: "", Records: nil}, nil, false, 0},
		{"corrupt", Config{Records: recs, Claimed: 9}, ErrCorrupt, false, 2},
		{"timeout", Config{Timeout: true}, ErrTimeout, false, 0},
		{"duplicate", Config{Records: recs, Duplicate: true}, nil, true, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			r := New(tc.cfg).Query(ctx)
			if !errors.Is(r.Err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", r.Err, tc.wantErr)
			}
			if len(r.Records) != tc.wantN {
				t.Fatalf("records = %d, want %d", len(r.Records), tc.wantN)
			}
			gotDup := len(r.Extras()) == 1 && r.Extras()[0].Duplicate
			if gotDup != tc.wantDup {
				t.Fatalf("duplicate = %v, want %v", gotDup, tc.wantDup)
			}
			if tc.wantErr == nil && r.Claimed != tc.wantN {
				t.Fatalf("claimed = %d, want %d", r.Claimed, tc.wantN)
			}
		})
	}
}

func TestStatus(t *testing.T) {
	cases := []struct {
		s    Status
		want string
	}{
		{StatusUnknown, "unknown"}, {StatusOK, "ok"}, {StatusTimedOut, "timed_out"},
		{StatusCorrupt, "corrupt"}, {StatusCanceled, "canceled"},
	}
	for _, tc := range cases {
		if tc.s.String() != tc.want {
			t.Fatalf("%d = %q, want %q", tc.s, tc.s.String(), tc.want)
		}
	}
}
