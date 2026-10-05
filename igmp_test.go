package ontology

import (
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

const grp = uint32(0xE0000100)

// newStd builds the switch from the spec example: P=4, ownIP=5, QI=125,
// QRI=10, Rb=2, LMQI=1, so GMI=260 and OQPI=255, no fast-leave port.
func newStd(t *testing.T, gmax, lp int, floodUnknown bool) *Switch {
	t.Helper()
	sw, err := New(4, 5, 125, 10, 2, 1, make([]bool, 4), floodUnknown, gmax, lp)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return sw
}

func gen(at int64) Query { return Query{Time: at, Kind: General} }

func specQ(at int64, group uint32, port int) Query {
	return Query{Time: at, Kind: Specific, Group: group, Port: port}
}

func normPorts(p []int) []int {
	if p == nil {
		return []int{}
	}
	return p
}

func normQueries(q []Query) []Query {
	if q == nil {
		return []Query{}
	}
	return q
}

func mustReport(t *testing.T, sw *Switch, port int, group uint32, now int64, want ...int) {
	t.Helper()
	got, err := sw.Report(port, group, now)
	if err != nil {
		t.Fatalf("Report(%d,%#x,%d): %v", port, group, now, err)
	}
	if !reflect.DeepEqual(normPorts(got), normPorts(want)) {
		t.Fatalf("Report(%d,%#x,%d)=%v, want %v", port, group, now, got, want)
	}
}

func mustLeave(t *testing.T, sw *Switch, port int, group uint32, now int64) {
	t.Helper()
	if err := sw.Leave(port, group, now); err != nil {
		t.Fatalf("Leave(%d,%#x,%d): %v", port, group, now, err)
	}
}

func mustQuery(t *testing.T, sw *Switch, port int, srcIP uint32, now int64) {
	t.Helper()
	if err := sw.Query(port, srcIP, now); err != nil {
		t.Fatalf("Query(%d,%d,%d): %v", port, srcIP, now, err)
	}
}

func mustForward(t *testing.T, sw *Switch, group uint32, inPort int, now int64, want ...int) {
	t.Helper()
	got, err := sw.Forward(group, inPort, now)
	if err != nil {
		t.Fatalf("Forward(%#x,%d,%d): %v", group, inPort, now, err)
	}
	if !reflect.DeepEqual(normPorts(got), normPorts(want)) {
		t.Fatalf("Forward(%#x,%d,%d)=%v, want %v", group, inPort, now, got, want)
	}
}

func mustDrain(t *testing.T, sw *Switch, now int64, want ...Query) {
	t.Helper()
	got, err := sw.Drain(now)
	if err != nil {
		t.Fatalf("Drain(%d): %v", now, err)
	}
	if !reflect.DeepEqual(normQueries(got), normQueries(want)) {
		t.Fatalf("Drain(%d)=%v, want %v", now, got, want)
	}
}

func mustErr(t *testing.T, err, want error, what string) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("%s: err=%v, want errors.Is %v", what, err, want)
	}
}

// TestExampleMembership replays the membership example from the spec:
// leave lowers the expiry to now+Rb*LMQI and last-member queries go out
// at now and now+LMQI; expiry is exact.
func TestExampleMembership(t *testing.T) {
	sw := newStd(t, 10, 10, false)
	mustReport(t, sw, 1, grp, 0)   // exp=260
	mustReport(t, sw, 2, grp, 100) // exp=360
	mustLeave(t, sw, 1, grp, 200)  // exp=min(260,202)=202, queries at 200,201
	mustForward(t, sw, grp, 3, 201, 1, 2)
	mustForward(t, sw, grp, 3, 202, 2)
	mustDrain(t, sw, 202, gen(0), gen(125), specQ(200, grp, 1), specQ(201, grp, 1))
}

// TestReportSameTickRefreshes covers the spec variant where a report at
// t=201 refreshes the member: the query due at 201 is still sent and the
// expiry becomes 201+GMI=461.
func TestReportSameTickRefreshes(t *testing.T) {
	sw := newStd(t, 10, 10, false)
	mustReport(t, sw, 1, grp, 0)
	mustReport(t, sw, 2, grp, 100)
	mustLeave(t, sw, 1, grp, 200)
	mustReport(t, sw, 1, grp, 201) // exp=461, cancels queries after 201
	mustDrain(t, sw, 300, gen(0), gen(125), specQ(200, grp, 1), specQ(201, grp, 1), gen(250))
	mustForward(t, sw, grp, 3, 460, 1) // port 2 expired at 360
	mustForward(t, sw, grp, 3, 461)
}

// TestLeaveNeverExtends covers the spec variant where the leave happens
// at t=259: min(260,261)=260 keeps the expiry, and the query due exactly
// at the expiry is cancelled with the membership.
func TestLeaveNeverExtends(t *testing.T) {
	sw := newStd(t, 10, 10, false)
	mustReport(t, sw, 1, grp, 0)
	mustReport(t, sw, 2, grp, 100)
	mustLeave(t, sw, 1, grp, 259) // exp stays 260, queries at 259,260
	mustForward(t, sw, grp, 3, 259, 1, 2)
	mustForward(t, sw, grp, 3, 260, 2)
	mustDrain(t, sw, 300, gen(0), gen(125), gen(250), specQ(259, grp, 1))
}

// TestExampleElection replays the querier-election example from the spec.
func TestExampleElection(t *testing.T) {
	sw := newStd(t, 10, 10, false)
	mustReport(t, sw, 1, grp, 0)
	mustReport(t, sw, 2, grp, 100)
	mustDrain(t, sw, 250, gen(0), gen(125), gen(250))
	mustQuery(t, sw, 4, 3, 300)           // 3<5: yield, oq=555, router port 4 until 555
	mustLeave(t, sw, 2, grp, 350)         // accepted but no effect: not the querier
	mustForward(t, sw, grp, 3, 359, 2, 4) // exp of port 2 untouched (360)
	mustQuery(t, sw, 4, 9, 400)           // 9>5: only router port 4 extended to 655
	mustDrain(t, sw, 500)                 // 375 and 500 suppressed
	mustReport(t, sw, 1, grp, 600, 4)
	mustReport(t, sw, 1, grp, 655) // router port 4 expired exactly at 655
	mustDrain(t, sw, 700, gen(555), gen(680))
}

// TestMemberExactExpiry: a membership is alive exactly while now < exp.
func TestMemberExactExpiry(t *testing.T) {
	sw := newStd(t, 10, 10, false)
	mustReport(t, sw, 1, grp, 0) // exp=260
	mustForward(t, sw, grp, 2, 259, 1)
	mustForward(t, sw, grp, 2, 260) // expired exactly at 260
}

// TestLeaveOnlyLowers: leave lowers the expiry to now+Rb*LMQI and never
// raises it; last-member queries due at or after the expiry are dropped.
func TestLeaveOnlyLowers(t *testing.T) {
	sw := newStd(t, 10, 10, false)
	mustReport(t, sw, 1, grp, 0)  // exp=260
	mustLeave(t, sw, 1, grp, 100) // exp=min(260,102)=102, queries at 100,101
	mustForward(t, sw, grp, 2, 101, 1)
	mustForward(t, sw, grp, 2, 102)
	mustDrain(t, sw, 102, gen(0), specQ(100, grp, 1), specQ(101, grp, 1))
}

// TestRepeatedLeaveNoReschedule: a second leave while specific queries
// are still pending neither reschedules nor changes the expiry.
func TestRepeatedLeaveNoReschedule(t *testing.T) {
	sw := newStd(t, 10, 10, false)
	mustReport(t, sw, 1, grp, 0)
	mustLeave(t, sw, 1, grp, 200) // exp=202, queries at 200,201
	mustLeave(t, sw, 1, grp, 200) // pending query at 201: no-op
	mustDrain(t, sw, 202, gen(0), gen(125), specQ(200, grp, 1), specQ(201, grp, 1))
	mustDrain(t, sw, 300, gen(250)) // nothing rescheduled
}

// TestReportCancelsRest: a report cancels the not-yet-due specific
// queries of (group, port) but the one due at the same instant is sent.
func TestReportCancelsRest(t *testing.T) {
	sw, err := New(4, 5, 125, 10, 3, 10, make([]bool, 4), false, 10, 10)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mustReport(t, sw, 1, grp, 0)   // exp=385
	mustLeave(t, sw, 1, grp, 200)  // exp=min(385,230)=230, queries at 200,210,220
	mustReport(t, sw, 1, grp, 205) // exp=590, cancels queries after 205
	mustDrain(t, sw, 300, gen(0), gen(125), specQ(200, grp, 1), gen(250))
	mustForward(t, sw, grp, 2, 589, 1)
	mustForward(t, sw, grp, 2, 590)
}

// TestFastLeave: a fast-leave port removes the membership immediately and
// schedules no last-member queries.
func TestFastLeave(t *testing.T) {
	sw, err := New(4, 5, 125, 10, 2, 1, []bool{true, false, false, false}, false, 10, 10)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mustReport(t, sw, 1, grp, 0)
	mustReport(t, sw, 2, grp, 0)
	mustLeave(t, sw, 1, grp, 50)
	mustForward(t, sw, grp, 3, 50, 2)
	mustDrain(t, sw, 100, gen(0))
}

// TestLeaveNotQuerier: while another querier owns the role, a leave is
// accepted but has no effect at all.
func TestLeaveNotQuerier(t *testing.T) {
	sw := newStd(t, 10, 10, false)
	mustQuery(t, sw, 2, 3, 10) // 3<5: yield, oq=265, router port 2 until 265
	mustReport(t, sw, 1, grp, 20, 2)
	mustLeave(t, sw, 1, grp, 30) // no effect: not the querier
	mustForward(t, sw, grp, 3, 279, 1)
	mustForward(t, sw, grp, 3, 280)
	mustDrain(t, sw, 300, gen(0), gen(265)) // recovery at oq=265
}

// TestHigherSrcIPKeepsQuerier: a higher source IP only refreshes the
// router port and does not affect the election.
func TestHigherSrcIPKeepsQuerier(t *testing.T) {
	sw := newStd(t, 10, 10, false)
	mustQuery(t, sw, 2, 9, 10) // 9>5: router port 2 until 265, still querier
	mustReport(t, sw, 1, grp, 20, 2)
	mustLeave(t, sw, 1, grp, 30) // querier: exp=min(280,32)=32, queries at 30,31
	mustForward(t, sw, grp, 3, 31, 1, 2)
	mustForward(t, sw, grp, 3, 32, 2) // membership expired exactly at 32
	mustDrain(t, sw, 50, gen(0), specQ(30, grp, 1), specQ(31, grp, 1))
}

// TestOQExactRecovery: a foreign query arriving exactly at oq sees the
// local machine already recovered (general query at oq goes out), then it
// yields again and the phase restarts at the next recovery.
func TestOQExactRecovery(t *testing.T) {
	sw, err := New(4, 5, 100, 10, 2, 1, make([]bool, 4), false, 10, 10)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// OQPI=205.
	mustQuery(t, sw, 1, 3, 10)  // yield, oq=215
	mustQuery(t, sw, 1, 3, 215) // recover at 215 (general query), yield again, oq=420
	mustDrain(t, sw, 215, gen(0), gen(215))
	mustDrain(t, sw, 500, gen(420)) // phase restarted at 420, not 300/400
}

// TestYieldCancelsSpecific: yielding cancels specific queries whose time
// has not yet come; queries due exactly at the yield instant are sent.
func TestYieldCancelsSpecific(t *testing.T) {
	sw := newStd(t, 10, 10, false)
	mustReport(t, sw, 1, grp, 0)
	mustLeave(t, sw, 1, grp, 200) // queries at 200,201
	mustQuery(t, sw, 2, 3, 201)   // yield at 201
	mustDrain(t, sw, 300, gen(0), gen(125), specQ(200, grp, 1), specQ(201, grp, 1))
	mustDrain(t, sw, 500, gen(456)) // recovery at 201+255

	sw2 := newStd(t, 10, 10, false)
	mustReport(t, sw2, 1, grp, 0)
	mustLeave(t, sw2, 1, grp, 200) // queries at 200,201
	mustQuery(t, sw2, 2, 3, 200)   // yield at 200: 200 sent, 201 cancelled
	mustDrain(t, sw2, 300, gen(0), gen(125), specQ(200, grp, 1))
}

// TestLimits: the per-port limit is reported before the group limit, and
// expired memberships count for neither.
func TestLimits(t *testing.T) {
	g2 := uint32(0xE0000200)
	sw, err := New(4, 5, 125, 10, 2, 1, make([]bool, 4), false, 1, 1)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mustReport(t, sw, 1, grp, 0) // exp=260
	_, err = sw.Report(1, g2, 10)
	mustErr(t, err, ErrPortLimit, "port limit precedes group limit")
	_, err = sw.Report(2, g2, 10)
	mustErr(t, err, ErrGroupLimit, "group limit on a fresh port")
	mustReport(t, sw, 1, grp, 20) // refresh of a live member: no limit check
	mustReport(t, sw, 1, g2, 300) // grp expired at 260: counts are zero again
	mustForward(t, sw, g2, 2, 300, 1)
}

// TestUnknownGroupFlood: unknown groups are flooded or sent to router
// ports only, depending on floodUnknown.
func TestUnknownGroupFlood(t *testing.T) {
	g2 := uint32(0xE0000200)
	sw := newStd(t, 10, 10, false)
	mustForward(t, sw, g2, 1, 0) // no members, no routers, no flood
	mustQuery(t, sw, 2, 9, 0)    // router port 2
	mustForward(t, sw, g2, 1, 0, 2)
	mustForward(t, sw, g2, 2, 0) // inPort is the only router port

	swF := newStd(t, 10, 10, true)
	mustForward(t, swF, g2, 1, 0, 2, 3, 4)
}

// TestLocalLink: link-local groups never create membership and always
// flood; reports and leaves on them are rejected.
func TestLocalLink(t *testing.T) {
	ll := uint32(0xE00000FE)
	sw := newStd(t, 10, 10, false)
	_, err := sw.Report(1, ll, 0)
	mustErr(t, err, ErrLocalLink, "report on link-local group")
	mustErr(t, sw.Leave(1, ll, 0), ErrLocalLink, "leave on link-local group")
	mustForward(t, sw, ll, 1, 0, 2, 3, 4)
	mustForward(t, sw, ll, 4, 10, 1, 2, 3)
}

// TestRejectionOrder: parameter errors first, then clock regression, then
// link-local, then non-member, then port limit, then group limit; a
// rejected operation changes neither state nor clock.
func TestRejectionOrder(t *testing.T) {
	ll := uint32(0xE00000FE)
	sw := newStd(t, 10, 10, false)
	mustReport(t, sw, 1, grp, 100) // maxNow=100

	mustErr(t, sw.Leave(0, ll, 50), ErrParam, "bad port beats clock and link-local")
	mustErr(t, sw.Leave(1, ll, 50), ErrClock, "clock beats link-local")
	mustErr(t, sw.Leave(1, ll, 100), ErrLocalLink, "link-local beats non-member")
	mustErr(t, sw.Leave(2, grp, 100), ErrNonMember, "non-member leave")

	_, err := sw.Report(0, ll, 50)
	mustErr(t, err, ErrParam, "report: bad port first")
	_, err = sw.Report(1, ll, 50)
	mustErr(t, err, ErrClock, "report: clock beats link-local")
	_, err = sw.Report(1, ll, 100)
	mustErr(t, err, ErrLocalLink, "report: link-local")
	_, err = sw.Report(1, 0xF0000000, 100)
	mustErr(t, err, ErrParam, "report: group out of range")
	_, err = sw.Report(1, grp, 99)
	mustErr(t, err, ErrClock, "report: clock regression")

	mustErr(t, sw.Query(1, 0, 100), ErrParam, "query: zero source")
	mustErr(t, sw.Query(1, 5, 100), ErrParam, "query: own source")
	mustErr(t, sw.Query(1, 3, 99), ErrClock, "query: clock regression")

	_, err = sw.Drain(99)
	mustErr(t, err, ErrClock, "drain: clock regression")
	_, err = sw.Drain(1e12 + 1)
	mustErr(t, err, ErrParam, "drain: time out of range")

	_, err = sw.Forward(grp, 0, 100)
	mustErr(t, err, ErrParam, "forward: bad port")
	_, err = sw.Forward(grp, 5, 100)
	mustErr(t, err, ErrParam, "forward: port out of range")
	_, err = sw.Forward(grp, 1, 99)
	mustErr(t, err, ErrClock, "forward: clock regression")

	mustReport(t, sw, 2, grp, 100) // clock untouched by the rejections
	mustDrain(t, sw, 100, gen(0))  // state intact
}

// TestTouchedBound: Forward touches at most the group's member records
// plus the router ports plus one, independent of the total group count.
func TestTouchedBound(t *testing.T) {
	base := uint32(0xE0001000)
	for _, n := range []int{10, 10000} {
		sw, err := New(4, 100, 125, 10, 2, 1, make([]bool, 4), false, 20000, 20000)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		for i := 0; i < n; i++ {
			mustReport(t, sw, 1, base+uint32(i), 0)
		}
		mustQuery(t, sw, 2, 50, 0) // router port 2
		mustForward(t, sw, base+5, 3, 0, 1, 2)
		if sw.touched > 1+1+1 {
			t.Fatalf("groups=%d: touched=%d, want <= members(1)+routers(1)+1", n, sw.touched)
		}
		t.Logf("groups=%d touched=%d (members=1 routers=1)", n, sw.touched)
	}
}

// TestNewValidation: constructor parameter checking.
func TestNewValidation(t *testing.T) {
	fl := make([]bool, 4)
	good := []any{4, uint32(5), int64(125), int64(10), int64(2), int64(1), fl, false, 10, 10}
	call := func(a []any) error {
		_, err := New(a[0].(int), a[1].(uint32), a[2].(int64), a[3].(int64), a[4].(int64), a[5].(int64),
			a[6].([]bool), a[7].(bool), a[8].(int), a[9].(int))
		return err
	}
	if err := call(good); err != nil {
		t.Fatalf("valid params rejected: %v", err)
	}
	bads := [][]any{
		{0, uint32(5), int64(125), int64(10), int64(2), int64(1), fl, false, 10, 10},
		{257, uint32(5), int64(125), int64(10), int64(2), int64(1), make([]bool, 257), false, 10, 10},
		{4, uint32(0), int64(125), int64(10), int64(2), int64(1), fl, false, 10, 10},
		{4, uint32(5), int64(0), int64(10), int64(2), int64(1), fl, false, 10, 10},
		{4, uint32(5), int64(125), int64(125), int64(2), int64(1), fl, false, 10, 10},
		{4, uint32(5), int64(125), int64(10), int64(0), int64(1), fl, false, 10, 10},
		{4, uint32(5), int64(125), int64(10), int64(8), int64(1), fl, false, 10, 10},
		{4, uint32(5), int64(125), int64(10), int64(2), int64(0), fl, false, 10, 10},
		{4, uint32(5), int64(125), int64(10), int64(2), int64(1), make([]bool, 3), false, 10, 10},
		{4, uint32(5), int64(125), int64(10), int64(2), int64(1), fl, false, 0, 10},
		{4, uint32(5), int64(125), int64(10), int64(2), int64(1), fl, false, 10, 0},
	}
	for i, b := range bads {
		if err := call(b); !errors.Is(err, ErrParam) {
			t.Fatalf("bad params #%d: err=%v, want ErrParam", i, err)
		}
	}
}

// TestConcurrent: operations from many goroutines behave as some serial
// order; run with -race.
func TestConcurrent(t *testing.T) {
	sw, err := New(8, 5, 50, 10, 2, 1, make([]bool, 8), true, 64, 4)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var clock int64
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				now := atomic.AddInt64(&clock, 1)
				port := 1 + (w+i)%8
				group := grp + uint32(i%16)*0x100
				switch i % 5 {
				case 0:
					_, _ = sw.Report(port, group, now)
				case 1:
					_ = sw.Leave(port, group, now)
				case 2:
					_ = sw.Query(port, uint32(1+i%12), now)
				case 3:
					_, _ = sw.Forward(group, port, now)
				case 4:
					_, _ = sw.Drain(now)
				}
			}
		}(w)
	}
	wg.Wait()
	if _, err := sw.Drain(atomic.LoadInt64(&clock)); err != nil {
		t.Fatalf("final Drain: %v", err)
	}
}
