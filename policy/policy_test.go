package policy

import "testing"

func fixedHash(v uint32) Hash { return func(string) uint32 { return v } }

func TestDecideOrder(t *testing.T) {
	cases := []struct {
		name       string
		err        bool
		maxDur     int64
		hash       uint32
		l, p       int64
		wantKeep   bool
		wantReason string
		why        string
	}{
		{"error wins", true, 1, 9999, 100, 0, true, ReasonError, "error flag first in order"},
		{"latency exactly L", false, 100, 9999, 100, 0, true, ReasonLatency, "maxDur >= L"},
		{"latency below L prob in", false, 99, 100, 100, 5000, true, ReasonProb, "100 < 5000"},
		{"prob boundary equal P", false, 0, 5000, 100, 5000, false, ReasonSampledOut, "5000 not < 5000"},
		{"P=10000 always in", false, 0, 9999, 100, 10000, true, ReasonProb, "9999 < 10000"},
		{"P=0 always out", false, 0, 0, 100, 0, false, ReasonSampledOut, "0 not < 0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := New(tc.l, tc.p, fixedHash(tc.hash))
			gotKeep, gotReason := p.Decide("tid", tc.err, tc.maxDur)
			t.Logf("input=(err=%v,maxDur=%d,h=%d,L=%d,P=%d) output=(%v,%s) why=%s",
				tc.err, tc.maxDur, tc.hash, tc.l, tc.p, gotKeep, gotReason, tc.why)
			if gotKeep != tc.wantKeep || gotReason != tc.wantReason {
				t.Fatalf("got (%v,%s) want (%v,%s)", gotKeep, gotReason, tc.wantKeep, tc.wantReason)
			}
		})
	}
}

func TestModBucket(t *testing.T) {
	// 10001 mod 10000 = 1, so P=2 accepts it.
	p := New(1<<62, 2, fixedHash(10001))
	keep, reason := p.Decide("x", false, 0)
	t.Logf("input=hash10001,P=2 output=(%v,%s) why=10001%%10000=1<2", keep, reason)
	if !keep || reason != ReasonProb {
		t.Fatalf("got (%v,%s)", keep, reason)
	}
}
