package fence

import (
	"sync"
	"sync/atomic"
	"testing"
)

// rings builds a footprint with cells (0,0)..(n-1,0) carrying ring 0..n-1.
func rings(n int64) map[Cell]int {
	m := make(map[Cell]int, n)
	for i := int64(0); i < n; i++ {
		m[Cell{X: i}] = int(i)
	}
	return m
}

func mustOK(t *testing.T, err error, ctx string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error: %v", ctx, err)
	}
}

func wantCode(t *testing.T, err error, want ErrorCode, ctx string) {
	t.Helper()
	if Code(err) != want {
		t.Fatalf("%s: want code %d, got %d (%v)", ctx, want, Code(err), err)
	}
}

// 1. A cell whose ring equals the effective radius is still reachable.
func TestRingEqualsEffectiveRadius(t *testing.T) {
	s := NewSystem()
	mustOK(t, s.RegisterMerchant(0, "m", "r", rings(4)), "register")
	mustOK(t, s.AddPlatformEvent(1, "e", "r", 0, 10, 2), "event")
	rc, err := s.QueryReachable(2, "m", Cell{X: 1})
	mustOK(t, err, "query ring1")
	if rc != Reachable {
		t.Fatalf("ring 1 at effective radius 1 must be reachable, got %d", rc)
	}
	rc, err = s.QueryReachable(2, "m", Cell{X: 2})
	mustOK(t, err, "query ring2")
	if rc != ShrunkTemporarily {
		t.Fatalf("ring 2 must be shrunk, got %d", rc)
	}
}

// 2. At exactly the right endpoint the half-open event no longer counts.
func TestEventRightEndpoint(t *testing.T) {
	s := NewSystem()
	mustOK(t, s.RegisterMerchant(0, "m", "r", rings(4)), "register")
	mustOK(t, s.AddPlatformEvent(0, "e", "r", 0, 5, 2), "event")
	rc, _ := s.QueryReachable(4, "m", Cell{X: 2})
	if rc != ShrunkTemporarily {
		t.Fatalf("t=4 inside [0,5): shrunk expected, got %d", rc)
	}
	rc, _ = s.QueryReachable(5, "m", Cell{X: 2})
	if rc != Reachable {
		t.Fatalf("t=5 at right endpoint: reachable expected, got %d", rc)
	}
}

// 3. Nested and overlapping events independently contribute their maximum.
func TestNestedOverlappingMax(t *testing.T) {
	s := NewSystem()
	mustOK(t, s.RegisterMerchant(0, "m", "r", rings(6)), "register")
	mustOK(t, s.AddPlatformEvent(0, "outer", "r", 0, 20, 1), "outer")
	mustOK(t, s.AddPlatformEvent(0, "nested", "r", 4, 12, 4), "nested")
	mustOK(t, s.AddPlatformEvent(0, "overlap", "r", 10, 30, 1), "overlap")
	cases := []struct {
		t     int64
		level int
	}{
		{2, 1}, {5, 4}, {11, 4}, {13, 1}, {25, 1}, {30, 0},
	}
	for _, c := range cases {
		rc, err := s.QueryReachable(c.t, "m", Cell{X: int64(5 - c.level)})
		mustOK(t, err, "query boundary")
		if rc != Reachable {
			t.Fatalf("t=%d level=%d: boundary cell should be reachable, got %d", c.t, c.level, rc)
		}
		if c.level > 0 {
			rc, _ = s.QueryReachable(c.t, "m", Cell{X: int64(5-c.level) + 1})
			if rc != ShrunkTemporarily {
				t.Fatalf("t=%d level=%d: one ring outside should be shrunk", c.t, c.level)
			}
		}
	}
}

// 4. After early termination the level falls back immediately.
func TestEarlyTerminationFalloff(t *testing.T) {
	s := NewSystem()
	mustOK(t, s.RegisterMerchant(0, "m", "r", rings(6)), "register")
	mustOK(t, s.AddPlatformEvent(0, "hi", "r", 0, 100, 4), "hi")
	mustOK(t, s.AddPlatformEvent(0, "lo", "r", 0, 100, 2), "lo")
	rc, _ := s.QueryReachable(5, "m", Cell{X: 2})
	if rc != ShrunkTemporarily {
		t.Fatalf("level 4: ring 2 shrunk expected")
	}
	mustOK(t, s.TerminateEvent(6, "hi", 6), "terminate hi")
	rc, _ = s.QueryReachable(6, "m", Cell{X: 3})
	if rc != Reachable {
		t.Fatalf("after terminate, ring 3 must be reachable at level 2")
	}
	rc, _ = s.QueryReachable(6, "m", Cell{X: 4})
	if rc != ShrunkTemporarily {
		t.Fatalf("after terminate, ring 4 must be shrunk at level 2")
	}
}

// 5. Effective level is max(covering platform events, merchant level).
func TestMerchantPlatformMax(t *testing.T) {
	s := NewSystem()
	mustOK(t, s.RegisterMerchant(0, "m", "r", rings(6)), "register")
	mustOK(t, s.AddPlatformEvent(0, "p", "r", 0, 100, 3), "platform")
	mustOK(t, s.SetMerchantLevel(1, "m", 1), "merchant low")
	rc, _ := s.QueryReachable(2, "m", Cell{X: 2})
	if rc != Reachable {
		t.Fatalf("platform 3 dominates merchant 1")
	}
	mustOK(t, s.SetMerchantLevel(10, "m", 5), "merchant high")
	rc, _ = s.QueryReachable(10, "m", Cell{X: 1})
	if rc != ShrunkTemporarily {
		t.Fatalf("merchant 5 dominates platform 3")
	}
}

// 6. Radius clamped to zero leaves only ring-zero cells reachable.
func TestRadiusClampedZero(t *testing.T) {
	s := NewSystem()
	mustOK(t, s.RegisterMerchant(0, "m", "r", rings(3)), "register")
	mustOK(t, s.SetMerchantLevel(0, "m", 9), "huge level")
	rc, _ := s.QueryReachable(1, "m", Cell{X: 0})
	if rc != Reachable {
		t.Fatalf("ring 0 must stay reachable even when level > base radius")
	}
	rc, _ = s.QueryReachable(1, "m", Cell{X: 1})
	if rc != ShrunkTemporarily {
		t.Fatalf("ring 1 must be shrunk when radius clamped to 0")
	}
}

// 7. Reachable at placement, shrunk at accept: reject + auto-cancel.
func TestAcceptShrunkAutoCancel(t *testing.T) {
	s := NewSystem()
	mustOK(t, s.RegisterMerchant(0, "m", "r", rings(5)), "register")
	mustOK(t, s.PlaceOrder(1, "o", "m", Cell{X: 3}), "place")
	mustOK(t, s.AddPlatformEvent(2, "e", "r", 0, 100, 3), "shrink after place")
	err := s.AcceptOrder(3, "o")
	wantCode(t, err, ErrTemporarilyUnreachable, "accept during shrink")
	info, _ := s.GetOrder("o")
	if info.Status != OrderCancelled || info.CancelReason != CancelledByShrink {
		t.Fatalf("order must be cancelled by shrink, got status=%d reason=%d", info.Status, info.CancelReason)
	}
	wantCode(t, s.AcceptOrder(4, "o"), ErrOrderCancelled, "re-accept cancelled")
	wantCode(t, s.DeliverOrder(4, "o"), ErrOrderCancelled, "deliver cancelled")
}

// 8. An accepted order is immune to every later shrink.
func TestAcceptedOrderImmune(t *testing.T) {
	s := NewSystem()
	mustOK(t, s.RegisterMerchant(0, "m", "r", rings(5)), "register")
	mustOK(t, s.PlaceOrder(1, "o", "m", Cell{X: 4}), "place")
	mustOK(t, s.AcceptOrder(2, "o"), "accept")
	mustOK(t, s.AddPlatformEvent(3, "e", "r", 0, 100, 4), "shrink")
	mustOK(t, s.SetMerchantLevel(4, "m", 4), "merchant shrink")
	mustOK(t, s.DeliverOrder(5, "o"), "deliver despite shrink")
	info, _ := s.GetOrder("o")
	if info.Status != OrderDelivered || info.Cell != (Cell{X: 4}) {
		t.Fatalf("accepted order must keep original cell and deliver")
	}
}

// 9. A rejected reroute keeps the original address; a success works once.
func TestRerouteRejectedKeepsAddress(t *testing.T) {
	s := NewSystem()
	mustOK(t, s.RegisterMerchant(0, "m", "r", rings(5)), "register")
	mustOK(t, s.PlaceOrder(1, "o", "m", Cell{X: 1}), "place")
	mustOK(t, s.AcceptOrder(2, "o"), "accept")
	mustOK(t, s.AddPlatformEvent(3, "e", "r", 0, 100, 3), "shrink")
	wantCode(t, s.RerouteOrder(4, "o", Cell{X: 3}), ErrTemporarilyUnreachable, "reroute into shrunk ring")
	info, _ := s.GetOrder("o")
	if info.Cell != (Cell{X: 1}) || info.Rerouted {
		t.Fatalf("rejected reroute must keep original address and flag clear")
	}
	mustOK(t, s.TerminateEvent(5, "e", 5), "lift shrink")
	mustOK(t, s.RerouteOrder(6, "o", Cell{X: 2}), "reroute into reachable ring")
	info, _ = s.GetOrder("o")
	if info.Cell != (Cell{X: 2}) || !info.Rerouted || info.Status != OrderAccepted {
		t.Fatalf("successful reroute changes cell once, keeps accepted state")
	}
	wantCode(t, s.RerouteOrder(7, "o", Cell{X: 0}), ErrOrderRerouted, "second reroute")
	// Reroute before acceptance is a state error; rerouting a delivered order
	// reports delivered first.
	mustOK(t, s.PlaceOrder(8, "p", "m", Cell{X: 0}), "place pending")
	wantCode(t, s.RerouteOrder(9, "p", Cell{X: 0}), ErrOrderNotAccepted, "reroute pending")
	mustOK(t, s.AcceptOrder(10, "p"), "accept p")
	mustOK(t, s.DeliverOrder(11, "p"), "deliver p")
	wantCode(t, s.RerouteOrder(12, "p", Cell{X: 0}), ErrOrderDelivered, "reroute delivered")
}

// 10-12. Recovery query: reachable now, scheduled recovery, no recovery;
// nested events recover at the earliest boundary that lowers the max enough.
func TestNextRecoveryThreeCases(t *testing.T) {
	s := NewSystem()
	mustOK(t, s.RegisterMerchant(0, "m", "r", rings(6)), "register")
	info, err := s.QueryNextRecovery(1, "m", Cell{X: 2})
	mustOK(t, err, "recovery query")
	if !info.Reachable || info.Next != 1 {
		t.Fatalf("reachable now: %+v", info)
	}
	mustOK(t, s.AddPlatformEvent(2, "p", "r", 0, 10, 3), "platform shrink")
	info, _ = s.QueryNextRecovery(3, "m", Cell{X: 4})
	if info.Reachable || info.NoRecovery || info.Next != 10 {
		t.Fatalf("scheduled recovery at 10 expected, got %+v", info)
	}
	mustOK(t, s.TerminateEvent(4, "p", 4), "end platform early")
	mustOK(t, s.SetMerchantLevel(5, "m", 3), "merchant blocks")
	info, _ = s.QueryNextRecovery(6, "m", Cell{X: 1})
	if !info.Reachable {
		t.Fatalf("ring 1 still reachable with level 3")
	}
	info, _ = s.QueryNextRecovery(6, "m", Cell{X: 4})
	if !info.NoRecovery || info.Reachable {
		t.Fatalf("merchant level blocks ring 4: no recovery, got %+v", info)
	}
	// Nested high event ends at 8, lower one at 10: recovery at 8.
	s2 := NewSystem()
	mustOK(t, s2.RegisterMerchant(0, "m2", "r", rings(6)), "register2")
	mustOK(t, s2.AddPlatformEvent(0, "lo", "r", 0, 10, 2), "lo")
	mustOK(t, s2.AddPlatformEvent(0, "hi", "r", 0, 8, 3), "hi")
	ri, _ := s2.QueryNextRecovery(1, "m2", Cell{X: 3})
	if ri.NoRecovery || ri.Next != 8 {
		t.Fatalf("nested events: ring 3 recovers when hi(level 3) ends at 8, got %+v", ri)
	}
}

// 13. Permanent-outside and temporary-shrink are distinguishable.
func TestUnreachableReasonsDistinct(t *testing.T) {
	s := NewSystem()
	mustOK(t, s.RegisterMerchant(0, "m", "r", rings(4)), "register")
	rc, err := s.QueryReachable(1, "m", Cell{X: 99})
	mustOK(t, err, "query outside")
	if rc != OutsideForever {
		t.Fatalf("cell never registered must classify as OutsideForever, got %d", rc)
	}
	mustOK(t, s.SetMerchantLevel(2, "m", 3), "shrink to ring 0")
	rc, _ = s.QueryReachable(3, "m", Cell{X: 2})
	if rc != ShrunkTemporarily {
		t.Fatalf("known cell must classify as ShrunkTemporarily, got %d", rc)
	}
	wantCode(t, s.PlaceOrder(4, "a", "m", Cell{X: 99}), ErrOutsideForever, "place outside")
	wantCode(t, s.PlaceOrder(4, "b", "m", Cell{X: 2}), ErrTemporarilyUnreachable, "place shrunk")
}

// Rejection precedence and monotonic clock; rejected calls change nothing.
func TestRejectionOrderAndClock(t *testing.T) {
	s := NewSystem()
	mustOK(t, s.RegisterMerchant(10, "m", "r", rings(3)), "register at 10")
	wantCode(t, s.SetMerchantLevel(3, "", 1), ErrInvalidArgument, "bad arg beats rollback")
	wantCode(t, s.SetMerchantLevel(3, "m", 1), ErrClockRollback, "rollback beats not-found")
	wantCode(t, s.SetMerchantLevel(11, "nope", 1), ErrMerchantNotFound, "missing merchant")
	mustOK(t, s.AddPlatformEvent(12, "e", "r", 0, 20, 1), "event")
	wantCode(t, s.TerminateEvent(13, "nope", 13), ErrEventNotFound, "missing event")
	mustOK(t, s.TerminateEvent(14, "e", 14), "terminate")
	wantCode(t, s.TerminateEvent(15, "e", 15), ErrEventTerminated, "double terminate")
	mustOK(t, s.PlaceOrder(16, "o", "m", Cell{X: 0}), "place")
	wantCode(t, s.AcceptOrder(17, "nope"), ErrOrderNotFound, "missing order")
	mustOK(t, s.AcceptOrder(18, "o"), "accept")
	wantCode(t, s.AcceptOrder(19, "o"), ErrOrderAccepted, "double accept")
	mustOK(t, s.DeliverOrder(20, "o"), "deliver")
	wantCode(t, s.DeliverOrder(21, "o"), ErrOrderDelivered, "double deliver")
	// A rejected call at t=9 must not move the clock backwards requirement.
	_ = s.SetMerchantLevel(9, "m", 1)
	mustOK(t, s.SetMerchantLevel(21, "m", 0), "clock still at 21")
}

// Concurrent calls are equivalent to some serial order. Each goroutine draws
// a fresh increasing timestamp from an atomic counter and retries on clock
// rollback, modelling a client that re-submits with a current timestamp; the
// resulting committed history is some valid serial interleaving.
func TestConcurrentSerializability(t *testing.T) {
	s := NewSystem()
	mustOK(t, s.RegisterMerchant(0, "m", "r", rings(3)), "register")
	var tick atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// All cells are rings 0..2 with no shrink, so always reachable.
			cell := Cell{X: int64(i % 3)}
			id := orderName(i)
			for err := s.PlaceOrder(tick.Add(1), id, "m", cell); err != nil; err = s.PlaceOrder(tick.Add(1), id, "m", cell) {
				if Code(err) != ErrClockRollback {
					t.Errorf("place %s: unexpected error: %v", id, err)
					return
				}
			}
			for err := s.AcceptOrder(tick.Add(1), id); err != nil; err = s.AcceptOrder(tick.Add(1), id) {
				if Code(err) != ErrClockRollback {
					t.Errorf("accept %s: unexpected error: %v", id, err)
					return
				}
			}
		}(i)
	}
	wg.Wait()
	for i := 0; i < 40; i++ {
		info, err := s.GetOrder(orderName(i))
		if err != nil || info.Status != OrderAccepted {
			t.Fatalf("order %s inconsistent: %+v err=%v", orderName(i), info, err)
		}
	}
}

func orderName(i int) string {
	return "o" + string(rune('a'+i/26)) + string(rune('a'+i%26))
}

// Terminating an event before its start truncates the interval on promotion.
func TestTerminateBeforeStart(t *testing.T) {
	s := NewSystem()
	mustOK(t, s.RegisterMerchant(0, "m", "r", rings(5)), "register")
	mustOK(t, s.AddPlatformEvent(0, "e", "r", 10, 20, 4), "future event")
	mustOK(t, s.TerminateEvent(1, "e", 15), "truncate to [10,15)")
	rc, _ := s.QueryReachable(5, "m", Cell{X: 2})
	if rc != Reachable {
		t.Fatalf("before start event has no effect")
	}
	rc, _ = s.QueryReachable(12, "m", Cell{X: 2})
	if rc != ShrunkTemporarily {
		t.Fatalf("live in truncated [10,15)")
	}
	ri, _ := s.QueryNextRecovery(12, "m", Cell{X: 2})
	if ri.NoRecovery || ri.Next != 15 {
		t.Fatalf("recovery at 15, got %+v", ri)
	}
	// Read-ahead on a private copy must leave the current level intact.
	rc, _ = s.QueryReachable(13, "m", Cell{X: 2})
	if rc != ShrunkTemporarily {
		t.Fatalf("primary index intact: still shrunk at 13, got %d", rc)
	}
	rc, _ = s.QueryReachable(15, "m", Cell{X: 2})
	if rc != Reachable {
		t.Fatalf("released at truncation boundary 15")
	}
}

// A far-future recovery read-ahead must not corrupt current effective level.
func TestRecoveryReadAheadIsolation(t *testing.T) {
	s := NewSystem()
	mustOK(t, s.RegisterMerchant(0, "m", "r", rings(6)), "register")
	mustOK(t, s.AddPlatformEvent(0, "long", "r", 0, 100, 4), "long event")
	ri, _ := s.QueryNextRecovery(5, "m", Cell{X: 4})
	if ri.NoRecovery || ri.Next != 100 {
		t.Fatalf("expected recovery at 100, got %+v", ri)
	}
	wantCode(t, s.PlaceOrder(6, "o", "m", Cell{X: 4}), ErrTemporarilyUnreachable, "place after read-ahead")
	mustOK(t, s.PlaceOrder(7, "p", "m", Cell{X: 1}), "place ring 1 reachable")
}

// Query cost must not depend on footprint size or finished-event count:
// after all events end, their heap entries are fully pruned.
func TestPerformanceConstant(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	for _, n := range []int{100, 10_000, 100_000} {
		s := NewSystem()
		cells := make(map[Cell]int, n)
		for i := 0; i < n; i++ {
			cells[Cell{X: int64(i)}] = i
		}
		mustOK(t, s.RegisterMerchant(0, "m", "r", cells), "register")
		for i := 0; i < 2000; i++ {
			base := int64(i * 10)
			mustOK(t, s.AddPlatformEvent(base, "e"+itoa(i), "r", base, base+5, 3), "event")
		}
		rc, err := s.QueryReachable(20_000, "m", Cell{X: int64(n - 1)})
		if err != nil || rc != Reachable {
			t.Fatalf("n=%d: far ring reachable with no live events, rc=%d err=%v", n, rc, err)
		}
		rs := s.engine.region("r")
		if rs.starts.Len() != 0 || rs.ends.Len() != 0 {
			t.Fatalf("n=%d: finished events must be pruned, starts=%d ends=%d", n, rs.starts.Len(), rs.ends.Len())
		}
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
