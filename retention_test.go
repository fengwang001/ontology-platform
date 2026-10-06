package retention

import (
	"errors"
	"sync"
	"testing"
)

func testPolicy() Policy {
	return Policy{ReachableRetention: 10, UnreachableRetention: 5, FreshGrace: 7}
}

func newStore(policy Policy) *Store {
	store, err := NewStore(policy)
	if err != nil {
		panic(err)
	}
	return store
}

func mustAddContent(t *testing.T, store *Store, id ID, size int64, written int64) {
	t.Helper()
	if err := store.AddContent(Content{ID: id, Size: size, Written: written}); err != nil {
		t.Fatalf("input AddContent(%q): actual %v; want nil", id, err)
	}
}

func mustAddCommit(t *testing.T, store *Store, id ID, parents, contents []ID, created, written int64) {
	t.Helper()
	err := store.AddCommit(Commit{ID: id, Parents: parents, Created: created, Contents: contents, Written: written})
	if err != nil {
		t.Fatalf("input AddCommit(%q parents=%v contents=%v): actual %v; want nil", id, parents, contents, err)
	}
}

func TestRetentionBoundariesAndMergeClassification(t *testing.T) {
	store := newStore(testPolicy())
	mustAddContent(t, store, "base-content", 100, 0)
	mustAddCommit(t, store, "base", nil, []ID{"base-content"}, 0, 0)
	mustAddCommit(t, store, "left", []ID{"base"}, nil, 1, 0)
	mustAddCommit(t, store, "right", []ID{"base"}, nil, 2, 0)
	mustAddCommit(t, store, "merge", []ID{"left", "right"}, nil, 3, 0)
	mustAddCommit(t, store, "other", nil, nil, 4, 0)
	for _, op := range []struct {
		ref    string
		commit ID
		at     int64
	}{
		{"reachable", "base", 1},
		{"reachable", "merge", 2},
		{"unreachable", "base", 5},
		{"unreachable", "other", 6},
		{"reachable", "other", 7},
		{"reachable", "merge", 8},
	} {
		if err := store.Set(op.ref, op.commit, op.at, "u"); err != nil {
			t.Fatalf("input Set(%s=%s@%d): actual %v", op.ref, op.commit, op.at, err)
		}
	}
	if err := store.Expire(11); err != nil {
		t.Fatalf("input Input Expire(11): actual %v", err)
	}
	if _, err := store.History("unreachable", 1); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("input History unreachable at equal age=5: actual %v; want record-not-found", err)
	}
	if _, err := store.History("reachable", 2); err != nil {
		t.Fatalf("input History reachable other->merge age=3: actual %v; want merge-ancestor record retained", err)
	}
	if err := store.Expire(12); err != nil {
		t.Fatalf("input Expire(12): actual %v", err)
	}
	if active := len(store.refs["reachable"].active); active != 2 {
		t.Fatalf("input reachable records at age=10: actual %d; want base->merge expired and two current-age records retained", active)
	}
	if record, err := store.History("reachable", 2); err != nil || record.Old != "other" {
		t.Fatalf("input reachable ancestor-classified update: actual %+v,%v; want other->merge retained", record, err)
	}
	if err := store.Expire(16); err != nil {
		t.Fatalf("input Expire(16): actual %v", err)
	}
	if record, err := store.History("reachable", 1); err != nil || record.New != "other" {
		t.Fatalf("input remaining reachable-classified record at age=9: actual %+v,%v; want other->merge retained", record, err)
	}
}

func TestLogKeepsObjectsAliveThenReleasesThem(t *testing.T) {
	store := newStore(Policy{100, 100, 0})
	mustAddCommit(t, store, "old", nil, nil, 0, 0)
	mustAddCommit(t, store, "new", nil, nil, 1, 0)
	if err := store.Set("r", "old", 0, "u"); err != nil {
		t.Fatal(err)
	}
	if err := store.Set("r", "new", 1, "u"); err != nil {
		t.Fatal(err)
	}
	got, err := store.Collect(50)
	t.Logf("input Collect(50); actual=%+v err=%v; criterion: unexpired old value is a root", got, err)
	if err != nil || got.CommitsDeleted != 0 {
		t.Fatalf("actual %+v,%v; want zero deletions", got, err)
	}
	got, err = store.Collect(101)
	t.Logf("input Collect(101); actual=%+v err=%v; criterion: old loses pin at equal age", got, err)
	if err != nil || got.CommitsDeleted != 1 {
		t.Fatalf("actual %+v,%v; want old deleted", got, err)
	}
	if _, err := store.History("r", 1); err != nil {
		t.Fatalf("input History after Collect: actual %v; want independent expire keeps log", err)
	}
}

func TestFreshGraceBoundaryAndIdempotence(t *testing.T) {
	store := newStore(Policy{1, 1, 10})
	mustAddContent(t, store, "c", 40, 5)
	mustAddCommit(t, store, "a", nil, []ID{"c"}, 5, 5)
	got, err := store.Collect(14)
	t.Logf("input Collect(14); actual=%+v err=%v; criterion: age 9 < grace 10", got, err)
	if err != nil || got != (GCResult{}) {
		t.Fatalf("actual %+v,%v; want retained", got, err)
	}
	got, err = store.Collect(15)
	t.Logf("input Collect(15); actual=%+v err=%v; criterion: age equals grace", got, err)
	want := GCResult{CommitsDeleted: 1, ContentsDeleted: 1, BytesDeleted: 40}
	if err != nil || got != want {
		t.Fatalf("actual %+v,%v; want %+v", got, err, want)
	}
	got, err = store.Collect(15)
	t.Logf("input repeated Collect(15); actual=%+v err=%v; criterion: unchanged second collection", got, err)
	if err != nil || got != (GCResult{}) {
		t.Fatalf("actual %+v,%v; want empty idempotent result", got, err)
	}
}

func TestInvalidPolicyAndContentReachability(t *testing.T) {
	if _, err := NewStore(Policy{-1, 0, 0}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("input NewStore(negative reachable retention); actual %v; want invalid argument", err)
	}
	if _, err := NewStore(Policy{0, -1, 0}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("input NewStore(negative unreachable retention); actual %v; want invalid argument", err)
	}
	if _, err := NewStore(Policy{0, 0, -1}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("input NewStore(negative grace); actual %v; want invalid argument", err)
	}
	store := newStore(Policy{1, 1, 0})
	mustAddContent(t, store, "orphan", 7, 0)
	mustAddContent(t, store, "shared", 3, 0)
	mustAddCommit(t, store, "commit", nil, []ID{"shared"}, 0, 0)
	if err := store.Set("r", "commit", 0, "u"); err != nil {
		t.Fatal(err)
	}
	got, err := store.Collect(2)
	t.Logf("input Collect with orphan content and committed shared content; actual=%+v err=%v; criterion: content lives only through commit edges", got, err)
	if err != nil || got.ContentsDeleted != 1 || got.BytesDeleted != 7 || got.CommitsDeleted != 0 {
		t.Fatalf("actual %+v,%v; want only orphan content deleted", got, err)
	}
}

func TestExpireCollectEquivalenceAndDeletedReference(t *testing.T) {
	policy := Policy{100, 4, 0}
	separate := newStore(policy)
	combined := newStore(policy)
	for _, store := range []*Store{separate, combined} {
		mustAddCommit(t, store, "a", nil, nil, 0, 0)
		mustAddCommit(t, store, "b", nil, nil, 0, 0)
		if err := store.Set("r", "a", 0, "u"); err != nil {
			t.Fatal(err)
		}
		if err := store.Set("r", "b", 1, "u"); err != nil {
			t.Fatal(err)
		}
		if err := store.Delete("r", 2, "u"); err != nil {
			t.Fatal(err)
		}
	}
	if err := separate.Expire(6); err != nil {
		t.Fatal(err)
	}
	first, err := separate.Collect(6)
	if err != nil {
		t.Fatal(err)
	}
	merged, err := combined.ExpireAndCollect(6)
	t.Logf("input Expire+Collect versus ExpireAndCollect at 6; actual separate=%+v merged=%+v err=%v; criterion: deleted ref uses unreachable tier", first, merged, err)
	if err != nil || first != merged || first.CommitsDeleted != 2 {
		t.Fatalf("actual separate=%+v merged=%+v,%v; want two equal deletions", first, merged, err)
	}
}

func TestReferenceRecreateSharesLogAndQueryStates(t *testing.T) {
	store := newStore(Policy{1, 1, 100})
	mustAddCommit(t, store, "a", nil, nil, 0, 0)
	mustAddCommit(t, store, "b", nil, nil, 0, 0)
	if err := store.Set("r", "a", 0, "u"); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete("r", 1, "u"); err != nil {
		t.Fatal(err)
	}
	if err := store.Set("r", "b", 2, "u"); err != nil {
		t.Fatal(err)
	}
	record, err := store.History("r", 2)
	t.Logf("input History(recreated r,2); actual=%+v err=%v; criterion: same-name recreation shares old log", record, err)
	if err != nil || record.New != "" {
		t.Fatalf("actual %+v,%v; want prior deletion record", record, err)
	}
	if err := store.Expire(3); err != nil {
		t.Fatal(err)
	}
	_, neverErr := store.History("never", 1)
	_, exhaustedErr := store.History("r", 1)
	t.Logf("input never-existed and fully-expired ref; actual never=%v exhausted=%v existed=%v; criterion: error classes differ", neverErr, exhaustedErr, store.RefExisted("r"))
	if !errors.Is(neverErr, ErrRefNotFound) || !errors.Is(exhaustedErr, ErrRecordNotFound) || !store.RefExisted("r") {
		t.Fatalf("actual never=%v exhausted=%v; want ref-not-found, record-not-found, tombstone retained", neverErr, exhaustedErr)
	}
}

func TestClockRejectAndErrorPrecedence(t *testing.T) {
	store := newStore(testPolicy())
	mustAddCommit(t, store, "c", nil, nil, 10, 10)
	cases := []struct {
		name string
		call func() error
		want error
	}{
		{"invalid before clock", func() error { return store.Set("", "missing", 0, "") }, ErrInvalidArgument},
		{"clock before commit", func() error { return store.Set("r", "missing", 9, "") }, ErrClockMovedBack},
		{"commit after clock", func() error { return store.Set("r", "missing", 11, "") }, ErrCommitNotFound},
		{"delete invalid before clock", func() error { return store.Delete("", 9, "") }, ErrInvalidArgument},
		{"delete clock before ref", func() error { return store.Delete("missing", 9, "") }, ErrClockMovedBack},
		{"delete ref after clock", func() error { return store.Delete("missing", 11, "") }, ErrRefNotFound},
		{"negative expire before clock", func() error { return store.Expire(-1) }, ErrInvalidArgument},
		{"expire clock rollback", func() error { return store.Expire(9) }, ErrClockMovedBack},
		{"history invalid before ref", func() error { _, err := store.History("", 1); return err }, ErrInvalidArgument},
		{"history ref before record", func() error { _, err := store.History("missing", 1); return err }, ErrRefNotFound},
	}
	for _, tc := range cases {
		err := tc.call()
		t.Logf("input %s; actual=%v want=%v; criterion: report earliest error class and make no side effect", tc.name, err, tc.want)
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: actual %v; want %v", tc.name, err, tc.want)
		}
	}
	if err := store.Set("r", "c", 12, "u"); err != nil {
		t.Fatal(err)
	}
	if err := store.Set("r", "c", 10, "u"); !errors.Is(err, ErrClockMovedBack) {
		t.Fatalf("input accepted-then-rollback Set at 10: actual %v; want clock rollback", err)
	}
	if _, err := store.History("r", 2); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("actual %v; want only accepted call appended", err)
	}
}

func TestConcurrentUpdateAndCollectionAreSerializable(t *testing.T) {
	for iteration := 0; iteration < 100; iteration++ {
		store := newStore(Policy{1000, 1000, 1000})
		mustAddCommit(t, store, "kept", nil, nil, 0, 0)
		mustAddCommit(t, store, "gone", nil, nil, 0, 0)
		if err := store.Set("r", "kept", 0, "init"); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_ = store.Set("r", "gone", 1, "update")
		}()
		go func() {
			defer wg.Done()
			<-start
			_, _ = store.Collect(1)
		}()
		close(start)
		wg.Wait()
		got, err := store.Collect(1)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("input concurrent Set(gone) and Collect(1), iteration=%d; actual=%+v; criterion: current target must survive in either serial order", iteration, got)
		if _, exists := store.commits["gone"]; !exists || got.CommitsDeleted != 0 {
			t.Fatalf("actual gone exists=%v result=%+v; want current target retained", exists, got)
		}
	}
}

func TestConcurrentSameTimestampUsesSuccessOrder(t *testing.T) {
	store := newStore(testPolicy())
	mustAddCommit(t, store, "a", nil, nil, 0, 0)
	mustAddCommit(t, store, "b", nil, nil, 0, 0)
	firstLocked := make(chan struct{})
	secondQueued := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		store.mu.Lock()
		defer store.mu.Unlock()
		close(firstLocked)
		<-secondQueued
		_ = store.appendRefRecord("r", "a", 5, "first")
	}()
	go func() {
		defer wg.Done()
		<-firstLocked
		close(secondQueued)
		store.Set("r", "b", 5, "second")
	}()
	wg.Wait()
	latest, err := store.History("r", 1)
	if err != nil {
		t.Fatal(err)
	}
	previous, err := store.History("r", 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("input two same-time updates; actual latest=%s previous=%s; criterion: successful lock order breaks timestamp tie", latest.Operator, previous.Operator)
	if latest.New != "b" || previous.New != "a" {
		t.Fatalf("actual latest=%s previous=%s; want later successful write b on top", latest.New, previous.New)
	}
}
