package throttle

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"
)

func testTime(second int) time.Time {
	return time.Unix(int64(second), 0).UTC()
}

func logSnapshot(t *testing.T, name string, r *Refresher) {
	t.Helper()
	s := r.Snapshot()
	t.Logf("%s: view=%v pending=%d totalBatches=%d lastTime=%s history=%v",
		name, s.View, s.PendingKeyCount, s.TotalBatchCount, s.LastLogicalTime.Format(time.RFC3339), s.RefreshedRecords)
}

func TestMergesChangesAndDelaysRefresh(t *testing.T) {
	r, _ := New(10*time.Second, 4)
	t0 := testTime(10)

	mustChange(t, r, "user:1", "a", t0)
	before := r.Snapshot()
	t.Logf("input: first change key=user:1 value=a at=%s; result=%v; basis=pending exists but view stays empty until scheduled refresh", t0, before)
	if len(before.View) != 0 || before.PendingKeyCount != 1 || before.TotalBatchCount != 0 {
		t.Fatalf("pending change leaked into view: %#v", before)
	}

	mustChange(t, r, "user:1", "b", t0.Add(4*time.Second))
	mustChange(t, r, "user:1", "c", t0.Add(7*time.Second))
	merged := r.Snapshot()
	t.Logf("input: same key later changes b@4s c@7s; result=%v; basis=count accumulates, latest value wins, scheduled time moves to 27s", merged)
	if len(merged.View) != 0 || merged.PendingKeyCount != 1 || merged.TotalBatchCount != 0 || !merged.LastLogicalTime.Equal(t0.Add(7*time.Second)) {
		t.Fatalf("merged batch state mismatch: %#v", merged)
	}

	early, err := r.Advance(t0.Add(7 * time.Second))
	if err != nil {
		t.Fatalf("advance after merged arrival failed: %v", err)
	}
	t.Logf("input: advance to=17s; result=%v snapshot=%v; basis=17s < scheduled 27s so no batch is produced", early, r.Snapshot())
	if len(early) != 0 || len(r.View()) != 0 {
		t.Fatalf("batch refreshed before its deadline: records=%v view=%v", early, r.View())
	}

	due, err := r.Advance(t0.Add(17 * time.Second))
	if err != nil {
		t.Fatalf("advance at deadline failed: %v", err)
	}
	want := []RefreshRecord{{
		Key:         "user:1",
		Value:       "c",
		Count:       3,
		ScheduledAt: t0.Add(17 * time.Second),
		RefreshAt:   t0.Add(17 * time.Second),
	}}
	t.Logf("input: advance to=27s; result=%v; basis=equal boundary refreshAt <= now and one record applies the merged batch", due)
	if !reflect.DeepEqual(due, want) {
		t.Fatalf("due records = %#v, want %#v", due, want)
	}
	after := r.Snapshot()
	if after.View["user:1"] != "c" || after.PendingKeyCount != 0 || after.TotalBatchCount != 1 {
		t.Fatalf("refreshed state mismatch: %#v", after)
	}
	logSnapshot(t, "final", r)
}

func TestAdvanceRefreshesAllDueKeysAndIncludesEqualBoundary(t *testing.T) {
	r, _ := New(5*time.Second, 3)
	t0 := testTime(20)
	mustChange(t, r, "a", "1", t0)
	mustChange(t, r, "b", "2", t0.Add(1*time.Second))
	mustChange(t, r, "c", "3", t0.Add(2*time.Second))

	records, err := r.Advance(t0.Add(7 * time.Second))
	if err != nil {
		t.Fatalf("advance failed: %v", err)
	}
	t.Logf("input: changes a@0 b@1 c@2 advance@7; result=%v; basis=deadlines 5,6,7 all satisfy refreshAt <= now and keys are deterministically sorted", records)
	if len(records) != 3 {
		t.Fatalf("refreshed %d records, want 3: %#v", len(records), records)
	}
	wantKeys := []string{"a", "b", "c"}
	for i, key := range wantKeys {
		if records[i].Key != key {
			t.Fatalf("record order = %q at %d, want sorted key %q", records[i].Key, i, key)
		}
		if !records[i].RefreshAt.Equal(t0.Add(7 * time.Second)) {
			t.Fatalf("record %s applied at %s, want %s", key, records[i].RefreshAt, t0.Add(7*time.Second))
		}
	}
	logSnapshot(t, "final", r)
}

func TestShutdownFlushesAllPendingImmediately(t *testing.T) {
	r, _ := New(time.Minute, 2)
	t0 := testTime(30)
	mustChange(t, r, "left", "old", t0)
	mustChange(t, r, "right", "new", t0.Add(2*time.Second))

	records, err := r.Shutdown(t0.Add(3 * time.Second))
	if err != nil {
		t.Fatalf("shutdown failed: %v", err)
	}
	t.Logf("input: pending two keys scheduled 60s later, shutdown@3s; result=%v; basis=shutdown flushes every pending batch immediately", records)
	if len(records) != 2 || r.PendingKeyCount() != 0 || r.TotalBatchCount() != 2 {
		t.Fatalf("shutdown did not flush all pending batches: records=%v snapshot=%v", records, r.Snapshot())
	}
	for _, record := range records {
		if !record.RefreshAt.Equal(t0.Add(3*time.Second)) || !record.ScheduledAt.After(t0.Add(3*time.Second)) {
			t.Fatalf("shutdown record timing mismatch: %#v", record)
		}
	}

	err = r.Change("after-close", "x", t0.Add(4*time.Second))
	t.Logf("input: change after shutdown at=34s; result=%v; basis=closed refresher rejects new changes", err)
	if !errors.Is(err, ErrRefresherClosed) {
		t.Fatalf("post-shutdown change error = %v, want ErrRefresherClosed", err)
	}
	logSnapshot(t, "final", r)
}

func TestLogicalClockAndTieArrivalOrder(t *testing.T) {
	r, _ := New(10*time.Second, 2)
	t0 := testTime(40)
	mustChange(t, r, "same", "first", t0)
	mustChange(t, r, "same", "second", t0)

	sameTime, err := r.Advance(t0.Add(10 * time.Second))
	if err != nil {
		t.Fatalf("equal timestamp should be accepted: %v", err)
	}
	t.Logf("input: two arrivals at same timestamp then advance at deadline; result=%v; basis=non-decreasing clock accepts ties, sequential arrival makes second value win, equality is due", sameTime)
	if len(sameTime) != 1 || sameTime[0].Value != "second" || sameTime[0].Count != 2 {
		t.Fatalf("same-timestamp tie handling mismatch: %#v", sameTime)
	}

	if err := r.Change("other", "fresh", t0.Add(10*time.Second)); err != nil {
		t.Fatalf("seed change failed: %v", err)
	}
	err = r.Change("other", "old", t0.Add(9*time.Second))
	t.Logf("input: change older than last successful time; result=%v; basis=strict Before check rejects rollback", err)
	if !errors.Is(err, ErrTimeBeforeLast) {
		t.Fatalf("rollback error = %v, want ErrTimeBeforeLast", err)
	}
	_, err = r.Advance(t0.Add(9 * time.Second))
	if !errors.Is(err, ErrTimeBeforeLast) {
		t.Fatalf("advance rollback error = %v, want ErrTimeBeforeLast", err)
	}
	logSnapshot(t, "after-rollback-rejection", r)
}

func TestRejectedInputsDoNotMutateState(t *testing.T) {
	tests := []struct {
		name        string
		invoke      func(r *Refresher) error
		constructor func() (*Refresher, error)
		want        error
	}{
		{
			name:        "non-positive interval",
			constructor: func() (*Refresher, error) { return New(0, 1) },
			want:        ErrNonPositiveParameter,
		},
		{
			name:        "non-positive pending limit",
			constructor: func() (*Refresher, error) { return New(time.Second, 0) },
			want:        ErrNonPositiveParameter,
		},
		{
			name:   "empty key",
			invoke: func(r *Refresher) error { return r.Change("", "v", testTime(50)) },
			want:   ErrEmptyKey,
		},
		{
			name:   "time rollback",
			invoke: func(r *Refresher) error { return r.Change("k", "v", testTime(49)) },
			want:   ErrTimeBeforeLast,
		},
		{
			name:   "pending key limit",
			invoke: func(r *Refresher) error { return r.Change("second", "v", testTime(50)) },
			want:   ErrTooManyPendingKeys,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.constructor != nil {
				r, err := tc.constructor()
				t.Logf("input: case=%s; result=(r=%v err=%v); basis=positive interval and limit are required", tc.name, r, err)
				if !errors.Is(err, tc.want) || r != nil {
					t.Fatalf("constructor result = (%v, %v), want (nil, %v)", r, err, tc.want)
				}
				return
			}

			r, _ := New(time.Second, 1)
			if err := r.Change("first", "seed", testTime(50)); err != nil {
				t.Fatalf("seed change failed: %v", err)
			}
			before := r.Snapshot()
			err := tc.invoke(r)
			after := r.Snapshot()
			t.Logf("input: case=%s before=%#v; result=%v after=%#v; basis=rejected operation mutates no clock, pending batch, view, or total", tc.name, before, err, after)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("state changed after rejection: before=%#v after=%#v", before, after)
			}

			err = r.Change("first", "still-usable", testTime(51))
			if err != nil {
				t.Fatalf("refresher not usable after rejection: %v", err)
			}
			t.Logf("input: follow-up valid change after rejection; result=%v; basis=rejected call left the instance usable", err)
		})
	}
}

func TestConcurrentReadsReturnIdenticalSnapshot(t *testing.T) {
	r, _ := New(10*time.Second, 4)
	t0 := testTime(60)
	mustChange(t, r, "a", "pending-a", t0)
	mustChange(t, r, "b", "pending-b", t0)
	_, _ = r.Advance(t0.Add(10 * time.Second))
	mustChange(t, r, "a", "new-pending", t0.Add(20*time.Second))

	const readerCount = 16
	start := make(chan struct{})
	var wg sync.WaitGroup
	snapshots := make([]Snapshot, readerCount)
	wg.Add(readerCount)
	for i := 0; i < readerCount; i++ {
		go func(index int) {
			defer wg.Done()
			<-start
			snapshots[index] = r.Snapshot()
		}(i)
	}
	close(start)
	wg.Wait()

	for i := 1; i < readerCount; i++ {
		if !reflect.DeepEqual(snapshots[0], snapshots[i]) {
			t.Fatalf("snapshot %d = %#v, want identical to %#v", i, snapshots[i], snapshots[0])
		}
	}
	t.Logf("input: %d concurrent Snapshot calls on one non-writing instance; result=%#v; basis=mutex serializes reads and maps/slices are deep-copied", readerCount, snapshots[0])

	if got := fmt.Sprint(snapshots[0].View); got != "map[a:pending-a b:pending-b]" {
		t.Fatalf("committed view = %s", got)
	}
	if snapshots[0].PendingKeyCount != 1 || snapshots[0].TotalBatchCount != 2 {
		t.Fatalf("unexpected read model: %#v", snapshots[0])
	}
}

func mustChange(t *testing.T, r *Refresher, key, value string, at time.Time) {
	t.Helper()
	if err := r.Change(key, value, at); err != nil {
		t.Fatalf("Change(%q, %q, %s) failed: %v", key, value, at, err)
	}
}
