package lessor

import (
	"errors"
	"fmt"
	"testing"
)

func testLessor(t *testing.T) *Lessor {
	t.Helper()
	l, err := New(Config{MinTTL: 5, MaxTTL: 1000, E: 3, R: 2, Kmax: 4})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return l
}

func mustPromote(t *testing.T, l *Lessor, now int64) {
	t.Helper()
	if err := l.Promote(now); err != nil {
		t.Fatalf("Promote(%d): %v", now, err)
	}
}

func mustGrant(t *testing.T, l *Lessor, id, ttl, now int64) {
	t.Helper()
	if err := l.Grant(id, ttl, now); err != nil {
		t.Fatalf("Grant(%d,%d,%d): %v", id, ttl, now, err)
	}
}

func ttlOf(t *testing.T, l *Lessor, id, now int64) int64 {
	t.Helper()
	v, err := l.TTL(id, now)
	if err != nil {
		t.Fatalf("TTL(%d,%d): %v", id, now, err)
	}
	return v
}

func assertRevocations(t *testing.T, got, want []Revocation) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("revocations len = %d (%v), want %d (%v)", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i].ID != want[i].ID {
			t.Fatalf("revocation[%d] id = %d, want %d", i, got[i].ID, want[i].ID)
		}
		if fmt.Sprint(got[i].Keys) != fmt.Sprint(want[i].Keys) {
			t.Fatalf("revocation[%d] keys = %v, want %v", i, got[i].Keys, want[i].Keys)
		}
	}
}

// TestExampleFromSpec replays the two worked examples from the specification.
func TestExampleFromSpec(t *testing.T) {
	l := testLessor(t)
	mustPromote(t, l, 0)
	mustGrant(t, l, 1, 10, 0)
	mustGrant(t, l, 2, 2, 1)
	mustGrant(t, l, 3, 8, 1)
	mustGrant(t, l, 4, 5, 2)

	if err := l.Attach("a", 1, 2); err != nil {
		t.Fatal(err)
	}
	if err := l.Attach("b", 2, 2); err != nil {
		t.Fatal(err)
	}
	if err := l.Attach("c", 2, 2); err != nil {
		t.Fatal(err)
	}
	if err := l.Attach("a", 3, 3); err != nil {
		t.Fatal(err)
	}

	got, err := l.Tick(9)
	if err != nil {
		t.Fatal(err)
	}
	want := []Revocation{{ID: 2, Keys: []string{"b", "c"}}, {ID: 4, Keys: []string{}}}
	assertRevocations(t, got, want)

	if _, err := l.Renew(3, 9); !errors.Is(err, ErrExpired) {
		t.Fatalf("Renew backlogged: want ErrExpired, got %v", err)
	}

	got, err = l.Tick(10)
	if err != nil {
		t.Fatal(err)
	}
	want = []Revocation{{ID: 3, Keys: []string{"a"}}, {ID: 1, Keys: []string{}}}
	assertRevocations(t, got, want)

	l = testLessor(t)
	mustPromote(t, l, 0)
	mustGrant(t, l, 7, 100, 0)
	if err := l.Checkpoint(30); err != nil {
		t.Fatal(err)
	}
	if err := l.Demote(40); err != nil {
		t.Fatal(err)
	}
	if v := ttlOf(t, l, 7, 45); v != 70 {
		t.Fatalf("follower TTL = %d, want 70", v)
	}
	if err := l.Promote(50); err != nil {
		t.Fatal(err)
	}
	if v := ttlOf(t, l, 7, 50); v != 73 {
		t.Fatalf("leader TTL after promote = %d, want 73", v)
	}
	g, err := l.Renew(7, 60)
	if err != nil {
		t.Fatal(err)
	}
	if g != 100 {
		t.Fatalf("Renew returned g = %d, want 100", g)
	}
	if v := ttlOf(t, l, 7, 60); v != 100 {
		t.Fatalf("TTL after renew = %d, want 100", v)
	}
	if v := ttlOf(t, l, 7, 160); v != 0 {
		t.Fatalf("TTL at x = %d, want 0", v)
	}
}

// TestEffectiveTTLAndRenew verifies g = max(ttl, MinTTL) and that Renew
// restores g, not the originally requested ttl.
func TestEffectiveTTLAndRenew(t *testing.T) {
	l := testLessor(t)
	mustPromote(t, l, 0)
	mustGrant(t, l, 1, 2, 0)
	if v := ttlOf(t, l, 1, 0); v != 5 {
		t.Fatalf("g = %d, want 5", v)
	}
	g, err := l.Renew(1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if g != 5 {
		t.Fatalf("Renew g = %d, want 5", g)
	}
	if v := ttlOf(t, l, 1, 2); v != 5 {
		t.Fatalf("after renew TTL = %d, want 5", v)
	}
	if v := ttlOf(t, l, 1, 7); v != 0 {
		t.Fatalf("TTL at expiry instant = %d, want 0", v)
	}
}

// TestExpiryAtExactNow covers x == now semantics.
func TestExpiryAtExactNow(t *testing.T) {
	l := testLessor(t)
	mustPromote(t, l, 0)
	mustGrant(t, l, 1, 5, 0)
	if _, err := l.Renew(1, 5); !errors.Is(err, ErrExpired) {
		t.Fatalf("Renew at x: %v", err)
	}
	if err := l.Attach("k", 1, 5); !errors.Is(err, ErrExpired) {
		t.Fatalf("Attach at x: %v", err)
	}
	if v := ttlOf(t, l, 1, 5); v != 0 {
		t.Fatalf("TTL at x = %d", v)
	}
	got, err := l.Tick(5)
	if err != nil {
		t.Fatal(err)
	}
	assertRevocations(t, got, []Revocation{{ID: 1, Keys: []string{}}})
}

// TestBacklogOrderAndRateLimit checks same-x id ordering, backlog spanning
// multiple Ticks, and expired Renew rejection leaving the lease in the
// backlog.
func TestBacklogOrderAndRateLimit(t *testing.T) {
	l := testLessor(t) // R = 2
	mustPromote(t, l, 0)
	mustGrant(t, l, 30, 10, 0)
	mustGrant(t, l, 10, 10, 0)
	mustGrant(t, l, 20, 10, 0)
	mustGrant(t, l, 40, 20, 0)

	got, err := l.Tick(10)
	if err != nil {
		t.Fatal(err)
	}
	assertRevocations(t, got, []Revocation{{ID: 10, Keys: []string{}}, {ID: 20, Keys: []string{}}})

	if _, err := l.Renew(30, 11); !errors.Is(err, ErrExpired) {
		t.Fatalf("Renew backlog: %v", err)
	}

	got, err = l.Tick(15)
	if err != nil {
		t.Fatal(err)
	}
	assertRevocations(t, got, []Revocation{{ID: 30, Keys: []string{}}})

	got, err = l.Tick(20)
	if err != nil {
		t.Fatal(err)
	}
	assertRevocations(t, got, []Revocation{{ID: 40, Keys: []string{}}})
}

// TestAttachMoveFullAndSorted covers key movement, full-check-before-detach,
// sorted return order and idempotent re-attach.
func TestAttachMoveFullAndSorted(t *testing.T) {
	l := testLessor(t) // Kmax = 4
	mustPromote(t, l, 0)
	mustGrant(t, l, 1, 100, 0)
	mustGrant(t, l, 2, 100, 0)

	for _, k := range []string{"d", "b", "a", "c"} {
		if err := l.Attach(k, 1, 1); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Attach("z", 2, 1); err != nil {
		t.Fatal(err)
	}
	if err := l.Attach("z", 1, 1); !errors.Is(err, ErrLeaseFull) {
		t.Fatalf("full attach: %v", err)
	}
	keys, err := l.Revoke(2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(keys) != "[z]" {
		t.Fatalf("revoked keys = %v, want [z]", keys)
	}

	mustGrant(t, l, 2, 100, 2)
	if err := l.Attach("a", 2, 2); err != nil {
		t.Fatal(err)
	}
	if err := l.Attach("a", 2, 3); err != nil {
		t.Fatal(err)
	}

	got, err := l.Tick(102)
	if err != nil {
		t.Fatal(err)
	}
	assertRevocations(t, got, []Revocation{
		{ID: 1, Keys: []string{"b", "c", "d"}},
		{ID: 2, Keys: []string{"a"}},
	})
}

// TestCheckpointAndFailover checks checkpoint only writes live leases,
// follower TTL reads sv, Promote adds E, and Renew clears sv.
func TestCheckpointAndFailover(t *testing.T) {
	l := testLessor(t)
	mustPromote(t, l, 0)
	mustGrant(t, l, 1, 100, 0)
	mustGrant(t, l, 2, 10, 0)
	l.leases[2].sv = 9

	if err := l.Checkpoint(30); err != nil {
		t.Fatal(err)
	}
	if l.leases[1].sv != 70 {
		t.Fatalf("sv live = %d, want 70", l.leases[1].sv)
	}
	if l.leases[2].sv != 9 {
		t.Fatalf("sv expired changed = %d, want 9", l.leases[2].sv)
	}

	if err := l.Demote(35); err != nil {
		t.Fatal(err)
	}
	if v := ttlOf(t, l, 1, 999); v != 70 {
		t.Fatalf("follower TTL with sv = %d, want 70", v)
	}
	if v := ttlOf(t, l, 2, 999); v != 9 {
		t.Fatalf("follower TTL backlogged sv = %d, want 9", v)
	}
	if err := l.Promote(40); err != nil {
		t.Fatal(err)
	}
	if v := ttlOf(t, l, 1, 40); v != 73 {
		t.Fatalf("lease1 TTL = %d, want 73 (E included)", v)
	}
	if v := ttlOf(t, l, 2, 40); v != 12 {
		t.Fatalf("lease2 TTL = %d, want 12", v)
	}

	// Renew clears sv; a failover after a fresh checkpoint gives the
	// checkpointed remainder; a renewed lease without checkpoint gets g.
	if _, err := l.Renew(1, 50); err != nil {
		t.Fatal(err)
	}
	if l.leases[1].sv != 0 {
		t.Fatalf("sv after renew = %d, want 0", l.leases[1].sv)
	}
	if err := l.Checkpoint(60); err != nil {
		t.Fatal(err)
	}
	if l.leases[1].sv != 90 {
		t.Fatalf("sv = %d, want 90", l.leases[1].sv)
	}
	if err := l.Demote(70); err != nil {
		t.Fatal(err)
	}
	if err := l.Promote(80); err != nil {
		t.Fatal(err)
	}
	if v := ttlOf(t, l, 1, 80); v != 93 {
		t.Fatalf("TTL = %d, want 93", v)
	}

	// g fallback: a brand-new lease, no checkpoint, renewed, then failover.
	mustGrant(t, l, 3, 50, 81)
	if _, err := l.Renew(3, 90); err != nil {
		t.Fatal(err)
	}
	if err := l.Demote(95); err != nil {
		t.Fatal(err)
	}
	if err := l.Promote(100); err != nil {
		t.Fatal(err)
	}
	if v := ttlOf(t, l, 3, 100); v != 53 {
		t.Fatalf("g-fallback TTL = %d, want 53", v)
	}
}

// TestRoleErrorsAndRejection verifies role precedence and that rejected calls
// change neither lease data, key ownership, the watermark nor the backlog.
func TestRoleErrorsAndRejection(t *testing.T) {
	l := testLessor(t)

	// Follower rejects leader-only ops, even with otherwise bad args:
	// argument validation comes before the role check though.
	if err := l.Grant(0, 1, 0); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("arg precedence: %v", err)
	}
	if err := l.Grant(1, 1, 0); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("follower grant: %v", err)
	}
	if _, err := l.Tick(0); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("follower tick: %v", err)
	}

	mustPromote(t, l, 0)
	if err := l.Promote(1); !errors.Is(err, ErrWrongRole) {
		t.Fatalf("double promote: %v", err)
	}

	mustGrant(t, l, 1, 10, 1)

	// Bad time must not move the watermark: after rejecting now=100 (ahead
	// calls are legal... use a backwards now), a later legal call at T works.
	if err := l.Grant(2, 10, 0); !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("backwards time: %v", err)
	}
	if err := l.Grant(1, 10, 1); !errors.Is(err, ErrLeaseExists) {
		t.Fatalf("dup grant: %v", err)
	}
	if _, err := l.Renew(9, 1); !errors.Is(err, ErrNoLease) {
		t.Fatalf("renew missing: %v", err)
	}
	if err := l.Attach("k", 9, 1); !errors.Is(err, ErrNoLease) {
		t.Fatalf("attach missing: %v", err)
	}
	if _, err := l.Revoke(9, 1); !errors.Is(err, ErrNoLease) {
		t.Fatalf("revoke missing: %v", err)
	}
	if _, err := l.TTL(9, 1); !errors.Is(err, ErrNoLease) {
		t.Fatalf("ttl missing: %v", err)
	}
	if err := l.Attach("", 1, 1); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("empty key: %v", err)
	}

	// Nothing changed: watermark still 1, lease 1 intact, key absent.
	if l.clock != 1 {
		t.Fatalf("watermark changed to %d", l.clock)
	}
	if len(l.leases) != 1 || l.leases[1] == nil {
		t.Fatalf("leases mutated: %v", l.leases)
	}
	if len(l.keyOwner) != 0 {
		t.Fatalf("keyOwner mutated: %v", l.keyOwner)
	}

	// Expired attach rejection must not move the key either.
	mustGrant(t, l, 2, 2, 1) // x = 6
	if err := l.Attach("q", 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := l.Attach("q", 2, 7); !errors.Is(err, ErrExpired) {
		t.Fatalf("attach expired lease: %v", err)
	}
	if l.keyOwner["q"] != 1 {
		t.Fatalf("q moved despite rejection: %v", l.keyOwner)
	}

	if err := l.Demote(8); err != nil {
		t.Fatal(err)
	}
	if err := l.Demote(9); !errors.Is(err, ErrWrongRole) {
		t.Fatalf("double demote: %v", err)
	}
}

// TestInvalidConfig checks the boundary rules for construction.
func TestInvalidConfig(t *testing.T) {
	good := Config{MinTTL: 5, MaxTTL: 1000, E: 3, R: 2, Kmax: 4}
	cases := []Config{
		{MinTTL: 0, MaxTTL: 1000, E: 3, R: 2, Kmax: 4},
		{MinTTL: 1_000_001, MaxTTL: 1_000_001, E: 3, R: 2, Kmax: 4},
		{MinTTL: 10, MaxTTL: 9, E: 3, R: 2, Kmax: 4},
		{MinTTL: 5, MaxTTL: 1_000_000_001, E: 3, R: 2, Kmax: 4},
		{MinTTL: 5, MaxTTL: 1000, E: -1, R: 2, Kmax: 4},
		{MinTTL: 5, MaxTTL: 1000, E: 1_000_000_001, R: 2, Kmax: 4},
		{MinTTL: 5, MaxTTL: 1000, E: 3, R: 0, Kmax: 4},
		{MinTTL: 5, MaxTTL: 1000, E: 3, R: 1_000_001, Kmax: 4},
		{MinTTL: 5, MaxTTL: 1000, E: 3, R: 2, Kmax: 0},
		{MinTTL: 5, MaxTTL: 1000, E: 3, R: 2, Kmax: 1_000_001},
	}
	if _, err := New(good); err != nil {
		t.Fatalf("good config rejected: %v", err)
	}
	for i, cfg := range cases {
		if _, err := New(cfg); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("case %d accepted: %+v err=%v", i, cfg, err)
		}
	}
}
