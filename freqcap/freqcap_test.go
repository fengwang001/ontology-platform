package freqcap

import (
	"testing"
)

func mustNew(t *testing.T, wg, cg, k, cc, g0 int64) *Controller {
	t.Helper()
	c, err := NewController(wg, cg, k, cc, g0)
	if err != nil {
		t.Fatalf("NewController(%d,%d,%d,%d,%d): %v", wg, cg, k, cc, g0, err)
	}
	return c
}

func wantAdmit(t *testing.T, d Decision, ctx string) {
	t.Helper()
	if !d.Allowed {
		t.Fatalf("%s: want admit, got reject(%s)", ctx, d.Reason)
	}
}

func wantReject(t *testing.T, d Decision, r RejectReason, ctx string) {
	t.Helper()
	if d.Allowed || d.Reason != r {
		t.Fatalf("%s: want reject(%s), got %v", ctx, r, d)
	}
}

// TestWorkedExample replays the scenario from the specification.
func TestWorkedExample(t *testing.T) {
	c := mustNew(t, 100, 3, 2, 4, 10)
	wantAdmit(t, c.Admit("u", "c1", "x", 0), "t=0 first exposure")
	wantReject(t, c.Admit("u", "c1", "x", 5), RejectCreativeInterval, "t=5 gap 5 < g0*1")
	wantAdmit(t, c.Admit("u", "c1", "x", 10), "t=10 gap == g0*1")
	wantReject(t, c.Admit("u", "c1", "x", 25), RejectCreativeInterval, "t=25 gap 15 < g0*2")
	wantAdmit(t, c.Admit("u", "c1", "y", 26), "t=26 different creative, E=3, n_c=2")
	wantReject(t, c.Admit("u", "c2", "z", 27), RejectGlobalWindow, "t=27 cg=3 == Cg")
	wantAdmit(t, c.Admit("u", "c2", "z", 110), "t=110: t=0,10 expired, cg=1, E=4")
}

// TestIntervalExactBoundary covers gap == g0*s passing and gap == g0*s-1
// being rejected, for s = 1, 2, 3.
func TestIntervalExactBoundary(t *testing.T) {
	c := mustNew(t, 1000, 100, 1000, 100, 10)
	wantAdmit(t, c.Admit("u", "c", "x", 0), "first")
	// s = 1, need 10.
	wantReject(t, c.Admit("u", "c", "x", 9), RejectCreativeInterval, "gap 9 < 10")
	wantAdmit(t, c.Admit("u", "c", "x", 10), "gap 10 == g0*1")
	// s = 2, need 20.
	wantReject(t, c.Admit("u", "c", "x", 29), RejectCreativeInterval, "gap 19 < 20")
	wantAdmit(t, c.Admit("u", "c", "x", 30), "gap 20 == g0*2")
	// s = 3, need 30.
	wantReject(t, c.Admit("u", "c", "x", 59), RejectCreativeInterval, "gap 29 < 30")
	wantAdmit(t, c.Admit("u", "c", "x", 60), "gap 30 == g0*3")
}

// TestStreakCapsAt3: with s = 4 the required gap is still g0*3, not g0*4.
func TestStreakCapsAt3(t *testing.T) {
	c := mustNew(t, 1000, 100, 1000, 100, 10)
	wantAdmit(t, c.Admit("u", "c", "x", 0), "s->1")
	wantAdmit(t, c.Admit("u", "c", "x", 10), "s->2")
	wantAdmit(t, c.Admit("u", "c", "x", 30), "s->3")
	wantAdmit(t, c.Admit("u", "c", "x", 60), "s->4")
	// s = 4, capped at 3: need 30, not 40.
	wantReject(t, c.Admit("u", "c", "x", 89), RejectCreativeInterval, "gap 29 < 30")
	wantAdmit(t, c.Admit("u", "c", "x", 90), "gap 30 == g0*3 (capped)")
	// s = 5, still capped at 3.
	wantAdmit(t, c.Admit("u", "c", "x", 120), "gap 30 == g0*3 (still capped)")
}

// TestStreakResetByDifferentCreative: a different creative breaks the run
// and the next same-creative run restarts at s = 1.
func TestStreakResetByDifferentCreative(t *testing.T) {
	c := mustNew(t, 1000, 100, 1000, 100, 10)
	wantAdmit(t, c.Admit("u", "c", "x", 0), "x s->1")
	wantAdmit(t, c.Admit("u", "c", "x", 10), "x s->2")
	wantAdmit(t, c.Admit("u", "c", "y", 11), "y: different creative, no interval")
	wantAdmit(t, c.Admit("u", "c", "x", 12), "x again: last creative is y, no interval")
	// Now the trailing run of x has length 1: need only g0*1 = 10.
	wantReject(t, c.Admit("u", "c", "x", 21), RejectCreativeInterval, "gap 9 < 10")
	wantAdmit(t, c.Admit("u", "c", "x", 22), "gap 10 == g0*1, run restarted at 1")
}

// TestIntervalRejectDoesNotAdvance: a rejected interval check must not
// advance the user clock nor grow the streak.
func TestIntervalRejectDoesNotAdvance(t *testing.T) {
	c := mustNew(t, 1000, 100, 1000, 100, 10)
	wantAdmit(t, c.Admit("u", "c", "x", 0), "first")
	wantReject(t, c.Admit("u", "c", "x", 5), RejectCreativeInterval, "rejected at t=5")
	// Clock must still be at 0: t=2 would be a rollback if the rejection
	// had advanced the clock to 5.
	wantReject(t, c.Admit("u", "c", "x", 2), RejectCreativeInterval, "t=2 still vs t_last=0")
	// Streak must still be 1: at t=10 the requirement is g0*1, not g0*2.
	wantAdmit(t, c.Admit("u", "c", "x", 10), "gap 10 == g0*1, streak unchanged")
}

// TestWindowExpiryBoundary: t+Wg == now is expired, t+Wg == now+1 counts.
func TestWindowExpiryBoundary(t *testing.T) {
	c := mustNew(t, 100, 2, 1000, 100, 1)
	wantAdmit(t, c.Admit("u", "c", "x", 0), "t=0")
	wantAdmit(t, c.Admit("u", "c", "y", 10), "t=10")
	// At now=100: t=0 has 0+100 == 100, expired; cg = 1 < 2.
	wantAdmit(t, c.Admit("u", "c", "z", 100), "t=0 expired at boundary")
	// At now=109: t=10 has 10+100 == 110 > 109 (still live, diff 1),
	// t=100 has 200 > 109 (live): cg = 2 == Cg, reject.
	wantReject(t, c.Admit("u", "c", "w", 109), RejectGlobalWindow, "t=10 live by 1")
	// At now=110: t=10 expires exactly; cg = 1.
	wantAdmit(t, c.Admit("u", "c", "w", 110), "t=10 expired at boundary")
}

// TestGlobalWindowFull: cg == Cg rejects, cg == Cg-1 passes.
func TestGlobalWindowFull(t *testing.T) {
	c := mustNew(t, 1000, 2, 1000, 100, 1)
	wantAdmit(t, c.Admit("u", "c", "x", 0), "cg 0->1")
	wantAdmit(t, c.Admit("u", "c", "y", 10), "cg 1->2")
	wantReject(t, c.Admit("u", "c", "z", 20), RejectGlobalWindow, "cg=2 == Cg")
	wantAdmit(t, c.Admit("u", "c", "z", 1001), "t=0 expired (0+1000 <= 1001)")
}

// TestEffectiveCapTightening: E = max(1, Cc - floor(cg/K)) tightens in
// steps as cg grows and never drops below 1.
func TestEffectiveCapTightening(t *testing.T) {
	// Wg huge so nothing expires; Cg huge so the window never blocks;
	// K=2, Cc=4: cg=0,1 -> E=4; cg=2,3 -> E=3; cg=4,5 -> E=2; cg>=6 -> E=1.
	c := mustNew(t, 1_000_000_000, 100, 2, 4, 1)
	// Camp A: admitted at cg=0 (E=4), cg=1 (E=4), cg=2 (E=3); at cg=3 the
	// cap has tightened to E=3 and n_c=3 rejects.
	wantAdmit(t, c.Admit("u", "A", "x", 0), "A cg=0 E=4")
	wantAdmit(t, c.Admit("u", "A", "x", 10), "A cg=1 E=4")
	wantAdmit(t, c.Admit("u", "A", "x", 20), "A cg=2 E=3")
	wantReject(t, c.Admit("u", "A", "x", 30), RejectCampaignDaily, "A cg=3 E=3, n_c=3")
	// Camp B: admitted at cg=3 (E=3) and cg=4 (E=2); at cg=5, E=2 rejects.
	wantAdmit(t, c.Admit("u", "B", "x", 40), "B cg=3 E=3")
	wantAdmit(t, c.Admit("u", "B", "x", 50), "B cg=4 E=2")
	wantReject(t, c.Admit("u", "B", "x", 60), RejectCampaignDaily, "B cg=5 E=2, n_c=2")
	// Camp C: admitted at cg=5 (E=2); at cg=6, E = max(1, 4-3) = 1 rejects.
	wantAdmit(t, c.Admit("u", "C", "x", 70), "C cg=5 E=2")
	wantReject(t, c.Admit("u", "C", "x", 80), RejectCampaignDaily, "C cg=6 E=1, n_c=1")
	// E never drops below 1: cg=7 gives E = max(1, 4-3) = 1, cg=8 gives
	// max(1, 4-4) = 1, so camp D still gets exactly one slot.
	wantAdmit(t, c.Admit("u", "D", "x", 90), "D cg=7 E=1 (floor)")
	wantReject(t, c.Admit("u", "D", "x", 100), RejectCampaignDaily, "D cg=8 E=1 (floor)")
}

// TestDailyCapBoundary: n_c == E rejects, n_c == E-1 passes.
func TestDailyCapBoundary(t *testing.T) {
	c := mustNew(t, 1000, 100, 1000, 2, 1)
	wantAdmit(t, c.Admit("u", "c", "x", 0), "n_c 0->1")
	wantAdmit(t, c.Admit("u", "c", "y", 10), "n_c 1->2")
	wantReject(t, c.Admit("u", "c", "z", 20), RejectCampaignDaily, "n_c=2 == E=2")
	// A different campaign is unaffected.
	wantAdmit(t, c.Admit("u", "d", "z", 20), "other campaign has own n_c")
}

// TestDayBoundaryReset: crossing a multiple of 86400 resets n_c but not
// the global window nor the creative streak.
func TestDayBoundaryReset(t *testing.T) {
	c := mustNew(t, 200_000, 100, 1000, 2, 10)
	wantAdmit(t, c.Admit("u", "c", "x", 86390), "day 0, n_c=1")
	wantAdmit(t, c.Admit("u", "c", "x", 86400), "day 1, n_c reset; streak continues (gap 10)")
	// Streak is 2 across the day boundary: need g0*2 = 20.
	wantReject(t, c.Admit("u", "c", "x", 86419), RejectCreativeInterval, "gap 19 < 20 across day")
	wantAdmit(t, c.Admit("u", "c", "x", 86420), "gap 20 == g0*2 across day")
	// n_c on day 1 is now 2 == Cc: daily cap still enforced on the new day.
	wantReject(t, c.Admit("u", "c", "y", 86430), RejectCampaignDaily, "day-1 n_c=2 == E")
	// Global window did not reset: cg = 3 at t=86430 (Wg=200000).
	cg := c.windowCount(&c.entry("u").st, 86430)
	if cg != 3 {
		t.Fatalf("window must not reset at day boundary: cg=%d, want 3", cg)
	}
}

// TestPeekConsistentAndReadOnly: Peek returns exactly what Admit would
// decide at the same instant and never changes any state.
func TestPeekConsistentAndReadOnly(t *testing.T) {
	c := mustNew(t, 100, 3, 2, 4, 10)
	wantAdmit(t, c.Admit("u", "c1", "x", 0), "seed")
	before := c.Stats("u")
	// Peek a rejection (interval) and an admission.
	wantReject(t, c.Peek("u", "c1", "x", 5), RejectCreativeInterval, "peek interval")
	if !c.Peek("u", "c1", "x", 10).Allowed {
		t.Fatal("peek at t=10 should allow")
	}
	// Repeated peeks are stable and state is untouched.
	wantReject(t, c.Peek("u", "c1", "x", 5), RejectCreativeInterval, "peek stable")
	if got := c.Stats("u"); got != before {
		t.Fatalf("Peek mutated state: before=%+v after=%+v", before, got)
	}
	// Admit at t=10 must match the earlier Peek.
	wantAdmit(t, c.Admit("u", "c1", "x", 10), "admit matches peek")
	// Peek must not record: t=20 is still measured against t_last=10 with
	// s=2, so it needs 20 and is rejected; had Peek recorded anything the
	// outcome would differ.
	wantReject(t, c.Peek("u", "c1", "x", 20), RejectCreativeInterval, "peek recorded nothing")
	wantAdmit(t, c.Admit("u", "c1", "x", 30), "gap 20 == g0*2")
}

// TestBatchRollbackInterval: a later item rejected by an earlier admitted
// item's streak rolls the whole batch back.
func TestBatchRollbackInterval(t *testing.T) {
	c := mustNew(t, 1000, 100, 1000, 100, 10)
	res := c.AdmitBatch("u", []Request{
		{Camp: "c", Cre: "x", Now: 0},  // admitted, streak 1
		{Camp: "c", Cre: "x", Now: 10}, // admitted, streak 2
		{Camp: "c", Cre: "x", Now: 25}, // gap 15 < g0*2 -> reject
	})
	if res.AdmittedAll || res.FailedIndex != 2 || res.Reason != RejectCreativeInterval {
		t.Fatalf("want failure at 2 (creative_interval), got %+v", res)
	}
	if got := c.Stats("u"); got.Admitted != 0 || got.Windowed != 0 {
		t.Fatalf("batch must roll back fully, stats=%+v", got)
	}
	// State is pristine: the same first item is admitted again.
	wantAdmit(t, c.Admit("u", "c", "x", 0), "rollback restored state")
}

// TestBatchRollbackDailyCap: a later item rejected by the tightened daily
// cap (fed by earlier batch items) rolls the whole batch back.
func TestBatchRollbackDailyCap(t *testing.T) {
	c := mustNew(t, 1000, 100, 1000, 2, 1)
	res := c.AdmitBatch("u", []Request{
		{Camp: "c", Cre: "x", Now: 0},
		{Camp: "c", Cre: "y", Now: 10},
		{Camp: "c", Cre: "z", Now: 20}, // n_c would become 3 > E=2 -> reject
	})
	if res.AdmittedAll || res.FailedIndex != 2 || res.Reason != RejectCampaignDaily {
		t.Fatalf("want failure at 2 (campaign_daily), got %+v", res)
	}
	if got := c.Stats("u"); got.Admitted != 0 {
		t.Fatalf("batch must roll back fully, stats=%+v", got)
	}
	// n_c was rolled back: two fresh admits for camp c are allowed.
	wantAdmit(t, c.Admit("u", "c", "x", 0), "n_c rolled back #1")
	wantAdmit(t, c.Admit("u", "c", "y", 10), "n_c rolled back #2")
	wantReject(t, c.Admit("u", "c", "z", 20), RejectCampaignDaily, "cap still 2")
}

// TestBatchSuccess: a fully passing batch is recorded atomically and
// influences subsequent calls.
func TestBatchSuccess(t *testing.T) {
	c := mustNew(t, 1000, 100, 1000, 100, 10)
	res := c.AdmitBatch("u", []Request{
		{Camp: "c", Cre: "x", Now: 0},
		{Camp: "c", Cre: "x", Now: 10},
		{Camp: "c", Cre: "x", Now: 30},
	})
	if !res.AdmittedAll {
		t.Fatalf("want all admitted, got %+v", res)
	}
	if got := c.Stats("u"); got.Admitted != 3 || got.Windowed != 3 {
		t.Fatalf("stats=%+v, want 3 admitted / 3 windowed", got)
	}
	// Streak is 3 now: the next same-creative admit needs g0*3 = 30.
	wantReject(t, c.Admit("u", "c", "x", 59), RejectCreativeInterval, "streak from batch")
	wantAdmit(t, c.Admit("u", "c", "x", 60), "gap 30 == g0*3")
}

// TestBatchValidation: bad lengths and bad items are invalid parameters.
func TestBatchValidation(t *testing.T) {
	c := mustNew(t, 100, 3, 2, 4, 10)
	if res := c.AdmitBatch("u", nil); res.AdmittedAll || res.Reason != RejectInvalidParam {
		t.Fatalf("empty batch: %+v", res)
	}
	big := make([]Request, 1001)
	for i := range big {
		big[i] = Request{Camp: "c", Cre: "x", Now: int64(i)}
	}
	if res := c.AdmitBatch("u", big); res.AdmittedAll || res.Reason != RejectInvalidParam {
		t.Fatalf("oversized batch: %+v", res)
	}
	if res := c.AdmitBatch("", []Request{{Camp: "c", Cre: "x", Now: 0}}); res.Reason != RejectInvalidParam {
		t.Fatalf("empty user: %+v", res)
	}
	res := c.AdmitBatch("u", []Request{
		{Camp: "c", Cre: "x", Now: 0},
		{Camp: "c", Cre: "", Now: 10}, // invalid item at index 1
	})
	if res.AdmittedAll || res.FailedIndex != 1 || res.Reason != RejectInvalidParam {
		t.Fatalf("invalid item: %+v", res)
	}
	if got := c.Stats("u"); got.Admitted != 0 {
		t.Fatalf("invalid batch must roll back, stats=%+v", got)
	}
}

// TestUsersIndependent: users never share clocks, streaks, windows or caps.
func TestUsersIndependent(t *testing.T) {
	c := mustNew(t, 100, 2, 2, 2, 10)
	wantAdmit(t, c.Admit("alice", "c", "x", 100), "alice ahead")
	// Bob at an earlier time is not a rollback: clocks are per-user.
	wantAdmit(t, c.Admit("bob", "c", "x", 0), "bob has his own clock")
	// Bob's own streak starts at 1 (gap 10 >= g0*1), unaffected by Alice.
	wantAdmit(t, c.Admit("bob", "c", "x", 10), "bob streak independent")
	// Alice fills her own window and daily cap.
	wantAdmit(t, c.Admit("alice", "c", "y", 110), "alice second exposure")
	wantReject(t, c.Admit("alice", "c", "z", 120), RejectGlobalWindow, "alice window full")
	// At t=110 Alice has cg=2 (full) and n_c(c)=2 (cap hit); Bob has cg=0
	// (his t=0,10 expired) and n_c(d)=0: neither window nor cap is shared.
	wantAdmit(t, c.Admit("bob", "d", "y", 110), "bob window and daily cap independent")
}

// TestRejectPriority: when several rules fail at once, the first one in
// the fixed order is reported.
func TestRejectPriority(t *testing.T) {
	c := mustNew(t, 100, 1, 1, 1, 10)
	wantAdmit(t, c.Admit("u", "c", "x", 100), "seed at t=100")
	// invalid param beats everything (empty creative, also rollback etc.).
	wantReject(t, c.Admit("u", "c", "", 50), RejectInvalidParam, "invalid first")
	wantReject(t, c.Admit("u", "c", "x", -1), RejectInvalidParam, "negative now")
	wantReject(t, c.Admit("u", "c", "x", MaxNow+1), RejectInvalidParam, "now too large")
	// clock rollback beats interval/window/daily (t=50 < 100, same creative).
	wantReject(t, c.Admit("u", "c", "x", 50), RejectClockRollback, "rollback second")
	// interval beats window and daily (same creative, gap 5 < 10; cg=1=Cg).
	wantReject(t, c.Admit("u", "c", "x", 105), RejectCreativeInterval, "interval third")
	// window beats daily (different creative; cg=1 == Cg=1; n_c=1 >= E=1).
	wantReject(t, c.Admit("u", "c", "y", 110), RejectGlobalWindow, "window fourth")
	// daily reported when it is the only failing rule.
	c2 := mustNew(t, 100, 10, 1, 1, 10)
	wantAdmit(t, c2.Admit("u", "c", "x", 0), "seed")
	wantReject(t, c2.Admit("u", "c", "y", 20), RejectCampaignDaily, "daily last")
}

// TestRejectedOpsDoNotMutate: every kind of rejection leaves the user
// state (records, streak, clock, counters) untouched.
func TestRejectedOpsDoNotMutate(t *testing.T) {
	c := mustNew(t, 100, 2, 2, 2, 10)
	wantAdmit(t, c.Admit("u", "c", "x", 0), "seed")
	before := c.Stats("u")
	wantReject(t, c.Admit("", "c", "x", 10), RejectInvalidParam, "empty user")
	wantReject(t, c.Admit("u", "c", "x", -5), RejectInvalidParam, "negative now")
	wantReject(t, c.Admit("u", "c", "x", 5), RejectCreativeInterval, "gap 5 < 10")
	if got := c.Stats("u"); got != before {
		t.Fatalf("rejections mutated state: %+v -> %+v", before, got)
	}
	// A valid call (different creative, room left) fills the window.
	wantAdmit(t, c.Admit("u", "c", "y", 10), "fill window")
	if got := c.Stats("u"); got.Admitted != before.Admitted+1 {
		t.Fatalf("only the valid call may record: %+v -> %+v", before, got)
	}
	// Now the window is full (cg=2 == Cg) and n_c=2 == E=2.
	mid := c.Stats("u")
	wantReject(t, c.Admit("u", "c", "z", 20), RejectGlobalWindow, "window full")
	wantReject(t, c.Admit("u", "c", "z", -1), RejectInvalidParam, "invalid")
	if got := c.Stats("u"); got != mid {
		t.Fatalf("rejections mutated state: %+v -> %+v", mid, got)
	}
	// Clock and streak intact: t=15 vs t_last=10 with streak 1 -> interval.
	wantReject(t, c.Admit("u", "c", "y", 15), RejectCreativeInterval, "streak intact")
}

// TestConstructorValidation: out-of-range constructor parameters fail.
func TestConstructorValidation(t *testing.T) {
	bad := [][5]int64{
		{0, 3, 2, 4, 10}, {1_000_000_001, 3, 2, 4, 10},
		{100, 0, 2, 4, 10}, {100, 1_000_001, 2, 4, 10},
		{100, 3, 0, 4, 10}, {100, 3, 1_000_001, 4, 10},
		{100, 3, 2, 0, 10}, {100, 3, 2, 1_000_001, 10},
		{100, 3, 2, 4, 0}, {100, 3, 2, 4, 1_000_000_001},
	}
	for _, p := range bad {
		if _, err := NewController(p[0], p[1], p[2], p[3], p[4]); err == nil {
			t.Errorf("NewController%v: want error", p)
		}
	}
	if _, err := NewController(1, 1, 1, 1, 1); err != nil {
		t.Errorf("min boundary: %v", err)
	}
	if _, err := NewController(1_000_000_000, 1_000_000, 1_000_000, 1_000_000, 1_000_000_000); err != nil {
		t.Errorf("max boundary: %v", err)
	}
}

// TestReplayDeterminism: the same operation sequence on a fresh controller
// reproduces identical decisions and reasons.
func TestReplayDeterminism(t *testing.T) {
	ops := []struct {
		user, camp, cre string
		now             int64
	}{
		{"u", "c1", "x", 0}, {"u", "c1", "x", 5}, {"u", "c1", "x", 10},
		{"u", "c1", "y", 11}, {"u", "c2", "x", 12}, {"v", "c1", "x", 3},
		{"u", "c1", "x", 4}, {"u", "c1", "z", 200}, {"v", "c1", "x", 8},
	}
	run := func() []Decision {
		c := mustNew(t, 100, 3, 2, 4, 10)
		out := make([]Decision, len(ops))
		for i, op := range ops {
			out[i] = c.Admit(op.user, op.camp, op.cre, op.now)
		}
		return out
	}
	first, second := run(), run()
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("op %d diverged: %v vs %v", i, first[i], second[i])
		}
	}
}
