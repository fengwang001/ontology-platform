package srvpick

import (
	"errors"
	"testing"
)

func mustNew(t *testing.T, capacity int, coolCap, wr int64) *Selector {
	t.Helper()
	s, err := New(capacity, coolCap, wr)
	if err != nil {
		t.Fatalf("New(%d, %d, %d) = %v, want nil", capacity, coolCap, wr, err)
	}
	return s
}

func mustAdd(t *testing.T, s *Selector, target string, port, priority, weight int, ttl, now int64) {
	t.Helper()
	if err := s.Add(target, port, priority, weight, ttl, now); err != nil {
		t.Fatalf("Add(%q, %d, ...) = %v, want nil", target, port, err)
	}
}

func mustPick(t *testing.T, s *Selector, r uint64, now int64, wantTarget string, wantPort int) {
	t.Helper()
	target, port, err := s.Pick(r, now)
	if err != nil {
		t.Fatalf("Pick(%d, %d) = %v, want (%q, %d)", r, now, err, wantTarget, wantPort)
	}
	if target != wantTarget || port != wantPort {
		t.Fatalf("Pick(%d, %d) = (%q, %d), want (%q, %d)", r, now, target, port, wantTarget, wantPort)
	}
}

func wantErr(t *testing.T, got, want error, what string) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
}

func recOf(t *testing.T, s *Selector, target string, port int) *record {
	t.Helper()
	rec, ok := s.records[key{target: target, port: port}]
	if !ok {
		t.Fatalf("record (%q, %d) not found", target, port)
	}
	return rec
}

func TestNewInvalidConfig(t *testing.T) {
	bad := []struct {
		capacity    int
		coolCap, wr int64
	}{
		{0, 1, 1},
		{-3, 1, 1},
		{1, 0, 1},
		{1, 1_000_000_001, 1},
		{1, -1, 1},
		{1, 1, 0},
		{1, 1, 1_000_000_001},
		{1, 1, -5},
	}
	for _, c := range bad {
		if _, err := New(c.capacity, c.coolCap, c.wr); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("New(%d, %d, %d) = %v, want ErrInvalidConfig", c.capacity, c.coolCap, c.wr, err)
		}
	}
	if _, err := New(1, 1, 1); err != nil {
		t.Errorf("New(1, 1, 1) = %v, want nil", err)
	}
	if _, err := New(100, 1_000_000_000, 1_000_000_000); err != nil {
		t.Errorf("New(100, 1e9, 1e9) = %v, want nil", err)
	}
}

// TestPickRFC2782Example covers the worked example: group 10 holds
// R1(w5, seq1), R2(w0, seq2), R3(w15, seq3); group 20 holds R4(w100).
func TestPickRFC2782Example(t *testing.T) {
	s := mustNew(t, 10, 60, 100)
	mustAdd(t, s, "r1", 1, 10, 5, 1_000_000, 0)
	mustAdd(t, s, "r2", 2, 10, 0, 1_000_000, 0)
	mustAdd(t, s, "r3", 3, 10, 15, 1_000_000, 0)
	mustAdd(t, s, "r4", 4, 20, 100, 1_000_000, 0)

	// Group order: R2, R1, R3; cumulative 0, 5, 20; S = 20.
	mustPick(t, s, 0, 0, "r2", 2) // r1 = 0 selects the zero-weight record
	for r := uint64(1); r <= 5; r++ {
		mustPick(t, s, r, 0, "r1", 1)
	}
	for r := uint64(6); r <= 20; r++ {
		mustPick(t, s, r, 0, "r3", 3)
	}
	mustPick(t, s, 20, 0, "r3", 3) // r1 == S selects the last weighted record
	mustPick(t, s, 21, 0, "r2", 2) // r1 wraps back to 0
	mustPick(t, s, 41, 0, "r3", 3) // 41 mod 21 == 20 -> last weighted record
}

// TestZeroWeightFirstOnlyAtR1Zero: zero-weight records sort first by
// sequence number and are only selectable when r1 == 0; the first of
// them wins.
func TestZeroWeightFirstOnlyAtR1Zero(t *testing.T) {
	s := mustNew(t, 10, 60, 100)
	mustAdd(t, s, "w1", 1, 10, 7, 1_000_000, 0) // seq 1
	mustAdd(t, s, "z1", 2, 10, 0, 1_000_000, 0) // seq 2
	mustAdd(t, s, "z2", 3, 10, 0, 1_000_000, 0) // seq 3
	mustAdd(t, s, "w2", 4, 10, 3, 1_000_000, 0) // seq 4
	// Order: z1, z2, w1, w2; cumulative 0, 0, 7, 10; S = 10.
	for _, r := range []uint64{0, 11, 22, 33} { // r1 == 0
		mustPick(t, s, r, 0, "z1", 2)
	}
	for r := uint64(1); r <= 7; r++ {
		mustPick(t, s, r, 0, "w1", 1)
	}
	for r := uint64(8); r <= 10; r++ {
		mustPick(t, s, r, 0, "w2", 4)
	}
}

// TestAllZeroWeightGroup: S == 0, r1 is always 0, the lowest sequence
// number wins for any r.
func TestAllZeroWeightGroup(t *testing.T) {
	s := mustNew(t, 10, 60, 100)
	mustAdd(t, s, "a", 1, 10, 0, 1_000_000, 0) // seq 1
	mustAdd(t, s, "b", 2, 10, 0, 1_000_000, 0) // seq 2
	mustAdd(t, s, "c", 3, 10, 0, 1_000_000, 0) // seq 3
	for _, r := range []uint64{0, 1, 7, 1 << 62, ^uint64(0)} {
		mustPick(t, s, r, 0, "a", 1)
	}
}

// TestCooldownRecoveryExactBoundary: a record is unusable while
// now < cooldownUntil and usable again exactly at cooldownUntil.
func TestCooldownRecoveryExactBoundary(t *testing.T) {
	s := mustNew(t, 10, 60, 100)
	mustAdd(t, s, "x", 1, 10, 1, 1_000_000, 0)
	mustAdd(t, s, "y", 2, 10, 1, 1_000_000, 0)
	if err := s.Failure("x", 1, 10, 100); err != nil {
		t.Fatalf("Failure = %v", err)
	}
	// f = 1, effective cooldown 10, deadline 110.
	mustPick(t, s, 0, 109, "y", 2) // x still cooling down
	mustPick(t, s, 0, 110, "x", 1) // x recovered exactly at its deadline
	mustPick(t, s, 2, 110, "y", 2) // S = 2, r1 = 2 selects y
}

// TestExpiryExactBoundary: a record is usable while now < expireAt and
// stops being usable exactly at now == expireAt.
func TestExpiryExactBoundary(t *testing.T) {
	s := mustNew(t, 10, 60, 100)
	mustAdd(t, s, "x", 1, 10, 1, 50, 0) // expireAt = 50
	mustAdd(t, s, "y", 2, 10, 1, 1_000_000, 0)
	mustPick(t, s, 0, 49, "x", 1) // still alive; r1 = 0 selects seq 1
	mustPick(t, s, 0, 50, "y", 2) // expired exactly at 50
	if err := s.Failure("x", 1, 10, 50); !errors.Is(err, ErrExpired) {
		t.Fatalf("Failure at expiry = %v, want ErrExpired", err)
	}
	if err := s.Success("x", 1, 50); !errors.Is(err, ErrExpired) {
		t.Fatalf("Success at expiry = %v, want ErrExpired", err)
	}
}

// TestUpdateKeepsSeqAndFailureState: re-registering an existing
// identity overwrites priority/weight/expiry but keeps the sequence
// number, cooldown deadline, f and last failure time.
func TestUpdateKeepsSeqAndFailureState(t *testing.T) {
	s := mustNew(t, 10, 60, 100)
	mustAdd(t, s, "x", 1, 10, 5, 1_000_000, 0) // seq 1
	mustAdd(t, s, "y", 2, 10, 5, 1_000_000, 0) // seq 2
	if err := s.Failure("x", 1, 10, 100); err != nil {
		t.Fatalf("Failure = %v", err)
	}
	before := recOf(t, s, "x", 1)
	if before.seq != 1 || before.failures != 1 || before.lastFail != 100 ||
		!before.hasLastFail || before.cooldownUntil != 110 {
		t.Fatalf("unexpected pre-update state: %+v", before)
	}
	// Update: new priority/weight and expiry, identity unchanged.
	mustAdd(t, s, "x", 1, 20, 9, 500, 105)
	after := recOf(t, s, "x", 1)
	if after.seq != 1 {
		t.Errorf("seq = %d, want 1 (kept)", after.seq)
	}
	if after.priority != 20 || after.weight != 9 {
		t.Errorf("priority/weight = %d/%d, want 20/9 (overwritten)", after.priority, after.weight)
	}
	if after.expireAt != 605 {
		t.Errorf("expireAt = %d, want 605 (now+ttl)", after.expireAt)
	}
	if after.cooldownUntil != 110 || after.failures != 1 ||
		after.lastFail != 100 || !after.hasLastFail {
		t.Errorf("failure state changed by update: %+v", after)
	}
	// y still holds seq 2; a new record gets seq 3.
	mustAdd(t, s, "z", 3, 10, 1, 1_000_000, 0)
	if got := recOf(t, s, "z", 3).seq; got != 3 {
		t.Errorf("z seq = %d, want 3", got)
	}
}

// TestFailureMaxDoesNotShorten: the cooldown deadline takes the max, so
// a later failure with a smaller effective cooldown never shortens it.
func TestFailureMaxDoesNotShorten(t *testing.T) {
	s := mustNew(t, 10, 1_000_000_000, 1_000_000_000)
	mustAdd(t, s, "x", 1, 10, 1, 1_000_000, 0)
	// f=1: eff = 100, deadline 0+100 = 100.
	if err := s.Failure("x", 1, 100, 0); err != nil {
		t.Fatalf("Failure = %v", err)
	}
	if got := recOf(t, s, "x", 1).cooldownUntil; got != 100 {
		t.Fatalf("cooldownUntil = %d, want 100", got)
	}
	// f=2 at now=90 (within Wr=1e9 silence window): eff = 200,
	// now+eff = 290 > 100, so the deadline extends.
	if err := s.Failure("x", 1, 100, 90); err != nil {
		t.Fatalf("Failure = %v", err)
	}
	if got := recOf(t, s, "x", 1).cooldownUntil; got != 290 {
		t.Fatalf("cooldownUntil = %d, want 290", got)
	}
	// f=3 at now=200: eff = 400, now+eff = 600 > 290, extends again.
	if err := s.Failure("x", 1, 100, 200); err != nil {
		t.Fatalf("Failure = %v", err)
	}
	if got := recOf(t, s, "x", 1).cooldownUntil; got != 600 {
		t.Fatalf("cooldownUntil = %d, want 600", got)
	}
	// f=4 at now=500 with a tiny cooldown: eff = 8*1=8? cooldown=1 ->
	// eff = 1<<3 = 8, now+eff = 508 < 600: deadline must stay 600.
	if err := s.Failure("x", 1, 1, 500); err != nil {
		t.Fatalf("Failure = %v", err)
	}
	rec := recOf(t, s, "x", 1)
	if got := rec.cooldownUntil; got != 600 {
		t.Errorf("cooldownUntil = %d, want 600 (max, not shortened)", got)
	}
	if rec.failures != 4 || rec.lastFail != 500 {
		t.Errorf("f/lf = %d/%d, want 4/500", rec.failures, rec.lastFail)
	}
}

// TestCooldownCapAndShiftCap: the effective cooldown is capped by
// CoolCap and the exponent is capped at 30.
func TestCooldownCapAndShiftCap(t *testing.T) {
	s := mustNew(t, 10, 60, 1_000_000_000)
	mustAdd(t, s, "x", 1, 10, 1, 1_000_000_000, 0)
	// f=1..4 with cooldown 10: eff = 10, 20, 40, min(60, 80) = 60;
	// deadlines max(prev, now+eff) = 10, 30, 60, 90.
	wantDeadline := []int64{10, 30, 60, 90}
	for i, want := range wantDeadline {
		now := int64(i) * 10
		if err := s.Failure("x", 1, 10, now); err != nil {
			t.Fatalf("Failure #%d = %v", i+1, err)
		}
		if got := recOf(t, s, "x", 1).cooldownUntil; got != want {
			t.Fatalf("after f=%d: cooldownUntil = %d, want %d", i+1, got, want)
		}
	}
	// Exponent cap: with cooldown 1 and CoolCap 1e9, shift 29 is still
	// exact (2^29 < 1e9) while shift >= 30 always exceeds CoolCap and is
	// truncated to it (2^30 > 1e9 >= CoolCap for any legal config).
	s2 := mustNew(t, 10, 1_000_000_000, 1_000_000_000)
	mustAdd(t, s2, "y", 1, 10, 1, 1_000_000_000, 0)
	for i := 1; i <= 32; i++ {
		if err := s2.Failure("y", 1, 1, int64(i)); err != nil {
			t.Fatalf("Failure #%d = %v", i, err)
		}
		rec := recOf(t, s2, "y", 1)
		var want int64
		switch {
		case i <= 30: // shift = i-1 <= 29, exact power of two
			want = int64(i) + (int64(1) << uint(i-1))
		default: // shift = min(i-1, 30) = 30, eff capped to CoolCap
			want = int64(i) + 1_000_000_000
		}
		if rec.cooldownUntil != want {
			t.Fatalf("f=%d: cooldownUntil = %d, want %d", i, rec.cooldownUntil, want)
		}
	}
}

// TestSilenceWindowReset: f resets exactly when now >= lf+Wr, and not
// one tick earlier.
func TestSilenceWindowReset(t *testing.T) {
	s := mustNew(t, 10, 1_000_000_000, 100)
	mustAdd(t, s, "x", 1, 10, 1, 1_000_000_000, 0)
	fail := func(cooldown, now int64) {
		t.Helper()
		if err := s.Failure("x", 1, cooldown, now); err != nil {
			t.Fatalf("Failure(cooldown=%d, now=%d) = %v", cooldown, now, err)
		}
	}
	fail(10, 1000) // f=1, lf=1000, eff=10, until=1010
	fail(10, 1001) // f=2, lf=1001, eff=20, until=1021
	// now = lf+Wr-1 = 1100: inside the silence window, no reset.
	fail(10, 1100) // f=3, eff=40, until=1140
	if got := recOf(t, s, "x", 1).failures; got != 3 {
		t.Fatalf("f = %d, want 3 (no reset one tick before lf+Wr)", got)
	}
	if got := recOf(t, s, "x", 1).cooldownUntil; got != 1140 {
		t.Fatalf("cooldownUntil = %d, want 1140", got)
	}
	// now = lf+Wr = 1200 exactly: reset to 0, then f=1, eff=10.
	fail(10, 1200)
	rec := recOf(t, s, "x", 1)
	if rec.failures != 1 || rec.lastFail != 1200 {
		t.Errorf("f/lf = %d/%d, want 1/1200 (reset at exactly lf+Wr)", rec.failures, rec.lastFail)
	}
	if rec.cooldownUntil != 1210 {
		t.Errorf("cooldownUntil = %d, want 1210", rec.cooldownUntil)
	}
}

// TestSuccessClearsFOnly: Success resets f but keeps the cooldown
// deadline and the last failure time.
func TestSuccessClearsFOnly(t *testing.T) {
	s := mustNew(t, 10, 60, 100)
	mustAdd(t, s, "x", 1, 10, 1, 1_000_000, 0)
	if err := s.Failure("x", 1, 10, 100); err != nil { // f=1, until=110
		t.Fatalf("Failure = %v", err)
	}
	if err := s.Failure("x", 1, 10, 105); err != nil { // f=2, until=125
		t.Fatalf("Failure = %v", err)
	}
	if err := s.Success("x", 1, 106); err != nil {
		t.Fatalf("Success = %v", err)
	}
	rec := recOf(t, s, "x", 1)
	if rec.failures != 0 {
		t.Errorf("f = %d, want 0 after Success", rec.failures)
	}
	if rec.cooldownUntil != 125 {
		t.Errorf("cooldownUntil = %d, want 125 (Success must not shorten)", rec.cooldownUntil)
	}
	if rec.lastFail != 105 || !rec.hasLastFail {
		t.Errorf("lf = %d/%v, want 105/true (Success must not touch lf)", rec.lastFail, rec.hasLastFail)
	}
	// Still cooling down at 124, usable at 125.
	mustAdd(t, s, "y", 2, 10, 1, 1_000_000, 0)
	mustPick(t, s, 0, 124, "y", 2)
	mustPick(t, s, 0, 125, "x", 1)
	// Next failure at 150: lf=105, 150 < 205 -> no silence reset, but
	// f was cleared by Success, so f=1 and eff=10; the deadline becomes
	// max(125, 150+10) = 160.
	if err := s.Failure("x", 1, 10, 150); err != nil {
		t.Fatalf("Failure = %v", err)
	}
	rec = recOf(t, s, "x", 1)
	if rec.failures != 1 {
		t.Errorf("f = %d, want 1 (Success had cleared it)", rec.failures)
	}
	if rec.cooldownUntil != 160 {
		t.Errorf("cooldownUntil = %d, want 160", rec.cooldownUntil)
	}
}

// TestGroupFallback: when every record of the lowest priority group is
// unusable, selection falls through to the next priority group.
func TestGroupFallback(t *testing.T) {
	s := mustNew(t, 10, 60, 100)
	mustAdd(t, s, "x", 1, 10, 5, 1_000_000, 0)
	mustAdd(t, s, "y", 2, 10, 5, 1_000_000, 0)
	mustAdd(t, s, "z", 3, 20, 100, 1_000_000, 0)
	// Cool down both group-10 records until 1000.
	if err := s.Failure("x", 1, 10, 990); err != nil { // until 1000
		t.Fatalf("Failure = %v", err)
	}
	if err := s.Failure("y", 2, 10, 990); err != nil { // until 1000
		t.Fatalf("Failure = %v", err)
	}
	// Group 10 entirely unusable: any r selects z.
	for _, r := range []uint64{0, 1, 50, 100, 1 << 40} {
		mustPick(t, s, r, 999, "z", 3)
	}
	// At 1000 group 10 is usable again: S = 10, r1 = r mod 11.
	mustPick(t, s, 0, 1000, "x", 1)
	mustPick(t, s, 6, 1000, "y", 2)
}

// TestPurgeReRegisterNewSeq: Purge deletes expired records and a
// re-registered identity gets a fresh sequence number.
func TestPurgeReRegisterNewSeq(t *testing.T) {
	s := mustNew(t, 10, 60, 100)
	mustAdd(t, s, "x", 1, 10, 1, 5, 0)   // seq 1, expireAt 5
	mustAdd(t, s, "y", 2, 10, 1, 100, 0) // seq 2, expireAt 100
	n, err := s.Purge(5)
	if err != nil || n != 1 {
		t.Fatalf("Purge(5) = %d, %v; want 1, nil", n, err)
	}
	if _, ok := s.records[key{"x", 1}]; ok {
		t.Fatal("x should have been purged")
	}
	// Re-registration gets a new sequence number (3, not 1).
	mustAdd(t, s, "x", 1, 10, 1, 1000, 6)
	if got := recOf(t, s, "x", 1).seq; got != 3 {
		t.Fatalf("re-registered x seq = %d, want 3", got)
	}
	// Purge with nothing expired deletes nothing; invalid time rejected.
	if n, err := s.Purge(6); err != nil || n != 0 {
		t.Fatalf("Purge(6) = %d, %v; want 0, nil", n, err)
	}
	if _, err := s.Purge(-1); !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("Purge(-1) = %v, want ErrInvalidTime", err)
	}
	if _, err := s.Purge(1_000_000_000_000_001); !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("Purge(1e15+1) = %v, want ErrInvalidTime", err)
	}
}

// TestFullUpdateAllowedNoEviction: at capacity, updating an existing
// identity needs no slot and triggers no eviction.
func TestFullUpdateAllowedNoEviction(t *testing.T) {
	s := mustNew(t, 2, 60, 100)
	mustAdd(t, s, "x", 1, 10, 1, 5, 0)   // expireAt 5
	mustAdd(t, s, "y", 2, 10, 1, 100, 0) // expireAt 100
	// At now=6 x is expired, but updating y must not evict x.
	mustAdd(t, s, "y", 2, 20, 9, 1000, 6)
	if _, ok := s.records[key{"x", 1}]; !ok {
		t.Fatal("x must not be evicted by an update")
	}
	rec := recOf(t, s, "y", 2)
	if rec.priority != 20 || rec.weight != 9 || rec.expireAt != 1006 || rec.seq != 2 {
		t.Fatalf("unexpected y after update: %+v", rec)
	}
}

// TestFullEvictExpiredThenInsert: a new identity at capacity first
// evicts expired records, then is inserted with a fresh sequence
// number. Mirrors the worked example with Cap = 2.
func TestFullEvictExpiredThenInsert(t *testing.T) {
	s := mustNew(t, 2, 60, 100)
	mustAdd(t, s, "x", 1, 10, 1, 5, 0)   // seq 1, expireAt 5
	mustAdd(t, s, "y", 2, 10, 1, 100, 1) // seq 2, expireAt 101
	// At now=5 x is expired (5 <= 5): evicted, z inserted with seq 3.
	mustAdd(t, s, "z", 3, 10, 1, 100, 5)
	if _, ok := s.records[key{"x", 1}]; ok {
		t.Fatal("x should have been evicted")
	}
	if got := recOf(t, s, "z", 3).seq; got != 3 {
		t.Fatalf("z seq = %d, want 3", got)
	}
	// At now=6 nothing is expired: adding a new identity fails full.
	wantErr(t, s.Add("w", 4, 10, 1, 100, 6), ErrFull, "Add(w) at full capacity")
	if len(s.records) != 2 {
		t.Fatalf("records = %d, want 2 (rejected Add must not change state)", len(s.records))
	}
}

// TestFullNoEvictionOnReject: when nothing is expired, a rejected Add
// performs no eviction at all.
func TestFullNoEvictionOnReject(t *testing.T) {
	s := mustNew(t, 2, 60, 100)
	mustAdd(t, s, "x", 1, 10, 1, 100, 0)
	mustAdd(t, s, "y", 2, 10, 1, 100, 0)
	wantErr(t, s.Add("z", 3, 10, 1, 100, 50), ErrFull, "Add(z)")
	if len(s.records) != 2 {
		t.Fatalf("records = %d, want 2", len(s.records))
	}
	if got := recOf(t, s, "x", 1).seq; got != 1 {
		t.Fatalf("x seq = %d, want 1", got)
	}
	// nextSeq must not have been consumed by the rejected Add: the next
	// successful insert (after Purge) gets seq 3.
	if n, err := s.Purge(100); err != nil || n != 2 {
		t.Fatalf("Purge(100) = %d, %v; want 2, nil", n, err)
	}
	mustAdd(t, s, "z", 3, 10, 1, 100, 100)
	if got := recOf(t, s, "z", 3).seq; got != 3 {
		t.Fatalf("z seq = %d, want 3", got)
	}
}

// TestExponentialCooldownExample replays the worked exponential
// cooldown example: CoolCap=60, Wr=100, cooldown=10.
func TestExponentialCooldownExample(t *testing.T) {
	s := mustNew(t, 10, 60, 100)
	mustAdd(t, s, "x", 1, 10, 1, 1_000_000_000, 0)
	fail := func(now int64) {
		t.Helper()
		if err := s.Failure("x", 1, 10, now); err != nil {
			t.Fatalf("Failure(now=%d) = %v", now, err)
		}
	}
	check := func(now int64, wantF int, wantLF int64, wantUntil int64) {
		t.Helper()
		rec := recOf(t, s, "x", 1)
		if rec.failures != wantF || rec.lastFail != wantLF || rec.cooldownUntil != wantUntil {
			t.Fatalf("after now=%d: f/lf/until = %d/%d/%d, want %d/%d/%d",
				now, rec.failures, rec.lastFail, rec.cooldownUntil, wantF, wantLF, wantUntil)
		}
	}
	fail(100)
	check(100, 1, 100, 110) // eff = 10
	fail(105)               // 105 < 100+100: no reset
	check(105, 2, 105, 125) // eff = 20, max(110, 125)
	fail(130)
	check(130, 3, 130, 170) // eff = 40, max(125, 170)
	fail(140)
	check(140, 4, 140, 200) // eff = min(60, 80) = 60, max(170, 200)
	if err := s.Success("x", 1, 150); err != nil {
		t.Fatalf("Success = %v", err)
	}
	rec := recOf(t, s, "x", 1)
	if rec.failures != 0 || rec.cooldownUntil != 200 || rec.lastFail != 140 {
		t.Fatalf("after Success: f/lf/until = %d/%d/%d, want 0/140/200",
			rec.failures, rec.lastFail, rec.cooldownUntil)
	}
	fail(160)               // 160 < 140+100: no reset, f = 0+1
	check(160, 1, 160, 200) // eff = 10, max(200, 170) = 200
	fail(300)               // 300 >= 160+100: reset first
	check(300, 1, 300, 310) // eff = 10, max(200, 310) = 310
}

// TestAddRejectionOrder: ErrInvalidParam before ErrInvalidTime before
// ErrFull, and rejections change nothing.
func TestAddRejectionOrder(t *testing.T) {
	s := mustNew(t, 1, 60, 100)
	mustAdd(t, s, "x", 1, 10, 1, 100, 0)
	cases := []struct {
		name                   string
		target                 string
		port, priority, weight int
		ttl, now               int64
		want                   error
	}{
		{"empty target", "", 1, 10, 1, 100, 0, ErrInvalidParam},
		{"port 0", "a", 0, 10, 1, 100, 0, ErrInvalidParam},
		{"port 65536", "a", 65536, 10, 1, 100, 0, ErrInvalidParam},
		{"priority 65536", "a", 1, 65536, 1, 100, 0, ErrInvalidParam},
		{"negative weight", "a", 1, 10, -1, 100, 0, ErrInvalidParam},
		{"weight 65536", "a", 1, 10, 65536, 100, 0, ErrInvalidParam},
		{"ttl 0", "a", 1, 10, 1, 0, 0, ErrInvalidParam},
		{"ttl 1e9+1", "a", 1, 10, 1, 1_000_000_001, 0, ErrInvalidParam},
		// Param beats time: both bad -> ErrInvalidParam.
		{"param before time", "", 1, 10, 1, 100, -1, ErrInvalidParam},
		{"negative now", "a", 1, 10, 1, 100, -1, ErrInvalidTime},
		{"now 1e15+1", "a", 1, 10, 1, 100, 1_000_000_000_000_001, ErrInvalidTime},
		// Time beats full: bad time on a full selector -> ErrInvalidTime.
		{"time before full", "a", 1, 10, 1, 100, -1, ErrInvalidTime},
		{"full", "a", 1, 10, 1, 100, 0, ErrFull},
	}
	for _, c := range cases {
		got := s.Add(c.target, c.port, c.priority, c.weight, c.ttl, c.now)
		if !errors.Is(got, c.want) {
			t.Errorf("%s: Add = %v, want %v", c.name, got, c.want)
		}
	}
	// Boundary values accepted: port/priority/weight 65535? port 65535,
	// priority 0, weight 0, ttl 1e9, now 1e15 -- but the selector is
	// full, so use an update of the existing identity instead.
	if err := s.Add("x", 1, 0, 0, 1_000_000_000, 1_000_000_000_000_000); err != nil {
		t.Errorf("boundary update = %v, want nil", err)
	}
	if len(s.records) != 1 {
		t.Fatalf("records = %d, want 1 (rejections must not change state)", len(s.records))
	}
}

// TestFailureRejectionOrder: ErrInvalidParam, ErrInvalidTime,
// ErrNotFound, ErrExpired, in that order.
func TestFailureRejectionOrder(t *testing.T) {
	s := mustNew(t, 10, 60, 100)
	mustAdd(t, s, "x", 1, 10, 1, 50, 0) // expireAt 50
	wantErr(t, s.Failure("", 1, 10, 0), ErrInvalidParam, "empty target")
	wantErr(t, s.Failure("x", 1, 0, 0), ErrInvalidParam, "cooldown 0")
	wantErr(t, s.Failure("x", 1, 1_000_000_001, 0), ErrInvalidParam, "cooldown 1e9+1")
	wantErr(t, s.Failure("", 1, 0, -1), ErrInvalidParam, "param beats time")
	wantErr(t, s.Failure("x", 1, 10, -1), ErrInvalidTime, "negative now")
	wantErr(t, s.Failure("x", 1, 10, 1_000_000_000_000_001), ErrInvalidTime, "now 1e15+1")
	wantErr(t, s.Failure("ghost", 9, 10, -1), ErrInvalidTime, "time beats notfound")
	wantErr(t, s.Failure("ghost", 9, 10, 0), ErrNotFound, "missing record")
	wantErr(t, s.Failure("ghost", 9, 10, 60), ErrNotFound, "notfound beats expired")
	wantErr(t, s.Failure("x", 1, 10, 50), ErrExpired, "expired at expireAt")
	wantErr(t, s.Failure("x", 1, 10, 60), ErrExpired, "expired later")
	// Nothing was recorded by the rejected calls.
	rec := recOf(t, s, "x", 1)
	if rec.failures != 0 || rec.hasLastFail || rec.cooldownUntil != 0 {
		t.Fatalf("rejected failures changed state: %+v", rec)
	}
}

// TestSuccessRejectionOrder: ErrInvalidParam, ErrInvalidTime,
// ErrNotFound, ErrExpired, in that order.
func TestSuccessRejectionOrder(t *testing.T) {
	s := mustNew(t, 10, 60, 100)
	mustAdd(t, s, "x", 1, 10, 1, 50, 0) // expireAt 50
	wantErr(t, s.Success("", 1, 0), ErrInvalidParam, "empty target")
	wantErr(t, s.Success("", 1, -1), ErrInvalidParam, "param beats time")
	wantErr(t, s.Success("x", 1, -1), ErrInvalidTime, "negative now")
	wantErr(t, s.Success("ghost", 9, -1), ErrInvalidTime, "time beats notfound")
	wantErr(t, s.Success("ghost", 9, 0), ErrNotFound, "missing record")
	wantErr(t, s.Success("x", 1, 50), ErrExpired, "expired at expireAt")
	if err := s.Success("x", 1, 49); err != nil {
		t.Fatalf("Success before expiry = %v, want nil", err)
	}
}

// TestPickErrors: invalid time first, then ErrNoAvailable.
func TestPickErrors(t *testing.T) {
	s := mustNew(t, 10, 60, 100)
	if _, _, err := s.Pick(0, -1); !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("Pick(0, -1) = %v, want ErrInvalidTime", err)
	}
	if _, _, err := s.Pick(0, 1_000_000_000_000_001); !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("Pick(0, 1e15+1) = %v, want ErrInvalidTime", err)
	}
	if _, _, err := s.Pick(0, 0); !errors.Is(err, ErrNoAvailable) {
		t.Fatalf("Pick on empty = %v, want ErrNoAvailable", err)
	}
	mustAdd(t, s, "x", 1, 10, 1, 10, 0)
	if _, _, err := s.Pick(0, 10); !errors.Is(err, ErrNoAvailable) {
		t.Fatalf("Pick with all expired = %v, want ErrNoAvailable", err)
	}
}

// TestPickDeterministicAndPure: the same (r, now) always yields the
// same record and Pick mutates nothing.
func TestPickDeterministicAndPure(t *testing.T) {
	s := mustNew(t, 10, 60, 100)
	mustAdd(t, s, "x", 1, 10, 5, 1_000_000, 0)
	mustAdd(t, s, "y", 2, 10, 0, 1_000_000, 0)
	mustAdd(t, s, "z", 3, 20, 7, 1_000_000, 0)
	if err := s.Failure("x", 1, 10, 100); err != nil {
		t.Fatalf("Failure = %v", err)
	}
	snapshot := func() map[key]record {
		out := make(map[key]record, len(s.records))
		for k, rec := range s.records {
			out[k] = *rec
		}
		return out
	}
	before := snapshot()
	for r := uint64(0); r < 50; r++ {
		t1, p1, err1 := s.Pick(r, 105)
		t2, p2, err2 := s.Pick(r, 105)
		if err1 != err2 || t1 != t2 || p1 != p2 {
			t.Fatalf("Pick(%d, 105) not deterministic: (%q,%d,%v) vs (%q,%d,%v)",
				r, t1, p1, err1, t2, p2, err2)
		}
	}
	after := snapshot()
	if len(before) != len(after) {
		t.Fatalf("Pick changed record count: %d -> %d", len(before), len(after))
	}
	for k, rec := range before {
		if after[k] != rec {
			t.Fatalf("Pick mutated record %v: %+v -> %+v", k, rec, after[k])
		}
	}
}
