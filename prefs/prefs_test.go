package prefs

import (
	"errors"
	"strconv"
	"sync"
	"testing"
)

func itoa(i int) string { return strconv.Itoa(i) }

func mustSet(t *testing.T, s *Store, r Rule) {
	t.Helper()
	if err := s.Set(r); err != nil {
		t.Fatalf("Set(%+v) failed: %v", r, err)
	}
}

func mustResolve(t *testing.T, s *Store, cat, ch string, now int64) Decision {
	t.Helper()
	d, err := s.Resolve(cat, ch, now)
	if err != nil {
		t.Fatalf("Resolve(%q, %q, %d) failed: %v", cat, ch, now, err)
	}
	return d
}

func wantDecision(t *testing.T, s *Store, cat, ch string, now int64, want Decision) {
	t.Helper()
	got := mustResolve(t, s, cat, ch, now)
	if got != want {
		t.Fatalf("Resolve(%q, %q, %d) = %+v, want %+v", cat, ch, now, got, want)
	}
}

func wantErr(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

// TestSpecExample walks the full scenario from the specification.
func TestSpecExample(t *testing.T) {
	s := NewStore()
	mustSet(t, s, Rule{Layer: LayerPlatform, Cat: "", Ch: ChAny, Eff: EffDeny, Ts: 1})
	mustSet(t, s, Rule{Layer: LayerOrg, Cat: "marketing", Ch: ChSMS, Eff: EffDeny, Locked: true, Ts: 2})
	mustSet(t, s, Rule{Layer: LayerOrg, Cat: "billing/invoice", Ch: ChAny, Eff: EffDeny, Ts: 3})
	mustSet(t, s, Rule{Layer: LayerUser, Cat: "billing", Ch: ChAny, Eff: EffAllow, Ts: 4})
	mustSet(t, s, Rule{Layer: LayerUser, Cat: "marketing/promo", Ch: ChEmail, Eff: EffAllow, Ts: 5})

	// Candidates: platform root, org billing/invoice, user billing.
	// Most specific wins: org deny.
	wantDecision(t, s, "billing/invoice", ChEmail, 5, Decision{
		Allowed: false, Layer: LayerOrg, Cat: "billing/invoice", Ch: ChAny, Seq: 3,
	})
	// User billing allow is the most specific candidate here.
	wantDecision(t, s, "billing/refund", ChEmail, 5, Decision{
		Allowed: true, Layer: LayerUser, Cat: "billing", Ch: ChAny, Seq: 4,
	})
	// Org locked rule decides marketing/promo on sms.
	wantDecision(t, s, "marketing/promo", ChSMS, 5, Decision{
		Allowed: false, Layer: LayerOrg, Cat: "marketing", Ch: ChSMS, Seq: 2,
	})

	// User Set covered by the org locked rule is rejected.
	err := s.Set(Rule{Layer: LayerUser, Cat: "marketing/promo", Ch: ChSMS, Eff: EffAllow, Ts: 6})
	wantErr(t, err, ErrLockedOverride)

	// UnsubscribeAll(6) invalidates user rules with ts 4 and 5.
	if err := s.UnsubscribeAll(6); err != nil {
		t.Fatalf("UnsubscribeAll failed: %v", err)
	}
	wantDecision(t, s, "billing/refund", ChEmail, 6, Decision{
		Allowed: false, Layer: LayerPlatform, Cat: "", Ch: ChAny, Seq: 1,
	})

	// A fresh user Set with ts > tomb takes effect again.
	mustSet(t, s, Rule{Layer: LayerUser, Cat: "billing", Ch: ChAny, Eff: EffAllow, Ts: 7})
	wantDecision(t, s, "billing/refund", ChEmail, 7, Decision{
		Allowed: true, Layer: LayerUser, Cat: "billing", Ch: ChAny, Seq: 6,
	})
	wantDecision(t, s, "billing/invoice", ChEmail, 7, Decision{
		Allowed: false, Layer: LayerOrg, Cat: "billing/invoice", Ch: ChAny, Seq: 3,
	})
}

// TestTombBoundary: user rule with ts == tomb is invalidated, ts == tomb+1
// is effective.
func TestTombBoundary(t *testing.T) {
	s := NewStore()
	mustSet(t, s, Rule{Layer: LayerUser, Cat: "a", Ch: ChAny, Eff: EffAllow, Ts: 5})
	if err := s.UnsubscribeAll(5); err != nil {
		t.Fatal(err)
	}
	// ts == tomb: invalidated, no candidate remains.
	wantDecision(t, s, "a", ChEmail, 5, Decision{Allowed: false})

	mustSet(t, s, Rule{Layer: LayerUser, Cat: "a", Ch: ChAny, Eff: EffAllow, Ts: 6})
	// ts == tomb+1: effective again.
	wantDecision(t, s, "a", ChEmail, 6, Decision{
		Allowed: true, Layer: LayerUser, Cat: "a", Ch: ChAny, Seq: 2,
	})
}

// TestExpiryBoundary: exp == now means expired, exp == now+1 still effective.
func TestExpiryBoundary(t *testing.T) {
	s := NewStore()
	mustSet(t, s, Rule{Layer: LayerPlatform, Cat: "a", Ch: ChAny, Eff: EffAllow, Exp: 10, Ts: 1})
	wantDecision(t, s, "a", ChEmail, 9, Decision{
		Allowed: true, Layer: LayerPlatform, Cat: "a", Ch: ChAny, Seq: 1,
	})
	wantDecision(t, s, "a", ChEmail, 10, Decision{Allowed: false})
	wantDecision(t, s, "a", ChEmail, 11, Decision{Allowed: false})
}

// TestRootAndSegmentPrefix: the root matches everything; prefix matching is
// segment-wise, so "billing" does not match "billingx/a".
func TestRootAndSegmentPrefix(t *testing.T) {
	s := NewStore()
	mustSet(t, s, Rule{Layer: LayerPlatform, Cat: "", Ch: ChAny, Eff: EffDeny, Ts: 1})
	mustSet(t, s, Rule{Layer: LayerUser, Cat: "billing", Ch: ChAny, Eff: EffAllow, Ts: 2})

	wantDecision(t, s, "billing", ChEmail, 2, Decision{
		Allowed: true, Layer: LayerUser, Cat: "billing", Ch: ChAny, Seq: 2,
	})
	wantDecision(t, s, "billing/invoice", ChEmail, 2, Decision{
		Allowed: true, Layer: LayerUser, Cat: "billing", Ch: ChAny, Seq: 2,
	})
	// "billing" is a string prefix of "billingx" but not a segment prefix.
	wantDecision(t, s, "billingx/a", ChEmail, 2, Decision{
		Allowed: false, Layer: LayerPlatform, Cat: "", Ch: ChAny, Seq: 1,
	})
	// Root matches any category.
	wantDecision(t, s, "anything/at/all", ChPush, 2, Decision{
		Allowed: false, Layer: LayerPlatform, Cat: "", Ch: ChAny, Seq: 1,
	})
}

// TestExactChannelBeatsWildcard: at equal category specificity an exact
// channel rule beats a * rule.
func TestExactChannelBeatsWildcard(t *testing.T) {
	s := NewStore()
	mustSet(t, s, Rule{Layer: LayerUser, Cat: "a", Ch: ChAny, Eff: EffDeny, Ts: 1})
	mustSet(t, s, Rule{Layer: LayerUser, Cat: "a", Ch: ChEmail, Eff: EffAllow, Ts: 2})
	wantDecision(t, s, "a", ChEmail, 2, Decision{
		Allowed: true, Layer: LayerUser, Cat: "a", Ch: ChEmail, Seq: 2,
	})
	// On sms only the * rule is a candidate.
	wantDecision(t, s, "a", ChSMS, 2, Decision{
		Allowed: false, Layer: LayerUser, Cat: "a", Ch: ChAny, Seq: 1,
	})
}

// TestHigherLayerWinsTie: at equal specificity and channel exactness the
// higher layer number wins.
func TestHigherLayerWinsTie(t *testing.T) {
	s := NewStore()
	mustSet(t, s, Rule{Layer: LayerOrg, Cat: "a", Ch: ChAny, Eff: EffDeny, Ts: 1})
	mustSet(t, s, Rule{Layer: LayerUser, Cat: "a", Ch: ChAny, Eff: EffAllow, Ts: 2})
	wantDecision(t, s, "a", ChEmail, 2, Decision{
		Allowed: true, Layer: LayerUser, Cat: "a", Ch: ChAny, Seq: 2,
	})
}

// TestOrgSpecificBeatsUserBroad: a more specific org rule beats a broader
// user rule.
func TestOrgSpecificBeatsUserBroad(t *testing.T) {
	s := NewStore()
	mustSet(t, s, Rule{Layer: LayerUser, Cat: "billing", Ch: ChAny, Eff: EffAllow, Ts: 1})
	mustSet(t, s, Rule{Layer: LayerOrg, Cat: "billing/invoice", Ch: ChAny, Eff: EffDeny, Ts: 2})
	wantDecision(t, s, "billing/invoice", ChEmail, 2, Decision{
		Allowed: false, Layer: LayerOrg, Cat: "billing/invoice", Ch: ChAny, Seq: 2,
	})
}

// TestPlatformAndOrgLocks: with both locks present the platform lock wins
// regardless of specificity.
func TestPlatformAndOrgLocks(t *testing.T) {
	s := NewStore()
	mustSet(t, s, Rule{Layer: LayerUser, Cat: "a/b/c", Ch: ChEmail, Eff: EffAllow, Ts: 1})
	mustSet(t, s, Rule{Layer: LayerOrg, Cat: "a/b", Ch: ChEmail, Eff: EffAllow, Locked: true, Ts: 2})
	mustSet(t, s, Rule{Layer: LayerPlatform, Cat: "", Ch: ChAny, Eff: EffDeny, Locked: true, Ts: 3})
	// Locked candidates only: platform root lock beats the more specific
	// org lock because the smaller layer number wins.
	wantDecision(t, s, "a/b/c", ChEmail, 3, Decision{
		Allowed: false, Layer: LayerPlatform, Cat: "", Ch: ChAny, Seq: 3,
	})
}

// TestLockedAmongLockedOrdering: between two org/platform locks, after
// layer, more segments then exact channel decide.
func TestLockedAmongLockedOrdering(t *testing.T) {
	s := NewStore()
	mustSet(t, s, Rule{Layer: LayerOrg, Cat: "a", Ch: ChAny, Eff: EffDeny, Locked: true, Ts: 1})
	mustSet(t, s, Rule{Layer: LayerOrg, Cat: "a/b", Ch: ChAny, Eff: EffAllow, Locked: true, Ts: 2})
	wantDecision(t, s, "a/b", ChEmail, 2, Decision{
		Allowed: true, Layer: LayerOrg, Cat: "a/b", Ch: ChAny, Seq: 2,
	})
}

// TestLockedExpiryAndTombImmunity: an expired locked rule no longer
// constrains anything; UnsubscribeAll never affects platform/org rules.
func TestLockedExpiryAndTombImmunity(t *testing.T) {
	s := NewStore()
	mustSet(t, s, Rule{Layer: LayerUser, Cat: "a", Ch: ChAny, Eff: EffAllow, Ts: 1})
	mustSet(t, s, Rule{Layer: LayerOrg, Cat: "a", Ch: ChAny, Eff: EffDeny, Locked: true, Exp: 10, Ts: 2})
	// While the lock is effective the user rule loses.
	wantDecision(t, s, "a", ChEmail, 9, Decision{
		Allowed: false, Layer: LayerOrg, Cat: "a", Ch: ChAny, Seq: 2,
	})
	// After expiry the lock is gone and the user rule surfaces.
	wantDecision(t, s, "a", ChEmail, 10, Decision{
		Allowed: true, Layer: LayerUser, Cat: "a", Ch: ChAny, Seq: 1,
	})
	// UnsubscribeAll does not touch org rules; re-add an org lock and
	// verify it still decides after a tomb newer than its ts.
	mustSet(t, s, Rule{Layer: LayerOrg, Cat: "a", Ch: ChAny, Eff: EffDeny, Locked: true, Ts: 11})
	if err := s.UnsubscribeAll(12); err != nil {
		t.Fatal(err)
	}
	wantDecision(t, s, "a", ChEmail, 12, Decision{
		Allowed: false, Layer: LayerOrg, Cat: "a", Ch: ChAny, Seq: 3,
	})
}

// TestLockedCoverageChannelAsymmetry: a locked rule with a concrete channel
// does not block a user * Set, yet still wins resolution on that channel.
func TestLockedCoverageChannelAsymmetry(t *testing.T) {
	s := NewStore()
	mustSet(t, s, Rule{Layer: LayerOrg, Cat: "a", Ch: ChEmail, Eff: EffDeny, Locked: true, Ts: 1})
	// Lock ch=email does not cover user ch=*: Set is accepted.
	mustSet(t, s, Rule{Layer: LayerUser, Cat: "a", Ch: ChAny, Eff: EffAllow, Ts: 2})
	// User ch=sms Set is not covered either.
	mustSet(t, s, Rule{Layer: LayerUser, Cat: "a", Ch: ChSMS, Eff: EffAllow, Ts: 3})
	// But user ch=email Set is covered and rejected.
	err := s.Set(Rule{Layer: LayerUser, Cat: "a", Ch: ChEmail, Eff: EffAllow, Ts: 4})
	wantErr(t, err, ErrLockedOverride)
	// On email the lock still wins over the accepted user * rule.
	wantDecision(t, s, "a", ChEmail, 4, Decision{
		Allowed: false, Layer: LayerOrg, Cat: "a", Ch: ChEmail, Seq: 1,
	})
	// On sms the user rules apply.
	wantDecision(t, s, "a", ChSMS, 4, Decision{
		Allowed: true, Layer: LayerUser, Cat: "a", Ch: ChSMS, Seq: 3,
	})
}

// TestNoCandidateDefaultsDeny: no candidate yields deny with empty source.
func TestNoCandidateDefaultsDeny(t *testing.T) {
	s := NewStore()
	wantDecision(t, s, "a", ChEmail, 0, Decision{Allowed: false})
	mustSet(t, s, Rule{Layer: LayerUser, Cat: "a", Ch: ChEmail, Eff: EffAllow, Ts: 1})
	// Different channel and different category: still no candidate.
	wantDecision(t, s, "a", ChSMS, 1, Decision{Allowed: false})
	wantDecision(t, s, "b", ChEmail, 1, Decision{Allowed: false})
}

// TestSetRejectionOrdering checks the fixed rejection priority for Set.
func TestSetRejectionOrdering(t *testing.T) {
	s := NewStore()
	mustSet(t, s, Rule{Layer: LayerOrg, Cat: "a", Ch: ChAny, Eff: EffDeny, Locked: true, Ts: 10})

	// Invalid parameter beats clock regression.
	err := s.Set(Rule{Layer: 9, Cat: "a", Ch: ChAny, Eff: EffDeny, Ts: 5})
	wantErr(t, err, ErrInvalidParam)
	// Clock regression beats permission.
	err = s.Set(Rule{Layer: LayerUser, Cat: "b", Ch: ChAny, Eff: EffAllow, Locked: true, Ts: 5})
	wantErr(t, err, ErrClockRegression)
	// Permission beats locked override.
	err = s.Set(Rule{Layer: LayerUser, Cat: "a/b", Ch: ChAny, Eff: EffAllow, Locked: true, Ts: 11})
	wantErr(t, err, ErrPermissionDenied)
	// Locked override fires for covered user rules.
	err = s.Set(Rule{Layer: LayerUser, Cat: "a/b", Ch: ChAny, Eff: EffAllow, Ts: 11})
	wantErr(t, err, ErrLockedOverride)
	// Uncovered user Set is fine.
	mustSet(t, s, Rule{Layer: LayerUser, Cat: "c", Ch: ChAny, Eff: EffAllow, Ts: 11})
}

// TestSetInvalidParams covers each invalid-parameter case for Set.
func TestSetInvalidParams(t *testing.T) {
	s := NewStore()
	base := Rule{Layer: LayerUser, Cat: "a", Ch: ChEmail, Eff: EffAllow, Ts: 1}

	bad := base
	bad.Layer = 0
	wantErr(t, s.Set(bad), ErrInvalidParam)
	bad = base
	bad.Cat = "A"
	wantErr(t, s.Set(bad), ErrInvalidParam)
	bad = base
	bad.Cat = "a//b"
	wantErr(t, s.Set(bad), ErrInvalidParam)
	bad = base
	bad.Cat = "a/b/c/d/e"
	wantErr(t, s.Set(bad), ErrInvalidParam)
	bad = base
	bad.Cat = "abcdefghijklmnopq" // 17 chars
	wantErr(t, s.Set(bad), ErrInvalidParam)
	bad = base
	bad.Ch = "webhook"
	wantErr(t, s.Set(bad), ErrInvalidParam)
	bad = base
	bad.Eff = "maybe"
	wantErr(t, s.Set(bad), ErrInvalidParam)
	bad = base
	bad.Exp = 1 // exp must be > ts
	wantErr(t, s.Set(bad), ErrInvalidParam)
	bad = base
	bad.Ts = -1
	wantErr(t, s.Set(bad), ErrInvalidParam)

	// Nothing was accepted; the store is still empty.
	wantDecision(t, s, "a", ChEmail, 0, Decision{Allowed: false})
}

// TestRejectedOpsKeepState verifies rejected operations change neither
// rules, seq, tomb nor maxTs.
func TestRejectedOpsKeepState(t *testing.T) {
	s := NewStore()
	mustSet(t, s, Rule{Layer: LayerPlatform, Cat: "", Ch: ChAny, Eff: EffDeny, Ts: 5})
	mustSet(t, s, Rule{Layer: LayerUser, Cat: "a", Ch: ChAny, Eff: EffAllow, Ts: 6})
	before := mustResolve(t, s, "a", ChEmail, 6)

	// Rejected Set: clock regression.
	wantErr(t, s.Set(Rule{Layer: LayerUser, Cat: "b", Ch: ChAny, Eff: EffAllow, Ts: 3}), ErrClockRegression)
	// Rejected Remove: not found.
	wantErr(t, s.Remove(LayerUser, "nope", ChAny, 7), ErrNotFound)
	// Rejected UnsubscribeAll: clock regression.
	wantErr(t, s.UnsubscribeAll(4), ErrClockRegression)
	// Rejected Resolve: invalid params.
	if _, err := s.Resolve("", ChEmail, 6); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("Resolve empty cat err = %v", err)
	}
	if _, err := s.Resolve("a", ChAny, 6); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("Resolve wildcard ch err = %v", err)
	}
	if _, err := s.Resolve("a", ChEmail, 5); !errors.Is(err, ErrClockRegression) {
		t.Fatalf("Resolve past now err = %v", err)
	}

	// State is untouched: same decision, and seq of a new Set continues
	// from 2 (rejected ops consumed no seq).
	if got := mustResolve(t, s, "a", ChEmail, 6); got != before {
		t.Fatalf("decision changed after rejected ops: %+v -> %+v", before, got)
	}
	mustSet(t, s, Rule{Layer: LayerUser, Cat: "b", Ch: ChAny, Eff: EffAllow, Ts: 7})
	wantDecision(t, s, "b", ChEmail, 7, Decision{
		Allowed: true, Layer: LayerUser, Cat: "b", Ch: ChAny, Seq: 3,
	})
	// Resolve does not advance the clock: ts == maxTs is still accepted.
	mustSet(t, s, Rule{Layer: LayerUser, Cat: "c", Ch: ChAny, Eff: EffAllow, Ts: 7})
}

// TestCapacity: at most 1000 rules; overwriting an existing key never
// counts as new.
func TestCapacity(t *testing.T) {
	s := NewStore()
	rule := func(i int) Rule {
		return Rule{
			Layer: LayerUser,
			Cat:   "c" + itoa(i),
			Ch:    ChAny, Eff: EffAllow, Ts: int64(i + 1),
		}
	}
	for i := 0; i < maxRules; i++ {
		if err := s.Set(rule(i)); err != nil {
			t.Fatalf("Set %d failed: %v", i, err)
		}
	}
	// Overwrite of an existing key is allowed at capacity.
	overwrite := rule(0)
	overwrite.Ts = int64(maxRules + 1)
	if err := s.Set(overwrite); err != nil {
		t.Fatalf("overwrite at capacity failed: %v", err)
	}
	// A new key is rejected.
	err := s.Set(Rule{Layer: LayerUser, Cat: "overflow", Ch: ChAny, Eff: EffAllow, Ts: int64(maxRules + 2)})
	wantErr(t, err, ErrCapacity)
	// After a Remove there is room again.
	if err := s.Remove(LayerUser, rule(0).Cat, ChAny, int64(maxRules+3)); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}
	mustSet(t, s, Rule{Layer: LayerUser, Cat: "overflow", Ch: ChAny, Eff: EffAllow, Ts: int64(maxRules + 4)})
}

// TestRemove covers Remove validation, locked override and not-found.
func TestRemove(t *testing.T) {
	s := NewStore()
	mustSet(t, s, Rule{Layer: LayerUser, Cat: "a", Ch: ChAny, Eff: EffAllow, Ts: 1})
	mustSet(t, s, Rule{Layer: LayerUser, Cat: "b/c", Ch: ChEmail, Eff: EffAllow, Ts: 2})
	mustSet(t, s, Rule{Layer: LayerOrg, Cat: "b", Ch: ChAny, Eff: EffDeny, Locked: true, Ts: 3})

	// Invalid params.
	wantErr(t, s.Remove(0, "a", ChAny, 4), ErrInvalidParam)
	wantErr(t, s.Remove(LayerUser, "A", ChAny, 4), ErrInvalidParam)
	wantErr(t, s.Remove(LayerUser, "a", "bad", 4), ErrInvalidParam)
	wantErr(t, s.Remove(LayerUser, "a", ChAny, -1), ErrInvalidParam)
	// Clock regression.
	wantErr(t, s.Remove(LayerUser, "a", ChAny, 2), ErrClockRegression)
	// Locked override applies to user-layer Remove only.
	wantErr(t, s.Remove(LayerUser, "b/c", ChEmail, 4), ErrLockedOverride)
	// Not found.
	wantErr(t, s.Remove(LayerUser, "zz", ChAny, 4), ErrNotFound)
	// Org-layer remove is not subject to locked override.
	if err := s.Remove(LayerOrg, "b", ChAny, 4); err != nil {
		t.Fatalf("Remove org failed: %v", err)
	}
	// Successful user remove drops the rule from resolution.
	if err := s.Remove(LayerUser, "a", ChAny, 5); err != nil {
		t.Fatalf("Remove user failed: %v", err)
	}
	wantDecision(t, s, "a", ChEmail, 5, Decision{Allowed: false})
}

// TestUnsubscribeAllValidation covers its rejection cases.
func TestUnsubscribeAllValidation(t *testing.T) {
	s := NewStore()
	wantErr(t, s.UnsubscribeAll(-1), ErrInvalidParam)
	if err := s.UnsubscribeAll(5); err != nil {
		t.Fatal(err)
	}
	wantErr(t, s.UnsubscribeAll(4), ErrClockRegression)
	// Equal ts is accepted.
	if err := s.UnsubscribeAll(5); err != nil {
		t.Fatal(err)
	}
}

// TestConcurrent exercises concurrent mixed operations under -race; every
// goroutine's Resolve must observe a consistent snapshot.
func TestConcurrent(t *testing.T) {
	s := NewStore()
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				ts := int64(w*200 + i + 1)
				_ = s.Set(Rule{
					Layer: LayerUser, Cat: "c" + itoa(w),
					Ch: ChAny, Eff: EffAllow, Ts: ts,
				})
				_, _ = s.Resolve("c"+itoa(w), ChEmail, ts)
				if i%50 == 0 {
					_ = s.UnsubscribeAll(ts)
				}
			}
		}(w)
	}
	wg.Wait()
}
