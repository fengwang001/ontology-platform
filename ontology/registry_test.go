package ontology

import (
	"bytes"
	"errors"
	"sort"
	"testing"
)

func b(s string) []byte { return []byte(s) }

func mustTargets(t *testing.T, reg *Registry, u string, now int64) []string {
	t.Helper()
	got, err := reg.Targets(b(u), now)
	if err != nil {
		t.Fatalf("Targets(%q,%d): %v", u, now, err)
	}
	out := make([]string, len(got))
	for i, tok := range got {
		out[i] = string(tok)
	}
	return out
}

func TestRZeroReleasesImmediately(t *testing.T) {
	reg, _ := NewRegistry(4, 0, 100, 100)
	_ = reg.Register(b("u"), b("d"), b("t1"), 1)
	_ = reg.Register(b("u"), b("d"), b("t2"), 2)
	bd := reg.bindings[deviceKey(b("u"), b("d"))]
	if len(bd.old) != 0 {
		t.Fatalf("R=0 keeps no old tokens: %d", len(bd.old))
	}
	if _, owned := reg.owners["t1"]; owned {
		t.Fatal("retired token must be released immediately with R=0")
	}
	if err := reg.Register(b("v"), b("x"), b("t1"), 3); err != nil {
		t.Fatal(err)
	}
}

func TestROldestDropped(t *testing.T) {
	reg, _ := NewRegistry(4, 2, 100, 100)
	_ = reg.Register(b("u"), b("d"), b("t1"), 1)
	_ = reg.Register(b("u"), b("d"), b("t2"), 2)
	_ = reg.Register(b("u"), b("d"), b("t3"), 3) // old = [t2,t1]
	_ = reg.Register(b("u"), b("d"), b("t4"), 4) // old = [t3,t2], t1 released
	bd := reg.bindings[deviceKey(b("u"), b("d"))]
	if len(bd.old) != 2 || string(bd.old[0].tok) != "t3" || string(bd.old[1].tok) != "t2" {
		t.Fatalf("old list = %+v", bd.old)
	}
	if _, owned := reg.owners["t1"]; owned {
		t.Fatal("oldest t1 must have been released")
	}
}

func TestGraceBoundary(t *testing.T) {
	reg, _ := NewRegistry(4, 4, 10, 0)
	_ = reg.Register(b("u"), b("d"), b("t1"), 1)
	_ = reg.Register(b("u"), b("d"), b("t2"), 5) // t1 retired at 5
	if got := mustTargets(t, reg, "u", 14); !eqStrings(got, []string{"t2", "t1"}) {
		t.Fatalf("grace-1: %v", got)
	}
	if got := mustTargets(t, reg, "u", 15); !eqStrings(got, []string{"t2"}) {
		t.Fatalf("grace boundary: %v", got)
	}
	bd := reg.bindings[deviceKey(b("u"), b("d"))]
	if len(bd.old) != 1 {
		t.Fatal("expired old tokens are not auto-deleted and still occupy an R slot")
	}
	if err := reg.Register(b("v"), b("x"), b("t1"), 16); err != nil {
		t.Fatal(err)
	}
	if len(reg.bindings[deviceKey(b("u"), b("d"))].old) != 0 {
		t.Fatal("expired old token must be removable via transfer")
	}
}

func TestEvictionTieDeviceName(t *testing.T) {
	reg, _ := NewRegistry(2, 0, 0, 0)
	_ = reg.Register(b("u"), b("db"), b("tb"), 5)
	_ = reg.Register(b("u"), b("da"), b("ta"), 5)
	_ = reg.Register(b("u"), b("dc"), b("tc"), 5) // evicts "da" (byte-order smallest)
	if _, ok := reg.bindings[deviceKey(b("u"), b("da"))]; ok {
		t.Fatal("da should be evicted (tie broken by device name)")
	}
	if _, ok := reg.bindings[deviceKey(b("u"), b("db"))]; !ok {
		t.Fatal("db should remain")
	}
}

func TestEvictedTokenImmediatelyUsable(t *testing.T) {
	reg, _ := NewRegistry(1, 0, 0, 0)
	_ = reg.Register(b("u"), b("d1"), b("t1"), 1)
	_ = reg.Register(b("u"), b("d2"), b("t2"), 2) // d1 and t1 evicted
	if err := reg.Register(b("v"), b("x"), b("t1"), 3); err != nil {
		t.Fatalf("evicted token must be free: %v", err)
	}
}

func TestFeedbackBZero(t *testing.T) {
	reg, _ := NewRegistry(4, 4, 0, 0)
	_ = reg.Register(b("u"), b("d"), b("t1"), 1)
	if err := reg.Feedback(b("t1"), 2); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(b("u"), b("d"), b("t1"), 2); err != nil {
		t.Fatalf("B=0 re-register at the same now: %v", err)
	}
}

func TestFeedbackOldTokenOnlyRemoves(t *testing.T) {
	reg, _ := NewRegistry(4, 4, 100, 20)
	_ = reg.Register(b("u"), b("d"), b("t1"), 1)
	_ = reg.Register(b("u"), b("d"), b("t2"), 2) // t1 old
	if err := reg.Feedback(b("t1"), 3); err != nil {
		t.Fatal(err)
	}
	bd := reg.bindings[deviceKey(b("u"), b("d"))]
	if string(bd.cur) != "t2" || len(bd.old) != 0 {
		t.Fatalf("feedback on old token: %+v", bd)
	}
	if err := reg.Register(b("v"), b("x"), b("t1"), 22); !errors.Is(err, ErrTokenBlocked) {
		t.Fatalf("fed-back old token blocked until 23: %v", err)
	}
	if err := reg.Register(b("v"), b("x"), b("t1"), 23); err != nil {
		t.Fatalf("release at block boundary: %v", err)
	}
}

func TestRejectionOrderAndNoStateChange(t *testing.T) {
	reg, _ := NewRegistry(2, 2, 10, 20)
	_ = reg.Register(b("u"), b("d"), b("t1"), 10)

	// Invalid argument beats clock skew.
	if err := reg.Register(nil, b("d"), b("t"), 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("got %v", err)
	}
	// Clock skew beats block window / not found.
	if err := reg.Register(b("u"), b("d"), b("zz"), 5); !errors.Is(err, ErrClockSkew) {
		t.Fatalf("register skew: %v", err)
	}
	if err := reg.Touch(b("u"), b("missing"), 5); !errors.Is(err, ErrClockSkew) {
		t.Fatalf("touch skew: %v", err)
	}
	if err := reg.Unregister(b("u"), b("missing"), 5); !errors.Is(err, ErrClockSkew) {
		t.Fatalf("unregister skew: %v", err)
	}
	if err := reg.Feedback(b("zz"), 5); !errors.Is(err, ErrClockSkew) {
		t.Fatalf("feedback skew: %v", err)
	}
	// Blocked token beats nothing else for Register (it is the Register-specific
	// rejection after the generic checks).
	_ = reg.Feedback(b("t1"), 11) // deletes (u,d); blk[t1] = 31
	if err := reg.Register(b("u"), b("d"), b("t1"), 20); !errors.Is(err, ErrTokenBlocked) {
		t.Fatalf("block: %v", err)
	}
	// Touch / Unregister report device not found.
	if err := reg.Touch(b("u"), b("d"), 20); !errors.Is(err, ErrDeviceNotFound) {
		t.Fatalf("touch missing: %v", err)
	}
	if err := reg.Unregister(b("u"), b("d"), 20); !errors.Is(err, ErrDeviceNotFound) {
		t.Fatalf("unregister missing: %v", err)
	}
	// Feedback on unowned token.
	if err := reg.Feedback(b("ghost"), 20); !errors.Is(err, ErrTokenUnowned) {
		t.Fatalf("feedback unowned: %v", err)
	}
	// maxNow must not have moved past 11 despite accepted-looking rejections.
	if reg.maxNow != 11 {
		t.Fatalf("rejected ops changed maxNow: %d", reg.maxNow)
	}
	// State snapshot before/after a rejected Register must be identical.
	before := dumpRegistry(reg)
	if err := reg.Register(b("u"), b("d2"), b("t1"), 25); !errors.Is(err, ErrTokenBlocked) {
		t.Fatal(err)
	}
	if after := dumpRegistry(reg); after != before {
		t.Fatalf("rejected op changed state:\nbefore %s\nafter  %s", before, after)
	}
}

func TestInvalidArguments(t *testing.T) {
	for _, c := range []struct {
		d, r  int
		g, bv int64
	}{
		{0, 1, 1, 1}, {65, 1, 1, 1}, {2, -1, 1, 1}, {2, 9, 1, 1},
		{2, 1, -1, 1}, {2, 1, 1_000_000_001, 1}, {2, 1, 1, -1}, {2, 1, 1, 1_000_000_001},
	} {
		if _, err := NewRegistry(c.d, c.r, c.g, c.bv); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("NewRegistry(%+v): %v", c, err)
		}
	}
	reg, _ := NewRegistry(2, 2, 10, 20)
	long := make([]byte, 65)
	tokLong := make([]byte, 257)
	cases := []func() error{
		func() error { return reg.Register(nil, b("d"), b("t"), 1) },
		func() error { return reg.Register(b("u"), long, b("t"), 1) },
		func() error { return reg.Register(b("u"), b("d"), tokLong, 1) },
		func() error { return reg.Register(b("u"), b("d"), b("t"), -1) },
		func() error { return reg.Register(b("u"), b("d"), b("t"), 1_000_000_000_001) },
		func() error { return reg.Touch(nil, b("d"), 1) },
		func() error { return reg.Unregister(b("u"), nil, 1) },
		func() error { return reg.Feedback(nil, 1) },
		func() error { _, e := reg.Targets(nil, 1); return e },
	}
	for i, fn := range cases {
		if err := fn(); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("case %d: %v", i, err)
		}
	}
}

func dumpRegistry(reg *Registry) string {
	var buf bytes.Buffer
	keys := make([]string, 0, len(reg.bindings))
	for k := range reg.bindings {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		bd := reg.bindings[k]
		buf.WriteString(string(bd.user))
		buf.WriteByte(0)
		buf.WriteString(string(bd.device))
		buf.WriteByte(0)
		buf.WriteString(string(bd.cur))
		buf.WriteByte(0)
		for _, ot := range bd.old {
			buf.WriteString(string(ot.tok))
			buf.WriteByte(':')
		}
		buf.WriteByte(';')
	}
	for _, k := range sortedInt64Keys(reg.blk) {
		buf.WriteString(k)
		buf.WriteByte('=')
	}
	return buf.String()
}

func sortedInt64Keys(m map[string]int64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// TestReregisterRefreshesLastSeen covers the idempotent current-token case.
func TestReregisterRefreshesLastSeen(t *testing.T) {
	reg, _ := NewRegistry(2, 2, 10, 20)
	if err := reg.Register(b("u"), b("d1"), b("t1"), 1); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(b("u"), b("d2"), b("t2"), 2); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(b("u"), b("d1"), b("t1"), 10); err != nil {
		t.Fatal(err)
	}
	if got := mustTargets(t, reg, "u", 10); !eqStrings(got, []string{"t1", "t2"}) {
		t.Fatalf("%v", got)
	}
	if len(reg.devices["u"]) != 2 {
		t.Fatalf("re-register must not create a device: %d", len(reg.devices["u"]))
	}
}

// TestTransferCurrentDeletesBinding: a stolen current token removes the whole
// source binding including its old tokens.
func TestTransferCurrentDeletesBinding(t *testing.T) {
	reg, _ := NewRegistry(4, 4, 100, 100)
	_ = reg.Register(b("u"), b("d1"), b("t1"), 1)
	_ = reg.Register(b("u"), b("d1"), b("t1b"), 2) // t1 retired, held as old token
	_ = reg.Register(b("v"), b("dx"), b("t1b"), 3) // steal current of (u,d1)
	if _, ok := reg.bindings[deviceKey(b("u"), b("d1"))]; ok {
		t.Fatal("source binding must be deleted entirely")
	}
	if _, owned := reg.owners["t1"]; owned {
		t.Fatal("old tokens of the stolen binding must be released too")
	}
}

// TestTransferOldTokenOnlyRemoves: stealing an old token removes only that
// entry and keeps the source binding.
func TestTransferOldTokenOnlyRemoves(t *testing.T) {
	reg, _ := NewRegistry(4, 4, 100, 100)
	_ = reg.Register(b("u"), b("d1"), b("t1"), 1)
	_ = reg.Register(b("u"), b("d1"), b("t2"), 2) // t1 now old
	_ = reg.Register(b("v"), b("dx"), b("t1"), 3) // steal u's old token
	src := reg.bindings[deviceKey(b("u"), b("d1"))]
	if src == nil {
		t.Fatal("source binding must survive")
	}
	if len(src.old) != 0 {
		t.Fatalf("only the stolen old token removed, got %d", len(src.old))
	}
	if _, owned := reg.owners["t1"]; !owned {
		t.Fatal("stolen token must belong to the new binding")
	}
}

// TestStealWithinUserFreesSlot: step (1) reduces the device count before the
// step (3) cap check, so stealing another device's current token deletes that
// device and no eviction is needed.
func TestStealWithinUserFreesSlot(t *testing.T) {
	reg, _ := NewRegistry(2, 2, 100, 100)
	_ = reg.Register(b("u"), b("d1"), b("t1"), 1)
	_ = reg.Register(b("u"), b("d2"), b("t2"), 2)
	if err := reg.Register(b("u"), b("d3"), b("t1"), 3); err != nil {
		t.Fatal(err)
	}
	if len(reg.devices["u"]) != 2 {
		t.Fatalf("expected 2 devices, got %d", len(reg.devices["u"]))
	}
	if _, ok := reg.bindings[deviceKey(b("u"), b("d2"))]; !ok {
		t.Fatal("d2 must not have been evicted")
	}
}

// TestOldTokenPromotedBack: re-registering an old token on its own device
// makes it current again and removes it from the old list.
func TestOldTokenPromotedBack(t *testing.T) {
	reg, _ := NewRegistry(4, 4, 100, 100)
	_ = reg.Register(b("u"), b("d"), b("t1"), 1)
	_ = reg.Register(b("u"), b("d"), b("t2"), 2) // t1 old
	_ = reg.Register(b("u"), b("d"), b("t1"), 3) // t1 promoted; t2 retired
	bd := reg.bindings[deviceKey(b("u"), b("d"))]
	if string(bd.cur) != "t1" {
		t.Fatalf("cur = %q", bd.cur)
	}
	if len(bd.old) != 1 || string(bd.old[0].tok) != "t2" {
		t.Fatalf("old list = %+v", bd.old)
	}
	if o := reg.owners["t1"]; o.isOld {
		t.Fatal("t1 must be a current owner again")
	}
}

func eqStrings(a, c []string) bool {
	if len(a) != len(c) {
		return false
	}
	for i := range a {
		if a[i] != c[i] {
			return false
		}
	}
	return true
}

// TestSpecWalkthrough verifies the worked example from the specification.
func TestSpecWalkthrough(t *testing.T) {
	reg, err := NewRegistry(2, 2, 10, 20)
	if err != nil {
		t.Fatal(err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	must(reg.Register(b("u1"), b("d1"), b("t1"), 1))
	must(reg.Register(b("u1"), b("d2"), b("t2"), 2))
	must(reg.Register(b("u1"), b("d3"), b("t3"), 3))
	if got := mustTargets(t, reg, "u1", 3); !eqStrings(got, []string{"t3", "t2"}) {
		t.Fatalf("after eviction: %v", got)
	}
	must(reg.Register(b("u2"), b("d9"), b("t3"), 4))
	if got := mustTargets(t, reg, "u1", 4); !eqStrings(got, []string{"t2"}) {
		t.Fatalf("after steal: %v", got)
	}
	if got := mustTargets(t, reg, "u2", 4); !eqStrings(got, []string{"t3"}) {
		t.Fatalf("u2: %v", got)
	}
	must(reg.Register(b("u1"), b("d2"), b("t2b"), 5))
	if got := mustTargets(t, reg, "u1", 5); !eqStrings(got, []string{"t2b", "t2"}) {
		t.Fatalf("retired t2 in grace: %v", got)
	}
	if got := mustTargets(t, reg, "u1", 15); !eqStrings(got, []string{"t2b"}) {
		t.Fatalf("grace expired at 15: %v", got)
	}
	must(reg.Feedback(b("t2b"), 16))
	if err := reg.Register(b("u1"), b("d4"), b("t2b"), 30); !errors.Is(err, ErrTokenBlocked) {
		t.Fatalf("blocked at 30: %v", err)
	}
	if err := reg.Register(b("u1"), b("d4"), b("t2b"), 36); err != nil {
		t.Fatalf("allowed at block boundary 36: %v", err)
	}
}
