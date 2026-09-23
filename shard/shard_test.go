package shard

import (
	"context"
	"errors"
	"testing"
	"time"
)

func drain(ctx context.Context, s Shard, id string) []Event {
	var got []Event
	for ev := range s.Fetch(ctx, id) {
		got = append(got, ev)
	}
	return got
}

func TestFakeFaults(t *testing.T) {
	recs := []Record{{ID: "a", Count: 2, Value: 3}, {ID: "b", Count: 1, Value: 4}}
	cases := []struct {
		name    string
		fake    *Fake
		events  int
		wantErr error
		corrupt bool
	}{
		{"ok", NewFake(recs, 4), 1, nil, false},
		{"duplicate", (&Fake{Records: recs, Bound: 4, Duplicate: true}), 2, nil, false},
		{"corrupt", (&Fake{Records: recs, Bound: 4, Corrupt: true}), 1, nil, true},
		{"fail", (&Fake{Records: recs, Bound: 4, Fail: true}), 1, ErrFailed, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			got := drain(ctx, tc.fake, "s")
			if len(got) != tc.events {
				t.Fatalf("events = %d, want %d", len(got), tc.events)
			}
			if tc.wantErr != nil && !errors.Is(got[0].Err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", got[0].Err, tc.wantErr)
			}
			if tc.corrupt && got[0].Claimed == len(got[0].Records) {
				t.Fatal("corrupt batch not detected: claim matches records")
			}
		})
	}
}

func TestFakeTimeoutDeliversNothing(t *testing.T) {
	f := &Fake{Timeout: true}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	got := drain(ctx, f, "s")
	if len(got) != 0 {
		t.Fatalf("timeout shard delivered %d events", len(got))
	}
}

func TestStatusStrings(t *testing.T) {
	// Byte-by-byte fixed ordering guarantees the enum/report contract.
	want := []string{"none", "ok", "timeout", "canceled", "corrupt", "duplicate", "failed"}
	for i, w := range want {
		if got := Status(i).String(); got != w {
			t.Fatalf("Status(%d) = %q, want %q", i, got, w)
		}
	}
}
