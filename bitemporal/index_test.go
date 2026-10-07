package bitemporal

import (
	"fmt"
	"math"
	"math/rand"
	"testing"

	"ontology/core"
)

var keyA = core.Key{Type: "Customer", ID: "A"}

func applySeq(ix *Index, k core.Key, versions ...core.Version) {
	for _, v := range versions {
		ix.Apply(k, v)
	}
}

func putV(seq uint64, biz int64, payload string) core.Version {
	return core.Version{Seq: seq, BizStart: biz, Payload: payload}
}

func delV(seq uint64, biz int64) core.Version {
	return core.Version{Seq: seq, BizStart: biz, Deleted: true}
}

func mustQuery(t *testing.T, ix *Index, k core.Key, sysQ uint64, bizQ int64) core.QueryResult {
	t.Helper()
	r, _ := ix.Query(k, sysQ, bizQ)
	return r
}

// 业务时间起点相等、系统时间不同的两条记录：系统时间更晚者覆盖可见性，
// 但两条记录都必须保留。
func TestEqualBizStartOverride(t *testing.T) {
	ix := New()
	applySeq(ix, keyA, putV(1, 10, "old"), putV(2, 10, "new"))

	if r := mustQuery(t, ix, keyA, 2, 10); r.Visibility != core.Found || r.Version.Payload != "new" {
		t.Fatalf("sys=2 时等起点覆盖应命中 new，得到 %s", r)
	}
	if r := mustQuery(t, ix, keyA, 1, 10); r.Visibility != core.Found || r.Version.Payload != "old" {
		t.Fatalf("sys=1 时应仍命中 old，得到 %s", r)
	}
	// 两条记录都保留：as-of 1 与 as-of 2 的时间线分别由它们代表。
	if iv := ix.RebuildAsOf(keyA, 1); len(iv) != 1 || iv[0].Version.Payload != "old" {
		t.Fatalf("RebuildAsOf(1) = %+v，期望 [old@10)", iv)
	}
	if iv := ix.RebuildAsOf(keyA, 2); len(iv) != 1 || iv[0].Version.Payload != "new" {
		t.Fatalf("RebuildAsOf(2) = %+v，期望 [new@10)", iv)
	}
	// 等起点记录与后续起点共同切分区间。
	ix.Apply(keyA, putV(3, 20, "later"))
	if r := mustQuery(t, ix, keyA, 3, 15); r.Version.Payload != "new" {
		t.Fatalf("biz=15 应落在 new 的区间 [10,20)，得到 %s", r)
	}
	if r := mustQuery(t, ix, keyA, 3, 25); r.Version.Payload != "later" {
		t.Fatalf("biz=25 应落在 later 的区间 [20,+inf)，得到 %s", r)
	}
}

// 查询时点早于该主键任何记录 / 晚于全部记录两种边界。
func TestQueryBoundaries(t *testing.T) {
	ix := New()
	applySeq(ix, keyA, putV(1, 10, "a"), putV(2, 20, "b"), putV(3, 30, "c"))

	if r := mustQuery(t, ix, keyA, 3, 5); r.Visibility != core.NotVisible {
		t.Fatalf("bizQ 早于全部业务起点应为 NOT_VISIBLE，得到 %s", r)
	}
	if r := mustQuery(t, ix, keyA, 0, 50); r.Visibility != core.NotVisible {
		t.Fatalf("sysQ 早于全部版本应为 NOT_VISIBLE，得到 %s", r)
	}
	if r := mustQuery(t, ix, keyA, 3, 1000); r.Visibility != core.Found || r.Version.Payload != "c" {
		t.Fatalf("bizQ 晚于全部记录应命中开放终点的最后版本，得到 %s", r)
	}
	if r := mustQuery(t, ix, core.Key{Type: "Customer", ID: "ghost"}, 9, 9); r.Visibility != core.NeverExisted {
		t.Fatalf("从未写入的主键应为 NEVER_EXISTED，得到 %s", r)
	}
}

// 追溯写入过去的业务时间段：as-of 查询按当时的可见集重建区间。
func TestRetroactiveWrite(t *testing.T) {
	ix := New()
	applySeq(ix, keyA, putV(1, 10, "first"), putV(2, 20, "second"))

	// sys=1 时 second 尚未写入，first 的区间开放，覆盖 biz=25。
	if r := mustQuery(t, ix, keyA, 1, 25); r.Visibility != core.Found || r.Version.Payload != "first" {
		t.Fatalf("sys=1, biz=25 应命中 first（当时区间开放），得到 %s", r)
	}
	// sys=2 时 second 已把 [20,+inf) 切走。
	if r := mustQuery(t, ix, keyA, 2, 25); r.Visibility != core.Found || r.Version.Payload != "second" {
		t.Fatalf("sys=2, biz=25 应命中 second，得到 %s", r)
	}
}

// 逻辑删除版本的可见性。
func TestDeleteVisibility(t *testing.T) {
	ix := New()
	applySeq(ix, keyA, putV(1, 10, "alive"), delV(2, 15), putV(3, 25, "revived"))

	if r := mustQuery(t, ix, keyA, 3, 12); r.Visibility != core.Found {
		t.Fatalf("删除前区间应 FOUND，得到 %s", r)
	}
	if r := mustQuery(t, ix, keyA, 3, 20); r.Visibility != core.Deleted {
		t.Fatalf("删除区间应 DELETED，得到 %s", r)
	}
	if r := mustQuery(t, ix, keyA, 3, 30); r.Visibility != core.Found || r.Version.Payload != "revived" {
		t.Fatalf("复活区间应 FOUND revived，得到 %s", r)
	}
}

// 复杂度验证：探测步数必须随版本数呈对数增长，而非线性增长。
// 该方法不依赖重新遍历全部历史版本——直接统计索引内部的探测次数。
func TestQueryComplexityIsLogarithmic(t *testing.T) {
	for _, n := range []int{1_000, 10_000, 100_000} {
		ix := New()
		for i := 0; i < n; i++ {
			ix.Apply(keyA, putV(uint64(i+1), int64(i*10), "p"))
		}
		// 再叠加一批等起点覆盖记录，制造每个起点的多版本二分场景。
		for i := 0; i < n; i += 100 {
			ix.Apply(keyA, putV(uint64(n+1+i/100), int64(i*10), "q"))
		}
		total := uint64(n + n/100)

		rng := rand.New(rand.NewSource(42))
		maxSteps := 0
		for q := 0; q < 2000; q++ {
			sysQ := uint64(rng.Int63n(int64(total) + 1))
			bizQ := rng.Int63n(int64(n)*10 + 100)
			_, steps := ix.Query(keyA, sysQ, bizQ)
			if steps > maxSteps {
				maxSteps = steps
			}
		}
		bound := 8*int(math.Ceil(math.Log2(float64(total)))) + 16
		t.Logf("versions=%d maxSteps=%d bound=%d", total, maxSteps, bound)
		if maxSteps > bound {
			t.Fatalf("versions=%d: maxSteps=%d 超出对数上界 %d，疑似退化为线性扫描",
				total, maxSteps, bound)
		}
		if n == 100_000 && maxSteps > 500 {
			t.Fatalf("10 万版本下单次查询探测 %d 步，明显偏离 O(log V)", maxSteps)
		}
	}
}

// 查询代价与对象类型下的实例总数无关：同一主键的历史在
// 1 个实例与 1 万个实例的索引中探测步数完全一致。
func TestQueryCostIndependentOfInstanceCount(t *testing.T) {
	build := func(others int) *Index {
		ix := New()
		for i := 0; i < others; i++ {
			k := core.Key{Type: "Customer", ID: fmt.Sprintf("other-%d", i)}
			ix.Apply(k, putV(1, 0, "x"))
		}
		for i := 0; i < 5_000; i++ {
			ix.Apply(keyA, putV(uint64(i+1), int64(i*7), "p"))
		}
		return ix
	}
	small, large := build(0), build(10_000)
	rng := rand.New(rand.NewSource(7))
	for q := 0; q < 500; q++ {
		sysQ := uint64(rng.Int63n(5001))
		bizQ := rng.Int63n(40000)
		r1, s1 := small.Query(keyA, sysQ, bizQ)
		r2, s2 := large.Query(keyA, sysQ, bizQ)
		if s1 != s2 {
			t.Fatalf("实例总数影响了查询代价: steps %d vs %d", s1, s2)
		}
		if r1.Visibility != r2.Visibility {
			t.Fatalf("实例总数影响了查询结果: %s vs %s", r1, r2)
		}
	}
}

func BenchmarkQuery(b *testing.B) {
	for _, n := range []int{1_000, 100_000} {
		ix := New()
		for i := 0; i < n; i++ {
			ix.Apply(keyA, putV(uint64(i+1), int64(i*10), "p"))
		}
		rng := rand.New(rand.NewSource(1))
		b.Run(fmt.Sprintf("versions=%d", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				ix.Query(keyA, uint64(rng.Int63n(int64(n)+1)), rng.Int63n(int64(n)*10))
			}
		})
	}
}
