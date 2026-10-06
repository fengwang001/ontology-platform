package statusengine

import (
	"bytes"
	"errors"
	"reflect"
	"testing"
)

func TestBatchAtomicityAndDirectoryExpansion(t *testing.T) {
	engine, err := NewEngine(Snapshot{
		"dir/a": []byte("old"),
		"dir/b": []byte("old"),
		"other": []byte("old"),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, engine, "dir/a", []byte("new-a"))
	mustWrite(t, engine, "dir/b", []byte("new-b"))

	before := engine.List()
	err = engine.Stage("dir", "missing")
	t.Logf("输入=dir,missing 实际=%v 判定=%v", err, ErrPathNotFound)
	if !errors.Is(err, ErrPathNotFound) {
		t.Fatal(err)
	}
	after := engine.List()
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("实际=%v 判定=%v，失败批次改变了状态", after, before)
	}

	if err := engine.Stage("dir"); err != nil {
		t.Fatal(err)
	}
	indexA, existsA, _ := engine.IndexPath("dir/a")
	indexB, existsB, _ := engine.IndexPath("dir/b")
	t.Logf("输入=dir 实际=(%s,%t),(%s,%t) 判定=new-a,new-b", indexA, existsA, indexB, existsB)
	if !existsA || !existsB || string(indexA) != "new-a" || string(indexB) != "new-b" {
		t.Fatal("directory staging did not expand")
	}

	err = engine.Stage("dir/a", "dir/a")
	t.Logf("输入=重复路径 实际=%v 判定=%v", err, ErrInvalidPath)
	if !errors.Is(err, ErrInvalidPath) {
		t.Fatal(err)
	}

	err = engine.Stage("empty-dir")
	t.Logf("输入=空前缀 实际=%v 判定=%v", err, ErrPathNotFound)
	if !errors.Is(err, ErrPathNotFound) {
		t.Fatal(err)
	}
}

func TestUnstageLeavesWorktreeUntouched(t *testing.T) {
	engine, err := NewEngine(Snapshot{"a": []byte("base")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, engine, "a", []byte("work"))
	if err := engine.Stage("a"); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, engine, "a", []byte("work-again"))
	if err := engine.Unstage("a"); err != nil {
		t.Fatal(err)
	}
	index, indexExists, _ := engine.IndexPath("a")
	worktree, worktreeExists, _ := engine.WorktreePath("a")
	t.Logf("输入=unstage a 实际=index:%s/%t worktree:%s/%t 判定=base,work-again", index, indexExists, worktree, worktreeExists)
	if string(index) != "base" || string(worktree) != "work-again" {
		t.Fatal("unstage changed worktree")
	}
}

func TestDiscardUntrackedRequiresForce(t *testing.T) {
	engine, err := NewEngine(Snapshot{"a": []byte("base")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, engine, "new", []byte("new"))
	err = engine.Discard(false, "new")
	t.Logf("输入=discard new 实际=%v 判定=%v", err, ErrUntrackedNeedsForce)
	if !errors.Is(err, ErrUntrackedNeedsForce) {
		t.Fatal(err)
	}
	if _, exists, _ := engine.WorktreePath("new"); !exists {
		t.Fatal("rejected discard removed worktree file")
	}
	if err := engine.Discard(true, "new"); err != nil {
		t.Fatal(err)
	}
	_, exists, _ := engine.WorktreePath("new")
	t.Logf("输入=force discard new 实际=exists:%t 判定=false", exists)
	if exists {
		t.Fatal("forced discard did not remove untracked file")
	}
}

func TestConflictResolutionAndCommitRules(t *testing.T) {
	engine, err := NewEngine(Snapshot{"a": []byte("base")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, engine, "a", []byte("resolved-work"))
	if err := engine.BeginMerge(map[string]Stages{
		"a":      {Base: []byte("base"), Ours: []byte("ours"), Theirs: []byte("theirs")},
		"delete": {Base: []byte("base"), Ours: []byte("ours"), Theirs: []byte("theirs")},
	}); err != nil {
		t.Fatal(err)
	}

	err = engine.Unstage("a")
	t.Logf("输入=unstage conflict 实际=%v 判定=%v", err, ErrConflictUnstage)
	if !errors.Is(err, ErrConflictUnstage) {
		t.Fatal(err)
	}
	_, err = engine.Commit(false)
	t.Logf("输入=commit during conflict 实际=%v 判定=%v", err, ErrUnresolvedConflicts)
	if !errors.Is(err, ErrUnresolvedConflicts) {
		t.Fatal(err)
	}
	if got, want := engine.UnresolvedConflicts(), []string{"a", "delete"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("实际=%v 判定=%v", got, want)
	}

	if err := engine.Stage("a"); err != nil {
		t.Fatal(err)
	}
	entry, _ := engine.Status("a")
	t.Logf("输入=stage conflict worktree 实际=%s 判定=已暂存修改", entry.Status)
	if entry.Status == StatusConflict {
		t.Fatal("staging did not resolve conflict")
	}

	if err := engine.Stage("delete"); err != nil {
		t.Fatal(err)
	}
	_, indexExists, _ := engine.IndexPath("delete")
	t.Logf("输入=stage missing conflict 实际=index:%t 判定=false", indexExists)
	if indexExists {
		t.Fatal("conflict was not resolved as deletion")
	}

	_, err = engine.Commit(false)
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.Commit(false)
	t.Logf("输入=commit unchanged 实际=%v 判定=%v", err, ErrNothingToCommit)
	if !errors.Is(err, ErrNothingToCommit) {
		t.Fatal(err)
	}
	id, err := engine.Commit(true)
	t.Logf("输入=commit --allow-empty 实际=%d,%v 判定=2,<nil>", id, err)
	if err != nil || id != 2 {
		t.Fatalf("id=%d err=%v", id, err)
	}
}

func TestDiscardRestoresIndexContent(t *testing.T) {
	engine, err := NewEngine(Snapshot{"a": []byte("base")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, engine, "a", []byte("staged"))
	if err := engine.Stage("a"); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, engine, "a", []byte("work"))
	if err := engine.Discard(false, "a"); err != nil {
		t.Fatal(err)
	}
	content, exists, _ := engine.WorktreePath("a")
	t.Logf("输入=discard a 实际=%s,%t 判定=staged,true", content, exists)
	if !exists || !bytes.Equal(content, []byte("staged")) {
		t.Fatal("discard did not restore index content")
	}
}

func TestCrossStoreAncestorConflictRejected(t *testing.T) {
	engine, err := NewEngine(Snapshot{"a": []byte("base")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, engine, "dir", []byte("dir"))
	if err := engine.Stage("dir"); err != nil {
		t.Fatal(err)
	}
	err = engine.WriteWorktree("a/child", []byte("child"))
	t.Logf("输入=a/child against file a 实际=%v 判定=%v", err, ErrInvalidPath)
	if !errors.Is(err, ErrInvalidPath) {
		t.Fatal(err)
	}
	err = engine.WriteWorktree("dir/child", []byte("child"))
	t.Logf("输入=dir/child against staged dir 实际=%v 判定=%v", err, ErrInvalidPath)
	if !errors.Is(err, ErrInvalidPath) {
		t.Fatal(err)
	}
}
