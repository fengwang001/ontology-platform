package audit

import (
	"errors"
	"sync"
	"testing"
)

// logf 统一打印输入、输出与判定依据，便于人工核对裁决过程。
func logf(t *testing.T, format string, args ...any) {
	t.Helper()
	t.Logf("[判定依据] "+format, args...)
}

func mustNew(t *testing.T, k int, width, grace int64, fanout []int64) *Auditor {
	t.Helper()
	a, err := New(k, width, grace, fanout)
	if err != nil {
		t.Fatalf("New 输入 k=%d width=%d grace=%d fanout=%v -> 非预期错误 %v", k, width, grace, fanout, err)
	}
	return a
}

func send(t *testing.T, a *Auditor, r Report) Verdict {
	t.Helper()
	v, err := a.Report(r)
	if err != nil {
		t.Fatalf("Report 输入 %+v -> 非预期拒绝 %v", r, err)
	}
	logf(t, "输入上报 %+v => 输出裁决 %s(stage=%d,diff=%d)", r, v.Category, v.Stage, v.Diff)
	return v
}

func sendReject(t *testing.T, a *Auditor, r Report, want error) {
	t.Helper()
	v, err := a.Report(r)
	if !errors.Is(err, want) {
		t.Fatalf("Report 输入 %+v => 期望拒绝 %v, 实际 err=%v verdict=%+v", r, want, err, v)
	}
	logf(t, "输入上报 %+v => 输出拒绝原因 %v（符合预期）", r, err)
}

// TestFanoutBalance 展开倍数下的守恒与不守恒。
func TestFanoutBalance(t *testing.T) {
	// f=[2,1]：阶段0收入10须发出20；阶段1收入20发出20即平衡。
	a := mustNew(t, 2, 10, 0, []int64{2, 1})
	send(t, a, Report{Stage: 0, Bucket: 0, Seq: 1, Input: 10, Output: 20, Dropped: 0})
	v := send(t, a, Report{Stage: 1, Bucket: 0, Seq: 1, Input: 20, Output: 20, Dropped: 0})
	if v.Category != Balanced {
		t.Fatalf("展开倍数守恒: 期望 balanced, 实际 %s", v.Category)
	}

	// 收入*f 与 发出+丢弃 不等（10*2 != 15+0），且先于任何跳检查。
	b := mustNew(t, 2, 10, 0, []int64{2, 1})
	send(t, b, Report{Stage: 0, Bucket: 0, Seq: 1, Input: 10, Output: 15, Dropped: 0})
	send(t, b, Report{Stage: 1, Bucket: 0, Seq: 1, Input: 15, Output: 15, Dropped: 0})
	st, _ := b.Get(0)
	if st.Verdict.Category != Unbalanced || st.Verdict.Stage != 0 {
		t.Fatalf("展开倍数不守恒: 期望 unbalanced@0, 实际 %+v", st.Verdict)
	}
	logf(t, "展开倍数: 10*2 != 15+0 => unbalanced stage=0")
}

// TestLostAndDuplicatedDirections 丢失与重复各自的方向。
func TestLostAndDuplicatedDirections(t *testing.T) {
	// 丢失方向：下游收入 18 < 上游发出 20，差额 2。
	lost := mustNew(t, 2, 10, 0, []int64{1, 1})
	send(t, lost, Report{Stage: 0, Bucket: 0, Seq: 1, Input: 20, Output: 20, Dropped: 0})
	v := send(t, lost, Report{Stage: 1, Bucket: 0, Seq: 1, Input: 18, Output: 18, Dropped: 0})
	if v.Category != Lost || v.Stage != 0 || v.Diff != 2 {
		t.Fatalf("丢失方向: 期望 lost@0 diff=2, 实际 %+v", v)
	}

	// 重复方向：下游收入 23 > 上游发出 20，差额 3。
	dup := mustNew(t, 2, 10, 0, []int64{1, 1})
	send(t, dup, Report{Stage: 0, Bucket: 0, Seq: 1, Input: 20, Output: 20, Dropped: 0})
	v = send(t, dup, Report{Stage: 1, Bucket: 0, Seq: 1, Input: 23, Output: 23, Dropped: 0})
	if v.Category != Duplicated || v.Stage != 0 || v.Diff != 3 {
		t.Fatalf("重复方向: 期望 duplicated@0 diff=3, 实际 %+v", v)
	}
	logf(t, "跳0: 下游<上游 => lost diff=2；下游>上游 => duplicated diff=3")
}

// TestMissingSuppressedByEarlierViolation 缺报被更靠前的违规压过。
func TestMissingSuppressedByEarlierViolation(t *testing.T) {
	// 阶段0自身不守恒，阶段2从未上报：应判 unbalanced@0 而非 missing@2。
	a := mustNew(t, 3, 10, 0, []int64{1, 1, 1})
	send(t, a, Report{Stage: 0, Bucket: 0, Seq: 1, Input: 10, Output: 9, Dropped: 0})
	send(t, a, Report{Stage: 1, Bucket: 0, Seq: 1, Input: 9, Output: 9, Dropped: 0})
	if err := a.AdvanceWatermark(10); err != nil {
		t.Fatalf("水位推进被拒: %v", err)
	}
	st, ok := a.Get(0)
	if !ok {
		t.Fatal("桶0 应当存在")
	}
	if st.Verdict.Category != Unbalanced || st.Verdict.Stage != 0 {
		t.Fatalf("期望更靠前的 unbalanced@0 压过 missing@2, 实际 %+v", st.Verdict)
	}
	logf(t, "阶段0 10 != 9+0 先于跳检查与缺报 => unbalanced@0，missing@2 被压过")

	// 反例：自身守恒、各跳平衡，仅阶段2缺报 => missing@2。
	b := mustNew(t, 3, 10, 0, []int64{1, 1, 1})
	send(t, b, Report{Stage: 0, Bucket: 0, Seq: 1, Input: 10, Output: 10, Dropped: 0})
	send(t, b, Report{Stage: 1, Bucket: 0, Seq: 1, Input: 10, Output: 10, Dropped: 0})
	if err := b.AdvanceWatermark(10); err != nil {
		t.Fatalf("水位推进被拒: %v", err)
	}
	st, _ = b.Get(0)
	if st.Verdict.Category != Missing || st.Verdict.Stage != 2 {
		t.Fatalf("期望 missing@2, 实际 %+v", st.Verdict)
	}
}

// TestStaleSeqRejected 乱序到达时旧序号被拒且不改变采用值。
func TestStaleSeqRejected(t *testing.T) {
	a := mustNew(t, 1, 10, 0, []int64{1})
	send(t, a, Report{Stage: 0, Bucket: 0, Seq: 5, Input: 7, Output: 7, Dropped: 0})
	// 更小序号与相等序号都必须被拒绝（序号不大于已采用者）。
	sendReject(t, a, Report{Stage: 0, Bucket: 0, Seq: 3, Input: 99, Output: 99, Dropped: 0}, ErrStaleSeq)
	sendReject(t, a, Report{Stage: 0, Bucket: 0, Seq: 5, Input: 99, Output: 99, Dropped: 0}, ErrStaleSeq)
	st, _ := a.Get(0)
	r := st.Readings[0]
	if r.Seq != 5 || r.Input != 7 || r.Output != 7 {
		t.Fatalf("旧上报不得改变采用值, 实际 %+v", r)
	}
	logf(t, "序号 3、5 均 <= 已采用序号 5 => stale，采用值保持 seq=5 input=7")
}

// TestValidationOrder 各参数错误按固定次序只报第一个，水位回退单独拒绝。
func TestValidationOrder(t *testing.T) {
	a := mustNew(t, 2, 10, 0, []int64{1, 1})
	sendReject(t, a, Report{Stage: 9, Bucket: -1, Seq: 0, Input: -1, Output: 0, Dropped: 0}, ErrStageOutOfRange)
	sendReject(t, a, Report{Stage: 0, Bucket: -1, Seq: 0, Input: -1, Output: 0, Dropped: 0}, ErrNegativeBucket)
	sendReject(t, a, Report{Stage: 0, Bucket: 0, Seq: 0, Input: -1, Output: 0, Dropped: 0}, ErrNegativeCount)
	sendReject(t, a, Report{Stage: 0, Bucket: 0, Seq: 0, Input: 1, Output: 1, Dropped: 0}, ErrNonPositiveSeq)

	if err := a.AdvanceWatermark(5); err != nil {
		t.Fatal(err)
	}
	if err := a.AdvanceWatermark(4); !errors.Is(err, ErrWatermarkRegression) {
		t.Fatalf("水位回退期望 %v, 实际 %v", ErrWatermarkRegression, err)
	}
	if got := a.Watermark(); got != 5 {
		t.Fatalf("被拒水位推进不得改变水位, 实际 %d", got)
	}
	logf(t, "拒绝次序: 阶段越界>负桶>负计数>非正序号；水位 5->4 拒绝且保持 5")
}

// TestLateReportRevisesLostToBalanced 结算后迟到上报把丢失修订为平衡，恰好计一次修订。
func TestLateReportRevisesLostToBalanced(t *testing.T) {
	a := mustNew(t, 2, 10, 0, []int64{1, 1})
	// 阶段1先以 seq=1 上报收入18；阶段0到达后两阶段齐报，判丢失差额2。
	send(t, a, Report{Stage: 1, Bucket: 0, Seq: 1, Input: 18, Output: 18, Dropped: 0})
	send(t, a, Report{Stage: 0, Bucket: 0, Seq: 1, Input: 20, Output: 20, Dropped: 0})
	st, _ := a.Get(0)
	if !st.Settled || st.Verdict.Category != Lost || st.Revision != 0 {
		t.Fatalf("齐报首次裁决: 期望 settled lost revision=0, 实际 %+v", st)
	}

	// 迟到的 seq=2 修订收入为20：丢失 -> 平衡，修订号 +1。
	v := send(t, a, Report{Stage: 1, Bucket: 0, Seq: 2, Input: 20, Output: 20, Dropped: 0})
	if v.Category != Balanced {
		t.Fatalf("迟到修订后期望 balanced, 实际 %s", v.Category)
	}
	st, _ = a.Get(0)
	if st.Revision != 1 {
		t.Fatalf("裁决变化应计一次修订, 实际 revision=%d", st.Revision)
	}

	// 再来 seq=3 但计数不变：裁决不变，不增加修订。
	send(t, a, Report{Stage: 1, Bucket: 0, Seq: 3, Input: 20, Output: 20, Dropped: 0})
	st, _ = a.Get(0)
	if st.Verdict.Category != Balanced || st.Revision != 1 {
		t.Fatalf("裁决不变不应计修订, 实际 %+v", st)
	}
	logf(t, "结算后迟到上报 lost(diff=2) -> balanced：revision 0 -> 1；裁决不变则不计修订")
}

// TestSettlementTriggers 水位结算与齐报结算两种触发，结算前为待定。
func TestSettlementTriggers(t *testing.T) {
	// 触发一：水位 >= 桶右端(10)+宽限(2) = 12。
	wm := mustNew(t, 2, 10, 2, []int64{1, 1})
	send(t, wm, Report{Stage: 0, Bucket: 0, Seq: 1, Input: 5, Output: 5, Dropped: 0})
	if st, _ := wm.Get(0); st.Settled || st.Verdict.Category != Pending {
		t.Fatalf("结算前应为 pending, 实际 %+v", st)
	}
	if err := wm.AdvanceWatermark(11); err != nil {
		t.Fatal(err)
	}
	if st, _ := wm.Get(0); st.Settled {
		t.Fatalf("水位 11 < 右端10+宽限2, 不应结算")
	}
	if err := wm.AdvanceWatermark(12); err != nil {
		t.Fatal(err)
	}
	st, _ := wm.Get(0)
	if !st.Settled || st.Verdict.Category != Missing || st.Verdict.Stage != 1 {
		t.Fatalf("水位到12应结算且缺报阶段1, 实际 %+v", st)
	}
	logf(t, "桶0右端=10, 宽限=2: 水位11不结算, 水位12结算 => missing@1")

	// 触发二：K 个阶段都上报即结算（先到者为准，无需水位）。
	full := mustNew(t, 2, 10, 100, []int64{1, 1})
	send(t, full, Report{Stage: 0, Bucket: 0, Seq: 1, Input: 5, Output: 5, Dropped: 0})
	v := send(t, full, Report{Stage: 1, Bucket: 0, Seq: 1, Input: 5, Output: 5, Dropped: 0})
	if v.Category != Balanced {
		t.Fatalf("齐报应立即结算为 balanced, 实际 %s", v.Category)
	}
	logf(t, "宽限虽为100，两阶段齐报先到 => 立即结算 balanced")
}

// TestArrivalOrderConfluence 同批上报打乱到达次序后，
// 最终采用值与裁决完全相同（修订号允许不同）。
func TestArrivalOrderConfluence(t *testing.T) {
	reports := []Report{
		{Stage: 1, Bucket: 0, Seq: 2, Input: 20, Output: 20, Dropped: 0},
		{Stage: 0, Bucket: 0, Seq: 1, Input: 20, Output: 20, Dropped: 0},
		// 旧序号：无论何时到达都应被拒绝。
		{Stage: 1, Bucket: 0, Seq: 1, Input: 18, Output: 18, Dropped: 0},
		{Stage: 0, Bucket: 1, Seq: 3, Input: 7, Output: 7, Dropped: 0},
	}

	run := func(order []int) BucketState {
		a := mustNew(t, 2, 10, 0, []int64{1, 1})
		for _, i := range order {
			r := reports[i]
			v, err := a.Report(r)
			logf(t, "重放次序%v 上报 %+v => v=%s err=%v", order, r, v.Category, err)
		}
		st, ok := a.Get(0)
		if !ok {
			t.Fatal("桶0 应存在")
		}
		return st
	}

	s1 := run([]int{0, 1, 2, 3})
	s2 := run([]int{2, 3, 1, 0})
	s3 := run([]int{1, 0, 3, 2})

	for _, s := range []BucketState{s2, s3} {
		if s.Verdict != s1.Verdict {
			t.Fatalf("裁决不一致: %+v vs %+v", s.Verdict, s1.Verdict)
		}
		if len(s.Readings) != len(s1.Readings) {
			t.Fatalf("采用值数量不一致: %d vs %d", len(s.Readings), len(s1.Readings))
		}
		for stage, r := range s1.Readings {
			if s.Readings[stage] != r {
				t.Fatalf("阶段%d 采用值不一致: %+v vs %+v", stage, s.Readings[stage], r)
			}
		}
	}
	if s1.Verdict.Category != Balanced {
		t.Fatalf("seq=2 修订后应为 balanced, 实际 %s", s1.Verdict.Category)
	}
	logf(t, "三种到达次序最终裁决均为 %s，采用值完全一致", s1.Verdict.Category)
}

// TestExactReplay 相同操作序列重放结果完全相同（含修订号）。
func TestExactReplay(t *testing.T) {
	ops := func(a *Auditor) {
		_, _ = a.Report(Report{Stage: 1, Bucket: 0, Seq: 1, Input: 18, Output: 18, Dropped: 0})
		_, _ = a.Report(Report{Stage: 0, Bucket: 0, Seq: 1, Input: 20, Output: 20, Dropped: 0})
		_ = a.AdvanceWatermark(10)
		_, _ = a.Report(Report{Stage: 1, Bucket: 0, Seq: 2, Input: 20, Output: 20, Dropped: 0})
		// 序号更新但计数制造新的不平衡，用于验证修订号同样可精确重放。
		_, _ = a.Report(Report{Stage: 0, Bucket: 0, Seq: 2, Input: 99, Output: 99, Dropped: 0})
	}

	a1 := mustNew(t, 2, 10, 2, []int64{1, 1})
	ops(a1)
	a2 := mustNew(t, 2, 10, 2, []int64{1, 1})
	ops(a2)

	b1 := a1.Buckets()
	b2 := a2.Buckets()
	if len(b1) != len(b2) {
		t.Fatalf("重放桶数不一致 %d vs %d", len(b1), len(b2))
	}
	for i := range b1 {
		x, y := b1[i], b2[i]
		if x.Bucket != y.Bucket || x.Settled != y.Settled || x.Revision != y.Revision || x.Verdict != y.Verdict {
			t.Fatalf("重放结果不一致: %+v vs %+v", x, y)
		}
	}
	logf(t, "相同序列重放：裁决=%s revision=%d 完全一致", b1[0].Verdict.Category, b1[0].Revision)
}

// TestConcurrentAccess 上报、推进水位与查询并发调用不产生数据竞争。
func TestConcurrentAccess(t *testing.T) {
	a := mustNew(t, 3, 4, 1, []int64{2, 1, 1})
	var wg sync.WaitGroup

	for s := 0; s < 3; s++ {
		wg.Add(1)
		go func(stage int) {
			defer wg.Done()
			for seq := int64(1); seq <= 50; seq++ {
				in := seq
				out := seq
				if stage == 0 {
					out = 2 * seq // f_0=2
					in = seq
				}
				_, _ = a.Report(Report{
					Stage: stage, Bucket: int64(seq % 4), Seq: seq,
					Input: in, Output: out, Dropped: 0,
				})
			}
		}(s)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for w := int64(0); w <= 30; w++ {
			_ = a.AdvanceWatermark(w)
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			_ = a.Buckets()
			_, _ = a.Get(int64(i % 5))
			_ = a.Watermark()
		}
	}()
	wg.Wait()

	states := a.Buckets()
	for _, st := range states {
		logf(t, "并发结束 桶=%d settled=%v 裁决=%s(stage=%d,diff=%d) revision=%d",
			st.Bucket, st.Settled, st.Verdict.Category, st.Verdict.Stage, st.Verdict.Diff, st.Revision)
	}
}
