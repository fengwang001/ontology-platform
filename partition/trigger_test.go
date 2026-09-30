package partition

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func newTestTrigger(t *testing.T, buf *bytes.Buffer) *Trigger {
	t.Helper()
	var log Logger
	if buf != nil {
		log = testLog{buf}
	}
	tr, err := New(Options{Writers: 2, Period: 10, AllowedRetries: 3, Lateness: 0, Log: log})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return tr
}

type testLog struct{ b *bytes.Buffer }

func (l testLog) Printf(format string, args ...any) {
	fmt.Fprintf(l.b, format+"\n", args...)
}

func state(tr *Trigger, id int64) State {
	sp := tr.parts[id]
	if sp == nil {
		return ""
	}
	return sp.state
}

// 水位恰等于就绪边界：P=10、延迟 0，分区 0 阈值 10；
// 全局水位 9 未就绪，10 恰好就绪。
func TestWatermarkExactBoundary(t *testing.T) {
	var buf bytes.Buffer
	tr := newTestTrigger(t, &buf)

	if id, err := tr.Write(0, 3); err != nil || id != 0 {
		t.Fatalf("write: id=%d err=%v", id, err)
	}
	if got := state(tr, 0); got != StateWaiting {
		t.Fatalf("want waiting, got %s", got)
	}

	if _, ok, err := tr.AdvanceWatermark(0, 9); err != nil || ok {
		t.Fatalf("first report: ok=%v err=%v", ok, err)
	}
	if got := state(tr, 0); got != StateWaiting {
		t.Fatalf("global absent => want waiting, got %s", got)
	}
	if gw, ok, err := tr.AdvanceWatermark(1, 9); err != nil || !ok || gw != 9 {
		t.Fatalf("global=9 exists after both reported: gw=%d ok=%v err=%v", gw, ok, err)
	}
	if got := state(tr, 0); got != StateWaiting {
		t.Fatalf("global 9 < threshold 10: want waiting, got %s", got)
	}

	if gw, ok, err := tr.AdvanceWatermark(0, 10); err != nil || !ok || gw != 9 {
		t.Fatalf("min stays 9: gw=%d ok=%v err=%v", gw, ok, err)
	}
	if got := state(tr, 0); got != StateWaiting {
		t.Fatalf("min(10,9)=9: want waiting, got %s", got)
	}
	if gw, ok, err := tr.AdvanceWatermark(1, 10); err != nil || !ok || gw != 10 {
		t.Fatalf("global=10: gw=%d ok=%v err=%v", gw, ok, err)
	}
	if got := state(tr, 0); got != StateReady {
		t.Fatalf("global == threshold 10: want ready, got %s", got)
	}
	if step, ok, err := tr.ExecutableStep(0); err != nil || !ok || step != StepRegisterMetadata {
		t.Fatalf("executable step: step=%d ok=%v err=%v", step, ok, err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("threshold=10")) {
		t.Fatalf("log missing decision basis:\n%s", buf.String())
	}
}

// 结束使水位跳到无穷大：最后一个 writer finish 后全局水位为 Infinity，
// 已创建分区全部就绪。
func TestFinishJumpsToInfinity(t *testing.T) {
	var buf bytes.Buffer
	tr := newTestTrigger(t, &buf)
	for _, tm := range []int64{5, 15, 25} {
		if _, err := tr.Write(0, tm); err != nil {
			t.Fatal(err)
		}
	}
	if gw, ok, err := tr.Finish(1); err != nil || ok {
		t.Fatalf("one writer still unreported: gw=%d ok=%v err=%v", gw, ok, err)
	}
	if gw, ok, err := tr.Finish(0); err != nil || !ok || gw != Infinity {
		t.Fatalf("finish: gw=%d ok=%v err=%v", gw, ok, err)
	}
	for id := int64(0); id < 3; id++ {
		if got := state(tr, id); got != StateReady {
			t.Fatalf("partition %d want ready, got %s", id, got)
		}
	}
	// 已结束 writer 拒绝写入、上报水位与重复结束。
	if _, err := tr.Write(1, 0); !errors.Is(err, ErrWriterFinished) {
		t.Fatalf("write finished writer: %v", err)
	}
	if _, _, err := tr.AdvanceWatermark(1, Infinity); !errors.Is(err, ErrWriterFinished) {
		t.Fatalf("advance finished writer: %v", err)
	}
	if _, _, err := tr.Finish(1); !errors.Is(err, ErrAlreadyFinished) {
		t.Fatalf("repeat finish: %v", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("watermark=+Inf")) {
		t.Fatalf("log missing infinity decision:\n%s", buf.String())
	}
}

func readyAll(tr *Trigger) {
	for id, sp := range tr.parts {
		_ = id
		if sp.state == StateWaiting {
			sp.state = StateReady
		}
	}
}

// 较小分区失败（提交失败）阻塞较大分区的第一步；
// 重置并完成后自动放行，提交严格按升序进行。
func TestSmallerFailureBlocksLarger(t *testing.T) {
	var buf bytes.Buffer
	tr := newTestTrigger(t, &buf)
	if _, err := tr.Write(0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Write(0, 10); err != nil {
		t.Fatal(err)
	}
	readyAll(tr)

	// 分区 1 就绪但被分区 0 阻塞。
	if _, ok, err := tr.ExecutableStep(1); err != nil || ok {
		t.Fatalf("p1 must be blocked by p0: ok=%v err=%v", ok, err)
	}
	if _, err := tr.ReportStep(1, StepRegisterMetadata, true); !errors.Is(err, ErrBlocked) {
		t.Fatalf("report on blocked partition: %v", err)
	}

	// 分区 0 完成第一步后第二步连续失败 R 次 -> 提交失败，仍阻塞。
	if _, err := tr.ReportStep(0, StepRegisterMetadata, true); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := tr.ReportStep(0, StepSuccessMarker, false); err != nil {
			t.Fatal(err)
		}
	}
	if got := state(tr, 0); got != StateCommitFailed {
		t.Fatalf("p0 want commit_failed, got %s", got)
	}
	if _, err := tr.ReportStep(1, StepRegisterMetadata, true); !errors.Is(err, ErrBlocked) {
		t.Fatalf("commit_failed smaller still blocks: %v", err)
	}

	// 人工重置：回到就绪、失败次数清零、从第一步重来。
	snap, err := tr.Reset(0)
	if err != nil || snap.State != StateReady || snap.Failures != 0 {
		t.Fatalf("reset: %+v err=%v", snap, err)
	}
	if _, err := tr.ReportStep(0, StepRegisterMetadata, true); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.ReportStep(0, StepSuccessMarker, true); err != nil {
		t.Fatal(err)
	}
	if got := state(tr, 0); got != StateCommitted {
		t.Fatalf("p0 want committed, got %s", got)
	}
	// 放行后分区 1 才能开始。
	if step, ok, err := tr.ExecutableStep(1); err != nil || !ok || step != StepRegisterMetadata {
		t.Fatalf("p1 unblocked: step=%d ok=%v err=%v", step, ok, err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("reason=blocked")) {
		t.Fatalf("log missing blocking decision:\n%s", buf.String())
	}
}

// 已提交后迟到写入：转待补提交，该轮只有写成功标记；
// 多次写入合并同一轮；补提交期间写入不改状态；补完版本加一。
func TestLateWritePatchRound(t *testing.T) {
	var buf bytes.Buffer
	tr := newTestTrigger(t, &buf)
	if _, err := tr.Write(0, 0); err != nil {
		t.Fatal(err)
	}
	readyAll(tr)
	if _, err := tr.ReportStep(0, StepRegisterMetadata, true); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.ReportStep(0, StepSuccessMarker, true); err != nil {
		t.Fatal(err)
	}
	if got := state(tr, 0); got != StateCommitted || tr.parts[0].ver != 1 {
		t.Fatalf("initial commit: state=%s ver=%d", got, tr.parts[0].ver)
	}

	// 第一次迟到写入 -> pending_patch，计数 +1。
	if id, err := tr.Write(1, 9); err != nil || id != 0 {
		t.Fatalf("late write: id=%d err=%v", id, err)
	}
	if got := state(tr, 0); got != StatePendingPatch {
		t.Fatalf("want pending_patch, got %s", got)
	}
	// 补提交完成前再写两次：合并在同一轮，仅计数增加。
	if _, err := tr.Write(0, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Write(0, 2); err != nil {
		t.Fatal(err)
	}
	if tr.parts[0].count != 4 {
		t.Fatalf("merged count want 4, got %d", tr.parts[0].count)
	}
	// 该轮唯一可执行步骤是写成功标记；报告登记元数据步骤不符。
	if step, ok, err := tr.ExecutableStep(0); err != nil || !ok || step != StepSuccessMarker {
		t.Fatalf("patch executable step: step=%d ok=%v err=%v", step, ok, err)
	}
	if _, err := tr.ReportStep(0, StepRegisterMetadata, true); !errors.Is(err, ErrWrongStep) {
		t.Fatalf("register step in patch round: %v", err)
	}
	if snap, err := tr.ReportStep(0, StepSuccessMarker, true); err != nil {
		t.Fatalf("patch success: %v", err)
	} else if snap.State != StateCommitted || snap.Version != 2 {
		t.Fatalf("after patch: %+v", snap)
	}
	if !bytes.Contains(buf.Bytes(), []byte("patch round complete")) {
		t.Fatalf("log missing patch decision:\n%s", buf.String())
	}
}

// 分区尚未提交完成（进行中）时的写入只增条数、状态不变。
func TestWriteDuringInProgressKeepsState(t *testing.T) {
	tr := newTestTrigger(t, nil)
	if _, err := tr.Write(0, 0); err != nil {
		t.Fatal(err)
	}
	readyAll(tr)
	if _, err := tr.ReportStep(0, StepRegisterMetadata, true); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Write(1, 5); err != nil {
		t.Fatal(err)
	}
	if got := state(tr, 0); got != StateInProgress || tr.parts[0].count != 2 {
		t.Fatalf("state=%s count=%d", got, tr.parts[0].count)
	}
}

// 拒绝原因必须可区分，且按规定次序判定；被拒绝操作不得改变状态。
func TestRejectionOrdering(t *testing.T) {
	tr := newTestTrigger(t, nil)

	// 写入：越界 > 负时间 > 已结束（题目规定次序）。
	if _, err := tr.Write(5, -1); !errors.Is(err, ErrWriterOutOfRange) {
		t.Fatalf("write: %v", err)
	}
	if _, err := tr.Write(0, -1); !errors.Is(err, ErrNegativeEventTime) {
		t.Fatalf("negative time: %v", err)
	}
	if _, _, err := tr.Finish(0); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Write(0, -1); !errors.Is(err, ErrNegativeEventTime) {
		t.Fatalf("negative precedes finished: %v", err)
	}
	if _, err := tr.Write(0, 0); !errors.Is(err, ErrWriterFinished) {
		t.Fatalf("finished writer rejected: %v", err)
	}

	// 水位上报：越界 > 已结束 > 回退。
	if _, _, err := tr.AdvanceWatermark(9, 0); !errors.Is(err, ErrWriterOutOfRange) {
		t.Fatalf("advance range: %v", err)
	}
	if _, _, err := tr.AdvanceWatermark(0, 1); !errors.Is(err, ErrWriterFinished) {
		t.Fatalf("advance finished: %v", err)
	}
	if _, _, err := tr.AdvanceWatermark(1, 5); err != nil {
		t.Fatal(err)
	}
	if _, _, err := tr.AdvanceWatermark(1, 4); !errors.Is(err, ErrWatermarkRegressed) {
		t.Fatalf("regress: %v", err)
	}

	// 步骤上报：不存在 > 未就绪 > 被阻塞 > 已提交/提交失败 > 步骤不符。
	if _, err := tr.ReportStep(99, 0, true); !errors.Is(err, ErrPartitionNotFound) {
		t.Fatalf("report missing: %v", err)
	}
	if _, err := tr.Write(1, 0); err != nil { // 建分区 0，仍 waiting（全局水位不存在）
		t.Fatal(err)
	}
	if _, err := tr.ReportStep(0, 0, true); !errors.Is(err, ErrNotReady) {
		t.Fatalf("not ready: %v", err)
	}

	tr2 := newTestTrigger(t, nil)
	_, _ = tr2.Write(0, 0)
	_, _ = tr2.Write(0, 10)
	readyAll(tr2)
	if _, err := tr2.ReportStep(1, 0, true); !errors.Is(err, ErrBlocked) {
		t.Fatalf("blocked precedence: %v", err)
	}
	_, _ = tr2.ReportStep(0, 0, true)
	_, _ = tr2.ReportStep(0, 1, true)
	if _, err := tr2.ReportStep(0, 1, true); !errors.Is(err, ErrAlreadyCommitted) {
		t.Fatalf("committed: %v", err)
	}
	tr2.parts[1].state = StateCommitFailed
	if _, err := tr2.ReportStep(1, 0, true); !errors.Is(err, ErrCommitFailed) {
		t.Fatalf("commit_failed: %v", err)
	}
	tr2.parts[1].state = StateReady
	if _, err := tr2.ReportStep(1, 1, true); !errors.Is(err, ErrWrongStep) {
		t.Fatalf("wrong step: %v", err)
	}

	// 重置：不存在 > 不在提交失败。
	if _, err := tr.Reset(99); !errors.Is(err, ErrPartitionNotFound) {
		t.Fatalf("reset missing: %v", err)
	}
	if _, err := tr.Reset(0); !errors.Is(err, ErrNotInCommitFailed) {
		t.Fatalf("reset not failed: %v", err)
	}

	// 被拒绝的水位回退不得改变已上报水位（tr 中 writer1 已上报 5）。
	if tr.watermark[1] != 5 || !tr.finished[0] {
		t.Fatalf("rejected op mutated state: wm=%v finished=%v", tr.watermark, tr.finished)
	}
}

// 补提交轮失败：计数独立累计；重置后回到待补提交、只补标记、版本保留。
func TestPatchFailureReset(t *testing.T) {
	tr := newTestTrigger(t, nil)
	_, _ = tr.Write(0, 0)
	readyAll(tr)
	_, _ = tr.ReportStep(0, 0, true)
	_, _ = tr.ReportStep(0, 1, true)
	if _, err := tr.Write(0, 1); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		_, _ = tr.ReportStep(0, StepSuccessMarker, false)
	}
	if got := state(tr, 0); got != StateCommitFailed {
		t.Fatalf("patch commit_failed: %s", got)
	}
	snap, err := tr.Reset(0)
	if err != nil {
		t.Fatal(err)
	}
	if snap.State != StatePendingPatch || snap.Version != 1 || snap.Failures != 0 {
		t.Fatalf("reset to patch start: %+v", snap)
	}
	if _, err := tr.ReportStep(0, StepRegisterMetadata, true); !errors.Is(err, ErrWrongStep) {
		t.Fatalf("patch restart only has marker step: %v", err)
	}
	snap, err = tr.ReportStep(0, StepSuccessMarker, true)
	if err != nil || snap.State != StateCommitted || snap.Version != 2 {
		t.Fatalf("patch complete: %+v err=%v", snap, err)
	}
}

// 并发调用：多 writer 并发写入/上报水位，状态保持自洽。
func TestConcurrent(t *testing.T) {
	tr := newTestTrigger(t, nil)
	const n = 200
	var wg sync.WaitGroup
	for w := 0; w < 2; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < n; i++ {
				if _, err := tr.Write(w, int64((i%5)*10)); err != nil {
					t.Errorf("write: %v", err)
					return
				}
				if _, _, err := tr.AdvanceWatermark(w, int64(i)); err != nil {
					t.Errorf("advance: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	var total int64
	for _, snap := range tr.SnapshotPartitions() {
		total += snap.Count
	}
	if total != int64(2*n) {
		t.Fatalf("lost writes: total=%d want=%d", total, 2*n)
	}
	// 全部水位推进到 n-1：阈值不超过该值的分区必须已就绪或更靠后。
	gw, ok := tr.GlobalWatermark()
	if !ok || gw != int64(n-1) {
		t.Fatalf("global=%d ok=%v", gw, ok)
	}
	for _, snap := range tr.SnapshotPartitions() {
		if gw >= (snap.ID+1)*10 && snap.State == StateWaiting {
			t.Fatalf("partition %d should be ready, still waiting", snap.ID)
		}
	}
}

func TestInvalidConfig(t *testing.T) {
	for _, o := range []Options{
		{Writers: 0, Period: 10, AllowedRetries: 3},
		{Writers: 2, Period: 0, AllowedRetries: 3},
		{Writers: 2, Period: 10, AllowedRetries: 0},
		{Writers: 2, Period: 10, AllowedRetries: 3, Lateness: -1},
	} {
		if _, err := New(o); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("config %+v: %v", o, err)
		}
	}
}

// 延迟参数参与就绪判定：阈值 = (k+1)P + lateness。
func TestLatenessBoundary(t *testing.T) {
	tr, err := New(Options{Writers: 1, Period: 10, AllowedRetries: 2, Lateness: 3})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Write(0, 0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := tr.AdvanceWatermark(0, 12); err != nil {
		t.Fatal(err)
	}
	if got := state(tr, 0); got != StateWaiting {
		t.Fatalf("12 < threshold 13: got %s", got)
	}
	if _, _, err := tr.AdvanceWatermark(0, 13); err != nil {
		t.Fatal(err)
	}
	if got := state(tr, 0); got != StateReady {
		t.Fatalf("13 == threshold: got %s", got)
	}
}

// 结束与可执行步骤查询的边界错误。
func TestQueryAndFinishEdges(t *testing.T) {
	tr := newTestTrigger(t, nil)
	if _, _, err := tr.Finish(-1); !errors.Is(err, ErrWriterOutOfRange) {
		t.Fatalf("finish range: %v", err)
	}
	if _, _, err := tr.ExecutableStep(7); !errors.Is(err, ErrPartitionNotFound) {
		t.Fatalf("executable missing: %v", err)
	}
	_, _ = tr.Write(0, 0)
	if step, ok, err := tr.ExecutableStep(0); err != nil || ok || step != 0 {
		t.Fatalf("waiting partition not executable: step=%d ok=%v err=%v", step, ok, err)
	}
	readyAll(tr)
	_, _ = tr.ReportStep(0, StepRegisterMetadata, true)
	if step, ok, err := tr.ExecutableStep(0); err != nil || !ok || step != StepSuccessMarker {
		t.Fatalf("in_progress step: step=%d ok=%v err=%v", step, ok, err)
	}
	_, _ = tr.ReportStep(0, StepSuccessMarker, true)
	if _, ok, err := tr.ExecutableStep(0); err != nil || ok {
		t.Fatalf("committed has no executable step: ok=%v err=%v", ok, err)
	}
	// 升序快照。
	_, _ = tr.Write(0, 20)
	ids := []int64{}
	for _, s := range tr.SnapshotPartitions() {
		ids = append(ids, s.ID)
	}
	if len(ids) != 2 || ids[0] != 0 || ids[1] != 2 {
		t.Fatalf("snapshot order: %v", ids)
	}
}
