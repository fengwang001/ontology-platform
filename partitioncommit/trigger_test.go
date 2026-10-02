package partitioncommit

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"
)

// 测试统一使用 P=10、W=2、延迟=5、R=2 的触发器，
// 分区 k 的就绪边界为 (k+1)*10+5。
func newTestTrigger() *Trigger {
	return NewTrigger(10, 2, 5, 2)
}

// logOp 打印一次操作的输入、输出与判定依据。
func logOp(t *testing.T, op, input string, err error, reason string) {
	t.Helper()
	out := "接受"
	if err != nil {
		out = "拒绝: " + err.Error()
	}
	t.Logf("操作=%s 输入=%s 输出=%s 判定依据=%s", op, input, out, reason)
}

func logState(t *testing.T, tr *Trigger, id int64) PartitionInfo {
	t.Helper()
	info, ok := tr.Partition(id)
	if !ok {
		t.Fatalf("分区 %d 应存在", id)
	}
	t.Logf("分区快照 id=%d 状态=%s 条数=%d 待执行步骤=%d 失败次数=%d 版本=%d",
		info.ID, info.State, info.Count, info.NextStep, info.Failures, info.Version)
	return info
}

func mustWrite(t *testing.T, tr *Trigger, subtask int, eventTime int64) {
	t.Helper()
	err := tr.Write(subtask, eventTime)
	logOp(t, "写入", fmt.Sprintf("子任务=%d 事件时间=%d", subtask, eventTime), err,
		fmt.Sprintf("事件时间归入分区 k=%d", eventTime/10))
	if err != nil {
		t.Fatalf("写入被拒绝: %v", err)
	}
}

func mustReportWM(t *testing.T, tr *Trigger, subtask int, wm int64) {
	t.Helper()
	err := tr.ReportWatermark(subtask, wm)
	logOp(t, "水位上报", fmt.Sprintf("子任务=%d 水位=%d", subtask, wm), err, "水位单调不减")
	if err != nil {
		t.Fatalf("水位上报被拒绝: %v", err)
	}
}

func mustStep(t *testing.T, tr *Trigger, id int64, step Step, success bool) {
	t.Helper()
	err := tr.ReportStep(id, step, success)
	logOp(t, "步骤上报", fmt.Sprintf("分区=%d 步骤=%s 成功=%v", id, step, success), err,
		"分区可执行且步骤与当前待执行步骤一致")
	if err != nil {
		t.Fatalf("步骤上报被拒绝: %v", err)
	}
}

func expectErr(t *testing.T, op, input, reason string, got, want error) {
	t.Helper()
	logOp(t, op, input, got, reason)
	if !errors.Is(got, want) {
		t.Fatalf("%s 应拒绝为 %v，实际 %v", op, want, got)
	}
}

// commitFirstRound 完成首轮两步提交。
func commitFirstRound(t *testing.T, tr *Trigger, id int64) {
	t.Helper()
	mustStep(t, tr, id, StepRegisterMetadata, true)
	mustStep(t, tr, id, StepWriteSuccessMarker, true)
}

// 水位恰等于就绪边界 (k+1)*P+延迟 时分区就绪。
func TestReadyAtExactBoundary(t *testing.T) {
	tr := newTestTrigger()
	mustWrite(t, tr, 0, 3) // 分区 0，就绪边界 = 1*10+5 = 15

	mustReportWM(t, tr, 0, 15)
	if info := logState(t, tr, 0); info.State != StateNotReady {
		t.Fatalf("子任务 1 未上报，全局水位不存在，应未就绪，实际 %s", info.State)
	}

	mustReportWM(t, tr, 1, 14)
	if info := logState(t, tr, 0); info.State != StateNotReady {
		t.Fatalf("全局水位 14 < 边界 15，应未就绪，实际 %s", info.State)
	}

	mustReportWM(t, tr, 1, 15)
	info := logState(t, tr, 0)
	if info.State != StateReady || info.NextStep != StepRegisterMetadata {
		t.Fatalf("全局水位 15 == 边界 15，应就绪且待执行第一步，实际 %s/%d", info.State, info.NextStep)
	}

	commitFirstRound(t, tr, 0)
	if info := logState(t, tr, 0); info.State != StateCommitted || info.Version != 1 {
		t.Fatalf("首轮提交完成应已提交且版本为 1，实际 %s/%d", info.State, info.Version)
	}
}

// 子任务结束后水位视为无穷大，全部结束时全局水位跳到无穷。
func TestEndSubtaskWatermarkInfinity(t *testing.T) {
	tr := newTestTrigger()
	mustWrite(t, tr, 0, 25) // 分区 2，就绪边界 = 3*10+5 = 35

	mustReportWM(t, tr, 0, 3)
	mustReportWM(t, tr, 1, 4)
	if info := logState(t, tr, 2); info.State != StateNotReady {
		t.Fatalf("全局水位 3 < 边界 35，应未就绪，实际 %s", info.State)
	}

	err := tr.EndSubtask(0)
	logOp(t, "结束子任务", "子任务=0", err, "结束后该子任务水位视为无穷大")
	if err != nil {
		t.Fatalf("结束子任务被拒绝: %v", err)
	}
	if info := logState(t, tr, 2); info.State != StateNotReady {
		t.Fatalf("全局水位 4 < 边界 35，应未就绪，实际 %s", info.State)
	}

	err = tr.EndSubtask(1)
	logOp(t, "结束子任务", "子任务=1", err, "全部结束，全局水位跳到无穷大")
	if err != nil {
		t.Fatalf("结束子任务被拒绝: %v", err)
	}
	wm, ok := tr.GlobalWatermark()
	t.Logf("全局水位=%d 存在=%v", wm, ok)
	if !ok || wm != math.MaxInt64 {
		t.Fatalf("全部子任务结束后全局水位应为无穷大，实际 %d/%v", wm, ok)
	}
	if info := logState(t, tr, 2); info.State != StateReady {
		t.Fatalf("全局水位无穷大应就绪，实际 %s", info.State)
	}

	commitFirstRound(t, tr, 2)
	if info := logState(t, tr, 2); info.State != StateCommitted {
		t.Fatalf("首轮提交完成应已提交，实际 %s", info.State)
	}
}

// 进行中或提交失败的较小分区会阻塞较大分区的第一步。
func TestSmallerPartitionBlocksLarger(t *testing.T) {
	tr := newTestTrigger()
	mustWrite(t, tr, 0, 1)  // 分区 0
	mustWrite(t, tr, 1, 11) // 分区 1
	mustReportWM(t, tr, 0, 100)
	mustReportWM(t, tr, 1, 100) // 两个分区均已就绪

	// 分区 0 第一步成功，处于进行中：阻塞分区 1。
	mustStep(t, tr, 0, StepRegisterMetadata, true)
	err := tr.ReportStep(1, StepRegisterMetadata, true)
	expectErr(t, "步骤上报", "分区=1 步骤=登记元数据", "较小分区 0 提交中，分区 1 被阻塞",
		err, ErrStepNotExecutable)

	// 分区 0 第二步连续失败 R=2 次，转提交失败：仍阻塞分区 1。
	mustStep(t, tr, 0, StepWriteSuccessMarker, false)
	mustStep(t, tr, 0, StepWriteSuccessMarker, false)
	if info := logState(t, tr, 0); info.State != StateCommitFailed {
		t.Fatalf("失败次数达 R=2，应转提交失败，实际 %s", info.State)
	}
	err = tr.ReportStep(1, StepRegisterMetadata, true)
	expectErr(t, "步骤上报", "分区=1 步骤=登记元数据", "较小分区 0 提交失败，分区 1 被阻塞",
		err, ErrStepNotExecutable)

	// 人工重置分区 0 并完成提交后，分区 1 解除阻塞。
	err = tr.Reset(0)
	logOp(t, "重置", "分区=0", err, "提交失败后人工重置，从该轮第一步重来")
	if err != nil {
		t.Fatalf("重置被拒绝: %v", err)
	}
	commitFirstRound(t, tr, 0)
	commitFirstRound(t, tr, 1)
	if info := logState(t, tr, 1); info.State != StateCommitted || info.Version != 1 {
		t.Fatalf("分区 1 应已提交且版本为 1，实际 %s/%d", info.State, info.Version)
	}
}

// 已提交分区收到迟到写入转待补提交，该轮只有「写成功标记」一步，
// 补提交完成前的多次写入合并在同一轮。
func TestLateWriteTriggersRecommit(t *testing.T) {
	tr := newTestTrigger()
	mustWrite(t, tr, 0, 2) // 分区 0
	mustReportWM(t, tr, 0, 100)
	mustReportWM(t, tr, 1, 100)
	commitFirstRound(t, tr, 0)

	mustWrite(t, tr, 1, 5) // 迟到写入，分区 0
	info := logState(t, tr, 0)
	if info.State != StatePendingRecommit || info.Count != 2 {
		t.Fatalf("已提交后迟到写入应转待补提交且条数=2，实际 %s/%d", info.State, info.Count)
	}

	mustWrite(t, tr, 0, 7) // 补提交完成前的再次迟到写入，合并在同一轮
	if info := logState(t, tr, 0); info.State != StatePendingRecommit || info.Count != 3 {
		t.Fatalf("补提交前多次写入应合并在同一轮且条数=3，实际 %s/%d", info.State, info.Count)
	}

	// 补提交轮只有「写成功标记」，登记元数据步骤不符。
	err := tr.ReportStep(0, StepRegisterMetadata, true)
	expectErr(t, "步骤上报", "分区=0 步骤=登记元数据", "补提交轮只含写成功标记",
		err, ErrStepMismatch)

	mustStep(t, tr, 0, StepWriteSuccessMarker, true)
	info = logState(t, tr, 0)
	if info.State != StateCommitted || info.Version != 2 {
		t.Fatalf("补提交完成应已提交且版本=2，实际 %s/%d", info.State, info.Version)
	}
}

// 失败重试：失败计数、达上限转提交失败、期间写入只增条数、重置后重来。
func TestFailureRetryAndReset(t *testing.T) {
	tr := newTestTrigger()
	mustWrite(t, tr, 0, 4) // 分区 0
	mustReportWM(t, tr, 0, 15)
	mustReportWM(t, tr, 1, 15)

	mustStep(t, tr, 0, StepRegisterMetadata, false)
	if info := logState(t, tr, 0); info.State != StateReady || info.Failures != 1 {
		t.Fatalf("失败 1 次未达 R=2，应仍就绪且失败次数=1，实际 %s/%d", info.State, info.Failures)
	}

	mustWrite(t, tr, 0, 6) // 尚未提交完成时的写入只增加条数、状态不变
	if info := logState(t, tr, 0); info.State != StateReady || info.Count != 2 {
		t.Fatalf("未提交完成时写入应只增条数，实际 %s/%d", info.State, info.Count)
	}

	mustStep(t, tr, 0, StepRegisterMetadata, false)
	if info := logState(t, tr, 0); info.State != StateCommitFailed || info.Failures != 2 {
		t.Fatalf("失败次数达 R=2，应转提交失败，实际 %s/%d", info.State, info.Failures)
	}

	mustWrite(t, tr, 1, 8) // 提交失败时的写入同样只增条数
	if info := logState(t, tr, 0); info.State != StateCommitFailed || info.Count != 3 {
		t.Fatalf("提交失败时写入应只增条数，实际 %s/%d", info.State, info.Count)
	}

	err := tr.ReportStep(0, StepRegisterMetadata, true)
	expectErr(t, "步骤上报", "分区=0 步骤=登记元数据", "提交失败不可执行，需人工重置",
		err, ErrStepNotExecutable)

	err = tr.Reset(0)
	logOp(t, "重置", "分区=0", err, "回到将就绪、从该轮第一步重来且失败次数清零")
	if err != nil {
		t.Fatalf("重置被拒绝: %v", err)
	}
	if info := logState(t, tr, 0); info.State != StateReady || info.Failures != 0 ||
		info.NextStep != StepRegisterMetadata {
		t.Fatalf("重置后应就绪、失败清零、从第一步重来，实际 %s/%d/%d",
			info.State, info.Failures, info.NextStep)
	}

	commitFirstRound(t, tr, 0)
	if info := logState(t, tr, 0); info.State != StateCommitted || info.Version != 1 {
		t.Fatalf("重置后应可正常提交，实际 %s/%d", info.State, info.Version)
	}
}

// 各操作的非法输入按所列次序整体拒绝，且不改变状态。
func TestValidationErrors(t *testing.T) {
	tr := newTestTrigger()
	mustWrite(t, tr, 0, 2) // 分区 0
	mustReportWM(t, tr, 0, 100)
	mustReportWM(t, tr, 1, 100)
	commitFirstRound(t, tr, 0)

	// 写入：子任务号越界、事件时间为负、子任务已结束（按此次序判定）。
	expectErr(t, "写入", "子任务=5 事件时间=-1", ErrSubtaskOutOfRange.Error(),
		tr.Write(5, -1), ErrSubtaskOutOfRange)
	expectErr(t, "写入", "子任务=0 事件时间=-3", ErrNegativeEventTime.Error(),
		tr.Write(0, -3), ErrNegativeEventTime)

	// 水位上报：子任务号越界、已结束、水位回退。
	expectErr(t, "水位上报", "子任务=9 水位=1", ErrSubtaskOutOfRange.Error(),
		tr.ReportWatermark(9, 1), ErrSubtaskOutOfRange)
	expectErr(t, "水位上报", "子任务=0 水位=50", ErrWatermarkRegression.Error(),
		tr.ReportWatermark(0, 50), ErrWatermarkRegression)

	// 结束：子任务号越界、重复结束。
	expectErr(t, "结束子任务", "子任务=-1", ErrSubtaskOutOfRange.Error(),
		tr.EndSubtask(-1), ErrSubtaskOutOfRange)
	if err := tr.EndSubtask(0); err != nil {
		t.Fatalf("首次结束子任务被拒绝: %v", err)
	}
	expectErr(t, "结束子任务", "子任务=0", ErrSubtaskAlreadyEnded.Error(),
		tr.EndSubtask(0), ErrSubtaskAlreadyEnded)
	expectErr(t, "写入", "子任务=0 事件时间=1", ErrSubtaskEnded.Error(),
		tr.Write(0, 1), ErrSubtaskEnded)
	expectErr(t, "水位上报", "子任务=0 水位=200", ErrSubtaskEnded.Error(),
		tr.ReportWatermark(0, 200), ErrSubtaskEnded)

	// 步骤上报：分区不存在、当前不可执行、步骤不符。
	expectErr(t, "步骤上报", "分区=7 步骤=登记元数据", ErrPartitionNotFound.Error(),
		tr.ReportStep(7, StepRegisterMetadata, true), ErrPartitionNotFound)
	expectErr(t, "步骤上报", "分区=0 步骤=登记元数据", "已提交分区不可执行",
		tr.ReportStep(0, StepRegisterMetadata, true), ErrStepNotExecutable)

	mustWrite(t, tr, 1, 12) // 新建分区 1（子任务 0 已结束，用子任务 1 写入）
	expectErr(t, "步骤上报", "分区=1 步骤=写成功标记", "首轮第一步应为登记元数据",
		tr.ReportStep(1, StepWriteSuccessMarker, true), ErrStepMismatch)

	// 重置：分区不存在、不在提交失败。
	expectErr(t, "重置", "分区=9", ErrPartitionNotFound.Error(),
		tr.Reset(9), ErrPartitionNotFound)
	expectErr(t, "重置", "分区=1", "分区 1 不就绪失败状态",
		tr.Reset(1), ErrNotCommitFailed)

	// 被拒绝的操作不得改变状态。
	if info := logState(t, tr, 0); info.State != StateCommitted || info.Version != 1 {
		t.Fatalf("拒绝操作不应改变分区 0 状态，实际 %s/%d", info.State, info.Version)
	}
	if info := logState(t, tr, 1); info.State != StateReady || info.Failures != 0 {
		t.Fatalf("拒绝操作不应改变分区 1 状态，实际 %s/%d", info.State, info.Failures)
	}
}

// 并发调用：效果等价于某个串行顺序，配合 -race 检测数据竞争。
func TestConcurrentAccess(t *testing.T) {
	tr := NewTrigger(10, 4, 5, 3)
	var wg sync.WaitGroup

	// 4 个子任务的写入与水位上报并发进行。
	for subtask := 0; subtask < 4; subtask++ {
		wg.Add(1)
		go func(subtask int) {
			defer wg.Done()
			for i := int64(0); i < 50; i++ {
				if err := tr.Write(subtask, i); err != nil {
					t.Errorf("并发写入被拒绝: %v", err)
				}
				if err := tr.ReportWatermark(subtask, i); err != nil {
					t.Errorf("并发水位上报被拒绝: %v", err)
				}
			}
		}(subtask)
	}
	wg.Wait()

	// 推高全局水位，使所有已创建分区就绪。
	for subtask := 0; subtask < 4; subtask++ {
		if err := tr.ReportWatermark(subtask, 1000); err != nil {
			t.Fatalf("水位上报被拒绝: %v", err)
		}
	}

	// 串行完成各分区提交，验证并发后的状态自洽。
	for _, info := range tr.Partitions() {
		if info.State != StateReady {
			t.Errorf("分区 %d 应就绪，实际 %s", info.ID, info.State)
		}
		commitFirstRound(t, tr, info.ID)
	}
	for _, info := range tr.Partitions() {
		t.Logf("分区快照 id=%d 状态=%s 条数=%d 版本=%d", info.ID, info.State, info.Count, info.Version)
		if info.State != StateCommitted || info.Version != 1 {
			t.Errorf("分区 %d 应已提交且版本=1，实际 %s/%d", info.ID, info.State, info.Version)
		}
	}
}
