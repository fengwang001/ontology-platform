package engine

import (
	"errors"
	"fmt"
	"testing"
)

func mustSeq(t *testing.T, seq int64, err error, want int64) {
	t.Helper()
	if err != nil || seq != want {
		t.Fatalf("操作 seq=%d err=%v, 期望 seq=%d", seq, err, want)
	}
}

func mustDel(t *testing.T, e *Engine, id string, want int64) {
	t.Helper()
	seq, err := e.Delete(id)
	mustSeq(t, seq, err, want)
}

func mustIdx(t *testing.T, e *Engine, id string, body []byte, ifSeq, want int64) {
	t.Helper()
	seq, err := e.Index(id, body, ifSeq)
	mustSeq(t, seq, err, want)
}

func wantErr(t *testing.T, err error, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("err=%v, 期望包含 %v", err, target)
	}
}

func searchIDs(e *Engine) []string {
	docs, err := e.Search()
	if err != nil {
		panic(err)
	}
	ids := make([]string, 0, len(docs))
	for _, d := range docs {
		ids = append(ids, d.ID)
	}
	return ids
}

func getSeq(t *testing.T, e *Engine, id string) int64 {
	t.Helper()
	d, err := e.Get(id)
	if err != nil {
		t.Fatalf("Get(%s) err=%v", id, err)
	}
	return d.Seq
}

func bb(s string) []byte { return []byte(s) }

// 题例一（Async）：Flush 后未 Sync 的刷新数据在崩溃后丢失。
func TestSpecExampleAsync(t *testing.T) {
	e := New(Async)
	mustIdx(t, e, "a", bb("1"), -1, 1)
	mustIdx(t, e, "b", bb("2"), -1, 2)
	if err := e.Flush(); err != nil {
		t.Fatal(err)
	}
	if e.Committed() != 2 || e.Synced() != 2 {
		t.Fatalf("Flush 后 committed=%d synced=%d", e.Committed(), e.Synced())
	}
	mustIdx(t, e, "c", bb("3"), -1, 3)
	if err := e.Sync(); err != nil || e.Synced() != 3 {
		t.Fatalf("Sync 后 synced=%d err=%v", e.Synced(), err)
	}
	mustIdx(t, e, "d", bb("4"), -1, 4)
	if err := e.Refresh(); err != nil {
		t.Fatal(err)
	}
	if ids := searchIDs(e); fmt.Sprint(ids) != "[a b c d]" {
		t.Fatalf("Refresh 后 Search=%v", ids)
	}
	mustDel(t, e, "a", 5)
	if _, err := e.Get("a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(a) err=%v, 期望不存在", err)
	}
	if ids := searchIDs(e); fmt.Sprint(ids) != "[a b c d]" {
		t.Fatalf("未 Refresh, Search 不应反映删除: %v", ids)
	}

	if err := e.Crash(); err != nil {
		t.Fatal(err)
	}
	n, err := e.Recover()
	if err != nil || n != 1 {
		t.Fatalf("Recover n=%d err=%v, 期望重放 1 条", n, err)
	}
	if e.replayed != 1 {
		t.Fatalf("replayed=%d, 期望 1", e.replayed)
	}
	if ids := searchIDs(e); fmt.Sprint(ids) != "[a b c]" {
		t.Fatalf("恢复后 Search=%v, 期望 [a b c]", ids)
	}
	if _, err := e.Get("d"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(d) err=%v, 期望不存在", err)
	}
	if e.MaxSeq() != 3 || e.Synced() != 3 {
		t.Fatalf("恢复后 maxSeq=%d synced=%d, 期望 3,3", e.MaxSeq(), e.Synced())
	}
	mustIdx(t, e, "d", bb("4"), -1, 4) // 丢失序号被重新分配

	// 再 Crash/Recover：committed=2, synced=3，重放仍为 1，结果幂等
	if err := e.Crash(); err != nil {
		t.Fatal(err)
	}
	n, err = e.Recover()
	if err != nil || n != 1 {
		t.Fatalf("二次恢复 n=%d err=%v, 期望 1", n, err)
	}
	if ids := searchIDs(e); fmt.Sprint(ids) != "[a b c]" {
		t.Fatalf("二次恢复后 Search=%v, 期望 [a b c]", ids)
	}
	if e.MaxSeq() != 3 {
		t.Fatalf("二次恢复后 maxSeq=%d, 期望 3", e.MaxSeq())
	}
}

// 题例一（Request）：committed=2,synced=5，Recover 重放 3 条。
func TestSpecExampleRequest(t *testing.T) {
	e := New(Request)
	mustIdx(t, e, "a", bb("1"), -1, 1)
	mustIdx(t, e, "b", bb("2"), -1, 2)
	if err := e.Flush(); err != nil {
		t.Fatal(err)
	}
	mustIdx(t, e, "c", bb("3"), -1, 3)
	mustIdx(t, e, "d", bb("4"), -1, 4)
	mustDel(t, e, "a", 5)
	if err := e.Refresh(); err != nil {
		t.Fatal(err)
	}
	if e.Synced() != 5 {
		t.Fatalf("Request synced=%d, 期望 5", e.Synced())
	}
	if err := e.Crash(); err != nil {
		t.Fatal(err)
	}
	n, err := e.Recover()
	if err != nil || n != 3 {
		t.Fatalf("Request Recover n=%d err=%v, 期望 3", n, err)
	}
	if ids := searchIDs(e); fmt.Sprint(ids) != "[b c d]" {
		t.Fatalf("Request 恢复后 Search=%v, 期望 [b c d]", ids)
	}
	if e.MaxSeq() != 5 {
		t.Fatalf("恢复后 maxSeq=%d, 期望 5（下一个 6）", e.MaxSeq())
	}
	mustIdx(t, e, "e", bb("5"), -1, 6)
}

// 题例二：条件写按实时状态判定，未 Sync 全丢。
func TestSpecExampleConditional(t *testing.T) {
	e := New(Async)
	mustIdx(t, e, "a", bb("v1"), -1, 1)
	if err := e.Refresh(); err != nil {
		t.Fatal(err)
	}
	mustIdx(t, e, "a", bb("v2"), 1, 2)
	docs, _ := e.Search()
	if string(docs[0].Body) != "v1" {
		t.Fatalf("未 Refresh, Search 应仍为 v1, 得 %q", docs[0].Body)
	}
	if seq := getSeq(t, e, "a"); seq != 2 {
		t.Fatalf("Get(a) seq=%d, 期望 2", seq)
	}
	_, err := e.Index("a", bb("v3"), 1)
	wantErr(t, err, ErrConflict)
	mustDel(t, e, "a", 3)
	_, err = e.Index("a", bb("v4"), 3)
	wantErr(t, err, ErrConflict)
	if _, err := e.Get("a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("删除后 Get(a) err=%v", err)
	}
	mustIdx(t, e, "a", bb("v4"), -1, 4)

	if err := e.Crash(); err != nil {
		t.Fatal(err)
	}
	n, err := e.Recover()
	if err != nil || n != 0 {
		t.Fatalf("未 Sync, 重放=%d err=%v, 期望 0", n, err)
	}
	if _, err := e.Get("a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("恢复后 a 应不存在, err=%v", err)
	}
	if e.MaxSeq() != 0 {
		t.Fatalf("恢复后 maxSeq=%d, 期望 0（下一个 1）", e.MaxSeq())
	}
	mustIdx(t, e, "a", bb("v1"), -1, 1)
}
