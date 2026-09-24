package shard

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestFakeBehaviors(t *testing.T) {
	records := []Record{{ID: "a", Value: 3, Has: true}, {ID: "b"}}
	cases := []struct {
		name    string
		build   func() *Fake
		wantN   int
		wantErr error
		wait    time.Duration
	}{
		{name: "normal", build: func() *Fake { return NewFake("s", records, 9) }, wantN: 1},
		{name: "delay", build: func() *Fake { return NewFake("s", records, 9).WithDelay(time.Millisecond) }, wantN: 1},
		{name: "corrupt", build: func() *Fake { return NewFake("s", records, 9).WithCorrupt(1) }, wantN: 1},
		{name: "duplicate", build: func() *Fake { return NewFake("s", records, 9).WithDuplicate() }, wantN: 2},
		{name: "timeout", build: func() *Fake { return NewFake("s", records, 9).WithHang() }, wantErr: ErrTimeout, wait: time.Millisecond},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			cancel := func() {}
			if tc.wait > 0 {
				ctx, cancel = context.WithTimeout(ctx, tc.wait)
			}
			defer cancel()
			got, err := tc.build().Query(ctx)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if len(got) != tc.wantN {
				t.Fatalf("responses = %d, want %d", len(got), tc.wantN)
			}
			for _, resp := range got {
				if tc.name == "corrupt" && resp.Declared == len(resp.Records) {
					t.Fatal("corrupt response was not injected")
				}
				if tc.name != "corrupt" && len(got) > 0 && resp.Declared != len(resp.Records) {
					t.Fatalf("declared = %d, records = %d", resp.Declared, len(resp.Records))
				}
			}
		})
	}
}

func TestStatusText(t *testing.T) {
	cases := []struct {
		status Status
		want   string
	}{
		{StatusOK, "ok"}, {StatusTimeout, "timeout"}, {StatusCorrupt, "corrupt"},
		{StatusFailed, "failed"}, {StatusUnknown, "unknown"},
	}
	for _, tc := range cases {
		if got := tc.status.String(); got != tc.want {
			t.Fatalf("%v = %q, want %q", tc.status, got, tc.want)
		}
	}
}
