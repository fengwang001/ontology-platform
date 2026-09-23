package shard

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestFakeDeliveries(t *testing.T) {
	records := []Record{{ID: "a", N: I64(2), Score: I64(5)}}
	cases := []struct {
		name    string
		opts    []Option
		want    int // number of delivered frames (with ctx deadline for hang)
		wantErr error
	}{
		{"once", nil, 1, nil},
		{"duplicate", []Option{WithRepeats(2)}, 2, nil},
		{"corrupt", []Option{WithCorrupt()}, 1, nil},
		{"hang", []Option{WithHang()}, 0, ErrTimeout},
		{"delayed_cancel", []Option{WithDelay(time.Hour)}, 0, ErrTimeout},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := NewFake("s", records, 5, tc.opts...)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			got := 0
			err := f.Fetch(ctx, func(Frame) { got++ })
			if got != tc.want {
				t.Fatalf("deliveries = %d, want %d", got, tc.want)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestStatusStrings(t *testing.T) {
	cases := []struct {
		s    Status
		want string
	}{
		{StatusUnknown, "unknown"},
		{StatusOK, "ok"},
		{StatusDuplicate, "duplicate"},
		{StatusCorrupt, "corrupt"},
		{StatusTimeout, "timeout"},
		{StatusFailed, "failed"},
	}
	for _, tc := range cases {
		if got := tc.s.String(); got != tc.want {
			t.Fatalf("Status(%d) = %q, want %q", tc.s, got, tc.want)
		}
	}
}

func TestCorruptFrameContract(t *testing.T) {
	f := NewFake("s", []Record{{ID: "a"}}, 1, WithCorrupt())
	var fr Frame
	err := f.Fetch(context.Background(), func(x Frame) { fr = x })
	if err != nil {
		t.Fatalf("unexpected err %v", err)
	}
	if fr.Claimed == len(fr.Records) {
		t.Fatalf("corrupt frame must mismatch: claimed=%d actual=%d", fr.Claimed, len(fr.Records))
	}
}
