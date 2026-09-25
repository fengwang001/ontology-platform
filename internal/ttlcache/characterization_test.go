package ttlcache

import "testing"

// 本文件为 characterization tests：钉住当前实现的真实行为，
// 断言不代表期望行为。对应 FINDINGS.md 中的逐条分析。

// 边界一：长短 TTL 交错时，evict 按 createdAt 最早（写入最早）驱逐，
// 而不是按 expireAt 最早（过期最早）驱逐。
// early 写入早但 TTL 长（过期晚），late 写入晚但 TTL 短（过期早）；
// 两者均已过期时，被驱逐的是 early——尽管 late 已过期更久。
func TestCharEvictEarliestCreatedNotEarliestExpire(t *testing.T) {
	cases := []struct {
		name              string
		longTTL, shortTTL int64
		writeGap          int64 // late 的写入时刻
		advanceTo         int64 // 触发驱逐前的时刻（两者均已过期）
	}{
		{"小跨度", 100, 10, 50, 100},
		{"大跨度", 1000, 5, 900, 1000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clk := &clock{}
			c := newCache(t, 2, clk)
			mustPut(t, c, "early", "long", tc.longTTL) // createdAt=0，expireAt=longTTL（过期晚）
			clk.Advance(tc.writeGap)
			mustPut(t, c, "late", "short", tc.shortTTL) // createdAt=writeGap，expireAt 更小（过期早）
			clk.Advance(tc.advanceTo - tc.writeGap)     // 两者均已过期
			mustPut(t, c, "new", "n", 10000)            // 触发驱逐

			// 真实行为：驱逐 createdAt 最早的 early；
			// late 虽已过期更久（expireAt 更小），仍被保留。
			if c.Delete("early") {
				t.Error("early 仍在缓存；当前实现按 createdAt 最早驱逐，early 应被驱逐")
			}
			if !c.Delete("late") {
				t.Error("late 被驱逐；当前实现按 createdAt 最早驱逐，late 应被保留")
			}
		})
	}
}

// 边界二：重复 Put 已存在的键不刷新 TTL，过期时刻仍以首次写入为准。
// 无论在首写后多久、用多大的新 TTL 重 Put，该键仍在首写 expireAt 到点过期。
func TestCharReputDoesNotExtendLife(t *testing.T) {
	cases := []struct {
		name     string
		reputAt  int64 // 重 Put 的时刻
		reputTTL int64 // 重 Put 携带的 TTL（被忽略）
	}{
		{"早刷新_小TTL", 1, 1},
		{"中途刷新_大TTL", 5, 1000},
		{"临期刷新_大TTL", 9, 1000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clk := &clock{}
			c := newCache(t, 2, clk)
			mustPut(t, c, "a", "old", 10) // expireAt=10
			clk.Advance(tc.reputAt)
			mustPut(t, c, "a", "new", tc.reputTTL) // 只更新值并提升 recency，不刷新 TTL
			clk.Advance(10 - tc.reputAt)           // 时刻 10：按首次写入已过期

			if _, ok := c.Get("a"); ok {
				t.Error("Get(a) 在时刻 10 命中；重复 Put 不延寿，应按首次写入时刻过期")
			}
			if got := c.Len(); got != 0 {
				t.Errorf("Len() = %d, want 0（过期 Get 已清理）", got)
			}
		})
	}
}

// 边界三：惰性单条驱逐。多个过期项可长期共存并占用容量（Len 计数），
// 后续每次 Put 只清理一个过期项，需多次 Put 才能逐出全部过期项。
func TestCharLazySingleEviction(t *testing.T) {
	clk := &clock{}
	c := newCache(t, 3, clk)
	mustPut(t, c, "a", "1", 10)
	mustPut(t, c, "b", "2", 10)
	mustPut(t, c, "c", "3", 10)

	clk.Advance(10) // 全部过期，但没有任何操作触发清理
	if got := c.Len(); got != 3 {
		t.Fatalf("Len() = %d, want 3（过期项仍占用容量）", got)
	}

	// 每次 Put 只驱逐一个过期项：Len 保持 3，其余过期项继续占位。
	for i, key := range []string{"d", "e", "f"} {
		mustPut(t, c, key, "v", 1000)
		if got := c.Len(); got != 3 {
			t.Fatalf("第 %d 次 Put 后 Len() = %d, want 3（每次 Put 只驱逐一个过期项）", i+1, got)
		}
	}

	// 三次 Put 各清理一个过期项后，a/b/c 才被全部逐出。
	for _, key := range []string{"a", "b", "c"} {
		if c.Delete(key) {
			t.Errorf("Delete(%q) = true；三次 Put 后该过期项应已被逐个驱逐", key)
		}
	}
}

// 边界四：同 createdAt 的多个过期项，evict 用严格小于比较、头→尾扫描，
// 保留先遇到的头侧（MRU）项——即最近使用的过期项反而先被驱逐。
func TestCharTieBreakEvictsMRUSide(t *testing.T) {
	cases := []struct {
		name    string
		reorder func(t *testing.T, c *Cache) // 可选：过期前调整 recency
		evicted string                       // 同刻冲突下实际被驱逐的键
		kept    string
	}{
		{"天然顺序_头侧b被驱逐", nil, "b", "a"},
		{"Get提升a后_a被驱逐", func(t *testing.T, c *Cache) {
			t.Helper()
			if _, ok := c.Get("a"); !ok {
				t.Fatal("Get(a) = miss, want hit")
			}
		}, "a", "b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clk := &clock{}
			c := newCache(t, 2, clk)
			mustPut(t, c, "a", "1", 10) // createdAt=0
			mustPut(t, c, "b", "2", 10) // createdAt=0（同一逻辑时刻）
			if tc.reorder != nil {
				tc.reorder(t, c)
			}
			clk.Advance(10) // 两者同时过期
			mustPut(t, c, "c", "3", 1000)

			// 真实行为：头侧（MRU）的过期项被驱逐，尾侧（LRU）的保留——与 LRU 直觉相反。
			if c.Delete(tc.evicted) {
				t.Errorf("%q 仍在缓存；同 createdAt 时应驱逐头侧（MRU）的 %q", tc.evicted, tc.evicted)
			}
			if !c.Delete(tc.kept) {
				t.Errorf("%q 被驱逐；同 createdAt 时应保留尾侧（LRU）的 %q", tc.kept, tc.kept)
			}
		})
	}
}

// 边界五：同一个过期项在三个入口下的口径不一致——
// Get 视为不存在（miss 并立即删除）、Delete 视为存在（返回 true）、Len 计入总数。
func TestCharExpiredEntryAcrossEntryPoints(t *testing.T) {
	cases := []struct {
		name       string
		op         func(c *Cache) bool // Get 的 ok / Delete 的返回值 / Len 是否计入
		wantRet    bool
		wantLenAft int // 操作后的 Len
	}{
		{"Get按miss处理并删除", func(c *Cache) bool { _, ok := c.Get("a"); return ok }, false, 0},
		{"Delete返回true并删除", func(c *Cache) bool { return c.Delete("a") }, true, 0},
		{"Len计入过期项", func(c *Cache) bool { return c.Len() == 1 }, true, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clk := &clock{}
			c := newCache(t, 2, clk)
			mustPut(t, c, "a", "1", 10)
			clk.Advance(10) // a 已过期、尚未清理

			if got := tc.op(c); got != tc.wantRet {
				t.Errorf("过期项上 %s 返回 %v, want %v", tc.name, got, tc.wantRet)
			}
			if got := c.Len(); got != tc.wantLenAft {
				t.Errorf("%s 后 Len() = %d, want %d", tc.name, got, tc.wantLenAft)
			}
		})
	}
}
