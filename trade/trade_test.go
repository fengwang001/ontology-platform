package trade_test

import (
	"testing"

	"ontology/inventory"
	"ontology/trade"
)

func newSys(t *testing.T, r, goldCap, slots, ttl, maxOpen int64) *trade.System {
	t.Helper()
	s, err := trade.New(r, goldCap, slots, ttl, maxOpen)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func mustErr(t *testing.T, got, want error) {
	t.Helper()
	if got != want {
		t.Fatalf("got error %v, want %v", got, want)
	}
}

func mustOpen(t *testing.T, s *trade.System, now int64, a, b string) int64 {
	t.Helper()
	sid, err := s.Open(now, a, b)
	if err != nil {
		t.Fatalf("Open(%d, %s, %s): %v", now, a, b, err)
	}
	return sid
}

func TestNewValidation(t *testing.T) {
	cases := []struct {
		name                            string
		r, goldCap, slots, ttl, maxOpen int64
		wantErr                         bool
	}{
		{"ok-min", 0, 1, 1, 1, 1, false},
		{"ok-max", 1000, 1_000_000_000_000_000, 10_000, 1_000_000_000, 10_000, false},
		{"r-negative", -1, 1, 1, 1, 1, true},
		{"r-too-high", 1001, 1, 1, 1, 1, true},
		{"cap-zero", 0, 0, 1, 1, 1, true},
		{"cap-too-high", 0, 1_000_000_000_000_001, 1, 1, 1, true},
		{"slots-zero", 0, 1, 0, 1, 1, true},
		{"slots-too-high", 0, 1, 10_001, 1, 1, true},
		{"ttl-zero", 0, 1, 1, 0, 1, true},
		{"ttl-too-high", 0, 1, 1, 1_000_000_001, 1, true},
		{"q-zero", 0, 1, 1, 1, 0, true},
		{"q-too-high", 0, 1, 1, 1, 10_001, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := trade.New(c.r, c.goldCap, c.slots, c.ttl, c.maxOpen)
			if c.wantErr {
				mustErr(t, err, trade.ErrParam)
			} else {
				mustOK(t, err)
			}
		})
	}
}

// TestTaxRounding covers ceil(g*r/1000) including r=0 and r=1000 and the
// worked examples from the spec (r=50: 101->6, 100->5, 1->1, 0->0).
func TestTaxRounding(t *testing.T) {
	cases := []struct {
		name              string
		r, gold           int64
		wantTax, wantRecv int64
	}{
		{"r50-g101", 50, 101, 6, 95},
		{"r50-g100", 50, 100, 5, 95},
		{"r50-g1", 50, 1, 1, 0},
		{"r50-g0", 50, 0, 0, 0},
		{"r0-g999", 0, 999, 0, 999},
		{"r1000-g777", 1000, 777, 777, 0},
		{"r1-g1", 1, 1, 1, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newSys(t, c.r, 1_000_000, 10, 1_000_000, 10)
			mustOK(t, s.Grant("a", "sword", 1))
			mustOK(t, s.GrantGold("b", 1000))
			sid := mustOpen(t, s, 0, "a", "b")
			mustOK(t, s.Offer(1, sid, "a", map[string]int64{"sword": 1}, 0))
			mustOK(t, s.Offer(2, sid, "b", nil, c.gold))
			mustOK(t, s.Confirm(3, sid, "a", 2))
			mustOK(t, s.Confirm(4, sid, "b", 2))
			if got := s.Gold("a"); got != c.wantRecv {
				t.Fatalf("a gold = %d, want %d", got, c.wantRecv)
			}
			if got := s.Gold("b"); got != 1000-c.gold {
				t.Fatalf("b gold = %d, want %d", got, 1000-c.gold)
			}
			if got := s.Burned(); got != c.wantTax {
				t.Fatalf("burned = %d, want %d", got, c.wantTax)
			}
			if got := s.Qty("b", "sword"); got != 1 {
				t.Fatalf("b sword = %d, want 1", got)
			}
		})
	}
}

// TestRejectionOrder pins the documented rejection precedence with
// operations that violate several rules at once.
func TestRejectionOrder(t *testing.T) {
	s := newSys(t, 0, 100, 4, 1000, 2)
	mustOK(t, s.Grant("a", "x", 10))
	mustOK(t, s.Grant("b", "y", 10))
	sid := mustOpen(t, s, 100, "a", "b") // clock now 100

	cases := []struct {
		name string
		got  error
		want error
	}{
		// Open: param > clock > no-player > session limit.
		{"open param>clock", errOf(func() error { _, e := s.Open(50, "a", "a"); return e }), trade.ErrParam},
		{"open clock>noplayer", errOf(func() error { _, e := s.Open(50, "ghost", "b"); return e }), trade.ErrClock},
		{"open noplayer", errOf(func() error { _, e := s.Open(150, "ghost", "b"); return e }), trade.ErrNoPlayer},
		// Offer: param > clock > no-session > not-member > insufficient.
		{"offer param>clock", s.Offer(50, sid, "a", map[string]int64{"x": 0}, 0), trade.ErrParam},
		{"offer param>clock bad gold", s.Offer(50, sid, "a", nil, 101), trade.ErrParam},
		{"offer clock>nosession", s.Offer(50, 999, "a", nil, 0), trade.ErrClock},
		{"offer nosession>notmember", s.Offer(150, 999, "ghost", nil, 0), trade.ErrNoSession},
		{"offer notmember>insufficient", s.Offer(150, sid, "ghost", map[string]int64{"x": 100}, 0), trade.ErrNotMember},
		{"offer insufficient", s.Offer(150, sid, "a", map[string]int64{"x": 100}, 0), trade.ErrInsufficient},
		// Confirm: param > clock > no-session > not-member > stale > confirmed > empty.
		{"confirm param>clock", s.Confirm(50, sid, "a", -1), trade.ErrParam},
		{"confirm clock>nosession", s.Confirm(50, 999, "a", 0), trade.ErrClock},
		{"confirm nosession>notmember", s.Confirm(150, 999, "ghost", 0), trade.ErrNoSession},
		{"confirm notmember>stale", s.Confirm(150, sid, "ghost", 99), trade.ErrNotMember},
		{"confirm empty trade", s.Confirm(150, sid, "a", 0), trade.ErrEmptyTrade},
		// Cancel: param > clock > no-session > not-member.
		{"cancel param>clock", s.Cancel(50, 0, "a"), trade.ErrParam},
		{"cancel clock>nosession", s.Cancel(50, 999, "a"), trade.ErrClock},
		{"cancel nosession>notmember", s.Cancel(150, 999, "ghost"), trade.ErrNoSession},
		{"cancel notmember", s.Cancel(150, sid, "ghost"), trade.ErrNotMember},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { mustErr(t, c.got, c.want) })
	}

	// stale > already-confirmed: a offers (ver=1) and confirms, then
	// confirming again with a wrong version reports stale, with the
	// current version reports already-confirmed.
	mustOK(t, s.Offer(160, sid, "a", map[string]int64{"x": 1}, 0))
	mustOK(t, s.Confirm(161, sid, "a", 1))
	mustErr(t, s.Confirm(162, sid, "a", 5), trade.ErrStale)
	mustErr(t, s.Confirm(162, sid, "a", 1), trade.ErrAlreadyConfirmed)
}

func errOf(f func() error) error { return f() }

// TestGrants covers inventory grant limits and player registration.
func TestGrants(t *testing.T) {
	s := newSys(t, 0, 10, 1, 1000, 2)
	mustOK(t, s.Grant("a", "x", 5))
	mustErr(t, s.Grant("a", "y", 1), inventory.ErrSlotLimit)
	mustOK(t, s.Grant("a", "x", 5)) // same kind, no new slot
	mustErr(t, s.GrantGold("a", 11), inventory.ErrGoldCap)
	mustOK(t, s.GrantGold("a", 10))
	mustErr(t, s.Grant("a", "", 1), inventory.ErrParam)
	mustErr(t, s.Grant("a", "x", 0), inventory.ErrParam)
	mustErr(t, s.GrantGold("a", -1), inventory.ErrParam)
	// A rejected grant does not register the player.
	mustErr(t, s.GrantGold("ghost", 11), inventory.ErrGoldCap)
	_, err := s.Open(0, "ghost", "a")
	mustErr(t, err, trade.ErrNoPlayer)
	// GrantGold(0) registers the player.
	mustOK(t, s.GrantGold("b", 0))
	mustOpen(t, s, 1, "a", "b")
}

// TestVersionAndConfirmClearing: an Offer bumps the version and clears
// both confirmations, even when the new quote equals the old one.
func TestVersionAndConfirmClearing(t *testing.T) {
	s := newSys(t, 0, 1000, 4, 100_000, 10)
	mustOK(t, s.Grant("a", "x", 10))
	mustOK(t, s.Grant("b", "y", 10))
	sid := mustOpen(t, s, 0, "a", "b")
	mustOK(t, s.Offer(1, sid, "a", map[string]int64{"x": 1}, 0)) // ver 1
	mustOK(t, s.Offer(2, sid, "b", map[string]int64{"y": 2}, 0)) // ver 2
	mustOK(t, s.Confirm(3, sid, "b", 2))
	// Identical re-offer still bumps the version and clears confirmations.
	mustOK(t, s.Offer(4, sid, "a", map[string]int64{"x": 1}, 0)) // ver 3
	if v, _ := s.Version(sid); v != 3 {
		t.Fatalf("ver = %d, want 3", v)
	}
	if conf, _ := s.Confirmed(sid, "b"); conf {
		t.Fatal("b confirmation should have been cleared")
	}
	mustErr(t, s.Confirm(5, sid, "b", 2), trade.ErrStale)
	mustOK(t, s.Confirm(6, sid, "b", 3))
	mustOK(t, s.Confirm(7, sid, "a", 3))
	if got := s.Qty("b", "x"); got != 1 {
		t.Fatalf("b x = %d, want 1", got)
	}
	if got := s.Qty("a", "y"); got != 2 {
		t.Fatalf("a y = %d, want 2", got)
	}
}

// TestExpiry: deadlines are inclusive (now >= expiry cancels), an Offer
// refreshes the deadline, and expired locks are released.
func TestExpiry(t *testing.T) {
	s := newSys(t, 0, 1000, 4, 1000, 10)
	mustOK(t, s.Grant("a", "x", 5))
	mustOK(t, s.GrantGold("b", 100))
	mustOK(t, s.GrantGold("c", 0))
	sid := mustOpen(t, s, 0, "a", "b")                             // expires at 1000
	mustOK(t, s.Offer(400, sid, "a", map[string]int64{"x": 2}, 0)) // expires 1400
	mustOK(t, s.Offer(500, sid, "b", nil, 50))                     // expires 1500
	mustOK(t, s.Confirm(1499, sid, "a", 2))
	mustErr(t, s.Confirm(1500, sid, "b", 2), trade.ErrNoSession) // 取等过期
	// The rejected confirm neither advanced the clock nor released the
	// locks: at the committed time 1499 the session is still valid.
	if got := s.LockedQty("a", "x"); got != 2 {
		t.Fatalf("a locked x = %d, want 2 (rejected op changes nothing)", got)
	}
	if _, err := s.Open(1499, "a", "c"); err != nil {
		t.Fatalf("Open at 1499 after rejected confirm: %v", err)
	}
	// An accepted operation at 1500 commits the clock past the deadline,
	// materializing the expired session and releasing its locks.
	if _, err := s.Open(1500, "a", "c"); err != nil {
		t.Fatalf("Open at 1500: %v", err)
	}
	if got := s.LockedQty("a", "x"); got != 0 {
		t.Fatalf("a locked x = %d, want 0 after expiry materialized", got)
	}
	if got := s.LockedGold("b"); got != 0 {
		t.Fatalf("b locked gold = %d, want 0 after expiry materialized", got)
	}
	if got := s.Qty("a", "x"); got != 5 {
		t.Fatalf("a x = %d, want 5 (locks released, nothing paid)", got)
	}
	if _, ok := s.Version(sid); ok {
		t.Fatal("expired session should be gone")
	}
}

// TestSessionLimit: Q bounds concurrently open sessions per player;
// cancel and expiry free the slot; a is checked before b.
func TestSessionLimit(t *testing.T) {
	s := newSys(t, 0, 1000, 4, 100, 1)
	for _, p := range []string{"a", "b", "c"} {
		mustOK(t, s.GrantGold(p, 0))
	}
	sid := mustOpen(t, s, 0, "a", "b")
	_, err := s.Open(1, "a", "c")
	mustErr(t, err, trade.ErrSessionLimit) // a at limit
	_, err = s.Open(1, "c", "a")
	mustErr(t, err, trade.ErrSessionLimit) // a (as b-side) at limit
	mustOK(t, s.Cancel(2, sid, "b"))
	if _, err := s.Open(3, "a", "c"); err != nil {
		t.Fatalf("open after cancel: %v", err)
	}
	// Expiry frees the slot: the session opened at 3 expires at 103.
	_, err = s.Open(103, "a", "b")
	if err != nil {
		t.Fatalf("open after expiry: %v", err)
	}
}

// TestAvailabilityAcrossSessions reproduces the spec's potion example,
// including locks surviving another session's settlement.
func TestAvailabilityAcrossSessions(t *testing.T) {
	s := newSys(t, 0, 1000, 10, 100_000, 10)
	mustOK(t, s.Grant("a", "potion", 5))
	for _, p := range []string{"b", "c", "d"} {
		mustOK(t, s.GrantGold(p, 0))
	}
	s1 := mustOpen(t, s, 0, "a", "b")
	mustOK(t, s.Offer(1, s1, "a", map[string]int64{"potion": 3}, 0))
	s2 := mustOpen(t, s, 2, "a", "c")
	// Available is 5-3=2, so offering 3 in s2 fails.
	mustErr(t, s.Offer(3, s2, "a", map[string]int64{"potion": 3}, 0), trade.ErrInsufficient)
	// Lowering s1 to 2 frees enough for s2's 3.
	mustOK(t, s.Offer(4, s1, "a", map[string]int64{"potion": 2}, 0))
	mustOK(t, s.Offer(5, s2, "a", map[string]int64{"potion": 3}, 0))
	// Settle s1: a pays 2 potions, keeps 3, all locked by s2.
	mustOK(t, s.Confirm(6, s1, "a", 2))
	mustOK(t, s.Confirm(7, s1, "b", 2))
	if got := s.Qty("a", "potion"); got != 3 {
		t.Fatalf("a potion = %d, want 3", got)
	}
	if got := s.LockedQty("a", "potion"); got != 3 {
		t.Fatalf("a locked potion = %d, want 3", got)
	}
	s3 := mustOpen(t, s, 8, "a", "d")
	mustErr(t, s.Offer(9, s3, "a", map[string]int64{"potion": 1}, 0), trade.ErrInsufficient)
	// Gold availability works the same way.
	mustOK(t, s.GrantGold("b", 100))
	g1 := mustOpen(t, s, 10, "b", "c")
	mustOK(t, s.Offer(11, g1, "b", nil, 70))
	g2 := mustOpen(t, s, 12, "b", "d")
	mustErr(t, s.Offer(13, g2, "b", nil, 31), trade.ErrInsufficient)
	mustOK(t, s.Offer(14, g2, "b", nil, 30))
}

// TestSlotsDeductThenAdd: kind counts are computed after paying, then
// receiving (spec's worked examples with Slots=2).
func TestSlotsDeductThenAdd(t *testing.T) {
	t.Run("pass", func(t *testing.T) {
		s := newSys(t, 0, 1000, 2, 100_000, 10)
		mustOK(t, s.Grant("a", "x", 1))
		mustOK(t, s.Grant("a", "y", 1))
		mustOK(t, s.Grant("b", "z", 1))
		sid := mustOpen(t, s, 0, "a", "b")
		mustOK(t, s.Offer(1, sid, "a", map[string]int64{"x": 1}, 0))
		mustOK(t, s.Offer(2, sid, "b", map[string]int64{"z": 1}, 0))
		mustOK(t, s.Confirm(3, sid, "a", 2))
		mustOK(t, s.Confirm(4, sid, "b", 2)) // a ends with y,z: 2 kinds
		if got := s.Kinds("a"); got != 2 {
			t.Fatalf("a kinds = %d, want 2", got)
		}
	})
	t.Run("fail", func(t *testing.T) {
		s := newSys(t, 0, 1000, 2, 100_000, 10)
		mustOK(t, s.Grant("a", "x", 2))
		mustOK(t, s.Grant("a", "y", 1))
		mustOK(t, s.Grant("b", "z", 1))
		sid := mustOpen(t, s, 0, "a", "b")
		mustOK(t, s.Offer(1, sid, "a", map[string]int64{"x": 1}, 0))
		mustOK(t, s.Offer(2, sid, "b", map[string]int64{"z": 1}, 0))
		mustOK(t, s.Confirm(3, sid, "b", 2))
		// a pays 1 of 2 x, keeps x, plus y and z: 3 kinds > 2.
		mustErr(t, s.Confirm(4, sid, "a", 2), trade.ErrSlots)
		if conf, _ := s.Confirmed(sid, "a"); conf {
			t.Fatal("failed confirm must not be recorded")
		}
		if conf, _ := s.Confirmed(sid, "b"); !conf {
			t.Fatal("other side's confirm must be kept")
		}
		if got := s.LockedQty("a", "x"); got != 1 {
			t.Fatalf("a locked x = %d, want 1 (state unchanged)", got)
		}
	})
}

// TestCapCheckOrder: gold cap is checked before the slot limit, each
// a before b, and only on the completing confirm.
func TestCapCheckOrder(t *testing.T) {
	// a violates slots, b violates gold cap: gold is reported first.
	s := newSys(t, 0, 100, 1, 100_000, 10)
	mustOK(t, s.Grant("a", "x", 1))
	mustOK(t, s.GrantGold("a", 90))
	mustOK(t, s.Grant("b", "z", 1))
	mustOK(t, s.GrantGold("b", 90))
	sid := mustOpen(t, s, 0, "a", "b")
	mustOK(t, s.Offer(1, sid, "a", nil, 50))                     // a pays 50 gold
	mustOK(t, s.Offer(2, sid, "b", map[string]int64{"z": 1}, 0)) // b pays z
	mustOK(t, s.Confirm(3, sid, "b", 2))
	// a: kinds x,z = 2 > 1 (slots); b: gold 90+50 = 140 > 100 (gold cap).
	mustErr(t, s.Confirm(4, sid, "a", 2), trade.ErrGoldCap)
	// Failed confirm recorded nothing; b lowers the gold offer so the
	// slot violation becomes the next error.
	mustOK(t, s.Offer(5, sid, "a", nil, 10)) // ver 3, clears confirms
	mustOK(t, s.Confirm(6, sid, "b", 3))
	mustErr(t, s.Confirm(7, sid, "a", 3), trade.ErrSlots)
}

// TestGoldCapReceiver: the receiver's post-trade gold (after tax) may
// not exceed CAP; a is checked before b.
func TestGoldCapReceiver(t *testing.T) {
	s := newSys(t, 0, 100, 4, 100_000, 10)
	mustOK(t, s.Grant("a", "x", 1))
	mustOK(t, s.GrantGold("a", 60))
	mustOK(t, s.GrantGold("b", 100))
	sid := mustOpen(t, s, 0, "a", "b")
	mustOK(t, s.Offer(1, sid, "a", map[string]int64{"x": 1}, 0))
	mustOK(t, s.Offer(2, sid, "b", nil, 50)) // a would hold 60+50=110 > 100
	mustOK(t, s.Confirm(3, sid, "b", 2))
	mustErr(t, s.Confirm(4, sid, "a", 2), trade.ErrGoldCap)
	// Lower the offer to exactly fit: 60+40=100.
	mustOK(t, s.Offer(5, sid, "b", nil, 40)) // ver 3
	mustOK(t, s.Confirm(6, sid, "b", 3))
	mustOK(t, s.Confirm(7, sid, "a", 3))
	if got := s.Gold("a"); got != 100 {
		t.Fatalf("a gold = %d, want 100", got)
	}
}

// TestEmptyTrade: confirming a trade where both quotes are empty fails.
func TestEmptyTrade(t *testing.T) {
	s := newSys(t, 0, 100, 4, 100_000, 10)
	mustOK(t, s.GrantGold("a", 0))
	mustOK(t, s.GrantGold("b", 0))
	sid := mustOpen(t, s, 0, "a", "b")
	mustErr(t, s.Confirm(1, sid, "a", 0), trade.ErrEmptyTrade)
	// An empty offer bumps the version but the trade is still empty.
	mustOK(t, s.Offer(2, sid, "a", nil, 0))
	mustErr(t, s.Confirm(3, sid, "b", 1), trade.ErrEmptyTrade)
}

// TestCancel: either member cancels, locks are released, the session is
// gone afterwards.
func TestCancel(t *testing.T) {
	s := newSys(t, 0, 100, 4, 100_000, 10)
	mustOK(t, s.Grant("a", "x", 3))
	mustOK(t, s.GrantGold("b", 0))
	sid := mustOpen(t, s, 0, "a", "b")
	mustOK(t, s.Offer(1, sid, "a", map[string]int64{"x": 2}, 0))
	mustOK(t, s.Cancel(2, sid, "b"))
	if got := s.LockedQty("a", "x"); got != 0 {
		t.Fatalf("a locked x = %d, want 0 after cancel", got)
	}
	mustErr(t, s.Offer(3, sid, "a", nil, 0), trade.ErrNoSession)
	mustErr(t, s.Confirm(3, sid, "a", 1), trade.ErrNoSession)
	mustErr(t, s.Cancel(3, sid, "a"), trade.ErrNoSession)
}

// TestRejectedOpsDoNotAdvanceClock: a rejected operation leaves the
// committed clock untouched, so a later op at an older (but valid) time
// still succeeds.
func TestRejectedOpsDoNotAdvanceClock(t *testing.T) {
	s := newSys(t, 0, 100, 4, 100_000, 10)
	mustOK(t, s.Grant("a", "x", 1))
	mustOK(t, s.GrantGold("b", 0))
	sid := mustOpen(t, s, 100, "a", "b")
	// Rejected at now=500 (a has no y); clock must stay at 100.
	mustErr(t, s.Offer(500, sid, "a", map[string]int64{"y": 1}, 0), trade.ErrInsufficient)
	mustOK(t, s.Offer(150, sid, "a", map[string]int64{"x": 1}, 0))
	// Rejected at now=900 (stale version); clock must stay at 150.
	mustErr(t, s.Confirm(900, sid, "a", 99), trade.ErrStale)
	mustOK(t, s.Confirm(200, sid, "a", 1))
}
