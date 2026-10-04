package delay

import (
	"testing"

	"ontology/live"
)

func TestCutoff(t *testing.T) {
	running := View{D: 3000}
	ended := View{D: 3000, Ended: true, TE: 10000}
	cases := []struct {
		name   string
		now    int64
		judge  bool
		v      View
		cutoff int64
	}{
		{"running equality", 4000, false, running, 1000},
		{"running negative cutoff allowed", 100, false, running, -2900},
		{"judge zero delay", 2000, true, running, 2000},
		{"judge after end", 11500, true, ended, 11500},
		{"ended pre-catchup", 10500, false, ended, 8000},
		{"ended one ms before", 11499, false, ended, 9998},
		{"ended catchup equality", 11500, false, ended, 10000},
		{"ended capped at tE", 20000, false, ended, 10000},
		{"ended at tE itself", 10000, false, ended, 7000},
		{"odd D boundary floor", 10001, false, View{D: 3001, Ended: true, TE: 10000}, 7001},
		{"odd D catchup exact ms", 11501, false, View{D: 3001, Ended: true, TE: 10000}, 10000},
		{"D zero running", 5000, false, View{D: 0}, 5000},
		{"D zero ended instant tE", 10000, false, View{D: 0, Ended: true, TE: 10000}, 10000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Cutoff(c.now, c.judge, c.v); got != c.cutoff {
				t.Fatalf("Cutoff = %d, want %d", got, c.cutoff)
			}
		})
	}
}

func TestVisibleHiddenGate(t *testing.T) {
	h := live.Event{Seq: 2, Now: 2000, Kind: live.Hidden}
	n := live.Event{Seq: 1, Now: 1000, Kind: live.Normal}
	end := live.Event{Seq: 5, Now: 10000, Kind: live.End}
	running := View{D: 3000}
	ended := View{D: 3000, Ended: true, TE: 10000}

	if Visible(h, 5500, false, running) {
		t.Fatal("hidden must not show while running even if t<=cutoff")
	}
	if Visible(h, 10500, false, ended) {
		t.Fatal("hidden hidden until cutoff==tE")
	}
	if !Visible(h, 11500, false, ended) {
		t.Fatal("hidden shown once caught up")
	}
	if !Visible(h, 2000, true, running) {
		t.Fatal("judge sees hidden immediately")
	}
	if !Visible(n, 4000, false, running) {
		t.Fatal("normal visible at equality t==cutoff")
	}
	if Visible(n, 3999, false, running) {
		t.Fatal("normal not visible t>cutoff")
	}
	if !Visible(end, 11500, false, ended) {
		t.Fatal("end visible when caught up")
	}
}

// TestOddDEveryMillisecond 遍历赛后追赶区间的每个整毫秒,
// 与朴素定义逐条对照(奇数 D 不做除法)。
func TestOddDEveryMillisecond(t *testing.T) {
	const D, tE int64 = 3001, 10000
	v := View{D: D, Ended: true, TE: tE}
	for now := tE; now <= tE+D+2; now++ {
		want := 2*now - tE - D
		if want > tE {
			want = tE
		}
		if got := Cutoff(now, false, v); got != want {
			t.Fatalf("now=%d got=%d want=%d", now, got, want)
		}
		caught := want == tE
		if (now >= 11501) != caught {
			t.Fatalf("now=%d caught mismatch", now)
		}
	}
}
