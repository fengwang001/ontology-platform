package batcher

import (
	"testing"
	"time"

	"ontology/req"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time { return c.t }

func mk(n int) *req.Request { return req.New(make([]byte, n)) }

func TestTriggers(t *testing.T) {
	cases := []struct {
		name       string
		cfg        Config
		adds       []int // payload sizes; >MaxBytes means oversize
		advance    time.Duration
		wantReady  bool
		wantReason Reason
		wantSize   int
		wantBytes  int
	}{
		{"count", Config{MaxCount: 3, MaxBytes: 100, MaxWait: time.Second}, []int{1, 1, 1}, 0, true, ReasonCount, 3, 3},
		{"bytes", Config{MaxCount: 10, MaxBytes: 10, MaxWait: time.Second}, []int{4, 4, 4}, 0, true, ReasonBytes, 3, 12},
		{"wait", Config{MaxCount: 10, MaxBytes: 100, MaxWait: 5 * time.Millisecond}, []int{2, 2}, 6 * time.Millisecond, true, ReasonWait, 2, 4},
		{"wait-zero", Config{MaxCount: 10, MaxBytes: 100, MaxWait: 0}, []int{7}, 0, true, ReasonWait, 1, 7},
		{"oversize-alone", Config{MaxCount: 3, MaxBytes: 10, MaxWait: time.Second}, []int{50}, 0, true, ReasonOversize, 1, 50},
		{"not-ready", Config{MaxCount: 10, MaxBytes: 100, MaxWait: time.Second}, []int{1, 1}, 0, false, 0, 2, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clk := &fakeClock{}
			tc.cfg.Clock = clk
			b := New(tc.cfg)
			var ready bool
			var reason Reason
			for _, sz := range tc.adds {
				ready, reason = b.Add(mk(sz))
				if ready {
					break
				}
			}
			if !ready && tc.advance > 0 {
				clk.t = clk.t.Add(tc.advance)
			}
			if !ready && b.TimedOut() {
				ready, reason = true, ReasonWait
			}
			if !tc.wantReady && b.TimedOut() {
				ready, reason = true, ReasonWait
			}
			if ready != tc.wantReady || (ready && reason != tc.wantReason) {
				t.Fatalf("ready=%v reason=%v, want %v/%v", ready, reason, tc.wantReady, tc.wantReason)
			}
			if b.Pending() != tc.wantSize || b.bytes != tc.wantBytes {
				t.Fatalf("pending=%d bytes=%d, want %d/%d", b.Pending(), b.bytes, tc.wantSize, tc.wantBytes)
			}
			if got := len(b.Drain()); got != tc.wantSize {
				t.Fatalf("drained %d, want %d", got, tc.wantSize)
			}
		})
	}
}
