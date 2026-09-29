package stagedcommit

import (
	"errors"
	"maps"
	"reflect"
	"sync"
	"testing"
)

func TestCommitUsesTwoPhaseLogAndClearsStaging(t *testing.T) {
	committer := New()

	if err := committer.Put("name", "alice"); err != nil {
		t.Fatalf("input Put(name=alice), result %v, want success", err)
	}
	if err := committer.Put("count", "1"); err != nil {
		t.Fatalf("input Put(count=1), result %v, want success", err)
	}

	t.Logf("input=two puts before commit; result=%v; basis=staged changes must be invisible until committed", committer.StagedSnapshot())
	if got := committer.ViewSnapshot(); len(got) != 0 {
		t.Fatalf("input=view before commit, result=%v, basis=uncommitted changes must not be visible", got)
	}

	id, err := committer.Commit()
	if err != nil || id != 1 {
		t.Fatalf("input=commit, result=(%d,%v), basis=first commit must consume ID 1", id, err)
	}

	logEntries := committer.LogSnapshot()
	t.Logf("input=commit; result=id %d log=%v; basis=log must end with prepared then committed entry", id, logEntries)
	if len(logEntries) != 1 || logEntries[0].State != StateCommitted || logEntries[0].CommitID != 1 {
		t.Fatalf("input=log after commit, result=%v, basis=entry must be finalized", logEntries)
	}

	if got := committer.ViewSnapshot(); !reflect.DeepEqual(got, map[string]string{"name": "alice", "count": "1"}) {
		t.Fatalf("input=view after commit, result=%v, basis=view must match all put operations", got)
	}
	if got := committer.StagedSnapshot(); len(got) != 0 {
		t.Fatalf("input=staging after commit, result=%v, basis=staging must be cleared", got)
	}

	if err := committer.Put("count", "2"); err != nil {
		t.Fatalf("input=Put(count=2), result=%v, want success", err)
	}
	if err := committer.Delete("name"); err != nil {
		t.Fatalf("input=Delete(name), result=%v, want success", err)
	}

	id, err = committer.Commit()
	if err != nil || id != 2 {
		t.Fatalf("input=overwrite and delete commit, result=(%d,%v), basis=IDs increase from 1", id, err)
	}
	if got := committer.ViewSnapshot(); !reflect.DeepEqual(got, map[string]string{"count": "2"}) {
		t.Fatalf("input=view after second commit, result=%v, basis=overwrite and delete must be applied", got)
	}
}

func TestCrashLeavesPreparedChangeInvisibleAndRecoverDiscardsID(t *testing.T) {
	committer := New()
	if err := committer.Put("visible", "old"); err != nil {
		t.Fatal(err)
	}
	if _, err := committer.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := committer.Put("visible", "new"); err != nil {
		t.Fatal(err)
	}
	if err := committer.Put("new", "value"); err != nil {
		t.Fatal(err)
	}

	id, err := committer.SimulateCrashAfterPrepare()
	if err != nil || id != 2 {
		t.Fatalf("input=crash after prepare, result=(%d,%v), basis=crash must consume ID 2", id, err)
	}

	view := committer.ViewSnapshot()
	staged := committer.StagedSnapshot()
	logEntries := committer.LogSnapshot()
	t.Logf("input=crash after prepare; result=view=%v staged=%v log=%v; basis=prepared entry exists but view stays at previous boundary", view, staged, logEntries)

	if !reflect.DeepEqual(view, map[string]string{"visible": "old"}) {
		t.Fatalf("input=view after crash, result=%v, basis=prepared commit must have no visible effect", view)
	}
	if len(staged) != 0 {
		t.Fatalf("input=staging after crash, result=%v, basis=staging is cleared as in a process crash", staged)
	}
	if len(logEntries) != 2 || logEntries[1].State != StatePrepared || logEntries[1].CommitID != 2 {
		t.Fatalf("input=log after crash, result=%v, basis=latest entry must remain prepared and unfinalized", logEntries)
	}

	recoveredID := committer.Recover()
	t.Logf("input=recover; result=%d; basis=latest prepared unfinalized entry is judged discarded", recoveredID)
	if recoveredID != 2 {
		t.Fatalf("input=first recover, result=%d, basis=discarded commit ID must be 2", recoveredID)
	}
	if got := committer.Recover(); got != 0 {
		t.Fatalf("input=second recover, result=%d, basis=repeated recovery must return zero", got)
	}
	if got := committer.ViewSnapshot(); !reflect.DeepEqual(got, map[string]string{"visible": "old"}) {
		t.Fatalf("input=view after recovery, result=%v, basis=aborted prepared entry was never applied", got)
	}

	if err := committer.Put("after", "recovery"); err != nil {
		t.Fatalf("input=Put after recovery, result=%v, basis=committer remains usable", err)
	}
	nextID, err := committer.Commit()
	if err != nil || nextID != 3 {
		t.Fatalf("input=commit after recovery, result=(%d,%v), basis=consumed ID 2 is not reused", nextID, err)
	}
	logEntries = committer.LogSnapshot()
	if len(logEntries) != 3 || logEntries[1].State != StateAborted || logEntries[2].State != StateCommitted {
		t.Fatalf("input=log after next commit, result=%v, basis=aborted entry remains distinguishable", logEntries)
	}
}

func TestInvalidInputsAndEmptyCommitLeaveAllStateUnchanged(t *testing.T) {
	committer := New()
	if err := committer.Put("existing", "value"); err != nil {
		t.Fatal(err)
	}
	if err := committer.Put("staged", "value"); err != nil {
		t.Fatal(err)
	}

	before := stateSnapshot{
		view:   committer.ViewSnapshot(),
		staged: committer.StagedSnapshot(),
		log:    committer.LogSnapshot(),
	}

	tests := []struct {
		name string
		call func() error
		want error
	}{
		{
			name: "empty key put",
			call: func() error { return committer.Put("", "value") },
			want: ErrEmptyKey,
		},
		{
			name: "empty key delete",
			call: func() error { return committer.Delete("") },
			want: ErrEmptyKey,
		},
		{
			name: "empty commit",
			call: func() func() error {
				empty := New()
				return func() error {
					_, err := empty.Commit()
					return err
				}
			}(),
			want: ErrEmptyCommit,
		},
		{
			name: "delete missing visible key",
			call: func() error { return committer.Delete("missing") },
			want: ErrDeleteNotAllowed,
		},
		{
			name: "delete already staged key",
			call: func() error { return committer.Delete("staged") },
			want: ErrDeleteNotAllowed,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			t.Logf("input=%s; result=%v; basis=%s must be rejected with a distinct sentinel error", tc.name, err, tc.want)
			if !errors.Is(err, tc.want) {
				t.Fatalf("input=%s, result=%v, want %v", tc.name, err, tc.want)
			}
		})
	}

	after := stateSnapshot{
		view:   committer.ViewSnapshot(),
		staged: committer.StagedSnapshot(),
		log:    committer.LogSnapshot(),
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("input=invalid operations; before=%+v after=%+v; basis=rejection changes no state", before, after)
	}
	id, err := committer.Commit()
	if err != nil || id != 1 {
		t.Fatalf("input=commit after rejected operations, result=(%d,%v), basis=rejections must not consume commit ID 1", id, err)
	}

	empty := New()
	if _, err := empty.SimulateCrashAfterPrepare(); !errors.Is(err, ErrEmptyCommit) {
		t.Fatalf("input=crash prepare with empty staging, result=%v, want %v", err, ErrEmptyCommit)
	}
	if len(empty.LogSnapshot()) != 0 || empty.Recover() != 0 {
		t.Fatalf("input=rejected empty crash; basis=it must leave no log or recoverable commit")
	}
	if err := empty.Put("recovered-empty", "ok"); err != nil {
		t.Fatalf("input=Put after rejected empty commit, result=%v, basis=committer must remain usable", err)
	}
	if id, err := empty.Commit(); err != nil || id != 1 {
		t.Fatalf("input=commit after rejected empty commit, result=(%d,%v), basis=empty rejection must not consume ID 1", id, err)
	}
}

func TestRollbackClearsStagingWithoutTrace(t *testing.T) {
	committer := New()
	if err := committer.Put("a", "1"); err != nil {
		t.Fatal(err)
	}

	committer.Rollback()
	t.Logf("input=rollback; result=staged=%v log=%v; basis=rollback only discards staging and writes no entry", committer.StagedSnapshot(), committer.LogSnapshot())

	if len(committer.StagedSnapshot()) != 0 {
		t.Fatal("rollback must clear staging")
	}
	if len(committer.LogSnapshot()) != 0 {
		t.Fatal("rollback must leave no log trace")
	}

	if err := committer.Put("a", "2"); err != nil {
		t.Fatal(err)
	}
	id, err := committer.Commit()
	if err != nil || id != 1 {
		t.Fatalf("commit after rollback got (%d,%v), want ID 1", id, err)
	}
}

func TestConcurrentSnapshotsAreImmutableAndConsistent(t *testing.T) {
	committer := New()
	if err := committer.Put("a", "1"); err != nil {
		t.Fatal(err)
	}
	if _, err := committer.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := committer.Put("b", "2"); err != nil {
		t.Fatal(err)
	}

	const readers = 32
	views := make([]map[string]string, readers)
	staged := make([]map[string]Change, readers)
	start := make(chan struct{})
	var wg sync.WaitGroup

	for i := range views {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			views[index] = committer.ViewSnapshot()
			staged[index] = committer.StagedSnapshot()
		}(i)
	}

	close(start)
	wg.Wait()

	for i := 1; i < readers; i++ {
		t.Logf("input=concurrent read %d; result=equal=%v; basis=all readers observe the same committed boundary", i, maps.EqualFunc(views[0], views[i], func(a, b string) bool { return a == b }))
		if !maps.Equal(views[0], views[i]) || !reflect.DeepEqual(staged[0], staged[i]) {
			t.Fatalf("concurrent snapshots differ: %v/%v and %v/%v", views[0], staged[0], views[i], staged[i])
		}
	}

	views[0]["mutated"] = "snapshot"
	staged[0]["mutated"] = Change{Op: OpPut}
	if _, exists := committer.ViewSnapshot()["mutated"]; exists {
		t.Fatal("view snapshot must be detached from committer state")
	}
	if _, exists := committer.StagedSnapshot()["mutated"]; exists {
		t.Fatal("staged snapshot must be detached from committer state")
	}
}

type stateSnapshot struct {
	view   map[string]string
	staged map[string]Change
	log    []LogEntry
}
