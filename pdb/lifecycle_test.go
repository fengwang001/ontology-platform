package pdb

import "testing"

// TestUnreadyGate verifies an unready pod passes without consuming allowance
// iff current ready >= required.
func TestUnreadyGate(t *testing.T) {
	lg := newOpLogger(t)
	s := New(10 * 1e9)
	must(t, s.UpsertBudget(BudgetSpec{Name: "b", Namespace: "ns",
		Selector: Selector{"app": "x"}, MinAvailable: minAv(1)}, at(0)), "budget")
	must(t, s.UpsertPod(mkPod("ns", "r1", true, PhaseRunning, map[string]string{"app": "x"}), at(1)), "r1")
	must(t, s.UpsertPod(mkPod("ns", "r2", true, PhaseRunning, map[string]string{"app": "x"}), at(100)), "r2")
	must(t, s.UpsertPod(mkPod("ns", "u1", false, PhasePending, map[string]string{"app": "x"}), at(102)), "u1")

	must(t, s.Evict(PodRef{"ns", "u1"}, at(103)), "unready at ready=2 req=1")
	lg.log("evict unready u1 ready=2>=1 => ALLOW")
	st, _ := s.Budget("ns", "b", at(104))
	if st.CurrentReady != 2 || st.DisruptionAllowed != 1 {
		t.Fatalf("unready eviction consumes nothing, got %+v", st)
	}
	must(t, s.Cancel(PodRef{"ns", "u1"}, at(105)), "cancel")

	// One ready eviction drops ready from 2 to 1; unready still passes (1>=1).
	must(t, s.Evict(PodRef{"ns", "r1"}, at(106)), "evict r1")
	err := s.Evict(PodRef{"ns", "u1"}, at(108))
	lg.log("evict unready u1 ready=1>=1 => %s", errStr(err))
	must(t, err, "unready at ready=1 req=1 passes")
	must(t, s.Cancel(PodRef{"ns", "u1"}, at(109)), "cancel u1")
	// The second ready pod goes not-ready via an update: ready=0.
	must(t, s.UpsertPod(mkPod("ns", "r2", false, PhaseRunning, map[string]string{"app": "x"}), at(110)), "r2 unready")
	err = s.Evict(PodRef{"ns", "u1"}, at(111))
	lg.log("evict unready u1 ready=0<1 => %s", errStr(err))
	wantReason(t, err, ReasonInsufficient)
	// r1 expires at 116 restoring one ready; unready passes again.
	must(t, s.Evict(PodRef{"ns", "u1"}, at(117)), "unready after r1 expiry")
	lg.log("after r1 eviction expires at 116, evict u1 at t=117 => ALLOW")
}

// TestTerminalPhase excludes Succeeded/Failed pods from stats and eviction.
func TestTerminalPhase(t *testing.T) {
	lg := newOpLogger(t)
	s := New(60 * 1e9)
	must(t, s.UpsertBudget(BudgetSpec{Name: "b", Namespace: "ns",
		Selector: Selector{"app": "x"}, MinAvailable: minAv(1)}, at(0)), "budget")
	must(t, s.UpsertPod(mkPod("ns", "ok", true, PhaseRunning, map[string]string{"app": "x"}), at(1)), "ok")
	must(t, s.UpsertPod(mkPod("ns", "done", true, PhaseSucceeded, map[string]string{"app": "x"}), at(2)), "done")
	st, _ := s.Budget("ns", "b", at(3))
	lg.log("status with terminal pod: expected=%d ready=%d", st.Expected, st.CurrentReady)
	if st.Expected != 1 || st.CurrentReady != 1 {
		t.Fatalf("terminal pods excluded: %+v", st)
	}
	err := s.Evict(PodRef{"ns", "done"}, at(4))
	lg.log("evict Succeeded => %s", errStr(err))
	wantReason(t, err, ReasonNotEvictablePhase)

	// Transitioning a running pod to terminal removes it from stats.
	must(t, s.UpsertPod(mkPod("ns", "ok", false, PhaseFailed, map[string]string{"app": "x"}), at(5)), "ok fails")
	st2, _ := s.Budget("ns", "b", at(6))
	if st2.Expected != 0 {
		t.Fatalf("failed pod leaves stats: %+v", st2)
	}
	lg.log("after ok -> Failed: expected=%d", st2.Expected)
}

// TestLifecycleBoundary checks in-flight rejection, left-closed expiry,
// cancel restore and confirm removal.
func TestLifecycleBoundary(t *testing.T) {
	lg := newOpLogger(t)
	s := New(10 * 1e9)
	must(t, s.UpsertBudget(BudgetSpec{Name: "b", Namespace: "ns",
		Selector: Selector{"app": "x"}, MinAvailable: minAv(0)}, at(0)), "budget")
	must(t, s.UpsertPod(mkPod("ns", "p", true, PhaseRunning, map[string]string{"app": "x"}), at(1)), "pod")

	must(t, s.Evict(PodRef{"ns", "p"}, at(2)), "evict")
	lg.log("evict p at t=2 deadline=12 => ALLOW, ready=false")
	wantReason(t, s.Evict(PodRef{"ns", "p"}, at(3)), ReasonAlreadyEvicting)
	lg.log("re-evict at t=3 => AlreadyEvicting")
	wantReason(t, s.UpsertPod(mkPod("ns", "p", true, PhaseRunning, map[string]string{"app": "x"}), at(3)),
		ReasonAlreadyEvicting)
	lg.log("redundant readiness upsert while evicting => AlreadyEvicting")

	st11, _ := s.Budget("ns", "b", at(11))
	ps, _ := s.InspectPod(PodRef{"ns", "p"})
	lg.log("t=11: evicting=%v ready=%d", ps.Evicting, st11.CurrentReady)
	if !ps.Evicting || st11.CurrentReady != 0 {
		t.Fatal("still in flight before deadline")
	}
	st12, _ := s.Budget("ns", "b", at(12))
	ps2, _ := s.InspectPod(PodRef{"ns", "p"})
	lg.log("t=12 == deadline: evicting=%v ready=%d", ps2.Evicting, st12.CurrentReady)
	if ps2.Evicting || st12.CurrentReady != 1 {
		t.Fatal("deadline instant must be expired and restore readiness")
	}

	must(t, s.Evict(PodRef{"ns", "p"}, at(20)), "evict2")
	must(t, s.Cancel(PodRef{"ns", "p"}, at(21)), "cancel")
	ps3, _ := s.InspectPod(PodRef{"ns", "p"})
	lg.log("cancel t=21: evicting=%v ready=%v", ps3.Evicting, ps3.Pod.Ready)
	if ps3.Evicting || !ps3.Pod.Ready {
		t.Fatal("cancel restores readiness")
	}

	must(t, s.Evict(PodRef{"ns", "p"}, at(30)), "evict3")
	must(t, s.Confirm(PodRef{"ns", "p"}, at(31)), "confirm")
	if _, ok := s.InspectPod(PodRef{"ns", "p"}); ok {
		t.Fatal("confirm removes pod")
	}
	lg.log("confirm t=31 removes pod")
}

// TestClockRollback verifies monotonic injected time and that rollback changes
// no state.
func TestClockRollback(t *testing.T) {
	lg := newOpLogger(t)
	s := New(60 * 1e9)
	must(t, s.UpsertPod(mkPod("ns", "p", true, PhaseRunning, nil), at(10)), "pod")
	err := s.Evict(PodRef{"ns", "p"}, at(9))
	lg.log("evict at t=9 after state at t=10 => %s", errStr(err))
	wantReason(t, err, ReasonClockRollback)
	if ps, _ := s.InspectPod(PodRef{"ns", "p"}); ps.Evicting {
		t.Fatal("rollback must not change state")
	}
	// Equal time is allowed.
	must(t, s.Evict(PodRef{"ns", "p"}, at(10)), "same instant allowed")
	lg.log("evict at t=10 == last accepted time => ALLOW")
}
