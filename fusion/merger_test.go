package fusion

import (
	"errors"
	"math/big"
	"reflect"
	"sync"
	"testing"
)

func mustAdd(t *testing.T, m *Merger, name string, w int, hb bool, ttl int64) {
	t.Helper()
	if err := m.AddSource(name, w, hb, ttl); err != nil {
		t.Fatalf("AddSource(%s) unexpected error: %v", name, err)
	}
}

func mustSubmit(t *testing.T, m *Merger, name string, now int64, hits ...Hit) {
	t.Helper()
	if err := m.Submit(name, hits, now); err != nil {
		t.Fatalf("Submit(%s@%d) unexpected error: %v", name, now, err)
	}
}

func docsOf(items []ResultItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Doc
	}
	return out
}

func itemMap(items []ResultItem) map[string]string {
	got := make(map[string]string, len(items))
	for _, it := range items {
		got[it.Doc] = it.Score
	}
	return got
}

// TestTTLBoundary：now-submit 恰等于 ttl 过期，少 1 有效。
func TestTTLBoundary(t *testing.T) {
	m := New()
	mustAdd(t, m, "A", 2, true, 5)
	mustSubmit(t, m, "A", 10, Hit{"d", 3})

	if _, err := m.Fuse(14, 10); err != nil {
		t.Fatalf("at ttl-1 should be valid, got %v", err)
	}
	if _, err := m.Fuse(15, 10); !errors.Is(err, ErrNothingToFuse) {
		t.Fatalf("at exactly ttl should be expired, got %v", err)
	}
	mustSubmit(t, m, "A", 15, Hit{"d", 3})
	if _, err := m.Fuse(16, 10); err != nil {
		t.Fatalf("resubmitted source should be valid again, got %v", err)
	}
}

// TestExpiredDoesNotAffectNormalization：过期来源不改变他源 lo/hi。
func TestExpiredDoesNotAffectNormalization(t *testing.T) {
	m := New()
	mustAdd(t, m, "A", 1, true, 100)
	mustAdd(t, m, "B", 1, true, 1)
	mustSubmit(t, m, "A", 0, Hit{"x", 0}, Hit{"y", 10})
	mustSubmit(t, m, "B", 0, Hit{"x", 50}, Hit{"y", 60})

	items, err := m.Fuse(1, 10) // B: 1-0 == ttl，已过期
	if err != nil {
		t.Fatal(err)
	}
	got := itemMap(items)
	if got["x"] != "0/1" || got["y"] != "1/1" {
		t.Fatalf("expired source must not shift A's lo/hi, got %v", got)
	}
}

// TestAllExpiredNothingToFuse：从未提交 / 全部过期 -> 无可融合。
func TestAllExpiredNothingToFuse(t *testing.T) {
	m := New()
	mustAdd(t, m, "A", 1, true, 2)
	if _, err := m.Fuse(0, 10); !errors.Is(err, ErrNothingToFuse) {
		t.Fatalf("never submitted: want ErrNothingToFuse, got %v", err)
	}
	mustSubmit(t, m, "A", 0, Hit{"d", 1})
	if _, err := m.Fuse(2, 10); !errors.Is(err, ErrNothingToFuse) {
		t.Fatalf("all expired: want ErrNothingToFuse, got %v", err)
	}
}

// TestWaterLevel：now==H 允许，now<H 拒绝且不改状态。
func TestWaterLevel(t *testing.T) {
	m := New()
	mustAdd(t, m, "A", 1, true, 1000)
	mustSubmit(t, m, "A", 10, Hit{"d", 1})

	if err := m.Submit("A", []Hit{{"e", 2}}, 9); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("submit rollback: got %v", err)
	}
	if _, err := m.Fuse(9, 10); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("fuse rollback: got %v", err)
	}
	items, err := m.Fuse(10, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(docsOf(items), []string{"d"}) {
		t.Fatalf("rejected call must not change state: %v", docsOf(items))
	}
	mustSubmit(t, m, "A", 10, Hit{"f", 5})
	items, err = m.Fuse(10, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(docsOf(items), []string{"f"}) {
		t.Fatalf("equal-water submit should replace list: %v", docsOf(items))
	}
	items2, err := m.Fuse(10, 10)
	if err != nil || !reflect.DeepEqual(items, items2) {
		t.Fatalf("repeat fuse mismatch: %v vs %v (%v)", items, items2, err)
	}
}

// TestFlatNormalization：hi==lo 时多文档与单文档均得 1。
func TestFlatNormalization(t *testing.T) {
	m := New()
	mustAdd(t, m, "A", 3, true, 100)
	mustSubmit(t, m, "A", 0, Hit{"x", 7}, Hit{"y", 7}, Hit{"z", 7})
	items, err := m.Fuse(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.Score != "3/1" {
			t.Fatalf("flat multi-doc should score weight/1: %v", items)
		}
	}

	n := New()
	mustAdd(t, n, "A", 1, false, 100)
	mustSubmit(t, n, "A", 0, Hit{"only", -5})
	items, err = n.Fuse(0, 10)
	if err != nil || items[0].Score != "1/1" {
		t.Fatalf("flat single-doc should be 1/1: %v %v", items, err)
	}
}

// TestAbsentEqualsWorst：缺席文档贡献 0。
func TestAbsentEqualsWorst(t *testing.T) {
	m := New()
	mustAdd(t, m, "A", 1, true, 100)
	mustAdd(t, m, "B", 1, true, 100)
	mustSubmit(t, m, "A", 0, Hit{"x", 0}, Hit{"y", 10})
	mustSubmit(t, m, "B", 0, Hit{"x", 5})
	items, err := m.Fuse(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	got := itemMap(items)
	if got["x"] != "1/1" || got["y"] != "1/1" {
		t.Fatalf("absent must contribute 0: %v", got)
	}
}

// TestLowerBetterDedup：higherBetter=false 时重复 doc 取最小。
func TestLowerBetterDedup(t *testing.T) {
	m := New()
	mustAdd(t, m, "A", 1, false, 100)
	mustSubmit(t, m, "A", 0, Hit{"x", 5}, Hit{"x", 1}, Hit{"x", 9}, Hit{"y", 9})
	items, err := m.Fuse(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	got := itemMap(items)
	if got["x"] != "1/1" || got["y"] != "0/1" {
		t.Fatalf("lower-better dedup should keep min: %v", got)
	}
}

// TestBoundaryScores：score 恰为 ±10^15 合法且归一化精确。
func TestBoundaryScores(t *testing.T) {
	m := New()
	mustAdd(t, m, "A", 1, true, 100)
	mustAdd(t, m, "B", 1, true, 100)
	mustSubmit(t, m, "A", 0,
		Hit{"x", -1_000_000_000_000_000},
		Hit{"y", 1_000_000_000_000_000},
	)
	mustSubmit(t, m, "B", 0,
		Hit{"x", 0},
		Hit{"y", 1_000_000_000_000_000},
		Hit{"z", -1_000_000_000_000_000},
	)
	items, err := m.Fuse(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	got := itemMap(items)
	if got["y"] != "2/1" {
		t.Fatalf("y want 2/1, got %s", got["y"])
	}
	if got["x"] != "1/2" {
		t.Fatalf("x want 1/2, got %s", got["x"])
	}
	if got["z"] != "0/1" {
		t.Fatalf("z want 0/1, got %s", got["z"])
	}
	if err := m.Submit("A", []Hit{{"d", 1_000_000_000_000_001}}, 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("score > 1e15 must be rejected, got %v", err)
	}
	if err := m.Submit("A", []Hit{{"d", -1_000_000_000_000_001}}, 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("score < -1e15 must be rejected, got %v", err)
	}
}

// TestLargeDenominatorProduct：多来源分母 LCM 超 int64 仍精确。
func TestLargeDenominatorProduct(t *testing.T) {
	// 三个两两互素的大跨度：LCM 乘积远超 int64。
	spans := []int64{900_000_001, 900_000_003, 900_000_011}
	m := New()
	for i, name := range []string{"A", "B", "C"} {
		mustAdd(t, m, name, 1, true, 100)
		mustSubmit(t, m, name, 0, Hit{"d", 1}, Hit{"base", 0}, Hit{"top", spans[i]})
	}
	items, err := m.Fuse(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := new(big.Rat)
	for _, span := range spans {
		want.Add(want, big.NewRat(1, span))
	}
	r, ok := new(big.Rat).SetString(itemMap(items)["d"])
	if !ok {
		t.Fatalf("unparseable fraction: %q", itemMap(items)["d"])
	}
	if r.Cmp(want) != 0 {
		t.Fatalf("large denominator mismatch: want %s got %s", want.RatString(), r.RatString())
	}
	if r.Denom().IsInt64() {
		t.Fatalf("test premise broken: reduced denom unexpectedly fits int64: %s", r.RatString())
	}
}

// TestRankInertia：总分并列时名次惯性的各情形。
func TestRankInertia(t *testing.T) {
	t.Run("example", func(t *testing.T) {
		m := New()
		mustAdd(t, m, "A", 2, true, 1000)
		mustAdd(t, m, "B", 2, true, 1000)
		mustSubmit(t, m, "A", 0, Hit{"y", 10}, Hit{"x", 0})
		mustSubmit(t, m, "B", 0, Hit{"y", 10}, Hit{"x", 0})
		items, err := m.Fuse(0, 10)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(docsOf(items), []string{"y", "x"}) {
			t.Fatalf("first fuse order: %v", docsOf(items))
		}
		if itemMap(items)["y"] != "4/1" || itemMap(items)["x"] != "0/1" {
			t.Fatalf("first fuse scores: %v", items)
		}
		mustSubmit(t, m, "A", 0, Hit{"x", 10}, Hit{"y", 0})
		items, err = m.Fuse(1, 10)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(docsOf(items), []string{"y", "x"}) {
			t.Fatalf("rank inertia should keep y before x: %v", docsOf(items))
		}
		if itemMap(items)["x"] != "2/1" || itemMap(items)["y"] != "2/1" {
			t.Fatalf("tie scores should both be 2/1: %v", items)
		}
	})

	t.Run("only_one_was_ranked", func(t *testing.T) {
		m := New()
		mustAdd(t, m, "A", 1, true, 1000)
		mustSubmit(t, m, "A", 0, Hit{"old", 1})
		if _, err := m.Fuse(0, 10); err != nil {
			t.Fatal(err)
		}
		// old 与 fresh 总分都为 1；fresh 从未上过名次表，old 应在前。
		mustSubmit(t, m, "A", 1, Hit{"old", 1}, Hit{"fresh", 1})
		items, err := m.Fuse(1, 10)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(docsOf(items), []string{"old", "fresh"}) {
			t.Fatalf("previously ranked doc must come first: %v", docsOf(items))
		}
	})

	t.Run("neither_ranked_byte_order", func(t *testing.T) {
		m := New()
		mustAdd(t, m, "A", 1, true, 1000)
		// 先制造一张不含 b/c 的名次表。
		mustSubmit(t, m, "A", 0, Hit{"anchor", 1})
		if _, err := m.Fuse(0, 10); err != nil {
			t.Fatal(err)
		}
		mustSubmit(t, m, "A", 1, Hit{"c", 1}, Hit{"b", 1})
		items, err := m.Fuse(1, 10)
		if err != nil {
			t.Fatal(err)
		}
		if got := docsOf(items); got[0] != "b" || got[1] != "c" {
			t.Fatalf("unranked ties should use byte order: %v", got)
		}
	})

	t.Run("full_ranking_survives_small_k", func(t *testing.T) {
		m := New()
		mustAdd(t, m, "A", 1, true, 1000)
		mustSubmit(t, m, "A", 0, Hit{"p", 3}, Hit{"q", 2}, Hit{"r", 1})
		if _, err := m.Fuse(0, 2); err != nil { // 只取前 2，但名次表须保存完整 3 个
			t.Fatal(err)
		}
		// 三个文档分数拉平成并列：应按上一次完整名次 p,q,r，而非仅 p,q。
		mustSubmit(t, m, "A", 1, Hit{"r", 5}, Hit{"q", 5}, Hit{"p", 5})
		items, err := m.Fuse(1, 10)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(docsOf(items), []string{"p", "q", "r"}) {
			t.Fatalf("rank table must keep full ranking beyond k: %v", docsOf(items))
		}
	})

	t.Run("rejected_fuse_keeps_rank_table", func(t *testing.T) {
		m := New()
		mustAdd(t, m, "A", 1, true, 1000)
		mustSubmit(t, m, "A", 0, Hit{"p", 3}, Hit{"q", 1})
		if _, err := m.Fuse(0, 10); err != nil {
			t.Fatal(err)
		}
		// 被拒绝的 Fuse（时钟回退）不得更新名次表。
		if _, err := m.Fuse(-1, 10); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("negative now must be invalid: %v", err)
		}
		if _, err := m.Fuse(10, 0); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("k<1 must be invalid: %v", err)
		}
		mustSubmit(t, m, "A", 5, Hit{"q", 9}, Hit{"p", 9})
		items, err := m.Fuse(5, 10)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(docsOf(items), []string{"p", "q"}) {
			t.Fatalf("rejected fuse must not update rank table: %v", docsOf(items))
		}
	})
}

// TestWeightsAndK：权重 1 与 1000、k 大于文档数。
func TestWeightsAndK(t *testing.T) {
	m := New()
	mustAdd(t, m, "lo", 1, true, 100)
	mustAdd(t, m, "hi", 1000, true, 100)
	mustSubmit(t, m, "lo", 0, Hit{"a", 10}, Hit{"b", 0})
	mustSubmit(t, m, "hi", 0, Hit{"a", 0}, Hit{"b", 10})
	items, err := m.Fuse(0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("k larger than doc count must return all docs: %v", items)
	}
	got := itemMap(items)
	// b: hi 权重 1000*1 + lo 0 = 1000；a: hi 0 + lo 1 = 1。
	if got["b"] != "1000/1" || got["a"] != "1/1" {
		t.Fatalf("weighted scores wrong: %v", got)
	}
	if docsOf(items)[0] != "b" {
		t.Fatalf("b must rank first: %v", docsOf(items))
	}
}

// TestSubmitReplaces：重新提交整体替换旧列表。
func TestSubmitReplaces(t *testing.T) {
	m := New()
	mustAdd(t, m, "A", 1, true, 100)
	mustSubmit(t, m, "A", 0, Hit{"old", 9})
	mustSubmit(t, m, "A", 1, Hit{"new", 1})
	items, err := m.Fuse(1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(docsOf(items), []string{"new"}) {
		t.Fatalf("resubmit must fully replace list: %v", docsOf(items))
	}
}

// TestRejectionOrderAndNoStateChange：各拒绝原因及优先级，拒绝不改状态。
func TestRejectionOrderAndNoStateChange(t *testing.T) {
	m := New()
	// AddSource：空名 -> 非法先于重名。
	if err := m.AddSource("", 1, true, 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty name: %v", err)
	}
	if err := m.AddSource("A", 0, true, 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("weight 0: %v", err)
	}
	if err := m.AddSource("A", 1001, true, 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("weight 1001: %v", err)
	}
	if err := m.AddSource("A", 1, true, 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("ttl 0: %v", err)
	}
	if err := m.AddSource("A", 1, true, 1_000_001); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("ttl over max: %v", err)
	}
	mustAdd(t, m, "A", 1, true, 10)
	if err := m.AddSource("A", 1, true, 10); !errors.Is(err, ErrDuplicateName) {
		t.Fatalf("duplicate: %v", err)
	}

	// Submit：来源不存在先于参数非法。
	if err := m.Submit("nope", nil, 0); !errors.Is(err, ErrSourceNotFound) {
		t.Fatalf("unknown source: %v", err)
	}
	if err := m.Submit("A", nil, 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty hits: %v", err)
	}
	if err := m.Submit("A", []Hit{{"", 1}}, 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty doc: %v", err)
	}
	if err := m.Submit("A", []Hit{{"d", 1}}, -1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("negative now: %v", err)
	}
	mustSubmit(t, m, "A", 5, Hit{"d", 1})
	if err := m.Submit("A", []Hit{{"e", 2}}, 4); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("rollback after water=5: %v", err)
	}

	// Fuse：参数非法先于时钟回退。
	if _, err := m.Fuse(-1, 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("fuse both invalid should report invalid: %v", err)
	}
	if _, err := m.Fuse(4, 1); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("fuse rollback: %v", err)
	}
	// 来源过期后再 Fuse：水位检查先于有效性检查（合法 now）。
	if _, err := m.Fuse(100, 1); !errors.Is(err, ErrNothingToFuse) {
		t.Fatalf("expired at 100: %v", err)
	}
}

// TestReturnedSliceNotAliased：修改返回切片不影响内部状态。
func TestReturnedSliceNotAliased(t *testing.T) {
	m := New()
	mustAdd(t, m, "A", 1, true, 100)
	mustSubmit(t, m, "A", 0, Hit{"d", 1})
	items, err := m.Fuse(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	items[0] = ResultItem{Doc: "tampered", Score: "999/1"}
	again, err := m.Fuse(0, 10)
	if err != nil || again[0].Doc != "d" || again[0].Score != "1/1" {
		t.Fatalf("internal state aliased by returned slice: %v %v", again, err)
	}
}

// TestConcurrentSafety：并发调用不崩溃且 -race 下无竞争。
func TestConcurrentSafety(t *testing.T) {
	m := New()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		name := string(rune('A' + i))
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = m.AddSource(name, 1, true, 1_000_000)
		}()
	}
	wg.Wait()
	for i := 0; i < 8; i++ {
		name := string(rune('A' + i))
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = m.Submit(name, []Hit{{"d", int64(i)}}, 0)
		}()
		go func() {
			defer wg.Done()
			_, _ = m.Fuse(0, 3)
		}()
	}
	wg.Wait()
}
