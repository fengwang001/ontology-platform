package fusion_test

import (
	"errors"
	"math/big"
	"reflect"
	"sync"
	"testing"

	"ontology/fusion"
)

func mustAdd(t *testing.T, f *fusion.Fuser, name string, weight int, hb bool, ttl int64) {
	t.Helper()
	if err := f.AddSource(name, weight, hb, ttl); err != nil {
		t.Fatalf("AddSource(%q) unexpected error: %v", name, err)
	}
}

func mustSubmit(t *testing.T, f *fusion.Fuser, name string, hits []fusion.Hit, now int64) {
	t.Helper()
	if err := f.Submit(name, hits, now); err != nil {
		t.Fatalf("Submit(%q) unexpected error: %v", name, err)
	}
}

func mustFuse(t *testing.T, f *fusion.Fuser, now int64, k int, want []fusion.Item) {
	t.Helper()
	got, err := f.Fuse(now, k)
	if err != nil {
		t.Fatalf("Fuse(now=%d, k=%d) unexpected error: %v", now, k, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Fuse(now=%d, k=%d) = %v, want %v", now, k, got, want)
	}
}

func wantErr(t *testing.T, err error, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("err = %v, want errors.Is %v", err, target)
	}
}

func TestSpecExample(t *testing.T) {
	f := fusion.NewFuser()
	mustAdd(t, f, "A", 2, true, 1000000)
	mustAdd(t, f, "B", 2, true, 1000000)
	mustSubmit(t, f, "A", []fusion.Hit{{Doc: "y", Score: 10}, {Doc: "x", Score: 0}}, 0)
	mustSubmit(t, f, "B", []fusion.Hit{{Doc: "y", Score: 10}, {Doc: "x", Score: 0}}, 0)
	mustFuse(t, f, 0, 10, []fusion.Item{{Doc: "y", Score: "4/1"}, {Doc: "x", Score: "0/1"}})
	mustSubmit(t, f, "A", []fusion.Hit{{Doc: "x", Score: 10}, {Doc: "y", Score: 0}}, 1)
	// x 与 y 总分均为 2/1，名次惯性使 y 仍在 x 前（尽管字节序 x 更小）。
	want := []fusion.Item{{Doc: "y", Score: "2/1"}, {Doc: "x", Score: "2/1"}}
	mustFuse(t, f, 1, 10, want)
	// 同一 now 下连续融合结果逐字段相同。
	mustFuse(t, f, 1, 10, want)
}

// now−提交时刻恰等于 ttl 时过期，少 1 时仍有效。
func TestTTLBoundary(t *testing.T) {
	f := fusion.NewFuser()
	mustAdd(t, f, "s", 1, true, 10)
	mustSubmit(t, f, "s", []fusion.Hit{{Doc: "a", Score: 1}}, 5)
	mustFuse(t, f, 14, 1, []fusion.Item{{Doc: "a", Score: "1/1"}}) // 14-5=9 < 10
	_, err := f.Fuse(15, 1)                                        // 15-5=10 == ttl，已过期
	wantErr(t, err, fusion.ErrNoFusableSources)
}

// 过期来源不参与计算，也不改变其他来源的 lo 与 hi。
func TestExpiredSourceDoesNotAffectOthers(t *testing.T) {
	f := fusion.NewFuser()
	mustAdd(t, f, "A", 1, true, 100)
	mustAdd(t, f, "B", 1, true, 5)
	mustSubmit(t, f, "A", []fusion.Hit{{Doc: "x", Score: 0}, {Doc: "y", Score: 10}}, 0)
	mustSubmit(t, f, "B", []fusion.Hit{{Doc: "z", Score: 7}}, 0)
	mustFuse(t, f, 1, 10, []fusion.Item{
		{Doc: "y", Score: "1/1"},
		{Doc: "z", Score: "1/1"},
		{Doc: "x", Score: "0/1"},
	})
	// B 过期后，A 的归一化不变，z 消失。
	mustFuse(t, f, 6, 10, []fusion.Item{
		{Doc: "y", Score: "1/1"},
		{Doc: "x", Score: "0/1"},
	})
}

// 全部过期（或从未提交）时无可融合。
func TestAllExpiredOrNeverSubmitted(t *testing.T) {
	f := fusion.NewFuser()
	mustAdd(t, f, "never", 1, true, 100)
	_, err := f.Fuse(0, 1)
	wantErr(t, err, fusion.ErrNoFusableSources)
	mustAdd(t, f, "s", 1, true, 5)
	mustSubmit(t, f, "s", []fusion.Hit{{Doc: "a", Score: 1}}, 0)
	_, err = f.Fuse(5, 1)
	wantErr(t, err, fusion.ErrNoFusableSources)
}

// now 等于水位允许，小于水位被拒且不改状态。
func TestWatermark(t *testing.T) {
	f := fusion.NewFuser()
	mustAdd(t, f, "s", 1, true, 1000)
	mustSubmit(t, f, "s", []fusion.Hit{{Doc: "a", Score: 1}, {Doc: "b", Score: 2}}, 10)
	// 时钟回退的 Submit 被拒，且列表不被替换。
	wantErr(t, f.Submit("s", []fusion.Hit{{Doc: "a", Score: 6}, {Doc: "b", Score: 5}}, 9), fusion.ErrClockRegression)
	// 时钟回退的 Fuse 被拒。
	_, err := f.Fuse(9, 1)
	wantErr(t, err, fusion.ErrClockRegression)
	// now 等于水位允许，且列表仍是旧列表（a=1,b=2 → a=0/1, b=1/1）。
	mustFuse(t, f, 10, 10, []fusion.Item{{Doc: "b", Score: "1/1"}, {Doc: "a", Score: "0/1"}})
	// 水位不被拒绝的调用改变：now=10 仍允许。
	mustFuse(t, f, 10, 10, []fusion.Item{{Doc: "b", Score: "1/1"}, {Doc: "a", Score: "0/1"}})
}

// hi 等于 lo 时归一化恒为 1：多文档与单文档列表。
func TestHiEqualsLo(t *testing.T) {
	f := fusion.NewFuser()
	mustAdd(t, f, "multi", 1, true, 100)
	mustAdd(t, f, "single", 1, false, 100)
	mustSubmit(t, f, "multi", []fusion.Hit{{Doc: "p", Score: 5}, {Doc: "q", Score: 5}}, 0)
	mustSubmit(t, f, "single", []fusion.Hit{{Doc: "r", Score: -3}}, 0)
	mustFuse(t, f, 0, 10, []fusion.Item{
		{Doc: "p", Score: "1/1"},
		{Doc: "q", Score: "1/1"},
		{Doc: "r", Score: "1/1"},
	})
}

// 缺席与列表中最差文档同为 0。
func TestAbsentAndWorstBothZero(t *testing.T) {
	f := fusion.NewFuser()
	mustAdd(t, f, "A", 1, true, 100)
	mustAdd(t, f, "B", 1, true, 100)
	mustSubmit(t, f, "A", []fusion.Hit{{Doc: "x", Score: 0}, {Doc: "y", Score: 10}}, 0)
	mustSubmit(t, f, "B", []fusion.Hit{{Doc: "y", Score: 5}}, 0)
	// x 在 A 中最差（0），在 B 中缺席（0），总分 0/1；y = 1 + 1 = 2/1。
	mustFuse(t, f, 0, 10, []fusion.Item{
		{Doc: "y", Score: "2/1"},
		{Doc: "x", Score: "0/1"},
	})
}

// higherBetter 为假且重复 doc 取最小。
func TestLowerBetterDuplicateMin(t *testing.T) {
	f := fusion.NewFuser()
	mustAdd(t, f, "s", 1, false, 100)
	mustSubmit(t, f, "s", []fusion.Hit{
		{Doc: "a", Score: 5}, {Doc: "a", Score: 3}, {Doc: "a", Score: 7},
		{Doc: "b", Score: 10},
	}, 0)
	// 去重后 a=3（最小），lo=3, hi=10；n(a)=(10-3)/7=1，n(b)=0。
	mustFuse(t, f, 0, 10, []fusion.Item{
		{Doc: "a", Score: "1/1"},
		{Doc: "b", Score: "0/1"},
	})
	// higherBetter 为真时重复 doc 取最大。
	g := fusion.NewFuser()
	mustAdd(t, g, "s", 1, true, 100)
	mustSubmit(t, g, "s", []fusion.Hit{
		{Doc: "a", Score: 5}, {Doc: "a", Score: 9}, {Doc: "a", Score: 3},
		{Doc: "b", Score: 1},
	}, 0)
	mustFuse(t, g, 0, 10, []fusion.Item{
		{Doc: "a", Score: "1/1"},
		{Doc: "b", Score: "0/1"},
	})
}

// score 恰为 ±10^15 合法，多来源分母乘积超过 int64 仍精确。
func TestExactBigScores(t *testing.T) {
	const M = 1000000000000000 // 10^15
	f := fusion.NewFuser()
	mustAdd(t, f, "A", 1, true, 100)
	mustAdd(t, f, "B", 1, true, 100)
	// A 的 span = 2*10^15，B 的 span = 2*10^15-1，分母乘积约 4*10^30 超过 int64。
	mustSubmit(t, f, "A", []fusion.Hit{{Doc: "d", Score: -M + 1}, {Doc: "lo", Score: -M}, {Doc: "e", Score: M}}, 0)
	mustSubmit(t, f, "B", []fusion.Hit{{Doc: "d", Score: -M + 1}, {Doc: "lo", Score: -M}, {Doc: "e", Score: M - 1}}, 0)
	total := big.NewRat(1, 2*M)
	total.Add(total, big.NewRat(1, 2*M-1))
	got, err := f.Fuse(0, 10)
	if err != nil {
		t.Fatalf("Fuse: %v", err)
	}
	var dScore string
	for _, it := range got {
		if it.Doc == "d" {
			dScore = it.Score
		}
	}
	if dScore != total.Num().String()+"/"+total.Denom().String() {
		t.Fatalf("d score = %q, want %s/%s", dScore, total.Num(), total.Denom())
	}
	// 分母确实超过 int64 范围。
	if total.Denom().BitLen() <= 63 {
		t.Fatalf("expected denominator exceeding int64, got %s", total.Denom())
	}
	// 越界分数被拒。
	wantErr(t, f.Submit("A", []fusion.Hit{{Doc: "d", Score: M + 1}}, 1), fusion.ErrInvalidArgument)
	wantErr(t, f.Submit("A", []fusion.Hit{{Doc: "d", Score: -M - 1}}, 1), fusion.ErrInvalidArgument)
	// 恰为 ±10^15 合法。
	mustSubmit(t, f, "A", []fusion.Hit{{Doc: "d", Score: M}, {Doc: "e", Score: -M}}, 1)
}

// 总分并列时：只有一方出现在名次表中，出现过的一方优先（覆盖字节序）。
func TestRankInertiaOnlyOneInTable(t *testing.T) {
	f := fusion.NewFuser()
	mustAdd(t, f, "s", 1, true, 1000)
	mustSubmit(t, f, "s", []fusion.Hit{{Doc: "m", Score: 2}, {Doc: "z", Score: 1}}, 0)
	mustFuse(t, f, 0, 10, []fusion.Item{{Doc: "m", Score: "1/1"}, {Doc: "z", Score: "0/1"}})
	// z 在名次表中，b 不在；并列时 z 优先，尽管字节序 b < z。
	mustSubmit(t, f, "s", []fusion.Hit{{Doc: "z", Score: 2}, {Doc: "b", Score: 2}}, 1)
	mustFuse(t, f, 1, 10, []fusion.Item{{Doc: "z", Score: "1/1"}, {Doc: "b", Score: "1/1"}})
}

// 总分并列时：两方都未出现在名次表中，按 doc 字节序升序。
func TestRankInertiaBothAbsent(t *testing.T) {
	f := fusion.NewFuser()
	mustAdd(t, f, "s", 1, true, 1000)
	mustSubmit(t, f, "s", []fusion.Hit{{Doc: "z", Score: 5}}, 0)
	mustFuse(t, f, 0, 10, []fusion.Item{{Doc: "z", Score: "1/1"}})
	// c 与 a 都未在名次表中且并列，按字节序 a 在 c 前；z 仍在表中排最前。
	mustSubmit(t, f, "s", []fusion.Hit{{Doc: "z", Score: 2}, {Doc: "c", Score: 1}, {Doc: "a", Score: 1}}, 1)
	mustFuse(t, f, 1, 10, []fusion.Item{
		{Doc: "z", Score: "1/1"},
		{Doc: "a", Score: "0/1"},
		{Doc: "c", Score: "0/1"},
	})
}

// 名次表保存完整排序而非前 k 个。
func TestRankTableKeepsFullOrder(t *testing.T) {
	f := fusion.NewFuser()
	mustAdd(t, f, "s", 1, true, 1000)
	mustSubmit(t, f, "s", []fusion.Hit{{Doc: "x", Score: 1}, {Doc: "y", Score: 2}, {Doc: "z", Score: 3}}, 0)
	// k=1 只返回 z，但完整名次表为 [z, y, x]。
	mustFuse(t, f, 0, 1, []fusion.Item{{Doc: "z", Score: "1/1"}})
	// x 与 y 并列；y 的名次（1）优于 x（2），尽管字节序 x < y。
	mustSubmit(t, f, "s", []fusion.Hit{{Doc: "x", Score: 5}, {Doc: "y", Score: 5}}, 1)
	mustFuse(t, f, 1, 10, []fusion.Item{{Doc: "y", Score: "1/1"}, {Doc: "x", Score: "1/1"}})
}

// 被拒绝的 Fuse 不更新名次表。
func TestRejectedFuseKeepsRankTable(t *testing.T) {
	f := fusion.NewFuser()
	mustAdd(t, f, "s", 1, true, 1000)
	mustSubmit(t, f, "s", []fusion.Hit{{Doc: "b", Score: 2}, {Doc: "a", Score: 1}}, 0)
	mustFuse(t, f, 0, 10, []fusion.Item{{Doc: "b", Score: "1/1"}, {Doc: "a", Score: "0/1"}})
	// 若这次融合成功，名次表将变为 [a, b]；但 k<1 被拒绝。
	mustSubmit(t, f, "s", []fusion.Hit{{Doc: "a", Score: 2}, {Doc: "b", Score: 1}}, 1)
	_, err := f.Fuse(1, 0)
	wantErr(t, err, fusion.ErrInvalidArgument)
	// 并列时仍按旧名次表 [b, a]：b 在 a 前，尽管字节序 a < b。
	mustSubmit(t, f, "s", []fusion.Hit{{Doc: "a", Score: 5}, {Doc: "b", Score: 5}}, 2)
	mustFuse(t, f, 2, 10, []fusion.Item{{Doc: "b", Score: "1/1"}, {Doc: "a", Score: "1/1"}})
}

// 权重边界：1 与 1000 合法，0 与 1001 非法；权重参与加权求和。
func TestWeights(t *testing.T) {
	f := fusion.NewFuser()
	wantErr(t, f.AddSource("w0", 0, true, 1), fusion.ErrInvalidArgument)
	wantErr(t, f.AddSource("w1001", 1001, true, 1), fusion.ErrInvalidArgument)
	mustAdd(t, f, "small", 1, true, 100)
	mustAdd(t, f, "big", 1000, true, 100)
	mustSubmit(t, f, "small", []fusion.Hit{{Doc: "a", Score: 1}}, 0)
	mustSubmit(t, f, "big", []fusion.Hit{{Doc: "a", Score: 5}, {Doc: "b", Score: 10}}, 0)
	// a = 1*1 + 1000*0 = 1；b = 1000*1 = 1000。
	mustFuse(t, f, 0, 10, []fusion.Item{
		{Doc: "b", Score: "1000/1"},
		{Doc: "a", Score: "1/1"},
	})
}

// AddSource 参数校验与重名，按顺序只报第一个原因。
func TestAddSourceErrors(t *testing.T) {
	f := fusion.NewFuser()
	wantErr(t, f.AddSource("", 1, true, 1), fusion.ErrInvalidArgument)
	wantErr(t, f.AddSource("s", 1, true, 0), fusion.ErrInvalidArgument)
	wantErr(t, f.AddSource("s", 1, true, 1000001), fusion.ErrInvalidArgument)
	mustAdd(t, f, "s", 1, true, 1000000)
	wantErr(t, f.AddSource("s", 1, true, 1), fusion.ErrDuplicateSource)
	// 参数非法与重名同时存在时，报参数非法。
	wantErr(t, f.AddSource("s", 0, true, 1), fusion.ErrInvalidArgument)
}

// Submit 拒绝原因顺序：来源不存在 > 参数非法 > 时钟回退。
func TestSubmitErrors(t *testing.T) {
	f := fusion.NewFuser()
	mustAdd(t, f, "s", 1, true, 100)
	// 来源不存在优先于参数非法。
	wantErr(t, f.Submit("nope", nil, 0), fusion.ErrSourceNotFound)
	// 空 hits、空 doc、负 now 均为参数非法；已提交列表不能被空列表清除。
	wantErr(t, f.Submit("s", nil, 0), fusion.ErrInvalidArgument)
	wantErr(t, f.Submit("s", []fusion.Hit{{Doc: "", Score: 1}}, 0), fusion.ErrInvalidArgument)
	wantErr(t, f.Submit("s", []fusion.Hit{{Doc: "a", Score: 1}}, -1), fusion.ErrInvalidArgument)
	mustSubmit(t, f, "s", []fusion.Hit{{Doc: "a", Score: 1}}, 5)
	// 参数非法优先于时钟回退。
	wantErr(t, f.Submit("s", nil, 4), fusion.ErrInvalidArgument)
	wantErr(t, f.Submit("s", []fusion.Hit{{Doc: "a", Score: 1}}, 4), fusion.ErrClockRegression)
	// 空列表提交被拒后，旧列表仍在。
	mustFuse(t, f, 5, 10, []fusion.Item{{Doc: "a", Score: "1/1"}})
}

// Fuse 拒绝原因顺序：参数非法 > 时钟回退 > 无可融合。
func TestFuseErrors(t *testing.T) {
	f := fusion.NewFuser()
	mustAdd(t, f, "s", 1, true, 100)
	mustSubmit(t, f, "s", []fusion.Hit{{Doc: "a", Score: 1}}, 5)
	// k<1 与负 now 为参数非法，且优先于时钟回退。
	_, err := f.Fuse(4, 0)
	wantErr(t, err, fusion.ErrInvalidArgument)
	_, err = f.Fuse(-1, 1)
	wantErr(t, err, fusion.ErrInvalidArgument)
	_, err = f.Fuse(4, 1)
	wantErr(t, err, fusion.ErrClockRegression)
	// 时钟回退优先于无可融合：来源在 now=104 已过期，但 now=4 先报回退。
	_, err = f.Fuse(4, 1)
	wantErr(t, err, fusion.ErrClockRegression)
	_, err = f.Fuse(105, 1)
	wantErr(t, err, fusion.ErrNoFusableSources)
}

// k 大于文档数时返回全部文档。
func TestKLargerThanDocs(t *testing.T) {
	f := fusion.NewFuser()
	mustAdd(t, f, "s", 1, true, 100)
	mustSubmit(t, f, "s", []fusion.Hit{{Doc: "a", Score: 1}, {Doc: "b", Score: 2}}, 0)
	mustFuse(t, f, 0, 100, []fusion.Item{{Doc: "b", Score: "1/1"}, {Doc: "a", Score: "0/1"}})
}

// 重新提交整体替换旧列表。
func TestResubmitReplaces(t *testing.T) {
	f := fusion.NewFuser()
	mustAdd(t, f, "s", 1, true, 1000)
	mustSubmit(t, f, "s", []fusion.Hit{{Doc: "a", Score: 1}, {Doc: "b", Score: 2}}, 0)
	mustFuse(t, f, 0, 10, []fusion.Item{{Doc: "b", Score: "1/1"}, {Doc: "a", Score: "0/1"}})
	mustSubmit(t, f, "s", []fusion.Hit{{Doc: "c", Score: 7}}, 1)
	mustFuse(t, f, 1, 10, []fusion.Item{{Doc: "c", Score: "1/1"}})
}

// 全部方法可并发调用（配合 -race 验证）。
func TestConcurrent(t *testing.T) {
	f := fusion.NewFuser()
	mustAdd(t, f, "s", 1, true, 1000000)
	mustSubmit(t, f, "s", []fusion.Hit{{Doc: "a", Score: 1}, {Doc: "b", Score: 2}}, 0)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = f.Submit("s", []fusion.Hit{{Doc: "a", Score: 1}}, 0)
				_, _ = f.Fuse(0, 2)
				_ = f.AddSource("x", 1, true, 1)
			}
		}()
	}
	wg.Wait()
}
