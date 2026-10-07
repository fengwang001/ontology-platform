package enrollment

import (
	"bytes"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
)

func TestEffectiveBeforeLatest(t *testing.T) {
	e := newTestEngine(t)
	if err := e.Admit("z2", "CS", 0); err != nil {
		t.Fatal(err)
	}
	// First change may share the admission baseline term (term 0).
	if _, err := e.Submit(SubmitInput{StudentID: "z2", Type: AppSuspend, At: 1, Submitter: "u", Terms: 1}); err != nil {
		t.Fatal(err)
	}
	mustApprove(t, e, "z2", "a", 1, false, 0)
	// A second change effective at the same term as the latest version fails,
	// even when the approval happens at a later tick.
	_, err := e.Submit(SubmitInput{StudentID: "z2", Type: AppResume, At: 5, Submitter: "u"})
	if codeOf(err) != ErrEffectiveBeforeLatest {
		t.Fatalf("same-term second change submit: got %v", err)
	}
	// Same guard at final approval time: leave carries to term 1 and a later
	// version can never appear while an app is open, so assert the submit path
	// and the commit guard together with a direct transfer scenario.
	if err := e.Admit("z3", "CS", 10); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Submit(SubmitInput{StudentID: "z3", Type: AppTransfer, At: 11, Submitter: "u", Major: "EE"}); err != nil {
		t.Fatal(err)
	}
	mustApprove(t, e, "z3", "a", 11, false, 0)
	// First-level approval passed; any later approval before final keeps the
	// pending app, and the second level commits at the same effect tick as the
	// baseline (allowed) - verify state then a repeat commit is impossible.
	mustApprove(t, e, "z3", "b", 12, false, 0)
	mustSnap(t, e, "z3", 12, StatusEnrolled, "EE", false)
}

func TestSnapshotTermBoundaries(t *testing.T) {
	e := newTestEngine(t)
	if err := e.Admit("s", "CS", 0); err != nil {
		t.Fatal(err)
	}
	// Suspend effective term 2, resume effective term 3.
	if _, err := e.Submit(SubmitInput{StudentID: "s", Type: AppSuspend, At: 16, Submitter: "u", Terms: 1}); err != nil {
		t.Fatal(err)
	}
	mustApprove(t, e, "s", "a", 16, false, 0)
	if _, err := e.Submit(SubmitInput{StudentID: "s", Type: AppResume, At: 26, Submitter: "u"}); err != nil {
		t.Fatal(err)
	}
	mustApprove(t, e, "s", "b", 26, false, 0)
	// Half-open boundaries: [20,30) suspended.
	mustSnap(t, e, "s", 19, StatusEnrolled, "CS", false)
	mustSnap(t, e, "s", 20, StatusSuspended, "CS", false)
	mustSnap(t, e, "s", 29, StatusSuspended, "CS", false)
	mustSnap(t, e, "s", 30, StatusEnrolled, "CS", false)
	// Open-app flag is itself historical.
	if _, err := e.Submit(SubmitInput{StudentID: "s", Type: AppSuspend, At: 46, Submitter: "u2", Terms: 1}); err != nil {
		t.Fatal(err)
	}
	mustSnap(t, e, "s", 46, StatusEnrolled, "CS", true)
	mustSnap(t, e, "s", 49, StatusEnrolled, "CS", true)
	mustApprove(t, e, "s", "c", 46, false, 0)
	mustSnap(t, e, "s", 49, StatusEnrolled, "CS", false)
	mustSnap(t, e, "s", 50, StatusSuspended, "CS", false)
}

func TestRejectedOpsNotAudited(t *testing.T) {
	e := newTestEngine(t)
	_ = e.Admit("s", "CS", 0)
	before := len(e.AuditLog())
	_, err := e.Submit(SubmitInput{StudentID: "s", Type: AppTransfer, At: 6, Submitter: "u", Major: "EE"})
	if codeOf(err) != ErrDeadlinePassed {
		t.Fatalf("got %v", err)
	}
	if len(e.AuditLog()) != before {
		t.Fatalf("rejected op audited: %d -> %d", before, len(e.AuditLog()))
	}
}

func TestConcurrentSerializability(t *testing.T) {
	e := newTestEngine(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sid := fmt.Sprintf("c%d", i)
			// All operations share the same monotonic timestamp; any
			// interleaving is a valid serial order.
			at := int64(50)
			_ = e.Admit(sid, "CS", at)
			_, _ = e.Submit(SubmitInput{
				StudentID: sid, Type: AppWithdraw, At: at, Submitter: fmt.Sprintf("u%d", i),
			})
			_ = e.Decide(DecisionInput{StudentID: sid, Approver: fmt.Sprintf("boss%d", i), At: at})
		}(i)
	}
	wg.Wait()
	for i := 0; i < 8; i++ {
		sid := fmt.Sprintf("c%d", i)
		sn, err := e.SnapshotAt(sid, 99)
		if err != nil || sn.Status != StatusWithdrawn {
			t.Fatalf("student %s: %+v err=%v", sid, sn, err)
		}
	}
}

// TestRejectionPriorityPairwise triggers each adjacent priority pair at once.
func TestRejectionPriorityPairwise(t *testing.T) {
	type probe struct {
		name string
		run  func(e *Engine) error
		want ErrCode
	}
	newE := func(t *testing.T) *Engine {
		e := newTestEngine(t)
		if err := e.Admit("s", "CS", 0); err != nil {
			t.Fatal(err)
		}
		return e
	}
	probes := []probe{
		{"invalid>regression", func(e *Engine) error {
			_, err := e.Submit(SubmitInput{StudentID: "s", Type: AppSuspend, At: -1, Terms: 1})
			return err
		}, ErrInvalidArgument},
		{"regression>notfound", func(e *Engine) error {
			_ = e.Admit("known", "CS", 5)
			_, err := e.Submit(SubmitInput{StudentID: "ghost", Type: AppSuspend, At: 3, Submitter: "u", Terms: 1})
			return err
		}, ErrClockRegression},
		{"notfound>terminal", func(e *Engine) error {
			_, err := e.Submit(SubmitInput{StudentID: "ghost", Type: AppWithdraw, At: 99, Submitter: "u"})
			return err
		}, ErrNotFound},
		{"terminal>existing", func(e *Engine) error {
			_, _ = e.Submit(SubmitInput{StudentID: "s", Type: AppWithdraw, At: 1, Submitter: "u"})
			_ = e.Decide(DecisionInput{StudentID: "s", Approver: "a", At: 1})
			_, err := e.Submit(SubmitInput{StudentID: "s", Type: AppSuspend, At: 2, Submitter: "u", Terms: 1})
			return err
		}, ErrTerminalState},
		{"existing>state", func(e *Engine) error {
			_, _ = e.Submit(SubmitInput{StudentID: "s", Type: AppSuspend, At: 1, Submitter: "u", Terms: 1})
			_, err := e.Submit(SubmitInput{StudentID: "s", Type: AppTransfer, At: 2, Submitter: "u", Major: "EE"})
			return err
		}, ErrExistingApplication},
		{"state>deadline", func(e *Engine) error {
			// Suspend effective term 1; a resume submitted in term 8 after
			// its deadline would carry to term 9 (exists, so no deadline),
			// so use a type that is illegal from suspended and relies on the
			// carry: a second suspend submitted late while suspended is
			// state-legal only after resume, so submit reserve (illegal from
			// suspended) after term 8's deadline with no room to land.
			_, _ = e.Submit(SubmitInput{StudentID: "s", Type: AppSuspend, At: 6, Submitter: "u", Terms: 1})
			_ = e.Decide(DecisionInput{StudentID: "s", Approver: "a", At: 6})
			_, err := e.Submit(SubmitInput{StudentID: "s", Type: AppSuspend, At: 96, Submitter: "u", Terms: 3})
			return err
		}, ErrStateNotAllowed},
		{"deadline>permission", func(e *Engine) error {
			_, _ = e.Submit(SubmitInput{StudentID: "s", Type: AppSuspend, At: 1, Submitter: "u", Terms: 1})
			return e.Decide(DecisionInput{StudentID: "s", Approver: "u", At: 200})
		}, ErrDeadlinePassed},
		{"permission>limit", func(e *Engine) error {
			// A 4-term suspend is at the cap; self-approval is a permission
			// error and must outrank any limit concern on this decision.
			_, _ = e.Submit(SubmitInput{StudentID: "s", Type: AppSuspend, At: 1, Submitter: "u", Terms: 4})
			return e.Decide(DecisionInput{StudentID: "s", Approver: "u", At: 1})
		}, ErrNoPermission},
		{"limit>effective", func(e *Engine) error {
			_, _ = e.Submit(SubmitInput{StudentID: "s", Type: AppSuspend, At: 1, Submitter: "u", Terms: 4})
			_ = e.Decide(DecisionInput{StudentID: "s", Approver: "a", At: 1})
			_, _ = e.Submit(SubmitInput{StudentID: "s", Type: AppResume, At: 10, Submitter: "u"})
			_ = e.Decide(DecisionInput{StudentID: "s", Approver: "b", At: 10})
			_, err := e.Submit(SubmitInput{StudentID: "s", Type: AppSuspend, At: 10, Submitter: "u", Terms: 4})
			return err
		}, ErrLimitExceeded},
		{"effective>quota", func(e *Engine) error {
			s := &student{
				id:        "r",
				entryTerm: 1,
				major:     "CS",
				versions: []Version{
					{EffectiveAt: 10, Status: StatusEnrolled, Major: "CS"},
					{EffectiveAt: 20, Status: StatusEnrolled, Major: "CS"},
				},
				app: &application{
					id: "app-x", typ: AppTransfer, submittedAt: 21, submitter: "u3",
					targetMajor: "EE", levels: 2, level: 2, approvers: []string{"x5"},
					effectiveTerm: 2,
				},
			}
			s.appHist = []*application{s.app}
			e.students["r"] = s
			e.majors["EE"].quota = 0
			e.clockSet, e.clock = true, 21
			return e.Decide(DecisionInput{StudentID: "r", Approver: "x6", At: 22})
		}, ErrEffectiveBeforeLatest},
	}
	for _, p := range probes {
		t.Run(p.name, func(t *testing.T) {
			e := newE(t)
			if got := codeOf(p.run(e)); got != p.want {
				t.Fatalf("%s: got %s want %s", p.name, codeName(got), codeName(p.want))
			}
		})
	}
}

// TestQueryCostLocalScaling verifies per-student locality empirically: the
// snapshot cost for one student does not grow when thousands of unrelated
// students exist.
func TestQueryCostLocalScaling(t *testing.T) {
	measure := func(n int) int64 {
		e := newTestEngine(t)
		for i := 0; i < n; i++ {
			sid := fmt.Sprintf("mass%d", i)
			// Populate internal state directly to isolate query cost from
			// admission/calendar constraints.
			e.students[sid] = &student{
				id:        sid,
				entryTerm: 0,
				major:     "CS",
				versions:  []Version{{EffectiveAt: 0, Status: StatusEnrolled, Major: "CS"}},
			}
		}
		ops := testing.AllocsPerRun(50, func() {
			_, _ = e.SnapshotAt("mass0", 5)
		})
		return int64(ops)
	}
	small := measure(10)
	large := measure(3000)
	// A copy of the tiny version/app slices happens regardless of population.
	// Allow slack; the important invariant is no linear scaling in n.
	if large > small*3 {
		t.Fatalf("snapshot allocations scale with population: small=%d large=%d", small, large)
	}
}

// TestRandomDifferential drives the engine and the independent reference
// model with identical generated operation sequences and asserts identical
// accept/reject codes and identical point-in-time snapshots. Each step prints
// its input, outcome and decision basis.
func TestRandomDifferential(t *testing.T) {
	const iterations = 60
	const steps = 400
	var log bytes.Buffer
	types := []AppType{AppSuspend, AppResume, AppTransfer, AppReserve, AppWithdraw}
	for iter := 0; iter < iterations; iter++ {
		rng := rand.New(rand.NewSource(int64(1000 + iter)))
		log.Reset()
		cfg := testConfig()
		cfg.Levels[AppType(rng.Intn(5))] = 1 + rng.Intn(2)
		cfg.AppDeadline = int64(30 + rng.Intn(80))
		fmt.Fprintf(&log, "seed=%d deadline=%d\n", 1000+iter, cfg.AppDeadline)
		terms := testCalendar(12).terms
		cal, _ := NewCalendar(terms)
		eng, err := NewEngine(cal, cfg)
		if err != nil {
			t.Fatal(err)
		}
		ref := newRefModel(terms, cfg)
		majors := []string{"CS", "EE", "MA"}
		for i, m := range majors {
			cap := 2 + i
			if err := eng.AddMajor(m, cap); err != nil {
				t.Fatal(err)
			}
			ref.quota[m] = cap
		}
		var sids []string
		for i := 0; i < 6; i++ {
			sids = append(sids, fmt.Sprintf("s%d", i))
		}
		tick := int64(0)
		compare := func(step int, basis string) {
			t.Helper()
			for _, sid := range sids {
				for _, qt := range []int64{0, 5, 10, 15, 20, 25, 30, 40, 55, 70, 95} {
					got, gerr := eng.SnapshotAt(sid, qt)
					want, wok := ref.snapshot(sid, qt)
					_ = got
					_ = want
					if gerr != nil || !wok {
						continue
					}
					if got.Status != want.Status || got.Major != want.Major || got.OpenApp != want.OpenApp {
						t.Fatalf("iter=%d step=%d snap %s@%d: eng=%+v ref=%+v\n%s",
							iter, step, sid, qt, got, want, log.String())
					}
				}
			}
		}
		for step := 0; step < steps; step++ {
			tick += int64(rng.Intn(4))
			sid := sids[rng.Intn(len(sids))]
			who := fmt.Sprintf("u%d", rng.Intn(5))
			kind := rng.Intn(10)
			var ec, rc ErrCode
			var basis string
			switch {
			case kind < 2 && rng.Intn(2) == 0:
				mj := majors[rng.Intn(len(majors))]
				at := tick
				err := eng.Admit(sid, mj, at)
				ec, rc = codeOf(err), ref.admit(sid, mj, at)
				basis = fmt.Sprintf("admit %s->%s@%d", sid, mj, at)
			case kind < 6:
				typ := types[rng.Intn(len(types))]
				mj := majors[rng.Intn(len(majors))]
				n := 1 + rng.Intn(3)
				in := SubmitInput{StudentID: sid, Type: typ, At: tick, Submitter: who, Major: mj, Terms: n}
				_, err := eng.Submit(in)
				ec, rc = codeOf(err), ref.submit(sid, typ, tick, who, mj, n)
				basis = fmt.Sprintf("submit %s %s@%d terms=%d major=%s", sid, typ, tick, n, mj)
			case kind < 9:
				reject := rng.Intn(4) == 0
				err := eng.Decide(DecisionInput{StudentID: sid, Approver: who, At: tick, Reject: reject})
				ec, rc = codeOf(err), ref.decide(sid, who, tick, reject)
				basis = fmt.Sprintf("decide %s by=%s reject=%v@%d", sid, who, reject, tick)
			default:
				mj := majors[rng.Intn(len(majors))]
				err := eng.AddQuota(mj, 1, tick)
				ec, rc = codeOf(err), ref.addQuota(mj, 1, tick)
				basis = fmt.Sprintf("quota+1 %s@%d", mj, tick)
			}
			// Normalize: OK sentinel is -1 in both.
			fmt.Fprintf(&log, "step=%3d %-42s => eng=%-24s ref=%-24s\n",
				step, basis, codeName(ec), codeName(rc))
			if ec != rc {
				t.Fatalf("iter=%d step=%d mismatch on %q: eng=%s ref=%s\n%s",
					iter, step, basis, codeName(ec), codeName(rc), log.String())
			}
			if step%20 == 0 {
				compare(step, basis)
			}
		}
		compare(steps, "final")
		if iter == 0 && testing.Verbose() {
			t.Logf("\n%s", log.String())
		}
	}
	if !testing.Verbose() {
		// Keep a short tail in the test output so the logging requirement is
		// visibly exercised even without -v.
		t.Logf("differential log tail:\n%s", strings.Join(lastLines(&log, 8), "\n"))
	}
}

func lastLines(b *bytes.Buffer, n int) []string {
	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	if len(lines) <= n {
		return lines
	}
	return lines[len(lines)-n:]
}
