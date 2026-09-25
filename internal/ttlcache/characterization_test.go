package ttlcache

import (
	"strconv"
	"testing"
)

// 本文件为 characterization tests（特征测试）：全部断言当前实现的真实行为
// （而非期望行为），用于钉住以下边界：
//  1. 过期项驱逐按 createdAt 还是 expireAt 排序；
//  2. 重复 Put 是否延长寿命；
//  3. 惰性单条驱逐下多个过期项长期占位；
//  4. createdAt 相同时的头侧（MRU）优先驱逐；
//  5. 过期项在 Get / Delete / Len 三个入口下的不同口径。
//
// 所有用例表驱动，并用循环遍历 TTL / 容量 / 过期形态，不展开成多个测试函数。

type charPut struct {
	at  int64 // 该次 Put 前把时钟设置到的绝对时刻
	key string
	val string
	ttl int64
}

func runCharPuts(t *testing.T, c *Cache, clk *clock, puts []charPut) {
	t.Helper()
	for _, p := range puts {
		clk.now = p.at
		mustPut(t, c, p.key, p.val, p.ttl)
	}
}

type evictSelectionCase struct {
	name             string
	capacity         int
	puts             []charPut
	triggerAt        int64 // 此时刻 Put 新键触发一次 evict
	evictedKeys      []string
	liveSurvivors    []string // 驱逐后 Get 仍命中的键
	expiredSurvivors []string // 驱逐后仍在表中、但已过期的键（用 Delete 探测，期望 true）
}

// 钉住 evict 的真实挑选谓词：在已过期项中选 createdAt 最小者，
// 而不是 expireAt 最小者。
func TestCharacterizeEvictSelection(t *testing.T) {
	var tests []evictSelectionCase

	// 形态一：长 / 短 TTL 交错，触发时两者都已过期。
	// a 写入最早（createdAt=0）但 TTL 长（过期晚）；
	// b 写入晚（createdAt=5）但 TTL 短（过期早）。
	// 用循环放大 TTL 尺度，行为不变：始终驱逐 createdAt 最早的 a。
	for _, factor := range []int64{1, 10, 100} {
		tests = append(tests, evictSelectionCase{
			name:     "earliest-created wins regardless of expireAt, factor " + strconv.FormatInt(factor, 10),
			capacity: 2,
			puts: []charPut{
				{0, "a", "va", 100 * factor},
				{5, "b", "vb", 10 * factor},
			},
			triggerAt:        100*factor + 5, // a 也已过期；b 早已过期
			evictedKeys:      []string{"a"},
			expiredSurvivors: []string{"b"},
		})
	}

	// 形态二：写入最早的项尚未过期，写入晚的项已过期 —— 只在过期项里挑。
	tests = append(tests, evictSelectionCase{
		name:     "earliest-created still live is not eligible",
		capacity: 2,
		puts: []charPut{
			{0, "a", "va", 50},
			{5, "b", "vb", 10},
		},
		triggerAt:     20,
		evictedKeys:   []string{"b"},
		liveSurvivors: []string{"a"},
	})

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clk := &clock{}
			c := newCache(t, tc.capacity, clk)
			runCharPuts(t, c, clk, tc.puts)

			clk.now = tc.triggerAt
			mustPut(t, c, "trigger", "v", 1000)

			// 已过期的幸存者只能用 Delete 探测（Get 会把它当场清理）。
			for _, key := range tc.evictedKeys {
				if c.Delete(key) {
					t.Errorf("Delete(%q) = true, want false (key should have been evicted)", key)
				}
			}
			for _, key := range tc.liveSurvivors {
				if _, ok := c.Get(key); !ok {
					t.Errorf("Get(%q) = miss, want hit (key should survive)", key)
				}
			}
			for _, key := range tc.expiredSurvivors {
				if !c.Delete(key) {
					t.Errorf("Delete(%q) = false, want true (expired key should still occupy the cache)", key)
				}
			}
			if _, ok := c.Get("trigger"); !ok {
				t.Error("Get(trigger) = miss, want hit")
			}
		})
	}
}

type reputCase struct {
	name       string
	reputAt    int64
	reputTTL   int64
	probeAt    int64 // 重 Put 之后的探测时刻
	wantHit    bool
	wantVal    string
	probeAgain int64 // 第二次探测时刻
}

// 钉住重复 Put 的真实行为：只改值 + moveToFront，createdAt/ttl 不变，
// 过期时刻永远以首次写入为准 —— 即使重 Put 时键已经过期也不续命。
func TestCharacterizeReputDoesNotExtendLife(t *testing.T) {
	var tests []reputCase

	// 形态一：到期前重 Put，无论新 TTL 给多大，仍在首次写入后 10ms 过期。
	for _, newTTL := range []int64{11, 100, 1000} {
		tests = append(tests, reputCase{
			name:       "reput before expiry with large ttl " + strconv.FormatInt(newTTL, 10),
			reputAt:    5,
			reputTTL:   newTTL,
			probeAt:    9,
			wantHit:    true,
			wantVal:    "new",
			probeAgain: 10,
		})
	}

	// 形态二：键已过期（但未被任何操作清理，仍在 map 中）后重 Put：
	// 走“已存在键”分支，只更新值，createdAt 仍是 0，立刻依旧过期。
	tests = append(tests, reputCase{
		name:       "reput after expiry does not revive",
		reputAt:    10,
		reputTTL:   1000,
		probeAt:    10,
		wantHit:    false,
		probeAgain: 10,
	})

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clk := &clock{}
			c := newCache(t, 2, clk)
			runCharPuts(t, c, clk, []charPut{{0, "a", "old", 10}})

			clk.now = tc.reputAt
			mustPut(t, c, "a", "new", tc.reputTTL)

			clk.now = tc.probeAt
			val, ok := c.Get("a")
			if ok != tc.wantHit || (ok && val != tc.wantVal) {
				t.Errorf("at %d Get(a) = %q, %v; want %q, %v", tc.probeAt, val, ok, tc.wantVal, tc.wantHit)
			}

			clk.now = tc.probeAgain
			if _, ok := c.Get("a"); ok {
				t.Errorf("at %d Get(a) = hit, want miss", tc.probeAgain)
			}
		})
	}
}

// 钉住惰性单条驱逐：多个过期项可以同时长期躺着占位，
// 每次 Put 只清掉其中一个；清理只在后续 Put/Get/Delete 逐个发生。
func TestCharacterizeLazySingleEviction(t *testing.T) {
	// 遍历容量与 TTL 形态：capacity 个全部过期的项 + 一次触发 Put。
	for _, capacity := range []int{2, 3, 4} {
		for _, ttl := range []int64{5, 10, 25} {
			name := "capacity " + strconv.Itoa(capacity) + " ttl " + strconv.FormatInt(ttl, 10)
			t.Run(name, func(t *testing.T) {
				clk := &clock{}
				c := newCache(t, capacity, clk)

				oldKeys := make([]string, capacity)
				for i := range oldKeys {
					oldKeys[i] = "k" + strconv.Itoa(i)
					mustPut(t, c, oldKeys[i], "v", ttl)
				}
				clk.now = ttl // 全部到点过期，但没有任何操作清理它们

				if c.Len() != capacity {
					t.Fatalf("Len() with all expired = %d, want %d", c.Len(), capacity)
				}

				mustPut(t, c, "fresh", "v", 1000)

				// 只驱逐了一个过期项：Len 仍等于容量，其余过期项继续占位。
				if c.Len() != capacity {
					t.Errorf("Len() after trigger put = %d, want %d (expired entries keep occupying capacity)", c.Len(), capacity)
				}
				survivors := 0
				for _, key := range oldKeys {
					if c.Delete(key) { // Delete 不看过期，只探测是否仍在表中
						survivors++
					}
				}
				if survivors != capacity-1 {
					t.Errorf("expired survivors = %d, want %d (evict removes exactly one entry)", survivors, capacity-1)
				}

				// 清出的容量可直接复用：再写 capacity-1 个键不应驱逐 fresh。
				for i := 0; i < capacity-1; i++ {
					mustPut(t, c, "fill"+strconv.Itoa(i), "v", 1000)
				}
				if c.Len() != capacity {
					t.Errorf("Len() after refill = %d, want %d", c.Len(), capacity)
				}
				if _, ok := c.Get("fresh"); !ok {
					t.Error("Get(fresh) = miss, want hit (freed capacity must not evict it)")
				}
			})
		}
	}
}

type tieBreakCase struct {
	name       string
	keys       []string // 同一时刻（createdAt 相同）依次 Put，链表头为最后写入者
	accessHead string   // 到期前 Get 该键以改变链表顺序；为空则不访问
}

// 钉住同 createdAt 的 tie-break：严格小于 + 头→尾扫描，
// 相等时保留先遇到（头侧 / MRU）的候选 —— MRU 反而先被驱逐。
func TestCharacterizeSameCreatedAtTieBreak(t *testing.T) {
	tests := []tieBreakCase{
		{name: "two entries, head-side MRU evicted", keys: []string{"a", "b"}},
		{name: "three entries, head-side MRU evicted", keys: []string{"a", "b", "c"}},
		{name: "recency reordered by Get, new MRU evicted", keys: []string{"a", "b"}, accessHead: "a"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clk := &clock{}
			c := newCache(t, len(tc.keys), clk)

			for _, key := range tc.keys { // 全程时钟保持 0：createdAt 全部相同
				mustPut(t, c, key, "v", 10)
			}

			var wantEvicted string
			if tc.accessHead != "" {
				clk.now = 5
				if _, ok := c.Get(tc.accessHead); !ok {
					t.Fatalf("Get(%q) = miss, want hit before expiry", tc.accessHead)
				}
				wantEvicted = tc.accessHead
			} else {
				wantEvicted = tc.keys[len(tc.keys)-1] // 最后写入者位于头侧
			}

			clk.now = 10
			mustPut(t, c, "trigger", "v", 1000)

			if c.Delete(wantEvicted) {
				t.Errorf("Delete(%q) = true, want false (MRU-side entry with same createdAt should be evicted)", wantEvicted)
			}
			for _, key := range tc.keys {
				if key == wantEvicted {
					continue
				}
				if !c.Delete(key) {
					t.Errorf("Delete(%q) = false, want true (LRU-side same-createdAt entry should survive)", key)
				}
			}
		})
	}
}

type expiredEntryPointCase struct {
	name         string
	ttl          int64
	op           string // "Get" | "Delete" | "Len"
	wantLenAfter int
}

// 钉住过期项在三个入口下的不一致口径：
// Get 当 miss 且立即删除；Delete 当存在返回 true 且删除；Len 照常计数、不删除。
func TestCharacterizeExpiredEntryEntryPoints(t *testing.T) {
	var tests []expiredEntryPointCase
	for _, ttl := range []int64{1, 10, 100} { // 循环遍历 TTL 形态，时钟恰好走到过期边界
		suffix := strconv.FormatInt(ttl, 10)
		tests = append(tests,
			expiredEntryPointCase{"Get treats expired as miss and deletes, ttl " + suffix, ttl, "Get", 0},
			expiredEntryPointCase{"Delete treats expired as existing and deletes, ttl " + suffix, ttl, "Delete", 0},
			expiredEntryPointCase{"Len counts expired without deleting, ttl " + suffix, ttl, "Len", 1},
		)
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clk := &clock{}
			c := newCache(t, 2, clk)
			mustPut(t, c, "a", "va", tc.ttl)
			clk.now = tc.ttl // now == createdAt+ttl，按到点即过期

			switch tc.op {
			case "Get":
				if val, ok := c.Get("a"); ok || val != "" {
					t.Errorf("Get(expired) = %q, %v; want %q, false", val, ok, "")
				}
			case "Delete":
				if !c.Delete("a") {
					t.Error("Delete(expired) = false, want true")
				}
			case "Len":
				if c.Len() != 1 {
					t.Error("Len() with expired entry = 0, want 1")
				}
			}

			if c.Len() != tc.wantLenAfter {
				t.Errorf("Len() after %s = %d, want %d", tc.op, c.Len(), tc.wantLenAfter)
			}
			// Get/Delete 已把过期项删掉，二次 Delete 返回 false；
			// Len 不清理，过期项仍在，二次 Delete 返回 true。
			wantSecondDelete := tc.op == "Len"
			if got := c.Delete("a"); got != wantSecondDelete {
				t.Errorf("Delete(a) after %s = %v, want %v", tc.op, got, wantSecondDelete)
			}
		})
	}
}
