package thinpool

import (
	"errors"
	"testing"
)

func requireCode(t *testing.T, err error, code ErrorCode) {
	t.Helper()
	var target Error
	if !errors.As(err, &target) || target.Code != code {
		t.Fatalf("got error %v, want code %s", err, code)
	}
}

func TestOvercommitBoundary(t *testing.T) {
	pool, err := NewPool(10, 150, 80, 90)
	if err != nil {
		t.Fatal(err)
	}

	if err := pool.CreateVolume("a", 15, 0); err != nil {
		t.Fatalf("exact overcommit limit was rejected: %v", err)
	}
	if err := pool.ResizeVolume("a", 16); err == nil {
		t.Fatal("one block beyond overcommit limit was accepted")
	} else {
		requireCode(t, err, ErrOvercommit)
	}

	snapshot := pool.Snapshot()
	if snapshot.TotalVirtual != 15 {
		t.Fatalf("rejected resize changed virtual total to %d", snapshot.TotalVirtual)
	}
}

func TestReservationPoolBoundaryAndVolumeLimit(t *testing.T) {
	pool, _ := NewPool(8, 200, 80, 90)
	if err := pool.CreateVolume("a", 4, 2); err != nil {
		t.Fatal(err)
	}
	if err := pool.CreateVolume("b", 4, 2); err != nil {
		t.Fatalf("reservations totaling pool size were rejected: %v", err)
	}
	if err := pool.CreateVolume("c", 1, 1); err != nil {
		requireCode(t, err, ErrReservePool)
	}
	if err := pool.CreateVolume("bad", 1, 2); err != nil {
		requireCode(t, err, ErrReserveVolume)
	}
}

func TestOwnReservationAllocationThenExtraAllocationRejected(t *testing.T) {
	pool, _ := NewPool(3, 300, 80, 90)
	if err := pool.CreateVolume("a", 3, 2); err != nil {
		t.Fatal(err)
	}
	if err := pool.CreateVolume("b", 3, 1); err != nil {
		t.Fatal(err)
	}

	if err := pool.WriteBlock("b", 0); err != nil {
		t.Fatalf("b own-reserved allocation failed: %v", err)
	}
	if err := pool.WriteBlock("a", 0); err != nil {
		t.Fatalf("uncommitted a allocation failed: %v", err)
	}
	err := pool.WriteBlock("b", 1)
	requireCode(t, err, ErrPoolExhausted)
	if err := pool.WriteBlock("a", 1); err != nil {
		t.Fatalf("a own-reserved allocation should succeed: %v", err)
	}

	snapshot := pool.Snapshot()
	if snapshot.Allocated != 3 || snapshot.Free != 0 || snapshot.TotalDeficit != 0 || snapshot.Volumes["b"].Used != 1 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
}

func TestDeficitRecoversAfterReclaimThenAllocationSucceeds(t *testing.T) {
	pool, _ := NewPool(2, 200, 80, 90)
	if err := pool.CreateVolume("a", 2, 1); err != nil {
		t.Fatal(err)
	}
	if err := pool.CreateVolume("b", 2, 1); err != nil {
		t.Fatal(err)
	}
	if err := pool.WriteBlock("a", 0); err != nil {
		t.Fatal(err)
	}
	if err := pool.WriteBlock("b", 0); err != nil {
		t.Fatal(err)
	}

	released, err := pool.ReclaimRange("a", 0, 1)
	if err != nil || released != 1 {
		t.Fatalf("reclaim returned (%d,%v), want 1,nil", released, err)
	}
	if err := pool.WriteBlock("a", 1); err != nil {
		t.Fatalf("allocation after deficit recovery failed: %v", err)
	}
}

func TestWatermarkExactThresholds(t *testing.T) {
	pool, _ := NewPool(4, 200, 50, 75)
	if err := pool.CreateVolume("a", 4, 0); err != nil {
		t.Fatal(err)
	}

	_ = pool.WriteBlock("a", 0)
	_ = pool.WriteBlock("a", 1)
	events := pool.Events()
	if len(events) != 1 || events[0].OldLevel != LevelNormal || events[0].NewLevel != LevelWarning || events[0].Allocated != 2 {
		t.Fatalf("warning threshold events wrong: %+v", events)
	}

	_ = pool.WriteBlock("a", 2)
	events = pool.Events()
	if len(events) != 2 || events[1].OldLevel != LevelWarning || events[1].NewLevel != LevelCritical || events[1].Allocated != 3 {
		t.Fatalf("critical threshold events wrong: %+v", events)
	}
}

func TestReclaimCanCrossTwoWaterLevelsInOneEvent(t *testing.T) {
	pool, _ := NewPool(8, 200, 25, 50)
	if err := pool.CreateVolume("a", 4, 0); err != nil {
		t.Fatal(err)
	}
	for block := uint64(0); block < 4; block++ {
		_ = pool.WriteBlock("a", block)
	}
	eventsBefore := len(pool.Events())
	if released, err := pool.ReclaimRange("a", 0, 3); err != nil || released != 3 {
		t.Fatalf("reclaim returned (%d,%v)", released, err)
	}
	events := pool.Events()
	if len(events) != eventsBefore+1 {
		t.Fatalf("got %d new events, want 1", len(events)-eventsBefore)
	}
	event := events[len(events)-1]
	if event.OldLevel != LevelCritical || event.NewLevel != LevelNormal || event.Allocated != 1 {
		t.Fatalf("two-tier crossing event wrong: %+v", event)
	}
}

func TestReclaimEndpointsZeroLengthAndInvalidRanges(t *testing.T) {
	pool, _ := NewPool(4, 200, 80, 90)
	if err := pool.CreateVolume("a", 4, 0); err != nil {
		t.Fatal(err)
	}
	for block := uint64(0); block < 4; block++ {
		_ = pool.WriteBlock("a", block)
	}
	eventsBefore := len(pool.Events())

	if released, err := pool.ReclaimRange("a", 1, 0); err != nil || released != 0 {
		t.Fatalf("zero-length reclaim returned (%d,%v)", released, err)
	}
	if released, err := pool.ReclaimRange("a", 4, 0); err != nil || released != 0 {
		t.Fatalf("zero-length end-boundary reclaim returned (%d,%v)", released, err)
	}
	if released, err := pool.ReclaimRange("a", 0, 4); err != nil || released != 4 {
		t.Fatalf("whole range reclaim returned (%d,%v)", released, err)
	}
	_, err := pool.ReclaimRange("a", 1, 4)
	requireCode(t, err, ErrInvalidArgument)
	_, err = pool.ReclaimRange("a", ^uint64(0), 1)
	requireCode(t, err, ErrInvalidArgument)
	if len(pool.Events()) != eventsBefore+1 {
		t.Fatal("invalid range or zero-length reclaim produced an event")
	}
}

func TestShrinkRejectedWhenExactlyOneMappedBlockRemains(t *testing.T) {
	pool, _ := NewPool(4, 200, 80, 90)
	if err := pool.CreateVolume("a", 4, 0); err != nil {
		t.Fatal(err)
	}
	if err := pool.WriteBlock("a", 2); err != nil {
		t.Fatal(err)
	}
	err := pool.ResizeVolume("a", 2)
	requireCode(t, err, ErrVolumeHasData)
	if snapshot := pool.Snapshot(); snapshot.Volumes["a"].Virtual != 4 {
		t.Fatalf("rejected shrink changed virtual size: %+v", snapshot.Volumes["a"])
	}

	released, err := pool.ReclaimRange("a", 2, 1)
	if err != nil || released != 1 {
		t.Fatalf("reclaim returned (%d,%v)", released, err)
	}
	if err := pool.ResizeVolume("a", 2); err != nil {
		t.Fatalf("shrink after removing mapped block failed: %v", err)
	}
	err = pool.WriteBlock("a", 2)
	requireCode(t, err, ErrInvalidArgument)
}

func TestDeleteReleasesBlocksAndNameCanBeReused(t *testing.T) {
	pool, _ := NewPool(2, 200, 80, 90)
	if err := pool.CreateVolume("a", 2, 0); err != nil {
		t.Fatal(err)
	}
	_ = pool.WriteBlock("a", 0)
	_ = pool.WriteBlock("a", 1)
	if err := pool.DeleteVolume("a"); err != nil {
		t.Fatal(err)
	}
	if err := pool.CreateVolume("a", 2, 0); err != nil {
		t.Fatalf("reusing deleted volume name failed: %v", err)
	}
	_ = pool.WriteBlock("a", 0)
	_ = pool.WriteBlock("a", 1)

	snapshot := pool.Snapshot()
	physical := snapshot.Volumes["a"].Physical
	if len(physical) != 2 || physical[0] != 0 || physical[1] != 1 {
		t.Fatalf("deleted blocks were not deterministically reused: %+v", physical)
	}
}

func TestRejectedOperationsLeaveNoTrace(t *testing.T) {
	pool, _ := NewPool(2, 200, 80, 90)
	if err := pool.CreateVolume("a", 2, 0); err != nil {
		t.Fatal(err)
	}
	_ = pool.WriteBlock("a", 0)
	before := pool.Snapshot()
	eventsBefore := len(pool.Events())

	requireCode(t, pool.WriteBlock("missing", 0), ErrNotFound)
	requireCode(t, pool.WriteBlock("a", 2), ErrInvalidArgument)
	requireCode(t, pool.WriteBlock("", 0), ErrInvalidArgument)
	requireCode(t, pool.CreateVolume("a", 1, 0), ErrExists)
	requireCode(t, pool.ResizeVolume("missing", 1), ErrNotFound)
	requireCode(t, pool.SetReservation("missing", 1), ErrNotFound)
	_, reclaimErr := pool.ReclaimRange("missing", 0, 1)
	requireCode(t, reclaimErr, ErrNotFound)
	requireCode(t, pool.DeleteVolume("missing"), ErrNotFound)

	after := pool.Snapshot()
	if len(pool.Events()) != eventsBefore || after.Allocated != before.Allocated || len(after.Volumes) != len(before.Volumes) {
		t.Fatalf("rejected operation changed state: before=%+v after=%+v", before, after)
	}
}
