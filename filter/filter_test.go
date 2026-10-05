// filter 包纯函数的表驱动测试。
package filter

import (
	"testing"

	"ontology/profile"
)

var prm = profile.Params{DB: 5, MinI: 100, MaxI: 1000, Lo: 0, Hi: 100}

func mkState(lastV int64, hasLast bool, lastAt int64, curTS, curV int64, hasCur bool, fault bool, deferAt int64) *profile.State {
	return &profile.State{
		Params:  prm,
		LastV:   lastV,
		HasLast: hasLast,
		LastAt:  lastAt,
		CurTS:   curTS,
		CurV:    curV,
		HasCur:  hasCur,
		Fault:   fault,
		Defer:   deferAt,
	}
}

func TestReportDue(t *testing.T) {
	cases := []struct {
		name string
		st   *profile.State
		want bool
	}{
		{"no_cur", mkState(0, false, 0, 0, 0, false, false, 0), false},
		{"first_sample", mkState(0, false, 0, 10, 50, true, false, 0), true},
		{"fault", mkState(0, false, 0, 10, 50, true, true, 0), false},
		{"cur_not_newer", mkState(50, true, 20, 20, 90, true, false, 0), false},
		{"diff_eq_db", mkState(50, true, 0, 30, 55, true, false, 0), false},
		{"diff_eq_db_neg", mkState(50, true, 0, 30, 45, true, false, 0), false},
		{"diff_gt_db", mkState(50, true, 0, 30, 56, true, false, 0), true},
	}
	for _, c := range cases {
		if got := ReportDue(c.st); got != c.want {
			t.Errorf("%s: ReportDue=%v, want %v", c.name, got, c.want)
		}
	}
}

func TestReportAt(t *testing.T) {
	cases := []struct {
		name string
		st   *profile.State
		want int64
	}{
		{"no_last", mkState(0, false, 0, 30, 50, true, false, 0), 30},
		{"no_last_defer", mkState(0, false, 0, 30, 50, true, false, 500), 500},
		{"mini_dominates", mkState(50, true, 0, 30, 90, true, false, 0), 100},
		{"cur_dominates", mkState(50, true, 0, 300, 90, true, false, 0), 300},
		{"defer_dominates", mkState(50, true, 0, 300, 90, true, false, 1000), 1000},
		{"mini_exact", mkState(50, true, 0, 100, 90, true, false, 0), 100},
	}
	for _, c := range cases {
		if got := ReportAt(c.st); got != c.want {
			t.Errorf("%s: ReportAt=%d, want %d", c.name, got, c.want)
		}
	}
}

func TestHeartbeat(t *testing.T) {
	if HeartbeatDue(mkState(0, false, 0, 0, 0, false, false, 0)) {
		t.Error("no lastAt: HeartbeatDue=true, want false")
	}
	if HeartbeatDue(mkState(50, true, 0, 0, 0, true, true, 0)) {
		t.Error("fault: HeartbeatDue=true, want false")
	}
	if got := HeartbeatAt(mkState(50, true, 100, 0, 0, true, false, 0)); got != 1100 {
		t.Errorf("HeartbeatAt=%d, want 1100", got)
	}
	if got := HeartbeatAt(mkState(50, true, 100, 0, 0, true, false, 5000)); got != 5000 {
		t.Errorf("HeartbeatAt with defer=%d, want 5000", got)
	}
}

func TestNext(t *testing.T) {
	// 待报与心跳同时刻：待报优先。
	at, k, ok := Next(mkState(50, true, 0, 1000, 90, true, false, 0))
	if !ok || at != 1000 || k != Report {
		t.Errorf("tie: got (%d,%v,%v), want (1000,Report,true)", at, k, ok)
	}
	// 心跳更早。
	at, k, ok = Next(mkState(50, true, 0, 5000, 90, true, false, 0))
	if !ok || at != 1000 || k != Heartbeat {
		t.Errorf("hb earlier: got (%d,%v,%v), want (1000,Heartbeat,true)", at, k, ok)
	}
	// 故障无事件。
	if _, _, ok = Next(mkState(50, true, 0, 10, 90, true, true, 0)); ok {
		t.Error("fault: Next ok=true, want false")
	}
	// 仅有心跳。
	at, k, ok = Next(mkState(50, true, 0, 0, 0, false, false, 0))
	if !ok || at != 1000 || k != Heartbeat {
		t.Errorf("hb only: got (%d,%v,%v), want (1000,Heartbeat,true)", at, k, ok)
	}
}

func TestReasonFor(t *testing.T) {
	s := mkState(0, false, 0, 30, 50, true, false, 0)
	if got := ReasonFor(s, Report, 30); got != profile.ReasonFirst {
		t.Errorf("first: got %v", got)
	}
	s = mkState(50, true, 0, 100, 90, true, false, 0)
	if got := ReasonFor(s, Report, 100); got != profile.ReasonChange {
		t.Errorf("change: got %v", got)
	}
	if got := ReasonFor(s, Report, 150); got != profile.ReasonTrailing {
		t.Errorf("trailing: got %v", got)
	}
	if got := ReasonFor(s, Heartbeat, 1000); got != profile.ReasonHeartbeat {
		t.Errorf("heartbeat: got %v", got)
	}
}
