package ontology

import (
	"reflect"
	"testing"
)

func assertState(t *testing.T, manager *SnapshotCommitManager, nextTimestamp, watermark int64, want [][]TableEntryView, active []int64) {
	t.Helper()

	got := manager.Snapshot()
	if got.NextTimestamp != nextTimestamp || got.Watermark != watermark || !reflect.DeepEqual(got.Buckets, want) || !reflect.DeepEqual(got.Active, active) {
		t.Fatalf("state = ts:%d wm:%d buckets:%#v active:%#v; want ts:%d wm:%d buckets:%#v active:%#v",
			got.NextTimestamp, got.Watermark, got.Buckets, got.Active,
			nextTimestamp, watermark, want, active)
	}
}

func TestExampleEvictionWatermarkAndConflictPrecedence(t *testing.T) {
	manager, err := NewSnapshotCommitManager(1, 2)
	if err != nil {
		t.Fatal(err)
	}

	s1 := manager.Begin()
	s2 := manager.Begin()
	s3 := manager.Begin()
	s4 := manager.Begin()
	if []int64{s1, s2, s3, s4}[0] != 1 || s4 != 4 {
		t.Fatalf("begin timestamps = %d,%d,%d,%d", s1, s2, s3, s4)
	}

	if result := manager.Commit(s2, []int64{10}); result != (CommitResult{Kind: CommitOk, Timestamp: 5}) {
		t.Fatalf("commit 10 = %#v", result)
	}
	if result := manager.Commit(s3, []int64{20}); result != (CommitResult{Kind: CommitOk, Timestamp: 6}) {
		t.Fatalf("commit 20 = %#v", result)
	}

	manager.resetProbeCount()
	result := manager.Commit(s4, []int64{30})
	probes := manager.resetProbeCount()
	if result != (CommitResult{Kind: CommitOk, Timestamp: 7}) {
		t.Fatalf("commit 30 = %#v", result)
	}
	if probes > 4*int64(1*2) {
		t.Fatalf("probes = %d, limit %d", probes, 4*1*2)
	}
	assertState(t, manager, 7, 5, [][]TableEntryView{{
		{Key: 20, Timestamp: 6},
		{Key: 30, Timestamp: 7},
	}}, []int64{1})

	result = manager.Commit(s1, []int64{40})
	if result != (CommitResult{Kind: CommitWatermark}) {
		t.Fatalf("watermark commit = %#v", result)
	}

	result = manager.Commit(s1, []int64{20})
	if result != (CommitResult{Kind: CommitRejected, Reason: RejectUnknownTransaction}) {
		t.Fatalf("finished transaction = %#v", result)
	}

	// Re-run the conflict-before-watermark condition while s1 is still active.
	other, _ := NewSnapshotCommitManager(1, 4)
	open := other.Begin()
	writer := other.Begin()
	third := other.Begin()
	fourth := other.Begin()
	fifth := other.Begin()
	evicted := other.Begin()
	other.Commit(third, []int64{30})
	other.Commit(fourth, []int64{40})
	other.Commit(fifth, []int64{50})
	other.Commit(evicted, []int64{60})
	conflicter := other.Begin()
	other.Commit(conflicter, []int64{20})
	result = other.Commit(writer, []int64{20})
	if result.Kind != CommitConflict {
		t.Fatalf("precedence = %#v, want conflict", result)
	}
	if open != 1 || writer != 2 {
		t.Fatalf("unexpected transaction ids %d %d", open, writer)
	}
}
