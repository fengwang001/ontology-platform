package delay

import (
	"testing"

	"ontology/live"
)

func TestCutoffTable(t *testing.T) {
	const tE = int64(10000)
	cases := []struct {
		name         string
		now, d       int64
		ended, judge bool
		want         int64
	}{
		{"pre-end basic", 4000, 3000, false, false, 1000},
		{"pre-end equality", 5000, 3000, false, false, 2000},
		{"pre-end negative", 100, 3000, false, false, -2900},
		{"pre-end D=0", 7000, 0, false, false, 7000},
		{"post-end mid", 10500, 3000, true, false, 8000},
		{"post-end D=0 clamps", 10000, 0, true, false, tE},
		{"post-end D=0 later", 12345, 0, true, false, tE},
		{"post-end just before tE", 9999, 3000, true, false, 6998},
		{"judge pre-end", 4000, 3000, false, true, 4000},
		{"judge post-end", 10500, 3000, true, true, 10500},
		{"judge D=0", 42, 0, false, true, 42},
	}
	for _, c := range cases {
		if got := Cutoff(c.now, tE, c.d, c.ended, c.judge); got != c.want {
			t.Errorf("%s: Cutoff = %d, want %d", c.name, got, c.want)
		}
	}
}

// TestCatchUpEveryMillisecond 对赛后两倍速追赶逐毫秒验证：
// cutoff = min(tE, 2*now-tE-D)，追平（cutoff==tE）恰发生在 now = tE+ceil(D/2)，
// 且追平前一毫秒 cutoff 恰为 tE-1。
func TestCatchUpEveryMillisecond(t *testing.T) {
	const tE = int64(10000)
	for _, d := range []int64{0, 1, 2, 3000, 3001, 999_999_999, 1_000_000_000} {
		catchUp := tE + (d+1)/2 // tE + ceil(D/2)
		for now := tE; now <= catchUp+2; now++ {
			got := Cutoff(now, tE, d, true, false)
			want := 2*now - tE - d
			if want > tE {
				want = tE
			}
			if got != want {
				t.Fatalf("D=%d now=%d: cutoff = %d, want %d", d, now, got, want)
			}
		}
		if got := Cutoff(catchUp, tE, d, true, false); got != tE {
			t.Errorf("D=%d: catch-up at now=%d gives cutoff=%d, want tE", d, catchUp, got)
		}
		if d > 0 {
			// 两倍速步进：追平前一毫秒的 cutoff 为 tE-1（奇数 D）或 tE-2（偶数 D）。
			want := tE - 2 + d%2
			if got := Cutoff(catchUp-1, tE, d, true, false); got != want {
				t.Errorf("D=%d: one ms before catch-up cutoff=%d, want %d", d, got, want)
			}
		}
	}
}

func TestHiddenOK(t *testing.T) {
	const tE = int64(10000)
	cases := []struct {
		name         string
		cutoff       int64
		ended, judge bool
		want         bool
	}{
		{"judge anytime", 5, false, true, true},
		{"non-judge pre-end", tE, false, false, false},
		{"non-judge ended but not caught up", tE - 1, true, false, false},
		{"non-judge caught up", tE, true, false, true},
	}
	for _, c := range cases {
		if got := HiddenOK(c.cutoff, tE, c.ended, c.judge); got != c.want {
			t.Errorf("%s: HiddenOK = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestDeliverable(t *testing.T) {
	const tE = int64(10000)
	ev := func(t int64, k live.Kind) live.Event { return live.Event{Seq: 1, T: t, Kind: k} }
	cases := []struct {
		name         string
		ev           live.Event
		cutoff       int64
		ended, judge bool
		want         bool
	}{
		{"normal within cutoff", ev(1000, live.Normal), 1000, false, false, true},
		{"normal beyond cutoff", ev(1001, live.Normal), 1000, false, false, false},
		{"end at cutoff equality", ev(tE, live.End), tE, true, false, true},
		{"hidden pre-end non-judge", ev(100, live.Hidden), 5000, false, false, false},
		{"hidden ended not caught up", ev(100, live.Hidden), tE - 1, true, false, false},
		{"hidden ended caught up", ev(100, live.Hidden), tE, true, false, true},
		{"hidden beyond cutoff even caught up", ev(tE+1, live.Hidden), tE, true, false, false},
		{"hidden judge pre-end", ev(100, live.Hidden), 100, false, true, true},
		{"hidden judge beyond now", ev(101, live.Hidden), 100, false, true, false},
	}
	for _, c := range cases {
		if got := Deliverable(c.ev, c.cutoff, tE, c.ended, c.judge); got != c.want {
			t.Errorf("%s: Deliverable = %v, want %v", c.name, got, c.want)
		}
	}
}
