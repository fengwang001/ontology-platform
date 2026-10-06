package statusengine

import (
	"errors"
	"reflect"
	"testing"
)

func TestNormalStatusesAreExclusive(t *testing.T) {
	engine, err := NewEngine(Snapshot{
		"tracked-mod":  []byte("base"),
		"tracked-del":  []byte("base"),
		"staged-mod":   []byte("base"),
		"staged-del":   []byte("base"),
		"staged-mixed": []byte("base"),
		"recreated":    []byte("base"),
	}, func(path string) bool {
		return path == "ignored"
	})
	if err != nil {
		t.Fatal(err)
	}

	mustWrite(t, engine, "staged-add", []byte("added"))
	mustWrite(t, engine, "untracked", []byte("new"))
	mustWrite(t, engine, "ignored", []byte("ignored-content"))

	for _, setup := range []func() error{
		func() error {
			engine.index = cloneContent(engine.snapshot)
			engine.indexStore = cloneStore(engine.snapshotStore)
			return nil
		},
	} {
		if err := setup(); err != nil {
			t.Fatal(err)
		}
	}

	mustWrite(t, engine, "tracked-mod", []byte("work"))
	if err := engine.RemoveWorktree("tracked-del"); err != nil {
		t.Fatal(err)
	}
	if err := engine.Stage("staged-add"); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, engine, "staged-mod", []byte("staged"))
	if err := engine.Stage("staged-mod"); err != nil {
		t.Fatal(err)
	}
	if err := engine.RemoveWorktree("staged-del"); err != nil {
		t.Fatal(err)
	}
	if err := engine.Stage("staged-del"); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, engine, "staged-mixed", []byte("staged"))
	if err := engine.Stage("staged-mixed"); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, engine, "staged-mixed", []byte("work-again"))
	if err := engine.RemoveWorktree("recreated"); err != nil {
		t.Fatal(err)
	}
	if err := engine.Stage("recreated"); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, engine, "recreated", []byte("rebuilt"))

	cases := []struct {
		path   string
		status Status
	}{
		{"staged-add", StatusStagedAdded},
		{"staged-mod", StatusStagedModified},
		{"staged-del", StatusStagedDeleted},
		{"tracked-mod", StatusWorktreeModified},
		{"tracked-del", StatusWorktreeDeleted},
		{"untracked", StatusUntracked},
		{"staged-mixed", StatusStagedThenModified},
		{"recreated", StatusStagedDeletedThenRecreated},
		{"ignored", StatusIgnored},
		{"staged-mod", StatusStagedModified},
	}
	seen := map[Status]int{}
	for _, tc := range cases {
		entry, err := engine.Status(tc.path)
		if err != nil {
			t.Fatalf("path=%s err=%v", tc.path, err)
		}
		t.Logf("输入=%s 实际=%s 判定=%s", tc.path, entry.Status, tc.status)
		if entry.Status != tc.status {
			t.Fatalf("path=%s got=%s want=%s", tc.path, entry.Status, tc.status)
		}
		seen[entry.Status]++
	}

	unchanged, err := engine.Status("staged-mod")
	if err != nil {
		t.Fatal(err)
	}
	_ = unchanged

	clean, err := NewEngine(Snapshot{"a": []byte("same")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := clean.Status("a")
	t.Logf("输入=a 实际=%s 判定=%s", entry.Status, StatusUnchanged)
	if err != nil || entry.Status != StatusUnchanged {
		t.Fatalf("got=%v,%v", entry, err)
	}
}

func TestIgnoreOnlyAffectsUntracked(t *testing.T) {
	engine, err := NewEngine(Snapshot{"ignored": []byte("snapshot")}, func(path string) bool {
		return path == "ignored" || path == "ignored-new"
	})
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, engine, "ignored", []byte("changed"))
	mustWrite(t, engine, "ignored-new", []byte("x"))

	tracked, err := engine.Status("ignored")
	if err != nil {
		t.Fatal(err)
	}
	untracked, err := engine.Status("ignored-new")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("输入=ignored 实际=%s 判定=%s", tracked.Status, StatusWorktreeModified)
	t.Logf("输入=ignored-new 实际=%s 判定=%s", untracked.Status, StatusIgnored)
	if tracked.Status != StatusWorktreeModified || untracked.Status != StatusIgnored {
		t.Fatal("tracked paths must not be ignored")
	}
}

func TestConflictKindsAndPriority(t *testing.T) {
	cases := []struct {
		name   string
		stages Stages
		kind   ConflictKind
	}{
		{"content", Stages{Base: []byte("b"), Ours: []byte("o"), Theirs: []byte("t")}, ConflictContent},
		{"ours-deleted", Stages{Base: []byte("b"), Ours: nil, Theirs: []byte("t")}, ConflictOursDeletedTheirsModified},
		{"theirs-deleted", Stages{Base: []byte("b"), Ours: []byte("o"), Theirs: nil}, ConflictOursModifiedTheirsDeleted},
		{"both-added", Stages{Base: nil, Ours: []byte("o"), Theirs: []byte("t")}, ConflictBothAdded},
	}
	for _, tc := range cases {
		engine, err := NewEngine(Snapshot{tc.name: []byte("base")}, nil)
		if err != nil {
			t.Fatal(err)
		}
		mustWrite(t, engine, tc.name, []byte("worktree-different"))
		if err := engine.BeginMerge(map[string]Stages{tc.name: tc.stages}); err != nil {
			t.Fatal(err)
		}
		entry, err := engine.Status(tc.name)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("输入=%s 实际=%s/%d 判定=%s/%d", tc.name, entry.Status, entry.ConflictKind, StatusConflict, tc.kind)
		if entry.Status != StatusConflict || entry.ConflictKind != tc.kind {
			t.Fatalf("got=%s/%d want=%s/%d", entry.Status, entry.ConflictKind, StatusConflict, tc.kind)
		}
	}
}

func TestInvalidPaths(t *testing.T) {
	cases := []string{"", ".", "..", "../x", "a//b", "a/../b", "a/./b", "/a"}
	for _, path := range cases {
		engine, err := NewEngine(Snapshot{"a": []byte("x")}, nil)
		if err != nil {
			t.Fatal(err)
		}
		err = engine.WriteWorktree(path, []byte("x"))
		t.Logf("输入=%q 实际=%v 判定=%v", path, err, ErrInvalidPath)
		if !errors.Is(err, ErrInvalidPath) {
			t.Fatalf("path=%q got=%v", path, err)
		}
	}

	engine, err := NewEngine(Snapshot{"a": []byte("x")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = engine.WriteWorktree("a/b", []byte("x"))
	t.Logf("输入=a/b 实际=%v 判定=%v", err, ErrInvalidPath)
	if !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("ancestor conflict: %v", err)
	}

	got := reflect.DeepEqual([]byte(nil), []byte{})
	_ = got
}

func mustWrite(t *testing.T, engine *Engine, path string, content []byte) {
	t.Helper()
	if err := engine.WriteWorktree(path, content); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
