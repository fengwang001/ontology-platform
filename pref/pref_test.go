package pref

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func mustSet(t *testing.T, s *Store, layer int, cat, ch, eff string, locked bool, exp, ts int64) {
	t.Helper()
	if err := s.Set(layer, cat, ch, eff, locked, exp, ts); err != nil {
		t.Fatalf("Set: %v", err)
	}
}

func mustResolve(t *testing.T, s *Store, cat, ch string, now int64) Decision {
	t.Helper()
	d, err := s.Resolve(cat, ch, now)
	if err != nil {
		t.Fatalf("Resolve(%q,%q,%d): %v", cat, ch, now, err)
	}
	return d
}

func wantDecision(t *testing.T, s *Store, cat, ch string, now int64, want Decision) {
	t.Helper()
	if got := mustResolve(t, s, cat, ch, now); got != want {
		t.Fatalf("Resolve(%q,%q,%d) = %+v, want %+v", cat, ch, now, got, want)
	}
}

func wantErr(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("err = %v, want %v", got, want)
	}
}

// TestExampleScenario replays the scenario from the specification.
func TestExampleScenario(t *testing.T) {
	s := NewStore()
	mustSet(t, s, LayerPlatform, "", ChAny, Deny, false, 0, 1)
	mustSet(t, s, LayerOrg, "marketing", ChSMS, Deny, true, 0, 2)
	mustSet(t, s, LayerOrg, "billing/invoice", ChAny, Deny, false, 0, 3)
	mustSet(t, s, LayerUser, "billing", ChAny, Allow, false, 0, 4)
	mustSet(t, s, LayerUser, "marketing/promo", ChEmail, Allow, false, 0, 5)

	// Org "billing/invoice" is the most specific candidate.
	wantDecision(t, s, "billing/invoice", ChEmail, 5,
		Decision{Allow: false, Layer: LayerOrg, Cat: "billing/invoice", Ch: ChAny, Seq: 3})
	// User "billing" allow beats the platform root deny.
	wantDecision(t, s, "billing/refund", ChEmail, 5,
		Decision{Allow: true, Layer: LayerUser, Cat: "billing", Ch: ChAny, Seq: 4})
	// The locked org "marketing"/sms rule dominates.
	wantDecision(t, s, "marketing/promo", ChSMS, 5,
		Decision{Allow: false, Layer: LayerOrg, Cat: "marketing", Ch: ChSMS, Seq: 2})

	// The locked org rule covers this user Set.
	wantErr(t, s.Set(LayerUser, "marketing/promo", ChSMS, Allow, false, 0, 6), ErrLockedOverride)

	if err := s.UnsubscribeAll(6); err != nil {
		t.Fatalf("UnsubscribeAll: %v", err)
	}
	// User rules with ts 4 and 5 are suppressed; platform root decides.
	wantDecision(t, s, "billing/refund", ChEmail, 6,
		Decision{Allow: false, Layer: LayerPlatform, Cat: "", Ch: ChAny, Seq: 1})

	// A user Set with ts > tomb takes effect again.
	mustSet(t, s, LayerUser, "billing", ChAny, Allow, false, 0, 7)
	wantDecision(t, s, "billing/refund", ChEmail, 7,
		Decision{Allow: true, Layer: LayerUser, Cat: "billing", Ch: ChAny, Seq: 6})
	// The org "billing/invoice" deny still wins on its subtree.
	wantDecision(t, s, "billing/invoice", ChEmail, 7,
		Decision{Allow: false, Layer: LayerOrg, Cat: "billing/invoice", Ch: ChAny, Seq: 3})
}

// TestTombBoundary checks that a user rule with ts == tomb is
// suppressed while one with ts == tomb+1 is live.
func TestTombBoundary(t *testing.T) {
	s := NewStore()
	mustSet(t, s, LayerUser, "a", ChAny, Allow, false, 0, 10)
	if err := s.UnsubscribeAll(10); err != nil {
		t.Fatalf("UnsubscribeAll: %v", err)
	}
	// ts == tomb: suppressed, no candidates remain.
	wantDecision(t, s, "a", ChEmail, 10, Decision{Allow: false})

	mustSet(t, s, LayerUser, "b", ChAny, Allow, false, 0, 11)
	// ts == tomb+1: live.
	wantDecision(t, s, "b", ChEmail, 11,
		Decision{Allow: true, Layer: LayerUser, Cat: "b", Ch: ChAny, Seq: 2})
	// The ts == 10 rule is still suppressed.
	wantDecision(t, s, "a", ChEmail, 11, Decision{Allow: false})
}

// TestExpiryBoundary checks that a rule with exp == now is dead while
// one with exp == now+1 is live.
func TestExpiryBoundary(t *testing.T) {
	s := NewStore()
	mustSet(t, s, LayerOrg, "a", ChAny, Allow, false, 5, 1)
	wantDecision(t, s, "a", ChEmail, 4,
		Decision{Allow: true, Layer: LayerOrg, Cat: "a", Ch: ChAny, Seq: 1})
	// now == exp: expired.
	wantDecision(t, s, "a", ChEmail, 5, Decision{Allow: false})

	s2 := NewStore()
	mustSet(t, s2, LayerOrg, "a", ChAny, Allow, false, 6, 1)
	// exp == now+1: still live.
	wantDecision(t, s2, "a", ChEmail, 5,
		Decision{Allow: true, Layer: LayerOrg, Cat: "a", Ch: ChAny, Seq: 1})
}

// TestSegmentPrefix checks root matching and segment-wise (not string)
// prefix semantics.
func TestSegmentPrefix(t *testing.T) {
	s := NewStore()
	mustSet(t, s, LayerPlatform, "", ChAny, Deny, false, 0, 1)
	mustSet(t, s, LayerOrg, "billing", ChAny, Allow, false, 0, 2)

	// Root matches every category.
	wantDecision(t, s, "anything/else", ChPush, 2,
		Decision{Allow: false, Layer: LayerPlatform, Cat: "", Ch: ChAny, Seq: 1})
	// Segment prefix matches.
	wantDecision(t, s, "billing/invoice", ChPush, 2,
		Decision{Allow: true, Layer: LayerOrg, Cat: "billing", Ch: ChAny, Seq: 2})
	// "billing" is not a segment prefix of "billingx/a".
	wantDecision(t, s, "billingx/a", ChPush, 2,
		Decision{Allow: false, Layer: LayerPlatform, Cat: "", Ch: ChAny, Seq: 1})
}

// TestChannelExactBeatsStar checks that at equal category specificity
// an exact channel beats "*".
func TestChannelExactBeatsStar(t *testing.T) {
	s := NewStore()
	mustSet(t, s, LayerOrg, "a", ChAny, Deny, false, 0, 1)
	mustSet(t, s, LayerOrg, "a", ChEmail, Allow, false, 0, 2)
	wantDecision(t, s, "a", ChEmail, 2,
		Decision{Allow: true, Layer: LayerOrg, Cat: "a", Ch: ChEmail, Seq: 2})
	// The exact email rule is not a candidate for sms.
	wantDecision(t, s, "a", ChSMS, 2,
		Decision{Allow: false, Layer: LayerOrg, Cat: "a", Ch: ChAny, Seq: 1})
}

// TestSameSpecificityHigherLayerWins checks the layer tie-break among
// non-locked candidates.
func TestSameSpecificityHigherLayerWins(t *testing.T) {
	s := NewStore()
	mustSet(t, s, LayerOrg, "a", ChAny, Deny, false, 0, 1)
	mustSet(t, s, LayerUser, "a", ChAny, Allow, false, 0, 2)
	wantDecision(t, s, "a", ChEmail, 2,
		Decision{Allow: true, Layer: LayerUser, Cat: "a", Ch: ChAny, Seq: 2})
}

// TestOrgSpecificBeatsUserWider checks that specificity is compared
// before layer: a narrower org rule beats a wider user rule.
func TestOrgSpecificBeatsUserWider(t *testing.T) {
	s := NewStore()
	mustSet(t, s, LayerUser, "a", ChAny, Allow, false, 0, 1)
	mustSet(t, s, LayerOrg, "a/b", ChAny, Deny, false, 0, 2)
	wantDecision(t, s, "a/b", ChEmail, 2,
		Decision{Allow: false, Layer: LayerOrg, Cat: "a/b", Ch: ChAny, Seq: 2})
	// On "a/c" the org rule does not match; the user rule applies.
	wantDecision(t, s, "a/c", ChEmail, 2,
		Decision{Allow: true, Layer: LayerUser, Cat: "a", Ch: ChAny, Seq: 1})
}

// TestPlatformAndOrgLocks checks that a platform lock beats an org
// lock regardless of specificity.
func TestPlatformAndOrgLocks(t *testing.T) {
	s := NewStore()
	mustSet(t, s, LayerOrg, "a/b/c", ChEmail, Allow, true, 0, 1)
	mustSet(t, s, LayerPlatform, "a", ChAny, Deny, true, 0, 2)
	wantDecision(t, s, "a/b/c", ChEmail, 2,
		Decision{Allow: false, Layer: LayerPlatform, Cat: "a", Ch: ChAny, Seq: 2})

	// Without the platform lock the org lock wins over wider rules.
	s2 := NewStore()
	mustSet(t, s2, LayerOrg, "a/b/c", ChEmail, Allow, true, 0, 1)
	mustSet(t, s2, LayerPlatform, "a", ChAny, Deny, false, 0, 2)
	wantDecision(t, s2, "a/b/c", ChEmail, 2,
		Decision{Allow: true, Layer: LayerOrg, Cat: "a/b/c", Ch: ChEmail, Seq: 1})
}

// TestLockedExpiryAndTomb checks that an expired lock loses effect and
// that UnsubscribeAll never suppresses locked (platform/org) rules.
func TestLockedExpiryAndTomb(t *testing.T) {
	s := NewStore()
	mustSet(t, s, LayerUser, "a", ChAny, Allow, false, 0, 1)
	mustSet(t, s, LayerOrg, "a", ChAny, Deny, true, 8, 2)
	// Lock still in effect at now=7.
	wantDecision(t, s, "a", ChEmail, 7,
		Decision{Allow: false, Layer: LayerOrg, Cat: "a", Ch: ChAny, Seq: 2})
	// At now=8 the lock is expired; the user rule decides. A user Set
	// at ts=8 is also no longer covered by the expired lock.
	wantDecision(t, s, "a", ChEmail, 8,
		Decision{Allow: true, Layer: LayerUser, Cat: "a", Ch: ChAny, Seq: 1})
	mustSet(t, s, LayerUser, "a/b", ChAny, Allow, false, 0, 8)

	// UnsubscribeAll does not affect locked rules.
	s2 := NewStore()
	mustSet(t, s2, LayerPlatform, "a", ChAny, Deny, true, 0, 3)
	if err := s2.UnsubscribeAll(5); err != nil {
		t.Fatalf("UnsubscribeAll: %v", err)
	}
	wantDecision(t, s2, "a", ChEmail, 5,
		Decision{Allow: false, Layer: LayerPlatform, Cat: "a", Ch: ChAny, Seq: 1})
}

// TestLockedCoverChannelAsymmetry checks that a locked rule with a
// concrete channel does not block a user "*" Set, yet still wins
// arbitration on that channel.
func TestLockedCoverChannelAsymmetry(t *testing.T) {
	s := NewStore()
	mustSet(t, s, LayerOrg, "", ChSMS, Deny, true, 0, 1)
	// Locked ch is concrete, so it does not cover the user "*" Set.
	mustSet(t, s, LayerUser, "x", ChAny, Allow, false, 0, 2)
	// But on sms the lock still wins.
	wantDecision(t, s, "x", ChSMS, 2,
		Decision{Allow: false, Layer: LayerOrg, Cat: "", Ch: ChSMS, Seq: 1})
	// On email only the user rule is a candidate.
	wantDecision(t, s, "x", ChEmail, 2,
		Decision{Allow: true, Layer: LayerUser, Cat: "x", Ch: ChAny, Seq: 2})
	// A user sms Set under the locked subtree is covered.
	wantErr(t, s.Set(LayerUser, "x", ChSMS, Allow, false, 0, 3), ErrLockedOverride)
}

// TestNoCandidateDefaultDeny checks the empty-source default.
func TestNoCandidateDefaultDeny(t *testing.T) {
	s := NewStore()
	wantDecision(t, s, "a", ChEmail, 0, Decision{Allow: false})
	mustSet(t, s, LayerUser, "a", ChSMS, Allow, false, 0, 1)
	// Channel mismatch: no candidate.
	wantDecision(t, s, "a", ChEmail, 1, Decision{Allow: false})
}

// TestSetValidation checks the rejection reasons of Set in order.
func TestSetValidation(t *testing.T) {
	s := NewStore()
	bad := []struct {
		layer  int
		cat    string
		ch     string
		eff    string
		locked bool
		exp    int64
		ts     int64
	}{
		{0, "a", ChAny, Allow, false, 0, 1}, // bad layer
		{4, "a", ChAny, Allow, false, 0, 1}, // bad layer
		{1, "A", ChAny, Allow, false, 0, 1}, // uppercase
		{1, "a//b", ChAny, Allow, false, 0, 1},
		{1, "a/b/c/d/e", ChAny, Allow, false, 0, 1},         // 5 segments
		{1, "abcdefghijklmnopq", ChAny, Allow, false, 0, 1}, // 17 chars
		{1, "a-b", ChAny, Allow, false, 0, 1},               // dash
		{1, "a", "webhook", Allow, false, 0, 1},             // bad channel
		{1, "a", ChAny, "maybe", false, 0, 1},               // bad effect
		{1, "a", ChAny, Allow, false, 1, 1},                 // exp <= ts
		{1, "a", ChAny, Allow, false, 0, -1},                // negative ts
	}
	for i, c := range bad {
		if err := s.Set(c.layer, c.cat, c.ch, c.eff, c.locked, c.exp, c.ts); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("case %d: err = %v, want ErrInvalidArgument", i, err)
		}
	}
	// Valid edge cats: 16-char segment, 4 segments, digits/underscores.
	mustSet(t, s, LayerPlatform, "abcdefghijklmnop", ChAny, Allow, false, 0, 1)
	mustSet(t, s, LayerPlatform, "a/b/c/d", ChAny, Allow, false, 0, 2)
	mustSet(t, s, LayerPlatform, "x_1/2_y", ChAny, Allow, false, 0, 3)

	// Clock regression: ts < maxTs.
	wantErr(t, s.Set(LayerPlatform, "z", ChAny, Allow, false, 0, 2), ErrClockRegression)
	// Permission: user-layer locked.
	wantErr(t, s.Set(LayerUser, "z", ChAny, Allow, true, 0, 3), ErrPermissionDenied)
	// exp == 0 never expires; exp > ts is fine.
	mustSet(t, s, LayerPlatform, "e", ChAny, Allow, false, 100, 3)
}

// TestRemoveFlow checks Remove semantics and rejection order.
func TestRemoveFlow(t *testing.T) {
	s := NewStore()
	mustSet(t, s, LayerOrg, "a", ChAny, Deny, false, 0, 1)
	mustSet(t, s, LayerUser, "b", ChAny, Allow, false, 0, 2)

	wantErr(t, s.Remove(LayerOrg, "a", "bad", 2), ErrInvalidArgument)
	wantErr(t, s.Remove(LayerOrg, "a", ChAny, 1), ErrClockRegression)
	wantErr(t, s.Remove(LayerOrg, "missing", ChAny, 2), ErrRuleNotFound)

	// Locked org rule covers removal of user rules under it.
	mustSet(t, s, LayerOrg, "b", ChAny, Deny, true, 0, 3)
	wantErr(t, s.Remove(LayerUser, "b", ChAny, 4), ErrLockedOverride)
	// Removing the org lock itself is not subject to the user check.
	if err := s.Remove(LayerOrg, "b", ChAny, 4); err != nil {
		t.Fatalf("Remove org lock: %v", err)
	}
	if err := s.Remove(LayerUser, "b", ChAny, 5); err != nil {
		t.Fatalf("Remove user rule: %v", err)
	}
	wantDecision(t, s, "b", ChEmail, 5, Decision{Allow: false})
	wantDecision(t, s, "a", ChEmail, 5,
		Decision{Allow: false, Layer: LayerOrg, Cat: "a", Ch: ChAny, Seq: 1})
}

// TestOverwriteSameKey checks that Set on an existing key replaces the
// rule and issues a new seq.
func TestOverwriteSameKey(t *testing.T) {
	s := NewStore()
	mustSet(t, s, LayerOrg, "a", ChAny, Deny, false, 0, 1)
	mustSet(t, s, LayerOrg, "a", ChAny, Allow, false, 0, 2)
	wantDecision(t, s, "a", ChEmail, 2,
		Decision{Allow: true, Layer: LayerOrg, Cat: "a", Ch: ChAny, Seq: 2})
	if n := len(s.Snapshot()); n != 1 {
		t.Fatalf("rule count = %d, want 1", n)
	}
}

// TestCapacity checks the 1000-rule bound: new keys beyond it are
// rejected, overwrites are not.
func TestCapacity(t *testing.T) {
	s := NewStore()
	for i := 0; i < MaxRules; i++ {
		cat := fmt.Sprintf("c%03d", i%100) + "/" + fmt.Sprintf("s%02d", i/100)
		if err := s.Set(LayerOrg, cat, ChAny, Deny, false, 0, int64(i)); err != nil {
			t.Fatalf("Set %d: %v", i, err)
		}
	}
	wantErr(t, s.Set(LayerOrg, "overflow", ChAny, Deny, false, 0, int64(MaxRules)), ErrCapacityExceeded)
	// Overwriting an existing key is not a new key.
	mustSet(t, s, LayerOrg, "c000/s00", ChAny, Allow, false, 0, int64(MaxRules))
	// After a Remove there is room again.
	if err := s.Remove(LayerOrg, "c001/s00", ChAny, int64(MaxRules)); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	mustSet(t, s, LayerOrg, "overflow", ChAny, Deny, false, 0, int64(MaxRules))
}

// stateFingerprint captures everything a rejected operation must not
// change: rules (with seq), tomb and maxTs.
func stateFingerprint(s *Store) string {
	f := ""
	for _, r := range s.Snapshot() {
		f += fmt.Sprintf("%d|%s|%s|%s|%v|%d|%d|%d;", r.Layer, r.Cat, r.Ch, r.Eff, r.Locked, r.Exp, r.Ts, r.Seq)
	}
	return f
}

// TestRejectedOpsKeepState checks that every rejection reason leaves
// rules, seq, tomb and maxTs untouched.
func TestRejectedOpsKeepState(t *testing.T) {
	s := NewStore()
	mustSet(t, s, LayerPlatform, "", ChAny, Deny, false, 0, 1)
	mustSet(t, s, LayerOrg, "m", ChSMS, Deny, true, 0, 2)
	mustSet(t, s, LayerUser, "x", ChAny, Allow, false, 0, 3)
	if err := s.UnsubscribeAll(4); err != nil {
		t.Fatalf("UnsubscribeAll: %v", err)
	}
	before := stateFingerprint(s)

	rejects := []error{
		s.Set(9, "a", ChAny, Allow, false, 0, 5),           // invalid layer
		s.Set(LayerUser, "a b", ChAny, Allow, false, 0, 5), // invalid cat
		s.Set(LayerUser, "a", "web", Allow, false, 0, 5),   // invalid ch
		s.Set(LayerUser, "a", ChAny, "hold", false, 0, 5),  // invalid eff
		s.Set(LayerUser, "a", ChAny, Allow, false, 5, 5),   // exp <= ts
		s.Set(LayerUser, "a", ChAny, Allow, false, 0, -1),  // negative ts
		s.Set(LayerUser, "a", ChAny, Allow, false, 0, 3),   // clock regression
		s.Set(LayerUser, "a", ChAny, Allow, true, 0, 5),    // permission
		s.Set(LayerUser, "m/p", ChSMS, Allow, false, 0, 5), // locked override
		s.Remove(9, "a", ChAny, 5),                         // invalid layer
		s.Remove(LayerUser, "a", ChAny, 3),                 // clock regression
		s.Remove(LayerUser, "m/p", ChSMS, 5),               // locked override
		s.Remove(LayerUser, "missing", ChAny, 5),           // not found
		s.UnsubscribeAll(-1),                               // invalid ts
		s.UnsubscribeAll(3),                                // clock regression
	}
	for i, err := range rejects {
		if err == nil {
			t.Fatalf("reject %d: expected error", i)
		}
	}
	if _, err := s.Resolve("", ChEmail, 5); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Resolve empty cat: %v", err)
	}
	if _, err := s.Resolve("a", ChAny, 5); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Resolve star ch: %v", err)
	}
	if _, err := s.Resolve("a", ChEmail, 3); !errors.Is(err, ErrClockRegression) {
		t.Fatalf("Resolve past now: %v", err)
	}
	if after := stateFingerprint(s); after != before {
		t.Fatalf("state changed by rejected ops:\nbefore: %s\nafter:  %s", before, after)
	}
	// maxTs is still 4: an op with ts=4 is accepted. Use the platform
	// layer so the tombstone does not suppress the new rule.
	mustSet(t, s, LayerPlatform, "y", ChAny, Allow, false, 0, 4)
	// seq only advanced by the accepted Sets (3 rules + this one = 4).
	wantDecision(t, s, "y", ChEmail, 4,
		Decision{Allow: true, Layer: LayerPlatform, Cat: "y", Ch: ChAny, Seq: 4})
	// tomb is still 4: the user rule with ts=3 stays suppressed.
	wantDecision(t, s, "x", ChEmail, 4,
		Decision{Allow: false, Layer: LayerPlatform, Cat: "", Ch: ChAny, Seq: 1})
}

// TestLockedExpiry checks that an expired lock neither wins arbitration
// nor covers user Sets.
func TestLockedExpiry(t *testing.T) {
	s := NewStore()
	mustSet(t, s, LayerUser, "a", ChAny, Allow, false, 0, 1)
	mustSet(t, s, LayerOrg, "a", ChAny, Deny, true, 8, 2)
	wantDecision(t, s, "a", ChEmail, 7,
		Decision{Allow: false, Layer: LayerOrg, Cat: "a", Ch: ChAny, Seq: 2})
	// Lock expired at now=8: the user rule decides.
	wantDecision(t, s, "a", ChEmail, 8,
		Decision{Allow: true, Layer: LayerUser, Cat: "a", Ch: ChAny, Seq: 1})
	// And a user Set at ts=8 is no longer covered.
	mustSet(t, s, LayerUser, "a/b", ChAny, Allow, false, 0, 8)
}

// TestLockedTombImmune checks that UnsubscribeAll never suppresses
// locked platform/org rules.
func TestLockedTombImmune(t *testing.T) {
	s := NewStore()
	mustSet(t, s, LayerPlatform, "a", ChAny, Deny, true, 0, 3)
	if err := s.UnsubscribeAll(5); err != nil {
		t.Fatalf("UnsubscribeAll: %v", err)
	}
	wantDecision(t, s, "a", ChEmail, 5,
		Decision{Allow: false, Layer: LayerPlatform, Cat: "a", Ch: ChAny, Seq: 1})
}

// TestReplayDeterminism applies the same op sequence to two stores and
// requires identical snapshots and decisions.
func TestReplayDeterminism(t *testing.T) {
	ops := func(s *Store) {
		mustSet(t, s, LayerPlatform, "", ChAny, Deny, false, 0, 1)
		mustSet(t, s, LayerOrg, "a", ChAny, Allow, false, 0, 2)
		mustSet(t, s, LayerUser, "a/b", ChEmail, Deny, false, 0, 3)
		mustSet(t, s, LayerOrg, "a/b", ChAny, Allow, false, 0, 4)
		if err := s.UnsubscribeAll(5); err != nil {
			t.Fatalf("UnsubscribeAll: %v", err)
		}
		mustSet(t, s, LayerUser, "a", ChSMS, Allow, false, 0, 6)
		if err := s.Remove(LayerOrg, "a/b", ChAny, 7); err != nil {
			t.Fatalf("Remove: %v", err)
		}
	}
	s1, s2 := NewStore(), NewStore()
	ops(s1)
	ops(s2)
	if f1, f2 := stateFingerprint(s1), stateFingerprint(s2); f1 != f2 {
		t.Fatalf("replay mismatch:\n%s\n%s", f1, f2)
	}
	for _, q := range [][2]string{{"a", ChEmail}, {"a/b", ChEmail}, {"a/b", ChSMS}, {"z", ChPush}} {
		d1 := mustResolve(t, s1, q[0], q[1], 7)
		d2 := mustResolve(t, s2, q[0], q[1], 7)
		if d1 != d2 {
			t.Fatalf("Resolve(%s,%s) mismatch: %+v vs %+v", q[0], q[1], d1, d2)
		}
	}
}

// TestConcurrentAccess exercises the store from many goroutines; run
// with -race. Results must remain serializable and Resolve stable.
func TestConcurrentAccess(t *testing.T) {
	s := NewStore()
	mustSet(t, s, LayerPlatform, "", ChAny, Deny, false, 0, 0)
	const workers = 8
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			cat := fmt.Sprintf("c%d", w)
			for i := 1; i <= 50; i++ {
				ts := int64(i * workers) // distinct, mostly increasing
				_ = s.Set(LayerUser, cat, ChAny, Allow, false, 0, ts)
				_ = s.Set(LayerOrg, cat, ChEmail, Deny, false, 0, ts)
				d1, err1 := s.Resolve(cat+"/sub", ChEmail, int64(50*workers))
				d2, err2 := s.Resolve(cat+"/sub", ChEmail, int64(50*workers))
				if err1 == nil && err2 == nil && d1 != d2 {
					t.Errorf("unstable Resolve: %+v vs %+v", d1, d2)
				}
				if i%10 == 0 {
					_ = s.Remove(LayerOrg, cat, ChEmail, ts)
				}
			}
		}(w)
	}
	wg.Wait()
	if n := len(s.Snapshot()); n > MaxRules {
		t.Fatalf("rule count %d exceeds %d", n, MaxRules)
	}
}
