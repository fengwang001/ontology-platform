package quota

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func TestExactLimitsAndGraceBoundary(t *testing.T) {
	mgr := newScenarioManager(t, 100, 200)
	mustAddGroup(t, mgr, 1, 150, 200, 0)
	mustAddUser(t, mgr, 1, 1, 50, 80, 0)

	mustAlloc(t, mgr, 1, 50, 0)
	assertState(t, mgr, 1, 50, false, 0)

	mustAlloc(t, mgr, 1, 20, 10)
	assertState(t, mgr, 1, 70, true, 10)

	mustAlloc(t, mgr, 1, 10, 50)
	assertState(t, mgr, 1, 80, true, 10)

	assertErr(t, mgr.Alloc(1, 1, 60), ErrUserHardLimit)

	mustFree(t, mgr, 1, 25, 109)
	assertState(t, mgr, 1, 55, true, 10)

	mustAlloc(t, mgr, 1, 5, 109)
	assertState(t, mgr, 1, 60, true, 10)

	assertErr(t, mgr.Alloc(1, 1, 110), ErrUserGraceExpired)
	assertState(t, mgr, 1, 60, true, 10)

	mustFree(t, mgr, 1, 10, 110)
	assertState(t, mgr, 1, 50, false, 0)

	mustAlloc(t, mgr, 1, 1, 111)
	assertState(t, mgr, 1, 51, true, 111)
}

func TestExactGraceOneUnitBoundaryAndFreeAfterExpiry(t *testing.T) {
	mgr := newScenarioManager(t, 50, 100)
	mustAddGroup(t, mgr, 1, 100, 100, 0)
	mustAddUser(t, mgr, 1, 1, 10, 50, 0)

	mustAlloc(t, mgr, 1, 11, 0)
	assertState(t, mgr, 1, 11, true, 0)

	mustAlloc(t, mgr, 1, 1, 49)
	assertState(t, mgr, 1, 12, true, 0)

	assertErr(t, mgr.Alloc(1, 1, 50), ErrUserGraceExpired)
	assertState(t, mgr, 1, 12, true, 0)

	mustFree(t, mgr, 1, 2, 60)
	assertState(t, mgr, 1, 10, false, 0)
}

func TestSimultaneousUserAndGroupGraceStart(t *testing.T) {
	mgr := newScenarioManager(t, 10, 20)
	mustAddGroup(t, mgr, 100, 5, 100, 0)
	mustAddUser(t, mgr, 1, 100, 5, 100, 0)

	mustAlloc(t, mgr, 1, 6, 7)
	assertState(t, mgr, 1, 6, true, 7)
	assertState(t, mgr, 100, 6, true, 7)
}

func TestGroupRejectionLeavesUserUnchanged(t *testing.T) {
	mgr := newScenarioManager(t, 100, 20)
	mustAddGroup(t, mgr, 100, 5, 10, 0)
	mustAddUser(t, mgr, 1, 100, 100, 100, 0)

	mustAlloc(t, mgr, 1, 6, 5)

	assertErr(t, mgr.Alloc(1, 1, 26), ErrGroupGraceExpired)
	assertState(t, mgr, 1, 6, false, 0)
	assertState(t, mgr, 100, 6, true, 5)

	mgr2 := newScenarioManager(t, 100, 100)
	mustAddGroup(t, mgr2, 200, 10, 10, 0)
	mustAddUser(t, mgr2, 2, 200, 100, 100, 0)
	mustAlloc(t, mgr2, 2, 10, 0)

	assertErr(t, mgr2.Alloc(2, 1, 1), ErrGroupHardLimit)
	assertState(t, mgr2, 2, 10, false, 0)
	assertState(t, mgr2, 200, 10, false, 0)
}

func TestSetLimitsTidyGrace(t *testing.T) {
	mgr := newScenarioManager(t, 100, 200)
	mustAddGroup(t, mgr, 100, 100, 200, 0)
	mustAddUser(t, mgr, 1, 100, 100, 200, 0)
	mustAlloc(t, mgr, 1, 50, 0)

	mustSetLimits(t, mgr, UserKind, 1, 40, 200, 10)
	assertState(t, mgr, 1, 50, true, 10)

	mustSetLimits(t, mgr, UserKind, 1, 50, 200, 20)
	assertState(t, mgr, 1, 50, false, 0)

	mustSetLimits(t, mgr, UserKind, 1, 40, 40, 30)
	assertState(t, mgr, 1, 50, true, 30)
	assertErr(t, mgr.Alloc(1, 1, 40), ErrUserHardLimit)
	assertState(t, mgr, 1, 50, true, 30)

	mustFree(t, mgr, 1, 10, 40)
	assertState(t, mgr, 1, 40, false, 0)
	assertErr(t, mgr.Alloc(1, 1, 50), ErrUserHardLimit)
}

func TestMoveChecksTargetAndReleasesSource(t *testing.T) {
	mgr := newScenarioManager(t, 100, 50)
	mustAddGroup(t, mgr, 100, 100, 100, 0)
	mustAddGroup(t, mgr, 202, 60, 100, 0)
	mustAddUser(t, mgr, 1, 100, 100, 100, 0)
	mustAlloc(t, mgr, 1, 20, 10)

	mustAddUser(t, mgr, 2, 202, 100, 200, 0)
	mustAlloc(t, mgr, 2, 81, 20)
	assertState(t, mgr, 202, 81, true, 20)

	assertErr(t, mgr.Move(1, 202, 60), ErrGroupHardLimit)
	assertState(t, mgr, 100, 20, false, 0)

	mustSetLimits(t, mgr, GroupKind, 202, 60, 200, 25)
	assertState(t, mgr, 202, 81, true, 20)
	mustAlloc(t, mgr, 2, 20, 40)
	assertState(t, mgr, 202, 101, true, 20)
	assertErr(t, mgr.Move(1, 202, 70), ErrGroupGraceExpired)

	mustFree(t, mgr, 2, 41, 75)
	assertState(t, mgr, 202, 60, false, 0)
	mustMove(t, mgr, 1, 202, 80)
	assertState(t, mgr, 100, 0, false, 0)
	assertState(t, mgr, 202, 80, true, 80)
}

func TestMoveZeroUsageSkipsTargetGraceAndSameGroupRejected(t *testing.T) {
	mgr := newScenarioManager(t, 100, 50)
	mustAddGroup(t, mgr, 100, 10, 100, 0)
	mustAddGroup(t, mgr, 202, 10, 100, 0)
	mustAddUser(t, mgr, 1, 100, 10, 100, 0)
	mustAddUser(t, mgr, 2, 202, 10, 100, 0)
	mustAlloc(t, mgr, 2, 11, 0)

	assertErr(t, mgr.Move(1, 100, 1), ErrSameGroup)
	mustMove(t, mgr, 1, 202, 60)
	assertState(t, mgr, 100, 0, false, 0)
	assertState(t, mgr, 202, 11, true, 0)
}

func TestRejectionsDoNotChangeClockOrState(t *testing.T) {
	mgr := newScenarioManager(t, 100, 200)
	mustAddGroup(t, mgr, 100, 10, 20, 0)
	mustAddUser(t, mgr, 1, 100, 5, 10, 0)
	mustAlloc(t, mgr, 1, 6, 5)

	assertErr(t, mgr.Alloc(1, 1, 4), ErrClockRewound)
	assertState(t, mgr, 1, 6, true, 5)
	assertState(t, mgr, 100, 6, false, 0)

	assertErr(t, mgr.Free(1, 7, 6), ErrOverRelease)
	assertState(t, mgr, 1, 6, true, 5)
	assertState(t, mgr, 100, 6, false, 0)

	assertErr(t, mgr.Alloc(99, 1, 6), ErrEntityNotFound)
	assertErr(t, mgr.SetLimits(GroupKind, 100, 100, 90, 6), ErrInvalidArgument)
	assertState(t, mgr, 100, 6, false, 0)

	mustAlloc(t, mgr, 1, 1, 6)
}

func TestConcurrentOperationsAreSerializable(t *testing.T) {
	mgr := newScenarioManager(t, 100, 200)
	mustAddGroup(t, mgr, 100, 1_000_000, 1_000_000, 0)

	const users = 16
	for id := int64(0); id < users; id++ {
		mustAddUser(t, mgr, id, 100, 1_000_000, 1_000_000, 0)
	}

	var now atomic.Int64
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for step := 0; step < 100; step++ {
				id := int64((worker + step) % users)
				operationNow := now.Add(1)
				err := mgr.Alloc(id, 1, operationNow)
				if errors.Is(err, ErrClockRewound) {
					continue
				}
				if err != nil {
					t.Errorf("Alloc(%d,1,%d) error = %v", id, operationNow, err)
					return
				}
				freeNow := now.Add(1)
				err = mgr.Free(id, 1, freeNow)
				if errors.Is(err, ErrClockRewound) {
					continue
				}
				if err != nil {
					t.Errorf("Free(%d,1,%d) error = %v", id, freeNow, err)
					return
				}
				_, _ = mgr.Usage(id)
				_, _, _ = mgr.Grace(id)
			}
		}(worker)
	}
	wg.Wait()

	total := int64(0)
	for id := int64(0); id < users; id++ {
		usage, err := mgr.Usage(id)
		if err != nil {
			t.Fatal(err)
		}
		total += usage
	}
	groupUsage, err := mgr.Usage(100)
	if err != nil {
		t.Fatal(err)
	}
	if groupUsage != total {
		t.Fatalf("group usage = %d, want sum of user usage %d", groupUsage, total)
	}
	t.Logf("query=Usage(100) output=%d; decision=equals sum of %d user usages", groupUsage, users)
}

func TestConstructionAndRegistrationValidation(t *testing.T) {
	assertErr(t, errInvalidNewManager(0, 1), ErrInvalidArgument)
	assertErr(t, errInvalidNewManager(1, 1_000_000_001), ErrInvalidArgument)

	mgr := newScenarioManager(t, 100, 200)
	assertErr(t, mgr.AddGroup(1, 20, 10), ErrInvalidArgument)
	assertErr(t, mgr.AddGroup(-1, 0, 10), ErrInvalidArgument)
	assertErr(t, mgr.AddUser(1, 99, 0, 10), ErrEntityNotFound)

	if err := mgr.AddGroup(1, 10, 20); err != nil {
		t.Fatal(err)
	}
	if err := mgr.AddUser(1, 1, 10, 20); err != nil {
		t.Fatal(err)
	}
	assertErr(t, mgr.AddGroup(1, 10, 20), ErrEntityExists)
	assertErr(t, mgr.AddUser(1, 1, 10, 20), ErrEntityExists)

	_, err := mgr.Usage(99)
	assertErr(t, err, ErrEntityNotFound)
	_, _, err = mgr.Grace(99)
	assertErr(t, err, ErrEntityNotFound)
}

func errInvalidNewManager(userGrace, groupGrace int64) error {
	_, err := NewManager(userGrace, groupGrace)
	return err
}

func newScenarioManager(t *testing.T, userGrace, groupGrace int64) *Manager {
	t.Helper()
	mgr, err := NewManager(userGrace, groupGrace)
	if err != nil {
		t.Fatalf("input=NewManager(%d,%d) output=%v decision=%v", userGrace, groupGrace, err, reasonName(err))
	}
	t.Logf("input=NewManager(%d,%d) output=<nil> decision=accepted", userGrace, groupGrace)
	return mgr
}

func mustAddGroup(t *testing.T, mgr *Manager, id, soft, hard, now int64) {
	t.Helper()
	err := mgr.AddGroup(id, soft, hard)
	t.Logf("input=AddGroup(%d,%d,%d) output=%v decision=%s", id, soft, hard, err, decision(err))
	if err != nil {
		t.Fatal(err)
	}
}

func mustAddUser(t *testing.T, mgr *Manager, id, groupID, soft, hard, now int64) {
	t.Helper()
	err := mgr.AddUser(id, groupID, soft, hard)
	t.Logf("input=AddUser(%d,%d,%d,%d) output=%v decision=%s", id, groupID, soft, hard, err, decision(err))
	if err != nil {
		t.Fatal(err)
	}
}

func mustAlloc(t *testing.T, mgr *Manager, id, amount, now int64) {
	t.Helper()
	err := mgr.Alloc(id, amount, now)
	t.Logf("input=Alloc(%d,%d,%d) output=%v decision=%s", id, amount, now, err, decision(err))
	if err != nil {
		t.Fatal(err)
	}
}

func mustFree(t *testing.T, mgr *Manager, id, amount, now int64) {
	t.Helper()
	err := mgr.Free(id, amount, now)
	t.Logf("input=Free(%d,%d,%d) output=%v decision=%s", id, amount, now, err, decision(err))
	if err != nil {
		t.Fatal(err)
	}
}

func mustSetLimits(t *testing.T, mgr *Manager, kind EntityKind, id, soft, hard, now int64) {
	t.Helper()
	err := mgr.SetLimits(kind, id, soft, hard, now)
	t.Logf("input=SetLimits(%s,%d,%d,%d,%d) output=%v decision=%s",
		kindName(kind), id, soft, hard, now, err, decision(err))
	if err != nil {
		t.Fatal(err)
	}
}

func mustMove(t *testing.T, mgr *Manager, id, target, now int64) {
	t.Helper()
	err := mgr.Move(id, target, now)
	t.Logf("input=Move(%d,%d,%d) output=%v decision=%s", id, target, now, err, decision(err))
	if err != nil {
		t.Fatal(err)
	}
}

func assertErr(t *testing.T, err, want error) {
	t.Helper()
	t.Logf("output=%v decision=%s", err, decision(err))
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func assertState(t *testing.T, mgr *Manager, id, usage int64, hasGrace bool, graceAt int64) {
	t.Helper()
	gotUsage, err := mgr.Usage(id)
	if err != nil {
		t.Fatal(err)
	}
	gotGraceAt, gotHasGrace, err := mgr.Grace(id)
	if err != nil {
		t.Fatal(err)
	}
	if gotUsage != usage || gotHasGrace != hasGrace || (hasGrace && gotGraceAt != graceAt) {
		t.Fatalf("entity %d state usage=%d grace=(%d,%v), want usage=%d grace=(%d,%v)",
			id, gotUsage, gotGraceAt, gotHasGrace, usage, graceAt, hasGrace)
	}
	t.Logf("query=Usage(%d) output=%d; query=Grace(%d) output=(%d,%t); decision=state matches",
		id, gotUsage, id, gotGraceAt, gotHasGrace)
}

func decision(err error) string {
	if err == nil {
		return "accepted"
	}
	return reasonName(err)
}
