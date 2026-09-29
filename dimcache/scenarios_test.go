package dimcache

import (
	"context"
	"testing"
)

// 回填接受：两步读取后缓存命中，内容与版本和源头一致；负缓存同样可写入。
func TestBackfillAcceptAndNegativeEntry(t *testing.T) {
	c, src, _ := newSystem(t, 0)
	ctx := context.Background()
	if err := src.Update(ctx, "k1", "v1"); err != nil {
		t.Fatal(err)
	}

	tok := issue(t, c, "k1")
	if c.PendingTokens() != 1 {
		t.Fatalf("pending before backfill = %d, want 1", c.PendingTokens())
	}
	if err := c.Backfill(ctx, tok); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if got, ok, _ := c.Query(ctx, "k1"); !ok || got.Value != "v1" || got.Version != 1 {
		t.Fatalf("query = %+v ok=%v", got, ok)
	}

	// 从未存在过的键：源头版本 0，负缓存条目版本 0。
	tok2 := issue(t, c, "ghost")
	if err := c.Backfill(ctx, tok2); err != nil {
		t.Fatalf("negative backfill: %v", err)
	}
	if got, ok, _ := c.Query(ctx, "ghost"); !ok || !got.Negative || got.Version != 0 {
		t.Fatalf("negative entry = %+v ok=%v", got, ok)
	}
}

// 回填拒绝：读取与失效交错，令牌版本低于回填下限时拒绝且不留痕；
// 版本相等（token.Version == fence）属于边界，必须接受。
func TestBackfillStaleRejectAndVersionEqualBoundary(t *testing.T) {
	c, src, _ := newSystem(t, 0)
	ctx := context.Background()
	if err := src.Update(ctx, "k", "old"); err != nil {
		t.Fatal(err)
	}

	// 第一步读取拿到 v1 令牌。
	stale := issue(t, c, "k")
	readsAfterIssue := c.SourceReads()

	// 源头继续更新到 v2，且 v2 的 CDC 事件先到达：栅栏推进到 2。
	if err := src.Update(ctx, "k", "new"); err != nil {
		t.Fatal(err)
	}
	if err := c.Deliver(Event{Key: "k", Version: 2, Kind: EventUpsert, Value: "new"}); err != nil {
		t.Fatal(err)
	}
	if c.Fence("k") != 2 {
		t.Fatalf("fence = %d, want 2", c.Fence("k"))
	}

	// v1 回填被下限 2 拒绝；不产生条目、栅栏不动、令牌被回收、读源头计数不变。
	wantErr(t, c.Backfill(ctx, stale), ErrTokenStale)
	if _, ok := c.SnapshotEntry("k"); ok {
		t.Fatal("stale backfill must not create a cache entry")
	}
	if c.Fence("k") != 2 {
		t.Fatalf("fence changed after reject: %d", c.Fence("k"))
	}
	if c.SourceReads() != readsAfterIssue {
		t.Fatalf("source reads changed after reject: %d", c.SourceReads())
	}

	// 版本相等边界：重新读取 v2，令牌版本 == 栅栏 2，必须接受（负缓存亦可）。
	eq := issue(t, c, "k")
	if eq.Version != 2 {
		t.Fatalf("token version = %d, want 2", eq.Version)
	}
	if err := c.Backfill(ctx, eq); err != nil {
		t.Fatalf("equal-version backfill must be accepted: %v", err)
	}
	assertEntryMatchesSource(t, c, src, "k")
}

// 删除事件：墓碑推进栅栏、驱逐旧条目；墓碑后的负缓存回填以及重复删除版本递增。
func TestDeleteEventTombstone(t *testing.T) {
	c, src, _ := newSystem(t, 0)
	ctx := context.Background()
	if err := src.Update(ctx, "k", "v1"); err != nil {
		t.Fatal(err)
	}
	if err := c.Backfill(ctx, issue(t, c, "k")); err != nil {
		t.Fatal(err)
	}

	if err := src.Delete(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	if err := c.Deliver(Event{Key: "k", Version: 2, Kind: EventDelete}); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.SnapshotEntry("k"); ok {
		t.Fatal("delete event must evict the old entry")
	}
	if c.Fence("k") != 2 {
		t.Fatalf("fence = %d, want 2", c.Fence("k"))
	}

	// 回填墓碑状态 -> 负缓存。
	if err := c.Backfill(ctx, issue(t, c, "k")); err != nil {
		t.Fatal(err)
	}
	if got, ok, _ := c.Query(ctx, "k"); !ok || !got.Negative || got.Version != 2 {
		t.Fatalf("tombstone negative entry = %+v ok=%v", got, ok)
	}

	// 重复删除墓碑：源头版本继续递增到 3，删除不存在的键才报错。
	if err := src.Delete(ctx, "k"); err != nil {
		t.Fatalf("re-delete tombstone must increment version: %v", err)
	}
	wantErr(t, src.Delete(ctx, "never"), ErrDeleteMissing)
}

// 延迟、乱序、重复事件：更旧/重复事件不做任何事，栅栏只随更大版本前进。
func TestOutOfOrderDuplicateAndLateEvents(t *testing.T) {
	c, _, _ := newSystem(t, 0)
	ctx := context.Background()

	deliver := func(e Event) {
		if err := c.Deliver(e); err != nil {
			t.Fatal(err)
		}
	}
	deliver(Event{Key: "k", Version: 3, Kind: EventUpsert, Value: "v3"})
	deliver(Event{Key: "k", Version: 1, Kind: EventUpsert, Value: "v1"}) // 迟到更旧
	if c.Fence("k") != 3 {
		t.Fatalf("fence after late event = %d, want 3", c.Fence("k"))
	}
	deliver(Event{Key: "k", Version: 3, Kind: EventUpsert, Value: "v3"}) // 重复
	if c.Fence("k") != 3 {
		t.Fatalf("fence after duplicate = %d, want 3", c.Fence("k"))
	}
	deliver(Event{Key: "k", Version: 4, Kind: EventDelete})
	if c.Fence("k") != 4 {
		t.Fatalf("fence = %d, want 4", c.Fence("k"))
	}

	// 队列层面同样允许延迟与乱序，FIFO 只是投递顺序。
	if err := c.queue.Enqueue(Event{Key: "k", Version: 9}); err != nil {
		t.Fatal(err)
	}
	if c.queue.Len() != 1 {
		t.Fatalf("queue len = %d", c.queue.Len())
	}
	_ = ctx
}

// 非法输入错误类别互不相同，且每次拒绝都不改变任何状态。
func TestRejectionReasonsLeaveNoTrace(t *testing.T) {
	c, src, q := newSystem(t, 2)
	ctx := context.Background()

	snapshot := func() string {
		return fmtState(src, q, c)
	}

	if err := src.Update(ctx, "a", "1"); err != nil {
		t.Fatal(err)
	}
	if err := src.Update(ctx, "b", "2"); err != nil {
		t.Fatal(err)
	}
	// 让缓存系统跟踪 a、b（两张一次性令牌）。
	if err := c.Backfill(ctx, issue(t, c, "a")); err != nil {
		t.Fatal(err)
	}
	if err := c.Backfill(ctx, issue(t, c, "b")); err != nil {
		t.Fatal(err)
	}

	// 1) 空键：源头更新/删除、查询、签发、投递、入队全部拒绝。
	before := snapshot()
	for _, try := range []func() error{
		func() error { return src.Update(ctx, "", "x") },
		func() error { return src.Delete(ctx, "") },
		func() error { _, _, err := c.Query(ctx, ""); return err },
		func() error { _, err := c.IssueReadToken(ctx, ""); return err },
		func() error { return c.Deliver(Event{Key: "", Version: 1}) },
		func() error { return q.Enqueue(Event{Key: "", Version: 1}) },
	} {
		wantErr(t, try(), ErrEmptyKey)
	}
	if snapshot() != before {
		t.Fatalf("state changed after empty-key rejections:\nbefore=%s\nafter =%s", before, snapshot())
	}

	// 2) 删除不存在的行。
	wantErr(t, src.Delete(ctx, "ghost"), ErrDeleteMissing)
	if snapshot() != before {
		t.Fatal("state changed after delete-missing rejection")
	}

	// 3) 跟踪键数超限：缓存系统尚未见过键 "c"，投递与签发全部拒绝。
	before = snapshot()
	wantErr(t, c.Deliver(Event{Key: "c", Version: 1}), ErrTrackedKeysExceeded)
	_, err := c.IssueReadToken(ctx, "c")
	wantErr(t, err, ErrTrackedKeysExceeded)
	if snapshot() != before {
		t.Fatal("state changed after tracked-limit rejections")
	}

	// 4) 未知令牌（伪造 ID）与 nil。
	before = snapshot()
	wantErr(t, c.Backfill(ctx, &Token{ID: 999, Key: "a", Version: 1}), ErrTokenUnknown)
	wantErr(t, c.Backfill(ctx, nil), ErrTokenUnknown)
	if snapshot() != before {
		t.Fatalf("state changed after unknown-token rejections:\nbefore=%s\nafter =%s", before, snapshot())
	}

	// 5) 已用令牌：正常回填后重复使用。
	tok := issue(t, c, "a")
	readsAfterUse := c.SourceReads()
	if err := c.Backfill(ctx, tok); err != nil {
		t.Fatal(err)
	}
	entryAfterUse, _ := c.SnapshotEntry("a")
	wantErr(t, c.Backfill(ctx, tok), ErrTokenUsed)
	if got, _ := c.SnapshotEntry("a"); got != entryAfterUse {
		t.Fatal("cache entry changed after token-used rejection")
	}
	if c.SourceReads() != readsAfterUse {
		t.Fatal("source read count changed during backfill/reject")
	}

	// 错误哨兵两两不同。
	errs := []error{ErrEmptyKey, ErrTokenUnknown, ErrTokenUsed, ErrDeleteMissing, ErrTrackedKeysExceeded, ErrTokenStale}
	for i := range errs {
		for j := i + 1; j < len(errs); j++ {
			if errs[i] == errs[j] {
				t.Fatalf("error reasons must be distinct: %v == %v", errs[i], errs[j])
			}
		}
	}
}
