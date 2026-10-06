package apf

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func at(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }

func mustController(t *testing.T, cfg Config) *Controller {
	t.Helper()
	c, err := NewController(t0, cfg)
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	return c
}

func wantErr(t *testing.T, err error, kind Kind, ctx string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected error %v, got nil", ctx, kind)
	}
	k, ok := KindOf(err)
	if !ok || k != kind {
		t.Fatalf("%s: expected kind %v, got %v", ctx, kind, err)
	}
	t.Logf("%s -> rejected: %v", ctx, err)
}

func wantLease(t *testing.T, c *Controller, tm time.Time, req Request, ctx string) *Lease {
	t.Helper()
	res, err := c.Admit(tm, req)
	if err != nil {
		t.Fatalf("%s: unexpected error: %v", ctx, err)
	}
	if res.Lease == nil {
		t.Fatalf("%s: expected immediate lease, got %+v", ctx, res)
	}
	t.Logf("%s -> execute immediately", ctx)
	return res.Lease
}

func wantTicket(t *testing.T, c *Controller, tm time.Time, req Request, ctx string) *Ticket {
	t.Helper()
	res, err := c.Admit(tm, req)
	if err != nil {
		t.Fatalf("%s: unexpected error: %v", ctx, err)
	}
	if res.Ticket == nil {
		t.Fatalf("%s: expected queued ticket, got %+v", ctx, res)
	}
	t.Logf("%s -> queued", ctx)
	return res.Ticket
}

func wantNoResult(t *testing.T, tk *Ticket, ctx string) {
	t.Helper()
	select {
	case r := <-tk.C():
		t.Fatalf("%s: unexpected ticket resolution: %+v", ctx, r)
	default:
	}
}

func wantTicketLease(t *testing.T, tk *Ticket, ctx string) *Lease {
	t.Helper()
	select {
	case r := <-tk.C():
		if r.Err != nil {
			t.Fatalf("%s: expected lease, got error %v", ctx, r.Err)
		}
		t.Logf("%s -> dequeued for execution", ctx)
		return r.Lease
	default:
		t.Fatalf("%s: expected resolved ticket, none pending", ctx)
		return nil
	}
}

func wantTicketErr(t *testing.T, tk *Ticket, kind Kind, ctx string) {
	t.Helper()
	select {
	case r := <-tk.C():
		if r.Err == nil {
			t.Fatalf("%s: expected error %v, got lease", ctx, kind)
		}
		k, _ := KindOf(r.Err)
		if k != kind {
			t.Fatalf("%s: expected kind %v, got %v", ctx, kind, r.Err)
		}
		t.Logf("%s -> ticket rejected: %v", ctx, r.Err)
	default:
		t.Fatalf("%s: expected resolved ticket, none pending", ctx)
	}
}

func levelByName(levels []LevelDebug, name string) LevelDebug {
	for _, l := range levels {
		if l.Name == name {
			return l
		}
	}
	return LevelDebug{Name: name}
}

// TestRulePrecedenceAndTieBreak verifies ordering by precedence value and,
// for equal values, by rule name.
func TestRulePrecedenceAndTieBreak(t *testing.T) {
	cfg := Config{
		TotalSeats: 10,
		Levels: []Level{
			{Name: "L1", Shares: 1, QueueLimit: 10, QueueTimeout: time.Minute},
			{Name: "L2", Shares: 1, QueueLimit: 10, QueueTimeout: time.Minute},
		},
		Rules: []Rule{
			// Same precedence: "alpha" beats "beta" by name even though
			// "beta" is listed first.
			{Name: "beta", Precedence: 5, Verbs: []string{"get"}, Level: "L2", DistinguishBy: ByUser},
			{Name: "alpha", Precedence: 5, Verbs: []string{"get"}, Level: "L1", DistinguishBy: ByUser},
			// Lower precedence value wins over both.
			{Name: "zeta", Precedence: 1, Users: []string{"root"}, Level: "L2", DistinguishBy: ByUser},
		},
	}
	c := mustController(t, cfg)

	// "get" matches alpha and beta; alpha wins the name tie-break -> L1.
	wantLease(t, c, at(1), Request{User: "u1", Verb: "get", Resource: "pods", Seats: 2}, "get by u1 (alpha rule, L1)")
	// root matches zeta (precedence 1) -> L2.
	wantLease(t, c, at(2), Request{User: "root", Verb: "get", Resource: "pods", Seats: 4}, "get by root (zeta rule, L2)")

	_, levels := c.DebugState()
	if got := levelByName(levels, "L1").Occupied; got != 2 {
		t.Fatalf("L1 occupied = %d, want 2 (tie-break by name picked alpha)", got)
	}
	if got := levelByName(levels, "L2").Occupied; got != 4 {
		t.Fatalf("L2 occupied = %d, want 4 (lower precedence value picked zeta)", got)
	}
	t.Log("判定依据: 同优先级按名称升序 alpha<beta -> L1; 优先级数值 1<5 -> zeta -> L2")
}

// TestRuleSetSemantics verifies AND of the three sets and empty-means-any.
func TestRuleSetSemantics(t *testing.T) {
	cfg := Config{
		TotalSeats: 10,
		Levels: []Level{
			{Name: "L1", Shares: 1, QueueLimit: 10, QueueTimeout: time.Minute},
		},
		Rules: []Rule{
			{Name: "specific", Precedence: 1, Users: []string{"alice", "bob"},
				Verbs: []string{"get"}, Resources: []string{"pods"},
				Level: "L1", DistinguishBy: ByNamespace},
		},
	}
	c := mustController(t, cfg)

	// All three sets match.
	wantLease(t, c, at(1), Request{User: "bob", Verb: "get", Resource: "pods", Seats: 1}, "bob get pods (all sets match)")
	// Verb not in set -> no match, even though user and resource match.
	wantErrFromAdmit(t, c, at(2), Request{User: "bob", Verb: "delete", Resource: "pods", Seats: 1}, KindNoMatch, "bob delete pods (verb outside set)")
	// User not in set -> no match.
	wantErrFromAdmit(t, c, at(3), Request{User: "carol", Verb: "get", Resource: "pods", Seats: 1}, KindNoMatch, "carol get pods (user outside set)")
}

func wantErrFromAdmit(t *testing.T, c *Controller, tm time.Time, req Request, kind Kind, ctx string) {
	t.Helper()
	_, err := c.Admit(tm, req)
	wantErr(t, err, kind, ctx)
}

// TestSeatAllocationRemainder verifies floor allocation and the remainder
// distribution order (shares descending, then name ascending).
func TestSeatAllocationRemainder(t *testing.T) {
	cases := []struct {
		name   string
		total  int
		levels []Level
		want   map[string]int
	}{
		{
			name:  "exact division",
			total: 10,
			levels: []Level{
				{Name: "a", Shares: 1}, {Name: "b", Shares: 3}, {Name: "c", Shares: 6},
			},
			want: map[string]int{"a": 1, "b": 3, "c": 6},
		},
		{
			name:  "remainder goes to larger shares first",
			total: 10,
			levels: []Level{
				{Name: "a", Shares: 1}, {Name: "b", Shares: 1}, {Name: "c", Shares: 1},
			},
			// floors are 3 each, remainder 1; shares tie -> name ascending.
			want: map[string]int{"a": 4, "b": 3, "c": 3},
		},
		{
			name:  "remainder by shares before name",
			total: 7,
			levels: []Level{
				{Name: "a", Shares: 2}, {Name: "b", Shares: 3}, {Name: "c", Shares: 2},
			},
			// floors: a=2, b=3, c=2 -> sum 7, remainder 0.
			want: map[string]int{"a": 2, "b": 3, "c": 2},
		},
		{
			name:  "remainder with distinct shares",
			total: 8,
			levels: []Level{
				{Name: "a", Shares: 1}, {Name: "b", Shares: 2}, {Name: "c", Shares: 4},
			},
			// floors: a=1 (8/7), b=2 (16/7), c=4 (32/7) -> sum 7, remainder 1
			// -> largest shares first: c gets it.
			want: map[string]int{"a": 1, "b": 2, "c": 5},
		},
		{
			name:  "zero shares get nothing",
			total: 5,
			levels: []Level{
				{Name: "a", Shares: 1}, {Name: "b", Shares: 0},
			},
			want: map[string]int{"a": 5, "b": 0},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := allocateNominalSeats(tc.levels, tc.total)
			for name, want := range tc.want {
				if got[name] != want {
					t.Fatalf("level %q: nominal = %d, want %d (all: %v)", name, got[name], want, got)
				}
			}
			t.Logf("total=%d -> %v", tc.total, got)
		})
	}
}

// TestExemptLevel verifies exempt requests execute immediately without
// seats or queues.
func TestExemptLevel(t *testing.T) {
	cfg := Config{
		TotalSeats: 1,
		Levels: []Level{
			{Name: "exempt", Exempt: true},
			{Name: "limited", Shares: 1, QueueLimit: 1, QueueTimeout: time.Minute},
		},
		Rules: []Rule{
			{Name: "admins", Precedence: 1, Users: []string{"admin"}, Level: "exempt", DistinguishBy: ByUser},
			{Name: "others", Precedence: 2, Level: "limited", DistinguishBy: ByUser},
		},
	}
	c := mustController(t, cfg)

	// Fill the only seat of the limited level.
	lease := wantLease(t, c, at(1), Request{User: "u1", Seats: 1}, "u1 occupies the only seat")
	// Exempt requests still execute immediately, even with 10 seats each,
	// and never touch the limited level's occupancy.
	var exemptLeases []*Lease
	for i := 0; i < 5; i++ {
		exemptLeases = append(exemptLeases,
			wantLease(t, c, at(2+i), Request{User: "admin", Seats: 10}, "admin exempt request"))
	}
	for i, l := range exemptLeases {
		if err := l.Finish(at(10 + i)); err != nil {
			t.Fatalf("exempt finish: %v", err)
		}
	}
	_, levels := c.DebugState()
	if got := levelByName(levels, "limited").Occupied; got != 1 {
		t.Fatalf("limited occupied = %d, want 1 (exempt requests take no seats)", got)
	}
	if err := lease.Finish(at(20)); err != nil {
		t.Fatalf("finish: %v", err)
	}
	t.Log("判定依据: 豁免级别不占席位、不排队")
}

// TestUnsatisfiable verifies requests wider than the nominal seats are
// rejected immediately, including levels with zero nominal seats.
func TestUnsatisfiable(t *testing.T) {
	cfg := Config{
		TotalSeats: 4,
		Levels: []Level{
			{Name: "L1", Shares: 1, QueueLimit: 10, QueueTimeout: time.Minute},
			{Name: "L0", Shares: 0, QueueLimit: 10, QueueTimeout: time.Minute},
		},
		Rules: []Rule{
			{Name: "to-l1", Precedence: 1, Resources: []string{"pods"}, Level: "L1", DistinguishBy: ByUser},
			{Name: "to-l0", Precedence: 2, Resources: []string{"jobs"}, Level: "L0", DistinguishBy: ByUser},
		},
	}
	c := mustController(t, cfg)

	// L1 nominal is 4: a 5-seat request can never run.
	wantErrFromAdmit(t, c, at(1), Request{User: "u", Resource: "pods", Seats: 5}, KindUnsatisfiable, "5 seats > nominal 4")
	// L0 nominal is 0: nothing ever fits.
	wantErrFromAdmit(t, c, at(2), Request{User: "u", Resource: "jobs", Seats: 1}, KindUnsatisfiable, "1 seat > nominal 0")
	// A fitting request still works.
	wantLease(t, c, at(3), Request{User: "u", Resource: "pods", Seats: 4}, "4 seats fit nominal 4")
}

// TestQueueFull verifies the queue length limit and that a rejected request
// changes nothing, including the clock.
func TestQueueFull(t *testing.T) {
	cfg := Config{
		TotalSeats: 1,
		Levels: []Level{
			{Name: "L1", Shares: 1, QueueLimit: 3, QueueTimeout: 10 * time.Minute},
		},
		Rules: []Rule{
			{Name: "all", Precedence: 1, Level: "L1", DistinguishBy: ByUser},
		},
	}
	c := mustController(t, cfg)

	lease := wantLease(t, c, at(1), Request{User: "u1", Seats: 1}, "u1 executes")
	wantTicket(t, c, at(2), Request{User: "u2", Seats: 1}, "u2 queued (1/3)")
	wantTicket(t, c, at(3), Request{User: "u3", Seats: 1}, "u3 queued (2/3)")
	wantTicket(t, c, at(3), Request{User: "u4", Seats: 1}, "u4 queued (3/3)")
	// Queue is full: rejected at t=100.
	wantErrFromAdmit(t, c, at(100), Request{User: "u5", Seats: 1}, KindQueueFull, "u5 rejected, queue full")

	_, levels := c.DebugState()
	if got := levelByName(levels, "L1").Waiters; got != 3 {
		t.Fatalf("waiters = %d, want 3 (rejected request left no trace)", got)
	}

	// The rejection must not have advanced the clock: an operation at t=4
	// (>= last accepted t=3) is still legal.
	// (a new admit would be rejected again for queue-full, so use a finish.)
	if err := lease.Finish(at(5)); err != nil {
		t.Fatalf("finish at t=5 after rejected op at t=100: %v", err)
	}
	t.Log("判定依据: 队列上限 3; 被拒绝请求不改变状态与时钟")
}

// TestFlowFIFOAndRoundRobin verifies FIFO inside a flow and round-robin
// between flows, where the rotation order follows the most recent
// empty->non-empty transition of each flow.
func TestFlowFIFOAndRoundRobin(t *testing.T) {
	cfg := Config{
		TotalSeats: 1,
		Levels: []Level{
			{Name: "L1", Shares: 1, QueueLimit: 20, QueueTimeout: time.Hour},
		},
		Rules: []Rule{
			{Name: "all", Precedence: 1, Level: "L1", DistinguishBy: ByUser},
		},
	}
	c := mustController(t, cfg)

	running := wantLease(t, c, at(1), Request{User: "A", Seats: 1}, "A1 executes (only seat)")
	a2 := wantTicket(t, c, at(2), Request{User: "A", Seats: 1}, "A2 queued, flow A becomes non-empty")
	b1 := wantTicket(t, c, at(3), Request{User: "B", Seats: 1}, "B1 queued, flow B becomes non-empty")
	a3 := wantTicket(t, c, at(4), Request{User: "A", Seats: 1}, "A3 queued behind A2 (flow A already non-empty)")
	c1 := wantTicket(t, c, at(5), Request{User: "C", Seats: 1}, "C1 queued, flow C becomes non-empty")
	b2 := wantTicket(t, c, at(6), Request{User: "B", Seats: 1}, "B2 queued behind B1")

	// Rotation order: A, B, C. Expected service: A2, B1, C1, A3, B2.
	tm := 10
	step := func(tk *Ticket, ctx string) *Lease {
		t.Helper()
		if err := running.Finish(at(tm)); err != nil {
			t.Fatalf("finish: %v", err)
		}
		tm++
		running = wantTicketLease(t, tk, ctx)
		return running
	}
	step(a2, "after A1 finishes: flow A is first in rotation -> A2")
	step(b1, "after A2 finishes: next flow after A is B -> B1")
	step(c1, "after B1 finishes: next flow after B is C -> C1")
	step(a3, "after C1 finishes: wrap around to A -> A3 (FIFO behind A2)")
	step(b2, "after A3 finishes: next non-empty flow is B -> B2")
	if err := running.Finish(at(tm)); err != nil {
		t.Fatalf("final finish: %v", err)
	}

	_, levels := c.DebugState()
	if got := levelByName(levels, "L1").Waiters; got != 0 {
		t.Fatalf("waiters = %d, want 0", got)
	}
	t.Log("判定依据: 流内 FIFO, 流间按“由空变非空”次序轮转")
}

// TestRoundRobinReactivation verifies that a flow which becomes empty and
// later non-empty again re-joins at the end of the rotation order.
func TestRoundRobinReactivation(t *testing.T) {
	cfg := Config{
		TotalSeats: 1,
		Levels: []Level{
			{Name: "L1", Shares: 1, QueueLimit: 20, QueueTimeout: time.Hour},
		},
		Rules: []Rule{
			{Name: "all", Precedence: 1, Level: "L1", DistinguishBy: ByUser},
		},
	}
	c := mustController(t, cfg)

	running := wantLease(t, c, at(1), Request{User: "X", Seats: 1}, "X0 executes")
	a1 := wantTicket(t, c, at(2), Request{User: "A", Seats: 1}, "A1 queued (order: A)")
	b1 := wantTicket(t, c, at(3), Request{User: "B", Seats: 1}, "B1 queued (order: A, B)")

	// Finish X0 -> A1 runs; finish A1 -> B1 runs. Flow A is now empty.
	if err := running.Finish(at(4)); err != nil {
		t.Fatalf("finish: %v", err)
	}
	running = wantTicketLease(t, a1, "A1 dequeued")
	if err := running.Finish(at(5)); err != nil {
		t.Fatalf("finish: %v", err)
	}
	running = wantTicketLease(t, b1, "B1 dequeued; flow A became empty")

	// C arrives, then A re-arrives: rotation order is now B(empty), C, A.
	c1 := wantTicket(t, c, at(6), Request{User: "C", Seats: 1}, "C1 queued (order: B, C)")
	a2 := wantTicket(t, c, at(7), Request{User: "A", Seats: 1}, "A2 queued (order: B, C, A)")

	// Last served was B; next with waiters after B is C, then A.
	if err := running.Finish(at(8)); err != nil {
		t.Fatalf("finish: %v", err)
	}
	running = wantTicketLease(t, c1, "C1 dequeued before A2 (A re-joined at the end)")
	if err := running.Finish(at(9)); err != nil {
		t.Fatalf("finish: %v", err)
	}
	running = wantTicketLease(t, a2, "A2 dequeued last")
	if err := running.Finish(at(10)); err != nil {
		t.Fatalf("finish: %v", err)
	}
	t.Log("判定依据: 流重新变非空时排到轮转次序末尾")
}

// TestHeadOfLineBlocking verifies that a wide request at the head of the
// selected flow stops the whole level; nobody jumps past it.
func TestHeadOfLineBlocking(t *testing.T) {
	cfg := Config{
		TotalSeats: 5,
		Levels: []Level{
			{Name: "L1", Shares: 1, QueueLimit: 20, QueueTimeout: time.Hour},
		},
		Rules: []Rule{
			{Name: "all", Precedence: 1, Level: "L1", DistinguishBy: ByUser},
		},
	}
	c := mustController(t, cfg)

	big := wantLease(t, c, at(1), Request{User: "big", Seats: 4}, "big executes, 4/5 seats")
	small := wantLease(t, c, at(2), Request{User: "small", Seats: 1}, "small executes, 5/5 seats")
	wide := wantTicket(t, c, at(3), Request{User: "wide", Seats: 5}, "wide(5) queued: 5 occupied")
	narrow := wantTicket(t, c, at(4), Request{User: "narrow", Seats: 1}, "narrow(1) queued behind waiters")

	// Free 4 seats: wide needs 5, still does not fit; narrow must NOT be
	// served instead even though it would fit.
	if err := big.Finish(at(5)); err != nil {
		t.Fatalf("finish big: %v", err)
	}
	wantNoResult(t, wide, "wide still blocked: 1 occupied, needs 5")
	wantNoResult(t, narrow, "narrow not served: head-of-line blocking")

	// Free the last seat: wide fits now and runs; narrow follows.
	if err := small.Finish(at(6)); err != nil {
		t.Fatalf("finish small: %v", err)
	}
	wideLease := wantTicketLease(t, wide, "wide dequeued once 5 seats are free")
	wantNoResult(t, narrow, "narrow waits for wide to finish (5/5 occupied)")
	if err := wideLease.Finish(at(7)); err != nil {
		t.Fatalf("finish wide: %v", err)
	}
	narrowLease := wantTicketLease(t, narrow, "narrow dequeued after wide finished")
	if err := narrowLease.Finish(at(8)); err != nil {
		t.Fatalf("finish narrow: %v", err)
	}
	t.Log("判定依据: 只考察轮到流的队首; 队首放不下则全级别停止出队")
}

// TestQueueTimeoutBoundary verifies that waiting exactly the queue timeout
// already counts as timed out (left-closed interval).
func TestQueueTimeoutBoundary(t *testing.T) {
	cfg := Config{
		TotalSeats: 1,
		Levels: []Level{
			{Name: "L1", Shares: 1, QueueLimit: 10, QueueTimeout: 10 * time.Second},
		},
		Rules: []Rule{
			{Name: "all", Precedence: 1, Level: "L1", DistinguishBy: ByUser},
		},
	}
	c := mustController(t, cfg)

	running := wantLease(t, c, at(0), Request{User: "u0", Seats: 1}, "u0 executes")
	tk := wantTicket(t, c, at(1), Request{User: "u1", Seats: 1}, "u1 queued at t=1, timeout 10s")

	// t=10: waited 9s < 10s, still queued. Trigger maintenance with a
	// no-op config update (maintenance runs for any valid operation).
	if err := c.UpdateConfig(at(10), cfg); err != nil {
		t.Fatalf("probe update at t=10: %v", err)
	}
	wantNoResult(t, tk, "u1 alive at t=10 (waited 9s)")

	// t=11: waited exactly 10s -> timed out at the next operation.
	if err := c.UpdateConfig(at(11), cfg); err != nil {
		t.Fatalf("probe update at t=11: %v", err)
	}
	wantTicketErr(t, tk, KindQueueTimeout, "u1 timed out at t=11 (waited exactly 10s)")

	if err := running.Finish(at(12)); err != nil {
		t.Fatalf("finish: %v", err)
	}
	t.Log("判定依据: 等待时长达到超时即失效 (左闭)")
}

// TestTimeoutUnblocksDispatch verifies that evicting a blocked head-of-line
// waiter triggers dispatch of what was stuck behind it.
func TestTimeoutUnblocksDispatch(t *testing.T) {
	cfg := Config{
		TotalSeats: 2,
		Levels: []Level{
			{Name: "L1", Shares: 1, QueueLimit: 10, QueueTimeout: 5 * time.Second},
		},
		Rules: []Rule{
			{Name: "all", Precedence: 1, Level: "L1", DistinguishBy: ByUser},
		},
	}
	c := mustController(t, cfg)

	running := wantLease(t, c, at(0), Request{User: "u0", Seats: 1}, "u0 executes, 1/2 seats")
	other := wantLease(t, c, at(0), Request{User: "u1", Seats: 1}, "u1 executes, 2/2 seats")
	wide := wantTicket(t, c, at(1), Request{User: "wide", Seats: 2}, "wide(2) queued at t=1, flow wide")
	narrow := wantTicket(t, c, at(2), Request{User: "narrow", Seats: 1}, "narrow(1) queued at t=2, flow narrow")

	// Free one seat at t=3: rotation serves flow "wide" first; its head
	// needs 2 seats, only 1 free -> whole level blocked, narrow stuck too.
	if err := running.Finish(at(3)); err != nil {
		t.Fatalf("finish: %v", err)
	}
	wantNoResult(t, wide, "wide blocked: 1 free seat < 2 needed")
	wantNoResult(t, narrow, "narrow stuck behind wide (head-of-line)")

	// wide's deadline is t=6. At t=6 it times out and narrow (1 seat fits
	// in the 1 free seat) is dispatched by the same maintenance pass.
	if err := c.UpdateConfig(at(6), cfg); err != nil {
		t.Fatalf("probe update at t=6: %v", err)
	}
	wantTicketErr(t, wide, KindQueueTimeout, "wide timed out at t=6")
	narrowLease := wantTicketLease(t, narrow, "narrow dispatched after wide's eviction")
	if err := narrowLease.Finish(at(7)); err != nil {
		t.Fatalf("finish narrow: %v", err)
	}
	if err := other.Finish(at(8)); err != nil {
		t.Fatalf("finish u1: %v", err)
	}
	t.Log("判定依据: 超时失效触发同一轮出队")
}

// TestHotUpdateInFlightAndQueued verifies config replacement: in-flight
// requests keep running, queued requests keep their level but face the new
// nominal seats, and newly unsatisfiable queued requests are rejected at
// update time.
func TestHotUpdateInFlightAndQueued(t *testing.T) {
	cfg := Config{
		TotalSeats: 4,
		Levels: []Level{
			{Name: "L1", Shares: 1, QueueLimit: 10, QueueTimeout: time.Hour},
		},
		Rules: []Rule{
			{Name: "all", Precedence: 1, Level: "L1", DistinguishBy: ByUser},
		},
	}
	c := mustController(t, cfg)

	// Occupy all 4 seats; queue a 4-seat and a 1-seat request.
	running := wantLease(t, c, at(1), Request{User: "u0", Seats: 4}, "u0 executes, 4/4 seats")
	wide := wantTicket(t, c, at(2), Request{User: "wide", Seats: 4}, "wide(4) queued")
	narrow := wantTicket(t, c, at(3), Request{User: "narrow", Seats: 1}, "narrow(1) queued")

	// Shrink L1 to 2 nominal seats (total 4, shares 1:1 with a new level).
	cfg2 := Config{
		TotalSeats: 4,
		Levels: []Level{
			{Name: "L1", Shares: 1, QueueLimit: 10, QueueTimeout: time.Hour},
			{Name: "L2", Shares: 1, QueueLimit: 10, QueueTimeout: time.Hour},
		},
		Rules: []Rule{
			{Name: "all", Precedence: 1, Level: "L1", DistinguishBy: ByUser},
		},
	}
	if err := c.UpdateConfig(at(4), cfg2); err != nil {
		t.Fatalf("update: %v", err)
	}
	// wide(4) can never fit in 2 seats: rejected immediately at update.
	wantTicketErr(t, wide, KindUnsatisfiable, "wide rejected at update (4 > new nominal 2)")
	// narrow(1) still fits and keeps queueing.
	wantNoResult(t, narrow, "narrow still queued (u0 occupies 4 > 2 nominal)")

	// In-flight u0 is not interrupted; when it finishes, occupied drops
	// from 4 to 0 and narrow is dispatched under the new nominal of 2.
	if err := running.Finish(at(5)); err != nil {
		t.Fatalf("finish u0: %v", err)
	}
	narrowLease := wantTicketLease(t, narrow, "narrow dispatched after u0 finishes")
	if err := narrowLease.Finish(at(6)); err != nil {
		t.Fatalf("finish narrow: %v", err)
	}

	_, levels := c.DebugState()
	if got := levelByName(levels, "L1").Nominal; got != 2 {
		t.Fatalf("L1 nominal = %d, want 2 after update", got)
	}
	t.Log("判定依据: 在途不打断; 排队者受新名义席位约束; 不可满足者更新时即拒")
}

// TestHotUpdateRemovedLevel verifies queued requests of a removed level are
// rejected at update (new nominal is zero) while in-flight ones finish
// normally.
func TestHotUpdateRemovedLevel(t *testing.T) {
	cfg := Config{
		TotalSeats: 2,
		Levels: []Level{
			{Name: "L1", Shares: 1, QueueLimit: 10, QueueTimeout: time.Hour},
		},
		Rules: []Rule{
			{Name: "all", Precedence: 1, Level: "L1", DistinguishBy: ByUser},
		},
	}
	c := mustController(t, cfg)

	running := wantLease(t, c, at(1), Request{User: "u0", Seats: 2}, "u0 executes, 2/2")
	queued := wantTicket(t, c, at(2), Request{User: "u1", Seats: 1}, "u1 queued in L1")

	// Replace L1 with an unrelated level: L1's nominal becomes zero.
	cfg2 := Config{
		TotalSeats: 2,
		Levels: []Level{
			{Name: "L2", Shares: 1, QueueLimit: 10, QueueTimeout: time.Hour},
		},
		Rules: []Rule{
			{Name: "all", Precedence: 1, Level: "L2", DistinguishBy: ByUser},
		},
	}
	if err := c.UpdateConfig(at(3), cfg2); err != nil {
		t.Fatalf("update: %v", err)
	}
	wantTicketErr(t, queued, KindUnsatisfiable, "u1 rejected: L1 vanished, nominal 0")

	// In-flight u0 still finishes cleanly; its seats are released into the
	// defunct level state.
	if err := running.Finish(at(4)); err != nil {
		t.Fatalf("finish u0: %v", err)
	}
	// New requests go to L2 and execute.
	wantLease(t, c, at(5), Request{User: "u2", Seats: 2}, "u2 executes in L2")
	t.Log("判定依据: 被移除级别名义席位为 0, 排队者更新时即拒, 在途正常结束")
}

// TestHotUpdateInvalid verifies an invalid update leaves everything intact.
func TestHotUpdateInvalid(t *testing.T) {
	cfg := Config{
		TotalSeats: 2,
		Levels: []Level{
			{Name: "L1", Shares: 1, QueueLimit: 10, QueueTimeout: time.Hour},
		},
		Rules: []Rule{
			{Name: "all", Precedence: 1, Level: "L1", DistinguishBy: ByUser},
		},
	}
	c := mustController(t, cfg)

	bad := cfg
	bad.Rules = []Rule{{Name: "broken", Precedence: 1, Level: "no-such-level", DistinguishBy: ByUser}}
	err := c.UpdateConfig(at(1), bad)
	wantErr(t, err, KindInvalidArgument, "update with dangling level reference")

	// The failed update did not advance the clock.
	if err := c.UpdateConfig(at(1), cfg); err != nil {
		t.Fatalf("re-apply old config at t=1: %v", err)
	}
	// The old config still works.
	wantLease(t, c, at(2), Request{User: "u", Seats: 1}, "old config still in effect")
	t.Log("判定依据: 非法更新整体不生效")
}

// TestErrorPrecedence verifies that when several error conditions hold,
// only the highest-precedence one is reported.
func TestErrorPrecedence(t *testing.T) {
	cfg := Config{
		TotalSeats: 1,
		Levels: []Level{
			{Name: "L1", Shares: 1, QueueLimit: 1, QueueTimeout: time.Hour},
		},
		Rules: []Rule{
			{Name: "pods", Precedence: 1, Resources: []string{"pods"}, Level: "L1", DistinguishBy: ByUser},
		},
	}
	c := mustController(t, cfg)

	// Fill the seat and the queue so queue-full is in play.
	running := wantLease(t, c, at(1), Request{User: "u0", Resource: "pods", Seats: 1}, "u0 executes")
	wantTicket(t, c, at(2), Request{User: "u1", Resource: "pods", Seats: 1}, "u1 queued (1/1)")

	// Invalid argument beats clock skew.
	wantErrFromAdmit(t, c, at(0), Request{User: "x", Resource: "pods", Seats: 0}, KindInvalidArgument,
		"seats=0 with clock skew -> invalid argument")
	// Clock skew beats no-match.
	wantErrFromAdmit(t, c, at(0), Request{User: "x", Resource: "secrets", Seats: 1}, KindClockSkew,
		"unknown request in the past -> clock skew")
	// No-match beats unsatisfiable and queue-full.
	wantErrFromAdmit(t, c, at(3), Request{User: "x", Resource: "secrets", Seats: 10}, KindNoMatch,
		"unknown request, seats>nominal, queue full -> no match")
	// Unsatisfiable beats queue-full.
	wantErrFromAdmit(t, c, at(4), Request{User: "x", Resource: "pods", Seats: 2}, KindUnsatisfiable,
		"known request, seats>nominal, queue full -> unsatisfiable")
	// Queue-full is reported when nothing higher applies.
	wantErrFromAdmit(t, c, at(5), Request{User: "x", Resource: "pods", Seats: 1}, KindQueueFull,
		"known request, fits, queue full -> queue full")

	if err := running.Finish(at(6)); err != nil {
		t.Fatalf("finish: %v", err)
	}
	t.Log("判定依据: 参数非法 > 时钟回退 > 无匹配 > 席位不可满足 > 队列已满")
}

// TestClockSkewRejected verifies operations earlier than the last accepted
// one are rejected and change nothing.
func TestClockSkewRejected(t *testing.T) {
	cfg := Config{
		TotalSeats: 1,
		Levels: []Level{
			{Name: "L1", Shares: 1, QueueLimit: 1, QueueTimeout: time.Hour},
		},
		Rules: []Rule{
			{Name: "all", Precedence: 1, Level: "L1", DistinguishBy: ByUser},
		},
	}
	c := mustController(t, cfg)

	wantLease(t, c, at(10), Request{User: "u0", Seats: 1}, "u0 executes at t=10")
	wantErrFromAdmit(t, c, at(9), Request{User: "u1", Seats: 1}, KindClockSkew, "admit at t=9")
	// A skewed update is rejected too and the config stays.
	err := c.UpdateConfig(at(5), cfg)
	wantErr(t, err, KindClockSkew, "update at t=5")
	t.Log("判定依据: 注入时刻不得早于此前被接受操作的时刻")
}
