package cache

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// mf 按 (路径, 摘要) 交替的参数构造清单。
func mf(pairs ...string) []Pair {
	if len(pairs)%2 != 0 {
		panic("mf 需要偶数个参数")
	}
	out := make([]Pair, 0, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		out = append(out, Pair{Path: pairs[i], Digest: pairs[i+1]})
	}
	return out
}

func mustPut(t *testing.T, c *Cache, key string, manifest []Pair, result string) {
	t.Helper()
	if err := c.Put(key, manifest, result); err != nil {
		t.Fatalf("Put(%q) 意外失败: %v", key, err)
	}
}

func mustLookup(t *testing.T, c *Cache, key string, current map[string]string) (string, bool) {
	t.Helper()
	result, hit, err := c.Lookup(key, current)
	if err != nil {
		t.Fatalf("Lookup(%q) 意外失败: %v", key, err)
	}
	return result, hit
}

// 清单互为真子集时，命中取 last 最大者，而非最长或最短者。
func TestSubsetManifestsPickMaxLast(t *testing.T) {
	c, err := New(4, 100)
	if err != nil {
		t.Fatal(err)
	}
	mustPut(t, c, "k", mf("a", "da", "b", "db", "c", "dc"), "long") // last=1
	mustPut(t, c, "k", mf("b", "db"), "short")                      // last=2
	mustPut(t, c, "k", mf("a", "da", "c", "dc"), "mid")             // last=3
	current := map[string]string{"a": "da", "b": "db", "c": "dc"}

	// 三者同时命中：最长者 last=1、最短者 last=2、中间者 last=3，
	// 应取 last 最大的 mid，与清单长短无关。
	if got, hit := mustLookup(t, c, "k", current); !hit || got != "mid" {
		t.Fatalf("判定依据: 三条互为子集的清单同时命中, 应取 last 最大者 mid, 实际 hit=%v got=%q", hit, got)
	}
	// 让 short 单独命中刷新为最新，此时应命中最短者 short。
	if _, hit := mustLookup(t, c, "k", map[string]string{"b": "db"}); !hit {
		t.Fatal("short 应单独命中并刷新 last")
	}
	if got, hit := mustLookup(t, c, "k", current); !hit || got != "short" {
		t.Fatalf("判定依据: short 刷新后 last 最大, 应命中最短者 short, 实际 hit=%v got=%q", hit, got)
	}
}

// 命中刷新 last 后，键内淘汰对象随之改变。
func TestHitRefreshChangesEviction(t *testing.T) {
	c, err := New(2, 100)
	if err != nil {
		t.Fatal(err)
	}
	mustPut(t, c, "k", mf("a", "da"), "r1") // last=1
	mustPut(t, c, "k", mf("b", "db"), "r2") // last=2
	// 命中 r1，使其 last=3，比 r2(last=2) 新。
	if got, hit := mustLookup(t, c, "k", map[string]string{"a": "da"}); !hit || got != "r1" {
		t.Fatalf("应命中 r1, 实际 hit=%v got=%q", hit, got)
	}
	// 新增第三条触发键内淘汰，应淘汰 last 最小的 r2 而非刚刷新的 r1。
	mustPut(t, c, "k", mf("c", "dc"), "r3") // last=4
	dump := c.Dump("k")
	if len(dump) != 2 {
		t.Fatalf("判定依据: M=2, 淘汰后应剩 2 条, 实际 %d 条", len(dump))
	}
	for _, e := range dump {
		if e.Result == "r2" {
			t.Fatalf("判定依据: r1 命中刷新为 last=3, 键内 last 最小者变为 r2(last=2), 应淘汰 r2, 实际 dump=%v", dump)
		}
	}
	if _, hit := mustLookup(t, c, "k", map[string]string{"b": "db"}); hit {
		t.Fatal("r2 已被淘汰, 不应再命中")
	}
}

// 同清单覆盖：不增条目、覆盖结果、刷新 last（淘汰次序随之改变）。
func TestOverwriteSameManifest(t *testing.T) {
	c, err := New(2, 100)
	if err != nil {
		t.Fatal(err)
	}
	mustPut(t, c, "k", mf("a", "da"), "old") // last=1
	mustPut(t, c, "k", mf("b", "db"), "r2")  // last=2
	// 同清单覆盖：条目数不变，结果更新，last 刷新为 3。
	mustPut(t, c, "k", mf("a", "da"), "new") // last=3
	if n := c.Len(); n != 2 {
		t.Fatalf("判定依据: 同清单覆盖不增条目, 期望 Len=2, 实际 %d", n)
	}
	if got, hit := mustLookup(t, c, "k", map[string]string{"a": "da"}); !hit || got != "new" {
		t.Fatalf("覆盖后应返回新结果 new, 实际 hit=%v got=%q", hit, got)
	}
	// 覆盖把该条目刷新到 last=3（命中再刷新到 4），新增第三条时
	// 键内 last 最小者是 r2(last=2)，应淘汰 r2。
	mustPut(t, c, "k", mf("c", "dc"), "r3")
	if _, hit := mustLookup(t, c, "k", map[string]string{"b": "db"}); hit {
		t.Fatal("判定依据: 覆盖刷新 last 后 r2 成为键内最旧, 应被淘汰")
	}
}

// 键内淘汰先于全局淘汰：全局最旧条目可能因键内先淘汰而存活。
func TestPerKeyEvictionBeforeGlobal(t *testing.T) {
	c, err := New(1, 2)
	if err != nil {
		t.Fatal(err)
	}
	mustPut(t, c, "A", mf("a", "da"), "ga") // last=1，全局最旧
	mustPut(t, c, "B", mf("b", "db"), "gb") // last=2
	// 新增 B 的第二条：键 B 内 {gb, nb} 超 M=1，先键内淘汰 gb；
	// 总数回落到 2，不再触发全局淘汰，全局最旧的 ga 存活。
	mustPut(t, c, "B", mf("c", "dc"), "nb") // last=3
	if got, hit := mustLookup(t, c, "A", map[string]string{"a": "da"}); !hit || got != "ga" {
		t.Fatalf("判定依据: 键内淘汰先于全局淘汰, 全局最旧的 ga 应存活, 实际 hit=%v got=%q", hit, got)
	}
	if _, hit := mustLookup(t, c, "B", map[string]string{"b": "db"}); hit {
		t.Fatal("gb 应在键内被淘汰")
	}
	if n := c.Len(); n != 2 {
		t.Fatalf("期望 Len=2, 实际 %d", n)
	}
}

// 文件缺失与摘要不同都不命中；清单之外的文件不影响命中。
func TestMissingFileAndDigestMismatch(t *testing.T) {
	c, err := New(4, 100)
	if err != nil {
		t.Fatal(err)
	}
	mustPut(t, c, "k", mf("a", "da", "b", "db"), "r")

	if _, hit := mustLookup(t, c, "k", map[string]string{"a": "da"}); hit {
		t.Fatal("判定依据: 清单中 b 在现状缺失(文件不存在), 不应命中")
	}
	if _, hit := mustLookup(t, c, "k", map[string]string{"a": "da", "b": "other"}); hit {
		t.Fatal("判定依据: b 的摘要逐字节不等, 不应命中")
	}
	// 现状多出清单之外的文件不影响命中。
	got, hit := mustLookup(t, c, "k", map[string]string{"a": "da", "b": "db", "extra": "dx"})
	if !hit || got != "r" {
		t.Fatalf("判定依据: 命中只取决于清单内的文件, 实际 hit=%v got=%q", hit, got)
	}
}

// 未命中不改 tick，也不改任何条目的 last。
func TestMissDoesNotChangeTick(t *testing.T) {
	c, err := New(4, 100)
	if err != nil {
		t.Fatal(err)
	}
	mustPut(t, c, "k", mf("a", "da"), "r") // tick=1
	before := c.Dump("k")
	if _, hit := mustLookup(t, c, "k", map[string]string{"a": "nope"}); hit {
		t.Fatal("摘要不同不应命中")
	}
	if _, hit := mustLookup(t, c, "k", map[string]string{}); hit {
		t.Fatal("文件缺失不应命中")
	}
	if _, hit := mustLookup(t, c, "ghost", map[string]string{"a": "da"}); hit {
		t.Fatal("不存在的键不应命中")
	}
	after := c.Dump("k")
	if len(after) != 1 || after[0].Last != before[0].Last {
		t.Fatalf("判定依据: 未命中不改 tick 与 last, 前=%v 后=%v", before, after)
	}
	// 下一次成功的 Put 取得紧邻的 tick，证明未命中没有消耗 tick。
	mustPut(t, c, "k", mf("b", "db"), "r2")
	if got := c.Dump("k")[0].Last; got != before[0].Last+1 {
		t.Fatalf("判定依据: 未命中不消耗 tick, 新条目 last 应为 %d, 实际 %d", before[0].Last+1, got)
	}
}

// 构造与 Put/Lookup 的拒绝次序：只报第一个原因，且被拒绝的操作
// 不改变 tick 与任何条目。
func TestRejectionOrder(t *testing.T) {
	if _, err := New(0, 0); !errors.Is(err, ErrInvalidM) {
		t.Fatalf("M 先于 Cap 报, 期望 ErrInvalidM, 实际 %v", err)
	}
	if _, err := New(0, 5); !errors.Is(err, ErrInvalidM) {
		t.Fatalf("期望 ErrInvalidM, 实际 %v", err)
	}
	if _, err := New(1, 0); !errors.Is(err, ErrInvalidCap) {
		t.Fatalf("期望 ErrInvalidCap, 实际 %v", err)
	}

	c, err := New(2, 10)
	if err != nil {
		t.Fatal(err)
	}
	mustPut(t, c, "k", mf("a", "da"), "r") // tick=1
	tickBefore := c.tick
	lenBefore := c.Len()

	cases := []struct {
		name     string
		key      string
		manifest []Pair
		result   string
		want     error
	}{
		{"空键优先于空清单", "", nil, "", ErrEmptyKey},
		{"空清单", "k", nil, "r", ErrEmptyManifest},
		{"空路径先于递增性", "k", mf("", "da", "", "db"), "r", ErrEmptyPath},
		{"空路径先于递增性(回落)", "k", mf("b", "db", "", "da"), "r", ErrEmptyPath},
		{"路径相等不递增", "k", mf("a", "da", "a", "db"), "r", ErrPathsNotOrdered},
		{"路径回落不递增", "k", mf("b", "db", "a", "da"), "r", ErrPathsNotOrdered},
		{"未递增先于空摘要", "k", mf("b", "", "a", ""), "r", ErrPathsNotOrdered},
		{"空摘要", "k", mf("a", ""), "r", ErrEmptyDigest},
		{"空摘要先于空结果", "k", mf("a", ""), "", ErrEmptyDigest},
		{"空结果", "k", mf("a", "da"), "", ErrEmptyResult},
	}
	for _, tc := range cases {
		err := c.Put(tc.key, tc.manifest, tc.result)
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: 期望 %v, 实际 %v", tc.name, tc.want, err)
		}
	}
	if _, _, err := c.Lookup("", map[string]string{}); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("Lookup 空键应报 ErrEmptyKey, 实际 %v", err)
	}
	if c.tick != tickBefore || c.Len() != lenBefore {
		t.Fatalf("判定依据: 被拒绝的操作不得改变 tick 与条目, tick %d->%d, Len %d->%d",
			tickBefore, c.tick, lenBefore, c.Len())
	}
}

// Dump 与 Len 的基本行为：Dump 按 last 降序，空键或不存在返回空。
func TestDumpAndLen(t *testing.T) {
	c, err := New(4, 100)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Dump(""); got != nil {
		t.Fatalf("空键 Dump 应返回空列表, 实际 %v", got)
	}
	if got := c.Dump("ghost"); got != nil {
		t.Fatalf("不存在的键 Dump 应返回空列表, 实际 %v", got)
	}
	mustPut(t, c, "k", mf("a", "da"), "r1") // last=1
	mustPut(t, c, "k", mf("b", "db"), "r2") // last=2
	mustPut(t, c, "x", mf("a", "da"), "rx") // last=3
	if n := c.Len(); n != 3 {
		t.Fatalf("期望 Len=3, 实际 %d", n)
	}
	dump := c.Dump("k")
	if len(dump) != 2 || dump[0].Last != 2 || dump[1].Last != 1 {
		t.Fatalf("Dump 应按 last 降序, 实际 %v", dump)
	}
	if dump[0].Result != "r2" || dump[1].Result != "r1" {
		t.Fatalf("Dump 结果不符: %v", dump)
	}
}

// Lookup 考察的条目数不随键总数增长：100 键与 10000 键
// （每键存满 M=4）下，单次 Lookup 考察条目数都不超过 4。
func TestLookupExaminedBound(t *testing.T) {
	for _, keyCount := range []int{100, 10000} {
		c, err := New(4, keyCount*4)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < keyCount; i++ {
			key := fmt.Sprintf("key-%d", i)
			for j := 0; j < 4; j++ {
				path := fmt.Sprintf("f%d", j)
				mustPut(t, c, key, mf(path, "d"), fmt.Sprintf("r%d", j))
			}
		}
		if _, hit := mustLookup(t, c, "key-0", map[string]string{"f0": "d"}); !hit {
			t.Fatalf("keyCount=%d: 应命中", keyCount)
		}
		if c.examined > 4 {
			t.Fatalf("keyCount=%d: 单次 Lookup 考察 %d 条, 超过每键上限 4", keyCount, c.examined)
		}
		t.Logf("keyCount=%d: 单次 Lookup 考察条目数=%d (<= M=4)", keyCount, c.examined)
	}
}

// 并发调用 Put/Lookup/Dump/Len：等价于某个串行顺序，
// 不变量（Len<=Cap、每键<=M、last 全局互不相同）始终成立。
func TestConcurrent(t *testing.T) {
	c, err := New(4, 64)
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{"k0", "k1", "k2", "k3", "k4", "k5", "k6", "k7"}
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				key := keys[(w+i)%len(keys)]
				path := fmt.Sprintf("p%d", (w+i)%6)
				digest := fmt.Sprintf("d%d", (w*7+i)%4)
				switch i % 4 {
				case 0, 1:
					_ = c.Put(key, mf(path, digest), fmt.Sprintf("r-%d-%d", w, i))
				case 2:
					_, _, _ = c.Lookup(key, map[string]string{path: digest})
				case 3:
					_ = c.Dump(key)
					_ = c.Len()
				}
			}
		}(w)
	}
	wg.Wait()

	if n := c.Len(); n > 64 {
		t.Fatalf("Len=%d 超过 Cap=64", n)
	}
	seen := make(map[uint64]string)
	for _, key := range keys {
		dump := c.Dump(key)
		if len(dump) > 4 {
			t.Fatalf("键 %s 有 %d 条, 超过 M=4", key, len(dump))
		}
		for _, e := range dump {
			if prev, dup := seen[e.Last]; dup {
				t.Fatalf("last=%d 同时出现在键 %s 与 %s, 违反全局互不相同", e.Last, prev, key)
			}
			seen[e.Last] = key
		}
	}
}
