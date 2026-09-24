package api_test

import (
	"errors"
	"math"
	"testing"

	"ontology/api"
)

func TestNewRejectsBadWindow(t *testing.T) {
	cases := []int64{0, -1, -2, -100}
	for _, w := range cases {
		d, err := api.New(w)
		if !errors.Is(err, api.ErrInvalidWindow) || d != nil {
			t.Fatalf("New(%d) = %v, %v; want ErrInvalidWindow, nil", w, d, err)
		}
	}
	if d, err := api.New(1); err != nil || d == nil {
		t.Fatalf("New(1) = %v, %v; want detector, nil", d, err)
	}
}

func TestFeedErrors(t *testing.T) {
	cases := []struct {
		name string
		w    int64
		seq  int64
		want error
	}{
		{"zero", 2, 0, api.ErrInvalidSeq},
		{"negative", 2, -7, api.ErrInvalidSeq},
		{"min", 2, math.MinInt64, api.ErrInvalidSeq},
		{"overflow-max", 2, math.MaxInt64, api.ErrSeqOverflow},
		{"overflow-max-1", 5, math.MaxInt64 - 1, api.ErrSeqOverflow},
		{"overflow-near", 5, math.MaxInt64 - 4, api.ErrSeqOverflow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, err := api.New(tc.w)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if err := d.Feed(tc.seq); !errors.Is(err, tc.want) {
				t.Fatalf("Feed(%d) = %v, want %v", tc.seq, err, tc.want)
			}
		})
	}
	// The three failure sentinels must be pairwise distinct.
	sentinels := []error{api.ErrInvalidWindow, api.ErrInvalidSeq, api.ErrSeqOverflow}
	for i := range sentinels {
		for j := i + 1; j < len(sentinels); j++ {
			if errors.Is(sentinels[i], sentinels[j]) {
				t.Fatalf("sentinels %v and %v are not distinguishable", sentinels[i], sentinels[j])
			}
		}
	}
}

// TestRejectionNoTrace pins invariant 4 at the public layer: every rejected
// Feed leaves state untouched and the detector stays usable.
func TestRejectionNoTrace(t *testing.T) {
	d, err := api.New(2)
	if err != nil {
		t.Fatal(err)
	}
	setup := []int64{1, 2, 3, 4, 6, 9}
	for _, s := range setup {
		if err := d.Feed(s); err != nil {
			t.Fatalf("setup Feed(%d): %v", s, err)
		}
	}
	bad := []int64{0, -1, math.MinInt64, math.MaxInt64, math.MaxInt64 - 1}
	for _, s := range bad {
		h0, g0 := d.Watermark(), d.Gaps()
		if err := d.Feed(s); err == nil {
			t.Fatalf("Feed(%d) unexpectedly accepted", s)
		}
		if d.Watermark() != h0 || len(d.Gaps()) != len(g0) {
			t.Fatalf("rejected Feed(%d) changed state: H %d->%d gaps %v->%v",
				s, h0, d.Watermark(), g0, d.Gaps())
		}
	}
	// Still fully usable afterward: the table converges to H=11, gaps 5,7,8.
	for _, s := range []int64{2, 3, 6, 10, 11} {
		if err := d.Feed(s); err != nil {
			t.Fatalf("post-reject Feed(%d): %v", s, err)
		}
	}
	if d.Watermark() != 11 {
		t.Fatalf("H=%d, want 11", d.Watermark())
	}
	want := []int64{5, 7, 8}
	if got := d.Gaps(); len(got) != len(want) {
		t.Fatalf("gaps=%v, want %v", got, want)
	} else {
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("gaps=%v, want %v", got, want)
			}
		}
	}
}

func TestSelfCheck(t *testing.T) {
	for _, w := range []int64{1, 2, 5} {
		d, err := api.New(w)
		if err != nil {
			t.Fatal(err)
		}
		if err := d.SelfCheck(); err != nil {
			t.Fatalf("SelfCheck: %v", err)
		}
	}
}
