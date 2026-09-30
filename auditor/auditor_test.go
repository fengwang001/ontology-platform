package auditor

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

func newAuditor(t *testing.T, stages int, fanout []int64, width, grace int64) *Auditor {
	t.Helper()
	a, err := New(Config{Stages: stages, Fanout: fanout, BucketWidth: width, Grace: grace})
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	return a
}

func mustReport(t *testing.T, a *Auditor, r Report) {
	t.Helper()
	t.Logf("输入上报: 阶段=%d 桶=%d 序号=%d 收入=%d 发出=%d 丢弃=%d",
		r.Stage, r.Bucket, r.Seq, r.In, r.Out, r.Drop)
	if err := a.Report(r); err != nil {
		t.Fatalf("上报被拒绝: %v", err)
	}
}

func logVerdict(t *testing.T, a *Auditor, bucket int64) Verdict {
	t.Helper()
	v := a.Verdict(bucket)
	t.Logf("输出裁决: 桶=%d 类别=%s 阶段=%d 跳=%d 差额=%d 修订=%d 已结算=%v",
		bucket, v.Category, v.Stage, v.Hop, v.Delta, v.Revision, v.Settled)
	t.Logf("判定依据: %s", v.Reason)
	return v
}

// 展开倍数下的守恒：f=[2,3]，各阶段 收入×f = 发出+丢弃，逐跳平衡。
func TestBalancedWithFanout(t *testing.T) {
	a := newAuditor(t, 2, []int64{2, 3}, 10, 5)
	mustReport(t, a, Report{Stage: 0, Bucket: 0, Seq: 1, In: 10, Out: 18, Drop: 2}) // 10*2=20=18+2
	mustReport(t, a, Report{Stage: 1, Bucket: 0, Seq: 1, In: 18, Out: 54, Drop: 0}) // 18*3=54
	v := logVerdict(t, a, 0)
	if v.Category != Balanced || !v.Settled || v.Revision != 0 {
		t.Fatalf("期望平衡且已结算、修订 0，得到 %+v", v)
	}
}

// 丢失方向：下一阶段收入小于上一阶段发出。
func TestLossDirection(t *testing.T) {
	a := newAuditor(t, 2, []int64{2, 1}, 10, 5)
	mustReport(t, a, Report{Stage: 0, Bucket: 0, Seq: 1, In: 10, Out: 20, Drop: 0})
	mustReport(t, a, Report{Stage: 1, Bucket: 0, Seq: 1, In: 17, Out: 17, Drop: 0})
	v := logVerdict(t, a, 0)
	if v.Category != Loss || v.Hop != 0 || v.Delta != 3 {
		t.Fatalf("期望跳 0 丢失差额 3，得到 %+v", v)
	}
}

// 重复方向：下一阶段收入大于上一阶段发出。
func TestDuplicateDirection(t *testing.T) {
	a := newAuditor(t, 2, []int64{2, 1}, 10, 5)
	mustReport(t, a, Report{Stage: 0, Bucket: 0, Seq: 1, In: 10, Out: 20, Drop: 0})
	mustReport(t, a, Report{Stage: 1, Bucket: 0, Seq: 1, In: 23, Out: 23, Drop: 0})
	v := logVerdict(t, a, 0)
	if v.Category != Duplicate || v.Hop != 0 || v.Delta != 3 {
		t.Fatalf("期望跳 0 重复差额 3，得到 %+v", v)
	}
}

// 不守恒：收入×f 不等于 发出+丢弃，记阶段。
func TestNonConservation(t *testing.T) {
	a := newAuditor(t, 2, []int64{2, 1}, 10, 5)
	mustReport(t, a, Report{Stage: 0, Bucket: 0, Seq: 1, In: 10, Out: 19, Drop: 0}) // 10*2=20≠19
	mustReport(t, a, Report{Stage: 1, Bucket: 0, Seq: 1, In: 19, Out: 19, Drop: 0})
	v := logVerdict(t, a, 0)
	if v.Category != NonConservation || v.Stage != 0 {
		t.Fatalf("期望阶段 0 不守恒，得到 %+v", v)
	}
}

// 缺报被更靠前的违规压过：阶段 0 不守恒且阶段 2 从未上报，
// 按阶段顺序检查，首个违规（不守恒）即为裁决，而不是缺报。
func TestMissingReportSuppressedByEarlierViolation(t *testing.T) {
	a := newAuditor(t, 3, []int64{2, 2, 1}, 10, 5)
	mustReport(t, a, Report{Stage: 0, Bucket: 0, Seq: 1, In: 10, Out: 21, Drop: 0}) // 不守恒
	mustReport(t, a, Report{Stage: 1, Bucket: 0, Seq: 1, In: 21, Out: 42, Drop: 0})
	// 阶段 2 不上报，水位触发结算。
	if err := a.AdvanceWatermark(15); err != nil {
		t.Fatalf("推进水位失败: %v", err)
	}
	v := logVerdict(t, a, 0)
	if v.Category != NonConservation || v.Stage != 0 {
		t.Fatalf("期望阶段 0 不守恒压过缺报，得到 %+v", v)
	}
}

// 无违规但有阶段从未上报：记编号最小的缺报阶段。
func TestMissingReport(t *testing.T) {
	a := newAuditor(t, 3, []int64{2, 2, 1}, 10, 5)
	mustReport(t, a, Report{Stage: 0, Bucket: 0, Seq: 1, In: 10, Out: 20, Drop: 0})
	mustReport(t, a, Report{Stage: 2, Bucket: 0, Seq: 1, In: 40, Out: 40, Drop: 0})
	if err := a.AdvanceWatermark(15); err != nil {
		t.Fatalf("推进水位失败: %v", err)
	}
	v := logVerdict(t, a, 0)
	if v.Category != MissingReport || v.Stage != 1 {
		t.Fatalf("期望阶段 1 缺报，得到 %+v", v)
	}
}

// 序号乱序：旧序号上报被拒，采用值保持高序号的值。
func TestStaleSeqRejected(t *testing.T) {
	a := newAuditor(t, 2, []int64{1, 1}, 10, 5)
	mustReport(t, a, Report{Stage: 0, Bucket: 0, Seq: 5, In: 10, Out: 10, Drop: 0})
	stale := Report{Stage: 0, Bucket: 0, Seq: 3, In: 999, Out: 999, Drop: 0}
	t.Logf("输入上报(应被拒): %+v", stale)
	err := a.Report(stale)
	if !errors.Is(err, ErrStaleSeq) {
		t.Fatalf("期望 ErrStaleSeq，得到 %v", err)
	}
	t.Logf("拒绝原因: %v", err)
	// 相同序号同样被拒（不大于已采用者）。
	dup := Report{Stage: 0, Bucket: 0, Seq: 5, In: 1, Out: 1, Drop: 0}
	if err := a.Report(dup); !errors.Is(err, ErrStaleSeq) {
		t.Fatalf("期望同序号也被拒，得到 %v", err)
	}
	mustReport(t, a, Report{Stage: 1, Bucket: 0, Seq: 1, In: 10, Out: 10, Drop: 0})
	v := logVerdict(t, a, 0)
	if v.Category != Balanced {
		t.Fatalf("旧上报应被拒、采用值不变而平衡，得到 %+v", v)
	}
}

// 结算后迟到上报把丢失修订为平衡并计一次修订。
func TestLateReportRevisesLossToBalanced(t *testing.T) {
	a := newAuditor(t, 2, []int64{2, 1}, 10, 5)
	mustReport(t, a, Report{Stage: 0, Bucket: 0, Seq: 1, In: 10, Out: 20, Drop: 0})
	mustReport(t, a, Report{Stage: 1, Bucket: 0, Seq: 1, In: 18, Out: 18, Drop: 0})
	v := logVerdict(t, a, 0)
	if v.Category != Loss || v.Delta != 2 || v.Revision != 0 {
		t.Fatalf("期望首次裁决丢失差额 2 修订 0，得到 %+v", v)
	}
	// 迟到上报（序号更大）修正阶段 1 收入，丢失变平衡。
	mustReport(t, a, Report{Stage: 1, Bucket: 0, Seq: 2, In: 20, Out: 20, Drop: 0})
	v = logVerdict(t, a, 0)
	if v.Category != Balanced || v.Revision != 1 {
		t.Fatalf("期望修订为平衡且修订号 1，得到 %+v", v)
	}
	// 与按最新上报重新推演的结果一致。
	fresh := newAuditor(t, 2, []int64{2, 1}, 10, 5)
	mustReport(t, fresh, Report{Stage: 0, Bucket: 0, Seq: 1, In: 10, Out: 20, Drop: 0})
	mustReport(t, fresh, Report{Stage: 1, Bucket: 0, Seq: 2, In: 20, Out: 20, Drop: 0})
	fv := logVerdict(t, fresh, 0)
	if fv.Category != v.Category || fv.Stage != v.Stage || fv.Hop != v.Hop || fv.Delta != v.Delta {
		t.Fatalf("迟到修订结果 %+v 与重新推演 %+v 不一致", v, fv)
	}
}

// 齐报触发结算：K 个阶段都上报过即结算，无需水位。
func TestSettleByAllStagesReported(t *testing.T) {
	a := newAuditor(t, 2, []int64{1, 1}, 10, 5)
	mustReport(t, a, Report{Stage: 0, Bucket: 3, Seq: 1, In: 5, Out: 5, Drop: 0})
	if v := a.Verdict(3); v.Category != Pending || v.Settled {
		t.Fatalf("结算前应待定，得到 %+v", v)
	}
	mustReport(t, a, Report{Stage: 1, Bucket: 3, Seq: 1, In: 5, Out: 5, Drop: 0})
	v := logVerdict(t, a, 3)
	if !v.Settled || v.Category != Balanced {
		t.Fatalf("齐报应触发结算为平衡，得到 %+v", v)
	}
}

// 水位触发结算：观察水位 ≥ 桶右端+宽限，即使阶段未齐。
func TestSettleByWatermark(t *testing.T) {
	a := newAuditor(t, 2, []int64{1, 1}, 10, 5)
	mustReport(t, a, Report{Stage: 0, Bucket: 0, Seq: 1, In: 5, Out: 5, Drop: 0})
	if v := a.Verdict(0); v.Category != Pending {
		t.Fatalf("水位未到时应待定，得到 %+v", v)
	}
	// 桶 0 右端 10，宽限 5，水位 14 不结算，15 结算。
	if err := a.AdvanceWatermark(14); err != nil {
		t.Fatalf("推进水位失败: %v", err)
	}
	if v := a.Verdict(0); v.Category != Pending {
		t.Fatalf("水位 14 应仍待定，得到 %+v", v)
	}
	if err := a.AdvanceWatermark(15); err != nil {
		t.Fatalf("推进水位失败: %v", err)
	}
	v := logVerdict(t, a, 0)
	if !v.Settled || v.Category != MissingReport || v.Stage != 1 {
		t.Fatalf("水位 15 应结算为阶段 1 缺报，得到 %+v", v)
	}
}

// 各类拒绝原因可区分，且被拒绝的操作不改变任何状态。
func TestRejectReasons(t *testing.T) {
	a := newAuditor(t, 2, []int64{1, 1}, 10, 5)
	mustReport(t, a, Report{Stage: 0, Bucket: 0, Seq: 2, In: 5, Out: 5, Drop: 0})
	cases := []struct {
		name string
		r    Report
		want error
	}{
		{"阶段越界", Report{Stage: 2, Bucket: 0, Seq: 1}, ErrStageOutOfRange},
		{"负阶段", Report{Stage: -1, Bucket: 0, Seq: 1}, ErrStageOutOfRange},
		{"桶号为负", Report{Stage: 0, Bucket: -1, Seq: 1}, ErrNegativeBucket},
		{"计数为负", Report{Stage: 0, Bucket: 0, Seq: 3, In: -1}, ErrNegativeCount},
		{"序号非正", Report{Stage: 0, Bucket: 0, Seq: 0}, ErrNonPositiveSeq},
		{"序号过旧", Report{Stage: 0, Bucket: 0, Seq: 1}, ErrStaleSeq},
	}
	for _, c := range cases {
		err := a.Report(c.r)
		t.Logf("输入上报(应被拒): %+v → 原因: %v", c.r, err)
		if !errors.Is(err, c.want) {
			t.Fatalf("%s: 期望 %v，得到 %v", c.name, c.want, err)
		}
	}
	// 多种违规同时存在时只报第一个（按既定顺序）。
	err := a.Report(Report{Stage: 9, Bucket: -1, Seq: 0, In: -1})
	if !errors.Is(err, ErrStageOutOfRange) {
		t.Fatalf("应优先报阶段越界，得到 %v", err)
	}
	// 被拒绝的操作不改变采用值：桶 0 阶段 0 仍是序号 2 的值。
	mustReport(t, a, Report{Stage: 1, Bucket: 0, Seq: 1, In: 5, Out: 5, Drop: 0})
	v := logVerdict(t, a, 0)
	if v.Category != Balanced || v.Revision != 0 {
		t.Fatalf("拒绝不应改变状态，期望平衡修订 0，得到 %+v", v)
	}
	// 水位回退整体拒绝。
	if err := a.AdvanceWatermark(20); err != nil {
		t.Fatalf("推进水位失败: %v", err)
	}
	if err := a.AdvanceWatermark(19); !errors.Is(err, ErrWatermarkRegression) {
		t.Fatalf("期望 ErrWatermarkRegression，得到 %v", err)
	}
	if a.Watermark() != 20 {
		t.Fatalf("回退被拒后水位应保持 20，得到 %d", a.Watermark())
	}
}

// 上报到达次序打乱后，同一批上报得到的最终采用值与裁决完全相同（修订号可不同）。
func TestShuffledArrivalSameFinalState(t *testing.T) {
	reports := []Report{
		{Stage: 0, Bucket: 0, Seq: 1, In: 8, Out: 16, Drop: 0},
		{Stage: 0, Bucket: 0, Seq: 2, In: 10, Out: 20, Drop: 0},
		{Stage: 1, Bucket: 0, Seq: 1, In: 19, Out: 19, Drop: 0},
		{Stage: 1, Bucket: 0, Seq: 3, In: 20, Out: 20, Drop: 0},
		{Stage: 1, Bucket: 0, Seq: 2, In: 18, Out: 18, Drop: 0},
	}
	want := runScript(t, []int64{2, 1}, reports, 15)
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 20; trial++ {
		perm := rng.Perm(len(reports))
		shuffled := make([]Report, len(reports))
		for i, p := range perm {
			shuffled[i] = reports[p]
		}
		got := runScript(t, []int64{2, 1}, shuffled, 15)
		if got.Category != want.Category || got.Stage != want.Stage ||
			got.Hop != want.Hop || got.Delta != want.Delta {
			t.Fatalf("次序 %v 得到 %+v，期望 %+v", perm, got, want)
		}
	}
	t.Logf("20 种打乱次序最终裁决一致: %s（%s）", want.Category, want.Reason)
}

func runScript(t *testing.T, fanout []int64, reports []Report, watermark int64) Verdict {
	t.Helper()
	a := newAuditor(t, len(fanout), fanout, 10, 5)
	for _, r := range reports {
		if err := a.Report(r); err != nil && !errors.Is(err, ErrStaleSeq) {
			t.Fatalf("上报被拒绝: %v", err)
		}
	}
	if err := a.AdvanceWatermark(watermark); err != nil {
		t.Fatalf("推进水位失败: %v", err)
	}
	return a.Verdict(0)
}

// 相同操作序列重放结果完全相同（含修订号）。
func TestReplayDeterministic(t *testing.T) {
	play := func() Verdict {
		a := newAuditor(t, 2, []int64{2, 1}, 10, 5)
		ops := []func() error{
			func() error { return a.Report(Report{Stage: 0, Bucket: 0, Seq: 1, In: 10, Out: 20, Drop: 0}) },
			func() error { return a.Report(Report{Stage: 1, Bucket: 0, Seq: 1, In: 18, Out: 18, Drop: 0}) },
			func() error { return a.AdvanceWatermark(15) },
			func() error { return a.Report(Report{Stage: 1, Bucket: 0, Seq: 2, In: 20, Out: 20, Drop: 0}) },
			func() error { return a.Report(Report{Stage: 0, Bucket: 0, Seq: 1, In: 1, Out: 1, Drop: 1}) }, // 过旧被拒
		}
		for i, op := range ops {
			if err := op(); err != nil {
				t.Logf("操作 %d 被拒: %v", i, err)
			}
		}
		return a.Verdict(0)
	}
	first := play()
	for i := 0; i < 5; i++ {
		got := play()
		if got != first {
			t.Fatalf("重放 %d 结果 %+v 与首次 %+v 不一致", i, got, first)
		}
	}
	t.Logf("重放一致: %s 修订 %d（%s）", first.Category, first.Revision, first.Reason)
}

// 并发调用上报、推进水位与查询：竞态检测下无数据竞争，结果可判定。
func TestConcurrentAccess(t *testing.T) {
	a := newAuditor(t, 3, []int64{2, 2, 1}, 10, 5)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 1; i <= 50; i++ {
				r := Report{
					Stage:  g % 3,
					Bucket: int64(g % 4),
					Seq:    int64(g*1000 + i),
					In:     int64(i), Out: int64(i), Drop: 0,
				}
				_ = a.Report(r)
				_ = a.AdvanceWatermark(int64(i))
				_ = a.Verdict(int64(g % 4))
			}
		}(g)
	}
	wg.Wait()
	for b := int64(0); b < 4; b++ {
		v := logVerdict(t, a, b)
		if !v.Settled {
			t.Fatalf("桶 %d 应已结算", b)
		}
	}
}

// 多桶互不影响：同一审计器内各桶独立裁决。
func TestMultipleBucketsIndependent(t *testing.T) {
	a := newAuditor(t, 2, []int64{1, 1}, 10, 5)
	mustReport(t, a, Report{Stage: 0, Bucket: 0, Seq: 1, In: 5, Out: 5, Drop: 0})
	mustReport(t, a, Report{Stage: 1, Bucket: 0, Seq: 1, In: 5, Out: 5, Drop: 0})
	mustReport(t, a, Report{Stage: 0, Bucket: 1, Seq: 1, In: 5, Out: 5, Drop: 0})
	mustReport(t, a, Report{Stage: 1, Bucket: 1, Seq: 1, In: 4, Out: 4, Drop: 0})
	if v := logVerdict(t, a, 0); v.Category != Balanced {
		t.Fatalf("桶 0 期望平衡，得到 %+v", v)
	}
	if v := logVerdict(t, a, 1); v.Category != Loss || v.Delta != 1 {
		t.Fatalf("桶 1 期望丢失差额 1，得到 %+v", v)
	}
}

// 配置校验。
func TestConfigValidation(t *testing.T) {
	if _, err := New(Config{Stages: 0, BucketWidth: 10}); err == nil {
		t.Fatal("阶段数 0 应报错")
	}
	if _, err := New(Config{Stages: 2, Fanout: []int64{1}, BucketWidth: 10}); err == nil {
		t.Fatal("展开倍数长度不一致应报错")
	}
	if _, err := New(Config{Stages: 1, Fanout: []int64{0}, BucketWidth: 10}); err == nil {
		t.Fatal("展开倍数 0 应报错")
	}
	if _, err := New(Config{Stages: 1, Fanout: []int64{1}, BucketWidth: 0}); err == nil {
		t.Fatal("桶宽 0 应报错")
	}
	if _, err := New(Config{Stages: 1, Fanout: []int64{1}, BucketWidth: 10, Grace: -1}); err == nil {
		t.Fatal("负宽限应报错")
	}
}

// 示例：打印一次完整审计过程。
func ExampleAuditor() {
	a, _ := New(Config{Stages: 2, Fanout: []int64{2, 1}, BucketWidth: 10, Grace: 5})
	_ = a.Report(Report{Stage: 0, Bucket: 0, Seq: 1, In: 10, Out: 20, Drop: 0})
	_ = a.Report(Report{Stage: 1, Bucket: 0, Seq: 1, In: 19, Out: 19, Drop: 0})
	v := a.Verdict(0)
	fmt.Printf("裁决=%s 跳=%d 差额=%d 修订=%d\n", v.Category, v.Hop, v.Delta, v.Revision)
	_ = a.Report(Report{Stage: 1, Bucket: 0, Seq: 2, In: 20, Out: 20, Drop: 0})
	v = a.Verdict(0)
	fmt.Printf("裁决=%s 修订=%d\n", v.Category, v.Revision)
	// Output:
	// 裁决=丢失 跳=0 差额=1 修订=0
	// 裁决=平衡 修订=1
}
