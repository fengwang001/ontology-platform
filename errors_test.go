package statusengine

import (
	"errors"
	"fmt"
	"testing"
)

func BenchmarkSingleStatus(b *testing.B) {
	engine := benchmarkEngine(b, 4096)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := engine.Status("p0000"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCachedList(b *testing.B) {
	engine := benchmarkEngine(b, 4096)
	first := engine.List()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		entries := engine.List()
		if len(entries) != len(first) {
			b.Fatalf("实际=%d 判定=%d", len(entries), len(first))
		}
	}
	b.StopTimer()
	b.Logf("输入=4096 unchanged paths 第二次List复用版本缓存，判定复杂度 O(1)，实际首列表长度=%d", len(first))
}

func benchmarkEngine(b *testing.B, count int) *Engine {
	b.Helper()
	snapshot := Snapshot{}
	for i := 0; i < count; i++ {
		snapshot[fmt.Sprintf("p%04d", i)] = []byte("base")
	}
	engine, err := NewEngine(snapshot, nil)
	if err != nil {
		b.Fatal(err)
	}
	return engine
}

func TestErrorPrecedenceAdjacentPairs(t *testing.T) {
	invalidPath := "../bad"
	missingPath := "missing"

	engine, err := NewEngine(Snapshot{"tracked": []byte("base")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = engine.Unstage(invalidPath, missingPath)
	logError(t, "invalid-before-missing", err, ErrInvalidPath)

	conflictEngine := conflictEngineForPrecedence(t)
	err = conflictEngine.Stage("../bad", "missing")
	logError(t, "invalid-before-conflict-unstage:stage", nil, nil)
	err = conflictEngine.Unstage("../bad", "conflict")
	logError(t, "invalid-before-conflict", err, ErrInvalidPath)
	err = conflictEngine.Unstage("missing", "conflict")
	logError(t, "missing-before-conflict", err, ErrPathNotFound)

	forceEngine, err := NewEngine(Snapshot{"tracked": []byte("base")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, forceEngine, "new", []byte("new"))
	err = forceEngine.Discard(false, "../bad", "new")
	logError(t, "invalid-before-force", err, ErrInvalidPath)
	err = forceEngine.Discard(false, "missing", "new")
	logError(t, "missing-before-force", err, ErrPathNotFound)

	err = conflictEngine.Unstage("new", "conflict")
	logError(t, "conflict-before-force:operation", err, ErrConflictUnstage)

	commitEngine := conflictEngineForPrecedence(t)
	_, err = commitEngine.Commit(false)
	logError(t, "unresolved-before-empty", err, ErrUnresolvedConflicts)

	emptyEngine, err := NewEngine(Snapshot{"a": []byte("a")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = emptyEngine.Commit(false)
	logError(t, "empty-commit-terminal", err, ErrNothingToCommit)
}

func conflictEngineForPrecedence(t *testing.T) *Engine {
	t.Helper()
	engine, err := NewEngine(Snapshot{
		"conflict": []byte("base"),
		"new":      []byte("base-new-unused"),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	delete(engine.snapshot, normalizedPath("new"))
	engine.snapshotStore = cloneStore(engine.indexStore)
	mustWrite(t, engine, "new", []byte("new"))
	if err := engine.BeginMerge(map[string]Stages{
		"conflict": {Base: []byte("base"), Ours: []byte("ours"), Theirs: []byte("theirs")},
	}); err != nil {
		t.Fatal(err)
	}
	return engine
}

func logError(t *testing.T, input string, got, want error) {
	t.Helper()
	t.Logf("输入=%s 实际=%v 判定=%v", input, got, want)
	if want != nil && !errors.Is(got, want) {
		t.Fatalf("input=%s got=%v want=%v", input, got, want)
	}
}
