package dimcache

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
)

func newTestManager(t *testing.T, maxKeys int) *Manager {
	t.Helper()
	var buf bytes.Buffer
	m := New(maxKeys, NewLogger(&buf))
	t.Cleanup(func() { t.Logf("manager log:\n%s", buf.String()) })
	return m
}

func mustUpdate(t *testing.T, m *Manager, key, value string) int64 {
	t.Helper()
	v, err := m.Update(key, value)
	if err != nil {
		t.Fatalf("Update(%q) unexpected error: %v", key, err)
	}
	return v
}

func drain(t *testing.T, m *Manager) {
	t.Helper()
	for {
		ok, err := m.DeliverNext()
		if err != nil {
			t.Fatalf("DeliverNext unexpected error: %v", err)
		}
		if !ok {
			return
		}
	}
}

// TestBackfillAcceptAndNegativeCache 验证正常回填与负缓存写入。
func TestBackfillAcceptAndNegativeCache(t *testing.T) {
	m := newTestManager(t, 10)
	v := mustUpdate(t, m, "k1", "alice")

	tok, tv, err := m.BeginRead("k1")
	if err != nil || tv != v {
		t.Fatalf("BeginRead = %q,%d,%v; want version %d", tok, tv, err, v)
	}
	if err := m.Backfill(tok); err != nil {
		t.Fatalf("Backfill unexpected error: %v", err)
	}
	entry, hit := m.CachedEntry("k1")
	if !hit || entry.Deleted || entry.Value != "alice" || entry.Version != v {
		t.Fatalf("cache entry = %+v hit=%v, want positive@%d", entry, hit, v)
	}

	// 不存在的键：源头版本 0，令牌回填后成为负缓存。
	tok2, tv0, err := m.BeginRead("missing")
	if err != nil || tv0 != 0 {
		t.Fatalf("BeginRead missing = %q,%d,%v", tok2, tv0, err)
	}
	if err := m.Backfill(tok2); err != nil {
		t.Fatalf("Backfill negative: %v", err)
	}
	neg, hit := m.CachedEntry("missing")
	if !hit || !neg.Deleted {
		t.Fatalf("negative cache = %+v hit=%v, want tombstone", neg, hit)
	}

	if got := m.SourceReads(); got != 2 {
		t.Fatalf("SourceReads=%d, want 2", got)
	}

	// 命中正/负缓存都不再回源。
	if val, found, err := m.Query("k1"); err != nil || !found || val != "alice" {
		t.Fatalf("Query k1 = %q,%v,%v", val, found, err)
	}
	if val, found, err := m.Query("missing"); err != nil || found || val != "" {
		t.Fatalf("Query missing = %q,%v,%v, want not found", val, found, err)
	}
	if got := m.SourceReads(); got != 2 {
		t.Fatalf("SourceReads=%d after cache hits, want 2", got)
	}
}

// TestBackfillRejectedStale 验证令牌版本低于栅栏时回填被拒，且缓存不留痕。
func TestBackfillRejectedStale(t *testing.T) {
	m := newTestManager(t, 10)
	v1 := mustUpdate(t, m, "k1", "v1")

	// 第一步读取拿到 v1 令牌（读取与失效之后交错）。
	tok, _, err := m.BeginRead("k1")
	if err != nil {
		t.Fatal(err)
	}

	// v2 更新及其事件先到，栅栏推进到 v2。
	v2 := mustUpdate(t, m, "k1", "v2")
	if err := m.Emit(Event{Key: "k1", Version: v2, Value: "v2"}); err != nil {
		t.Fatal(err)
	}
	if ok, err := m.DeliverNext(); err != nil || !ok {
		t.Fatalf("DeliverNext = %v,%v", ok, err)
	}
	if f := m.Fence("k1"); f != v2 {
		t.Fatalf("fence=%d, want %d", f, v2)
	}

	// 旧令牌回填：v1 < 栅栏 v2，必须拒绝，且不得产生缓存条目。
	if err := m.Backfill(tok); !errors.Is(err, ErrTokenStale) {
		t.Fatalf("Backfill err=%v, want ErrTokenStale", err)
	}
	if _, hit := m.CachedEntry("k1"); hit {
		t.Fatal("stale backfill must not create a cache entry")
	}

	// 被拒不留痕：令牌仍处于 pending，重试得到同样的拒绝原因。
	if err := m.Backfill(tok); !errors.Is(err, ErrTokenStale) {
		t.Fatalf("retry stale token err=%v, want ErrTokenStale", err)
	}
	if err := m.Backfill("tok-does-not-exist"); !errors.Is(err, ErrUnknownToken) {
		t.Fatalf("unknown token err=%v, want ErrUnknownToken", err)
	}

	// 全新令牌版本 v2 == 栅栏 v2：版本相等边界，回填成功。
	tok2, tv2, err := m.BeginRead("k1")
	if err != nil || tv2 != v2 {
		t.Fatalf("BeginRead = %q,%d,%v", tok2, tv2, err)
	}
	if err := m.Backfill(tok2); err != nil {
		t.Fatalf("Backfill at equal version: %v", err)
	}
	// 成功消费后令牌进入已用集合：“已用”与“未知”给出不同原因。
	if err := m.Backfill(tok2); !errors.Is(err, ErrTokenUsed) {
		t.Fatalf("reuse consumed token err=%v, want ErrTokenUsed", err)
	}
	entry, hit := m.CachedEntry("k1")
	if !hit || entry.Version != v2 || entry.Value != "v2" {
		t.Fatalf("entry=%+v hit=%v, want v2@%d", entry, hit, v2)
	}
	_ = v1
}

// TestDeleteEventInvalidation 验证删除事件使正缓存失效并可形成负缓存。
func TestDeleteEventInvalidation(t *testing.T) {
	m := newTestManager(t, 10)
	v1 := mustUpdate(t, m, "k1", "alice")
	if err := m.Emit(Event{Key: "k1", Version: v1, Value: "alice"}); err != nil {
		t.Fatal(err)
	}
	drain(t, m)

	tok, _, _ := m.BeginRead("k1")
	if err := m.Backfill(tok); err != nil {
		t.Fatal(err)
	}

	// 源头删除产生墓碑 v2（版本继续递增），删除事件到达后正缓存必须被清除。
	v2, err := m.Delete("k1")
	if err != nil || v2 != v1+1 {
		t.Fatalf("Delete = %d,%v; want %d", v2, err, v1+1)
	}
	if err := m.Emit(Event{Key: "k1", Version: v2, Deleted: true}); err != nil {
		t.Fatal(err)
	}
	drain(t, m)
	if f := m.Fence("k1"); f != v2 {
		t.Fatalf("fence=%d want %d", f, v2)
	}
	if _, hit := m.CachedEntry("k1"); hit {
		t.Fatal("positive cache must be invalidated by delete event")
	}

	// 查询回源得到 not-found 并写入 v2 负缓存。
	if val, found, err := m.Query("k1"); err != nil || found || val != "" {
		t.Fatalf("Query deleted key = %q,%v,%v", val, found, err)
	}
	neg, hit := m.CachedEntry("k1")
	if !hit || !neg.Deleted || neg.Version != v2 {
		t.Fatalf("negative entry=%+v hit=%v want tombstone@%d", neg, hit, v2)
	}

	// 复活更新 v3 的事件到达后，版本过旧的负缓存被清除。
	v3 := mustUpdate(t, m, "k1", "bob")
	if err := m.Emit(Event{Key: "k1", Version: v3, Value: "bob"}); err != nil {
		t.Fatal(err)
	}
	drain(t, m)
	if _, hit := m.CachedEntry("k1"); hit {
		t.Fatal("negative cache must be invalidated by newer event")
	}

	// 删除不存在 / 已删除的行被整体拒绝。
	if _, err := m.Delete("ghost"); !errors.Is(err, ErrDeleteMissing) {
		t.Fatalf("Delete ghost err=%v want ErrDeleteMissing", err)
	}
	v4, err := m.Delete("k1")
	if err != nil || v4 != v3+1 {
		t.Fatalf("Delete live = %d,%v", v4, err)
	}
	if _, err := m.Delete("k1"); !errors.Is(err, ErrDeleteMissing) {
		t.Fatalf("Delete tombstoned err=%v want ErrDeleteMissing", err)
	}
}

// TestFenceVersionBoundary 验证版本相等（重复）与更旧事件被忽略。
func TestFenceVersionBoundary(t *testing.T) {
	m := newTestManager(t, 10)
	v1 := mustUpdate(t, m, "k", "a")
	v2 := mustUpdate(t, m, "k", "b")

	// 先到 v2 事件。
	if err := m.Emit(Event{Key: "k", Version: v2, Value: "b"}); err != nil {
		t.Fatal(err)
	}
	drain(t, m)
	if f := m.Fence("k"); f != v2 {
		t.Fatalf("fence=%d want %d", f, v2)
	}

	// 延迟到达的 v1 事件：更旧，不做任何事。
	if err := m.Emit(Event{Key: "k", Version: v1, Value: "a"}); err != nil {
		t.Fatal(err)
	}
	drain(t, m)
	if f := m.Fence("k"); f != v2 {
		t.Fatalf("fence changed to %d after stale event", f)
	}

	// 版本相等的重复事件：同样不做任何事，同版本缓存条目保持不动。
	m.mu.Lock()
	m.cache["k"] = cacheRow{version: v2, value: "b"}
	m.mu.Unlock()
	m.applyEventLocked(Event{Key: "k", Version: v2, Value: "b"})
	if f := m.Fence("k"); f != v2 {
		t.Fatalf("fence changed to %d after duplicate event", f)
	}
	if e, hit := m.CachedEntry("k"); !hit || e.Version != v2 {
		t.Fatalf("entry with version == fence must be kept, got %+v %v", e, hit)
	}
}

// TestRejectionsLeaveNoTrace 校验非法输入的互异错误类别与拒绝后状态不变。
func TestRejectionsLeaveNoTrace(t *testing.T) {
	m := newTestManager(t, 2)
	mustUpdate(t, m, "a", "1")
	mustUpdate(t, m, "b", "2")

	snapshot := func() string {
		m.mu.Lock()
		defer m.mu.Unlock()
		return fmt.Sprintf("source=%d queue=%d fence_a=%d cache=%d reads=%d pendingTok=%d usedTok=%d",
			len(m.source), len(m.queue), m.fences["a"], len(m.cache), m.sourceReads,
			len(m.pendingTokens), len(m.usedTokens))
	}

	check := func(name string, want error, fn func() error) {
		t.Helper()
		before := snapshot()
		err := fn()
		if !errors.Is(err, want) {
			t.Fatalf("%s: err=%v want %v", name, err, want)
		}
		if after := snapshot(); after != before {
			t.Fatalf("%s: state changed %s -> %s after rejection", name, before, after)
		}
	}

	check("Update empty", ErrEmptyKey, func() error {
		_, e := m.Update("", "x")
		return e
	})
	check("Delete empty", ErrEmptyKey, func() error {
		_, e := m.Delete("")
		return e
	})
	check("Delete missing", ErrDeleteMissing, func() error {
		_, e := m.Delete("nope")
		return e
	})
	check("BeginRead empty", ErrEmptyKey, func() error {
		_, _, e := m.BeginRead("")
		return e
	})
	check("Query empty", ErrEmptyKey, func() error {
		_, _, e := m.Query("")
		return e
	})
	check("DirectQuery empty", ErrEmptyKey, func() error {
		_, _, e := m.DirectQuery("")
		return e
	})
	check("Emit empty key", ErrEmptyKey, func() error {
		return m.Emit(Event{Key: "", Version: 1})
	})
	check("Emit bad version", ErrInvalidVersion, func() error {
		return m.Emit(Event{Key: "a", Version: 0})
	})
	check("Emit tombstone with value", ErrInvalidTombstone, func() error {
		return m.Emit(Event{Key: "a", Version: 3, Deleted: true, Value: "x"})
	})
	check("Backfill unknown", ErrUnknownToken, func() error {
		return m.Backfill("tok-nope")
	})

	// 已用令牌与未知令牌互异；重复回填已用令牌状态不变。
	tok, _, _ := m.BeginRead("a")
	if err := m.Backfill(tok); err != nil {
		t.Fatal(err)
	}
	check("Backfill used", ErrTokenUsed, func() error {
		return m.Backfill(tok)
	})

	// 跟踪键数超限：占满两键后回填第三个键必须拒绝，栅栏/缓存/队列不变。
	tokB, _, _ := m.BeginRead("b")
	if err := m.Backfill(tokB); err != nil {
		t.Fatal(err)
	}
	tokC, _, _ := m.BeginRead("c")
	before := snapshot()
	err := m.Backfill(tokC)
	if !errors.Is(err, ErrTooManyTrackedKeys) {
		t.Fatalf("Backfill over limit err=%v want ErrTooManyTrackedKeys", err)
	}
	if after := snapshot(); after != before {
		t.Fatalf("rejected over-limit backfill changed state: %s -> %s", before, after)
	}
	if len(m.cache) != 2 {
		t.Fatalf("cache size=%d want 2 after rejected backfill", len(m.cache))
	}
	if m.Fence("c") != 0 {
		t.Fatalf("fence for rejected key = %d want 0", m.Fence("c"))
	}

	// 全部错误哨兵两两不同。
	sentinels := []error{
		ErrEmptyKey, ErrDeleteMissing, ErrUnknownToken, ErrTokenUsed,
		ErrTokenStale, ErrTooManyTrackedKeys, ErrInvalidVersion, ErrInvalidTombstone,
	}
	for i := range sentinels {
		for j := i + 1; j < len(sentinels); j++ {
			if sentinels[i] == sentinels[j] {
				t.Fatalf("sentinels %d and %d are identical", i, j)
			}
		}
	}
}
