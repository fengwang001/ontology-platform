package kms

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// TestRotationDueExactlyAtNow: d == now counts as due.
func TestRotationDueExactlyAtNow(t *testing.T) {
	m, err := NewManager(3, 100, 1000, 2)
	mustOK(t, err)
	mustCreate(t, m, "k", 60, 0)
	cred, err := m.Encrypt("k", 60)
	mustOK(t, err)
	if cred != (Credential{"k", 1, 2}) {
		t.Fatalf("cred = %+v, want {k 1 2}", cred)
	}
	checkView(t, mustDescribe(t, m, "k", 60), Enabled, 1,
		[]Version{{1, 0}, {2, 60}}, 120, 0)
}

// TestCatchUpFloorAndScheduledTimes: c uses floor division and versions are
// stamped with their scheduled times, not now.
func TestCatchUpFloorAndScheduledTimes(t *testing.T) {
	m, err := NewManager(4, 100, 1000, 2)
	mustOK(t, err)
	mustCreate(t, m, "k", 60, 0)

	// now=179: c = floor(119/60)+1 = 2, versions 2@60, 3@120, d=180.
	cred, err := m.Encrypt("k", 179)
	mustOK(t, err)
	if cred.Version != 3 {
		t.Fatalf("cred.Version = %d, want 3", cred.Version)
	}
	checkView(t, mustDescribe(t, m, "k", 179), Enabled, 1,
		[]Version{{1, 0}, {2, 60}, {3, 120}}, 180, 0)
}

// TestVersionNumbersAdvanceFullC: version numbers advance by the full c
// while only the last V versions are retained/materialized.
func TestVersionNumbersAdvanceFullC(t *testing.T) {
	m, err := NewManager(3, 100, 1000, 2)
	mustOK(t, err)
	mustCreate(t, m, "k", 60, 0)

	// now=10000: c = floor(9940/60)+1 = 166, versions 2..167, only the
	// last 3 retained: 165@9840, 166@9900, 167@9960, d=10020.
	cred, err := m.Encrypt("k", 10000)
	mustOK(t, err)
	if cred.Version != 167 {
		t.Fatalf("cred.Version = %d, want 167", cred.Version)
	}
	checkView(t, mustDescribe(t, m, "k", 10000), Enabled, 1,
		[]Version{{165, 9840}, {166, 9900}, {167, 9960}}, 10020, 0)
	if got := m.Materialized(); got != 3 {
		t.Fatalf("materialized = %d, want 3", got)
	}
}

// TestV1: with V=1 only the newest version survives each catch-up.
func TestV1(t *testing.T) {
	m, err := NewManager(1, 100, 1000, 2)
	mustOK(t, err)
	mustCreate(t, m, "k", 60, 0)

	// c = floor(120/60)+1 = 3, versions 2,3,4 built, only 4 retained.
	cred, err := m.Encrypt("k", 180)
	mustOK(t, err)
	if cred != (Credential{"k", 1, 4}) {
		t.Fatalf("cred = %+v, want {k 1 4}", cred)
	}
	checkView(t, mustDescribe(t, m, "k", 180), Enabled, 1,
		[]Version{{4, 180}}, 240, 0)

	// Version 3 was evicted in the same catch-up that created it.
	_, err = m.Decrypt(Credential{"k", 1, 3}, 180)
	mustErr(t, err, ErrVersionProblem, "version retired")

	isMax, err := m.Decrypt(Credential{"k", 1, 4}, 180)
	mustOK(t, err)
	if !isMax {
		t.Fatalf("Decrypt((k,1,4),180) = false, want true")
	}
}

// TestDisabledNoCatchUp: a Disabled key does not rotate.
func TestDisabledNoCatchUp(t *testing.T) {
	m, err := NewManager(3, 100, 1000, 2)
	mustOK(t, err)
	mustCreate(t, m, "k", 60, 0)
	mustOK(t, m.Disable("k", 0))

	// Long idle while Disabled: no catch-up, no rotation, d frozen.
	checkView(t, mustDescribe(t, m, "k", 100000), Disabled, 1,
		[]Version{{1, 0}}, 60, 0)
	if got := m.Materialized(); got != 0 {
		t.Fatalf("materialized = %d, want 0", got)
	}

	// Enable at 100000: d=60 <= 100000, so d becomes 100060, and the
	// missed rotations are not caught up.
	mustOK(t, m.Enable("k", 100000))
	checkView(t, mustDescribe(t, m, "k", 100000), Enabled, 1,
		[]Version{{1, 0}}, 100060, 0)
}

// TestPendingVsDisabledDecryptReason: the two denial reasons differ.
func TestPendingVsDisabledDecryptReason(t *testing.T) {
	m, err := NewManager(3, 100, 1000, 3)
	mustOK(t, err)
	mustCreate(t, m, "d", 0, 0)
	mustCreate(t, m, "p", 0, 0)
	mustOK(t, m.Disable("d", 0))
	mustOK(t, m.ScheduleDeletion("p", 100, 0))

	_, err = m.Decrypt(Credential{"d", 1, 1}, 1)
	ke := mustErr(t, err, ErrStateDenied, "disabled")
	if ke.State != Disabled {
		t.Fatalf("state = %v, want Disabled", ke.State)
	}
	_, err = m.Decrypt(Credential{"p", 1, 1}, 1)
	ke = mustErr(t, err, ErrStateDenied, "pending deletion")
	if ke.State != Pending {
		t.Fatalf("state = %v, want Pending", ke.State)
	}

	// Encrypt follows the same reasons.
	_, err = m.Encrypt("d", 1)
	mustErr(t, err, ErrStateDenied, "disabled")
	_, err = m.Encrypt("p", 1)
	mustErr(t, err, ErrStateDenied, "pending deletion")
}

// TestDeleteAtBoundary: at deleteAt the key is deleted; one second earlier
// it is still alive.
func TestDeleteAtBoundary(t *testing.T) {
	m, err := NewManager(3, 100, 1000, 2)
	mustOK(t, err)
	mustCreate(t, m, "k", 0, 0)
	mustOK(t, m.ScheduleDeletion("k", 100, 400)) // deleteAt = 500

	if _, err := m.Describe("k", 499); err != nil {
		t.Fatalf("Describe(k,499) = %v, want success", err)
	}
	_, err = m.Encrypt("k", 499)
	mustErr(t, err, ErrStateDenied, "pending deletion")

	// At deleteAt the key is gone for every operation.
	_, err = m.Describe("k", 500)
	mustErr(t, err, ErrNotFound, "")
	mustErr(t, m.CancelDeletion("k", 500), ErrNotFound, "")
	_, err = m.Encrypt("k", 500)
	mustErr(t, err, ErrNotFound, "")
	_, err = m.Decrypt(Credential{"k", 1, 1}, 500)
	mustErr(t, err, ErrKeyDeleted, "")
}

// TestSlotFreedImmediately: an expired key frees its Kmax slot at once.
func TestSlotFreedImmediately(t *testing.T) {
	m, err := NewManager(3, 100, 1000, 1)
	mustOK(t, err)
	mustCreate(t, m, "k", 0, 0)
	mustOK(t, m.ScheduleDeletion("k", 100, 0)) // deleteAt = 100

	_, err = m.Create("x", 0, 99)
	mustErr(t, err, ErrLimitExceeded, "")

	// At deleteAt the slot is free even though nothing was reclaimed yet.
	if gen := mustCreate(t, m, "x", 0, 100); gen != 1 {
		t.Fatalf("gen = %d, want 1", gen)
	}
}

// TestGenerations: recreation bumps the generation; old-generation
// credentials report key-deleted while unknown ids report not-found.
func TestGenerations(t *testing.T) {
	m, err := NewManager(3, 100, 1000, 2)
	mustOK(t, err)
	if gen := mustCreate(t, m, "k", 0, 0); gen != 1 {
		t.Fatalf("gen = %d, want 1", gen)
	}
	mustOK(t, m.ScheduleDeletion("k", 100, 0))
	if gen := mustCreate(t, m, "k", 0, 100); gen != 2 {
		t.Fatalf("gen = %d, want 2", gen)
	}
	if gen := mustCreate(t, m, "j", 0, 100); gen != 1 {
		t.Fatalf("gen = %d, want 1", gen)
	}

	_, err = m.Decrypt(Credential{"k", 1, 1}, 100)
	mustErr(t, err, ErrKeyDeleted, "")
	isMax, err := m.Decrypt(Credential{"k", 2, 1}, 100)
	mustOK(t, err)
	if !isMax {
		t.Fatalf("Decrypt((k,2,1),100) = false, want true")
	}

	// Generation above the max ever created: not-found, not key-deleted.
	_, err = m.Decrypt(Credential{"k", 3, 1}, 100)
	mustErr(t, err, ErrNotFound, "")
	// Never-created id: not-found.
	_, err = m.Decrypt(Credential{"zzz", 1, 1}, 100)
	mustErr(t, err, ErrNotFound, "")
}

// TestVersionRetiredVsNotExist distinguishes the two version problems.
func TestVersionRetiredVsNotExist(t *testing.T) {
	m, err := NewManager(2, 100, 1000, 2)
	mustOK(t, err)
	mustCreate(t, m, "k", 60, 0)
	cred, err := m.Encrypt("k", 180) // versions 2,3,4; retain 3,4
	mustOK(t, err)
	if cred.Version != 4 {
		t.Fatalf("cred.Version = %d, want 4", cred.Version)
	}

	_, err = m.Decrypt(Credential{"k", 1, 2}, 180)
	mustErr(t, err, ErrVersionProblem, "version retired")
	_, err = m.Decrypt(Credential{"k", 1, 5}, 180)
	mustErr(t, err, ErrVersionProblem, "version does not exist")

	isMax, err := m.Decrypt(Credential{"k", 1, 3}, 180)
	mustOK(t, err)
	if isMax {
		t.Fatalf("Decrypt((k,1,3),180) = true, want false")
	}
}

// TestScheduleDeletionWindowBounds: w == Wmin and w == Wmax are accepted,
// anything outside is an invalid-parameter rejection.
func TestScheduleDeletionWindowBounds(t *testing.T) {
	m, err := NewManager(3, 100, 1000, 4)
	mustOK(t, err)
	mustCreate(t, m, "a", 0, 0)
	mustCreate(t, m, "b", 0, 0)
	mustCreate(t, m, "c", 0, 0)
	mustCreate(t, m, "d", 0, 0)

	mustOK(t, m.ScheduleDeletion("a", 100, 0))  // w == Wmin
	mustOK(t, m.ScheduleDeletion("b", 1000, 0)) // w == Wmax
	checkView(t, mustDescribe(t, m, "a", 0), Pending, 1,
		[]Version{{1, 0}}, 0, 100)
	checkView(t, mustDescribe(t, m, "b", 0), Pending, 1,
		[]Version{{1, 0}}, 0, 1000)

	mustErr(t, m.ScheduleDeletion("c", 99, 0), ErrInvalidParam, "")
	mustErr(t, m.ScheduleDeletion("d", 1001, 0), ErrInvalidParam, "")
	// Rejected attempts left both keys Enabled.
	checkView(t, mustDescribe(t, m, "c", 0), Enabled, 1,
		[]Version{{1, 0}}, 0, 0)
	checkView(t, mustDescribe(t, m, "d", 0), Enabled, 1,
		[]Version{{1, 0}}, 0, 0)
}

// TestReEncryptUnchanged: a credential already at the max version is
// returned as-is with changed=false.
func TestReEncryptUnchanged(t *testing.T) {
	m, err := NewManager(3, 100, 1000, 2)
	mustOK(t, err)
	mustCreate(t, m, "k", 60, 0)
	cred, err := m.Encrypt("k", 215) // (k,1,4)
	mustOK(t, err)

	fresh, changed, err := m.ReEncrypt(cred, 215)
	mustOK(t, err)
	if changed {
		t.Fatalf("changed = true, want false")
	}
	if fresh != cred {
		t.Fatalf("fresh = %+v, want %+v", fresh, cred)
	}

	// After more rotation an old credential is re-encrypted to the max.
	if _, err := m.Encrypt("k", 300); err != nil { // builds 5@240, 6@300
		t.Fatal(err)
	}
	fresh, changed, err = m.ReEncrypt(Credential{"k", 1, 4}, 300)
	mustOK(t, err)
	if !changed {
		t.Fatalf("changed = false, want true")
	}
	if fresh != (Credential{"k", 1, 6}) {
		t.Fatalf("fresh = %+v, want {k 1 6}", fresh)
	}

	// ReEncrypt applies the same version checks as Decrypt.
	_, _, err = m.ReEncrypt(Credential{"k", 1, 1}, 300)
	mustErr(t, err, ErrVersionProblem, "version retired")
	_, _, err = m.ReEncrypt(Credential{"k", 1, 99}, 300)
	mustErr(t, err, ErrVersionProblem, "version does not exist")
}

// TestDescribeVirtualCatchUp: Describe computes the catch-up view without
// changing state, counters or the clock.
func TestDescribeVirtualCatchUp(t *testing.T) {
	m, err := NewManager(3, 100, 1000, 2)
	mustOK(t, err)
	mustCreate(t, m, "k", 60, 0)

	// Virtual catch-up at 1000: c = floor(940/60)+1 = 16, versions 2..17,
	// retained 15@840, 16@900, 17@960, d=1020.
	checkView(t, mustDescribe(t, m, "k", 1000), Enabled, 1,
		[]Version{{15, 840}, {16, 900}, {17, 960}}, 1020, 0)
	if got := m.Materialized(); got != 0 {
		t.Fatalf("materialized = %d, want 0 (virtual catch-up)", got)
	}

	// The clock did not advance: an accepted op at 500 is still fine, and
	// the real catch-up starts from the untouched d=60 (c=8, versions 2..9).
	cred, err := m.Encrypt("k", 500)
	mustOK(t, err)
	if cred.Version != 9 {
		t.Fatalf("cred.Version = %d, want 9", cred.Version)
	}
}

// TestRejectedOpsHaveNoSideEffects: rejected operations change nothing,
// including the clock, catch-up and reclamation counters.
func TestRejectedOpsHaveNoSideEffects(t *testing.T) {
	m, err := NewManager(3, 100, 1000, 2)
	mustOK(t, err)
	mustCreate(t, m, "k", 60, 0)
	mustCreate(t, m, "j", 0, 0)
	snapshot := mustDescribe(t, m, "k", 0)
	pops := m.HeapPops()

	// Every rejection category at now=500.
	_, err = m.Create("", 60, 500)
	mustErr(t, err, ErrInvalidParam, "")
	_, err = m.Encrypt("nope", 500)
	mustErr(t, err, ErrNotFound, "")
	_, err = m.Decrypt(Credential{"k", 1, 99}, 500) // would need catch-up
	mustErr(t, err, ErrVersionProblem, "version does not exist")
	mustErr(t, m.Enable("k", 500), ErrStateConflict, "")
	_, err = m.Create("k", 0, 500)
	mustErr(t, err, ErrStateConflict, "")
	_, err = m.Create("x", 0, 500)
	mustErr(t, err, ErrLimitExceeded, "")
	mustErr(t, m.ScheduleDeletion("k", 5, 500), ErrInvalidParam, "")

	// Nothing changed: same view, no catch-up, no reclamation, no clock.
	checkView(t, mustDescribe(t, m, "k", 0), snapshot.State, snapshot.Gen,
		snapshot.Versions, snapshot.NextRotation, snapshot.DeleteAt)
	if got := m.Materialized(); got != 0 {
		t.Fatalf("materialized = %d, want 0", got)
	}
	if got := m.HeapPops(); got != pops {
		t.Fatalf("heapPops = %d, want %d", got, pops)
	}
	// Clock still at 0: an accepted op at 100 must not regress, and the
	// catch-up starts from the untouched d=60.
	cred, err := m.Encrypt("k", 100)
	mustOK(t, err)
	if cred != (Credential{"k", 1, 2}) {
		t.Fatalf("cred = %+v, want {k 1 2}", cred)
	}
	checkView(t, mustDescribe(t, m, "k", 100), Enabled, 1,
		[]Version{{1, 0}, {2, 60}}, 120, 0)
}

// TestInvalidConfig: the whole configuration is rejected when any bound is
// illegal.
func TestInvalidConfig(t *testing.T) {
	cases := [][4]int64{
		{0, 1, 1, 1},              // V < 1
		{65, 1, 1, 1},             // V > 64
		{1, 0, 1, 1},              // Wmin < 1
		{1, 2, 1, 1},              // Wmin > Wmax
		{1, 1, 1_000_000_001, 1},  // Wmax > 1e9
		{1, 1, 1, 0},              // Kmax < 1
		{1, 1, 1, 1_000_001},      // Kmax > 1e6
		{64, 1, 1_000_000_000, 1}, // valid, all bounds
		{1, 1_000_000_000, 1_000_000_000, 1_000_000}, // valid, all bounds
	}
	for i, c := range cases {
		_, err := NewManager(c[0], c[1], c[2], c[3])
		if i < 7 && err == nil {
			t.Fatalf("case %d: NewManager%v succeeded, want rejection", i, c)
		}
		if i >= 7 && err != nil {
			t.Fatalf("case %d: NewManager%v = %v, want success", i, c, err)
		}
		if err != nil {
			var ke *Error
			if !errors.As(err, &ke) || ke.Code != ErrInvalidParam {
				t.Fatalf("case %d: error = %v, want invalid-parameter", i, err)
			}
		}
	}
}

// TestInvalidParams: per-operation parameter validation.
func TestInvalidParams(t *testing.T) {
	m, err := NewManager(3, 100, 1000, 2)
	mustOK(t, err)

	_, err = m.Create("", 60, 0)
	mustErr(t, err, ErrInvalidParam, "")
	_, err = m.Create("k", 59, 0)
	mustErr(t, err, ErrInvalidParam, "")
	_, err = m.Create("k", 1, 0)
	mustErr(t, err, ErrInvalidParam, "")
	_, err = m.Create("k", 1_000_000_001, 0)
	mustErr(t, err, ErrInvalidParam, "")
	_, err = m.Create("k", 60, -1)
	mustErr(t, err, ErrInvalidParam, "")
	_, err = m.Create("k", 60, MaxNow+1)
	mustErr(t, err, ErrInvalidParam, "")

	// Boundary periods are legal.
	mustCreate(t, m, "k", 60, 0)
	mustCreate(t, m, "j", 1_000_000_000, 0)

	_, err = m.Decrypt(Credential{"", 1, 1}, 0)
	mustErr(t, err, ErrInvalidParam, "")
	_, err = m.Decrypt(Credential{"k", 0, 1}, 0)
	mustErr(t, err, ErrInvalidParam, "")
	_, err = m.Decrypt(Credential{"k", 1, 0}, 0)
	mustErr(t, err, ErrInvalidParam, "")
	_, err = m.Encrypt("k", MaxNow+1)
	mustErr(t, err, ErrInvalidParam, "")
}

// TestClockRegression: now below the max accepted now is rejected,
// including for Describe, and rejection ordering puts params first.
func TestClockRegression(t *testing.T) {
	m, err := NewManager(3, 100, 1000, 2)
	mustOK(t, err)
	mustCreate(t, m, "k", 60, 100)

	_, err = m.Encrypt("k", 99)
	ke := mustErr(t, err, ErrClockRegression, "")
	if ke.MaxNow != 100 {
		t.Fatalf("MaxNow = %d, want 100", ke.MaxNow)
	}
	_, err = m.Describe("k", 99)
	mustErr(t, err, ErrClockRegression, "")
	_, err = m.Decrypt(Credential{"k", 1, 1}, 99)
	mustErr(t, err, ErrClockRegression, "")

	// Equal to the max is fine; Describe does not advance the clock.
	_, err = m.Encrypt("k", 100)
	mustOK(t, err)
	_, err = m.Describe("k", 100)
	mustOK(t, err)

	// Invalid params are reported before clock regressions.
	_, err = m.Encrypt("", 0)
	mustErr(t, err, ErrInvalidParam, "")
	// Not-found is reported after clock regressions.
	_, err = m.Encrypt("nope", 0)
	mustErr(t, err, ErrClockRegression, "")
}

// TestMaterializationBounded: a catch-up after a 1e15-second idle
// materializes exactly min(c, V) = V versions, never c.
func TestMaterializationBounded(t *testing.T) {
	m, err := NewManager(3, 100, 1000, 2)
	mustOK(t, err)
	mustCreate(t, m, "k", 60, 0)

	before := m.Materialized()
	cred, err := m.Encrypt("k", MaxNow)
	mustOK(t, err)
	// c = floor((1e15-60)/60)+1 is enormous, but only V=3 versions exist.
	if got := m.Materialized() - before; got != 3 {
		t.Fatalf("materialized delta = %d, want 3 (== V)", got)
	}
	// Version numbers still advanced by the full c.
	wantMax := int64(1) + (MaxNow-60)/60 + 1
	if cred.Version != wantMax {
		t.Fatalf("cred.Version = %d, want %d", cred.Version, wantMax)
	}
	view := mustDescribe(t, m, "k", MaxNow)
	if int64(len(view.Versions)) != 3 {
		t.Fatalf("retained = %d, want 3", len(view.Versions))
	}
	if view.Versions[2].Number != wantMax {
		t.Fatalf("max retained = %d, want %d", view.Versions[2].Number, wantMax)
	}

	// A short idle materializes min(c, V) as well, and never more than V.
	m2, err := NewManager(3, 100, 1000, 2)
	mustOK(t, err)
	mustCreate(t, m2, "k", 60, 0)
	if _, err := m2.Encrypt("k", 100); err != nil { // c = 1
		t.Fatal(err)
	}
	if got := m2.Materialized(); got != 1 {
		t.Fatalf("materialized = %d, want 1 (== min(c,V))", got)
	}
}

// TestHeapPopsBounded: reclamation pops are bounded by expiries plus stale
// (cancelled) entries, independent of the live-key count.
func TestHeapPopsBounded(t *testing.T) {
	m, err := NewManager(4, 10, 100, 1000)
	mustOK(t, err)
	mustCreate(t, m, "a", 0, 0)
	mustCreate(t, m, "b", 0, 0)
	mustCreate(t, m, "c", 0, 0)

	mustOK(t, m.ScheduleDeletion("a", 10, 0)) // deleteAt 10
	mustOK(t, m.ScheduleDeletion("b", 20, 0)) // deleteAt 20
	mustOK(t, m.CancelDeletion("b", 5))       // b's entry becomes stale

	// At 10: one expiry; the stale entry (deleteAt 20) is not surfaced.
	_, err = m.Encrypt("c", 10)
	mustOK(t, err)
	if got := m.HeapPops(); got != 1 {
		t.Fatalf("heapPops = %d, want 1", got)
	}
	// At 20: the stale entry surfaces and is popped; b is still alive.
	_, err = m.Encrypt("c", 20)
	mustOK(t, err)
	if got := m.HeapPops(); got != 2 {
		t.Fatalf("heapPops = %d, want 2", got)
	}
	checkView(t, mustDescribe(t, m, "b", 20), Disabled, 1,
		[]Version{{1, 0}}, 0, 20)

	// Pops are independent of the number of live keys: with 500 live keys
	// and no pending deletions, reclamation never pops.
	m2, err := NewManager(4, 10, 100, 1000)
	mustOK(t, err)
	for i := 0; i < 500; i++ {
		mustCreate(t, m2, fmt.Sprintf("key-%d", i), 0, 0)
	}
	for i := 0; i < 500; i++ {
		_, err = m2.Encrypt(fmt.Sprintf("key-%d", i), 1000)
		mustOK(t, err)
	}
	if got := m2.HeapPops(); got != 0 {
		t.Fatalf("heapPops = %d, want 0", got)
	}
}

// TestConcurrentSmoke: concurrent operations are safe (run with -race) and
// every result is serializable — here simply: no unexpected errors and a
// consistent final state.
func TestConcurrentSmoke(t *testing.T) {
	m, err := NewManager(3, 100, 1000, 4)
	mustOK(t, err)
	mustCreate(t, m, "k", 60, 0)
	mustCreate(t, m, "j", 60, 0)

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				now := int64(i)
				id := "k"
				if g%2 == 1 {
					id = "j"
				}
				cred, err := m.Encrypt(id, now)
				if err != nil {
					continue // clock regression from a racing goroutine
				}
				_, _ = m.Decrypt(cred, now)
				_, _, _ = m.ReEncrypt(cred, now)
				if _, err := m.Describe(id, now); err != nil {
					var ke *Error
					if !errors.As(err, &ke) || ke.Code != ErrClockRegression {
						t.Errorf("Describe: unexpected error %v", err)
					}
				}
			}
		}(g)
	}
	wg.Wait()

	view, err := m.Describe("k", 199)
	mustOK(t, err)
	if view.State != Enabled || len(view.Versions) == 0 {
		t.Fatalf("inconsistent final view: %+v", view)
	}
}
