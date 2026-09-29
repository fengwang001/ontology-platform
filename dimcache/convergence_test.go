package dimcache

import (
	"context"
	"strings"
	"testing"
)

// 交错场景：更新、删除、延迟/乱序事件与两步读取持续交错；
// 队列清空且无未完成令牌后，任意有缓存条目的键必须与无缓存直读源头一致，且可复现。
func TestConvergenceMatchesReadThrough(t *testing.T) {
	run := func(seed int64) string {
		t.Run("seed", func(t *testing.T) {})
		c, src, q := newSystem(t, 0)
		ctx := context.Background()

		// 固定脚本（确定性，不依赖随机数）：
		//  1. 建 k1=v1, k2=v2，投递事件 v1
		//  2. 签发 k1 的 v1 令牌（尚未回填）
		//  3. k1 更新到 v2，v2 删除事件延迟，只先投递 k1-upsert-v2
		//  4. 此时回填 v1 令牌 -> 过期拒绝
		//  5. k2 升级到 v2 但事件延迟；先回填旧 v2 令牌前，事件到达边界相等 -> 接受
		//  6. k1 删除 v3，墓碑事件迟到；先用 v2 令牌回填，再到墓碑
		//  7. 清空队列，校验静止与一致性
		must(t, src.Update(ctx, "k1", "v1"))
		must(t, src.Update(ctx, "k2", "v2"))
		must(t, q.Enqueue(Event{Key: "k1", Version: 1, Kind: EventUpsert, Value: "v1"}))
		must(t, q.Enqueue(Event{Key: "k2", Version: 1, Kind: EventUpsert, Value: "v2"}))
		drainAll(t, c)

		oldK1 := issue(t, c, "k1") // v1 令牌，持有不回填

		must(t, src.Update(ctx, "k1", "v2"))
		must(t, q.Enqueue(Event{Key: "k1", Version: 2, Kind: EventUpsert, Value: "v2"}))
		drainAll(t, c)
		if err := c.Backfill(ctx, oldK1); !errorsIs(err, ErrTokenStale) {
			t.Fatalf("old token backfill: want ErrTokenStale, got %v", err)
		}

		must(t, src.Update(ctx, "k2", "v2b"))
		tokK2 := issue(t, c, "k2") // 读到 v2
		// 版本相等边界：k2 事件 v2 在回填前一刻到达，fence==2==token.Version。
		must(t, q.Enqueue(Event{Key: "k2", Version: 2, Kind: EventUpsert, Value: "v2b"}))
		drainAll(t, c)
		if err := c.Backfill(ctx, tokK2); err != nil {
			t.Fatalf("equal-floor backfill should be accepted: %v", err)
		}

		// k1 墓碑：先回填 v2 正缓存，再让迟到的删除事件驱逐，再回填负缓存。
		tokK1v2 := issue(t, c, "k1")
		must(t, c.Backfill(ctx, tokK1v2))
		must(t, src.Delete(ctx, "k1"))
		if e, ok := c.SnapshotEntry("k1"); !ok || e.Negative || e.Version != 2 {
			t.Fatalf("pre-tombstone entry = %+v ok=%v", e, ok)
		}
		must(t, q.Enqueue(Event{Key: "k1", Version: 3, Kind: EventDelete}))
		drainAll(t, c)
		if _, ok := c.SnapshotEntry("k1"); ok {
			t.Fatal("late tombstone must evict the entry")
		}
		must(t, c.Backfill(ctx, issue(t, c, "k1"))) // 负缓存 v3

		// 重复投递旧事件：无效果。
		must(t, c.Deliver(Event{Key: "k1", Version: 1, Kind: EventUpsert}))
		must(t, c.Deliver(Event{Key: "k1", Version: 3, Kind: EventDelete}))
		if c.Fence("k1") != 3 {
			t.Fatalf("fence = %d, want 3", c.Fence("k1"))
		}

		if !c.Quiescent() {
			t.Fatalf("not quiescent: queue=%d pending=%d", q.Len(), c.PendingTokens())
		}
		for _, key := range []string{"k1", "k2"} {
			assertEntryMatchesSource(t, c, src, key)
		}
		// 未跟踪、从无此键的键也不能出现错误条目。
		if _, ok := c.SnapshotEntry("unknown"); ok {
			t.Fatal("untracked key must not have an entry")
		}
		return c.stateDigest()
	}

	first := run(1)
	second := run(1)
	if first != second {
		t.Fatalf("non-reproducible final state:\n%s\nvs\n%s", first, second)
	}
}

// 日志中必须能看到每步输入、栅栏、缓存与判定依据。
func TestLogsShowFencesAndDecisions(t *testing.T) {
	logger := &testLogger{t: t}
	src := NewSource(logger)
	q := NewEventQueue(logger)
	c := NewCache(src, q, 0, logger)
	ctx := context.Background()
	must(t, src.Update(ctx, "k", "v"))
	must(t, c.Backfill(ctx, issueWith(c, t, "k")))
	must(t, c.Deliver(Event{Key: "k", Version: 2, Kind: EventDelete}))

	out := logger.text()
	for _, want := range []string{
		"issue.ok",        // 输入令牌
		"backfill.ok",     // 回填判定
		"fence",           // 栅栏
		"new_fence",       // 栅栏推进依据
		"cache.evict",     // 缓存驱逐
		"deliver.advance", // 事件判定
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("log missing %q:\n%s", want, out)
		}
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func errorsIs(err, target error) bool {
	if err == nil {
		return target == nil
	}
	return err == target
}

func drainAll(t *testing.T, c *Cache) {
	t.Helper()
	for {
		ok, err := c.DrainDeliver(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			return
		}
	}
}

func issueWith(c *Cache, t *testing.T, key string) *Token {
	t.Helper()
	tok, err := c.IssueReadToken(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}
