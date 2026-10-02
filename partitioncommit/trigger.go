// Package partitioncommit 实现按事件时间划分的分区提交触发器。
//
// 分区 k 覆盖事件时间 [k*P, (k+1)*P)。全局水位为全部 W 个写入子任务
// 水位的最小值（子任务结束后视为无穷大）；当全局水位达到
// (k+1)*P+delay 时分区就绪。提交按固定步骤（登记元数据 -> 写成功标记）
// 依分区编号升序进行，支持失败重试、人工重置与迟到数据补提交。
package partitioncommit

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
)

// Step 表示提交流程中的一个步骤。
type Step int

const (
	// StepRegisterMetadata 第一步：登记元数据（仅首轮提交包含）。
	StepRegisterMetadata Step = iota + 1
	// StepWriteSuccessMarker 第二步：写成功标记（首轮与补提交轮均包含）。
	StepWriteSuccessMarker
)

func (s Step) String() string {
	switch s {
	case StepRegisterMetadata:
		return "登记元数据"
	case StepWriteSuccessMarker:
		return "写成功标记"
	default:
		return fmt.Sprintf("未知步骤(%d)", int(s))
	}
}

// State 表示分区的生命周期状态。
type State int

const (
	// StateNotReady 已创建但尚未满足水位就绪条件。
	StateNotReady State = iota
	// StateReady 已就绪，可开始（或继续）一轮提交。
	StateReady
	// StateCommitting 一轮提交进行中。
	StateCommitting
	// StateCommitted 本轮提交已完成。
	StateCommitted
	// StatePendingRecommit 已提交分区收到迟到写入，等待补提交。
	StatePendingRecommit
	// StateCommitFailed 失败次数达到上限，等待人工重置。
	StateCommitFailed
)

func (s State) String() string {
	switch s {
	case StateNotReady:
		return "未就绪"
	case StateReady:
		return "就绪"
	case StateCommitting:
		return "提交中"
	case StateCommitted:
		return "已提交"
	case StatePendingRecommit:
		return "待补提交"
	case StateCommitFailed:
		return "提交失败"
	default:
		return fmt.Sprintf("未知状态(%d)", int(s))
	}
}

// 各操作的可区分拒绝原因，按题目所列校验次序依次判定。
var (
	// 写入
	ErrSubtaskOutOfRange = errors.New("子任务号越界")
	ErrNegativeEventTime = errors.New("事件时间为负")
	ErrSubtaskEnded      = errors.New("子任务已结束")
	// 水位上报
	ErrWatermarkRegression = errors.New("水位回退")
	// 结束
	ErrSubtaskAlreadyEnded = errors.New("重复结束")
	// 步骤上报
	ErrPartitionNotFound = errors.New("分区不存在")
	ErrStepNotExecutable = errors.New("当前不可执行")
	ErrStepMismatch      = errors.New("步骤不符")
	// 重置
	ErrNotCommitFailed = errors.New("不在提交失败")
)

// PartitionInfo 是分区的只读快照。
type PartitionInfo struct {
	ID       int64 // 分区编号 k
	Count    int64 // 累计写入条数
	State    State // 当前状态
	NextStep Step  // 当前轮待执行的步骤（0 表示无待执行步骤）
	Failures int   // 当前步骤累计失败次数
	Version  int64 // 已完成的提交轮数
}

// Trigger 是分区提交触发器，所有方法可并发调用，
// 效果等价于某个串行顺序。
type Trigger struct {
	mu         sync.Mutex
	period     int64 // P：分区时间跨度
	delay      int64 // 就绪延迟
	workers    int   // W：写入子任务数
	maxRetries int   // R：单步失败上限

	watermarks []int64 // 各子任务当前水位
	reported   []bool  // 各子任务是否已上报过水位
	ended      []bool  // 各子任务是否已结束

	partitions map[int64]*partition
}

type partition struct {
	id       int64
	count    int64
	state    State
	nextStep Step
	failures int
	version  int64
}

// info 生成分区快照。
func (p *partition) info() PartitionInfo {
	return PartitionInfo{
		ID:       p.id,
		Count:    p.count,
		State:    p.state,
		NextStep: p.nextStep,
		Failures: p.failures,
		Version:  p.version,
	}
}

// roundSteps 返回当前提交轮包含的步骤序列：
// 首轮（version==0）为「登记元数据 -> 写成功标记」，
// 补提交轮只有「写成功标记」。
func (p *partition) roundSteps() []Step {
	if p.version == 0 {
		return []Step{StepRegisterMetadata, StepWriteSuccessMarker}
	}
	return []Step{StepWriteSuccessMarker}
}

// lastStepOfRound 判断 step 是否为当前轮的最后一步。
func (p *partition) lastStepOfRound(step Step) bool {
	steps := p.roundSteps()
	return steps[len(steps)-1] == step
}

// NewTrigger 创建触发器：period 为分区时间跨度 P，workers 为写入子任务
// 数 W，delay 为就绪延迟，maxRetries 为单步失败上限 R。
func NewTrigger(period int64, workers int, delay int64, maxRetries int) *Trigger {
	return &Trigger{
		period:     period,
		delay:      delay,
		workers:    workers,
		maxRetries: maxRetries,
		watermarks: make([]int64, workers),
		reported:   make([]bool, workers),
		ended:      make([]bool, workers),
		partitions: make(map[int64]*partition),
	}
}

// Write 写入一条事件：subtask 为写入子任务号，eventTime 为非负事件时间。
// 首次写入会创建对应分区并计条数。
func (t *Trigger) Write(subtask int, eventTime int64) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if subtask < 0 || subtask >= t.workers {
		return ErrSubtaskOutOfRange
	}
	if eventTime < 0 {
		return ErrNegativeEventTime
	}
	if t.ended[subtask] {
		return ErrSubtaskEnded
	}

	id := eventTime / t.period
	p, ok := t.partitions[id]
	if !ok {
		p = &partition{id: id, state: StateNotReady}
		t.partitions[id] = p
	}
	p.count++
	// 已提交分区收到迟到写入：转为待补提交，新一轮只含「写成功标记」，
	// 补提交完成前的多次写入合并在同一轮（状态保持待补提交）。
	// 尚未提交完成（含提交失败）时的写入只增加条数、状态不变。
	if p.state == StateCommitted {
		p.state = StatePendingRecommit
		p.nextStep = p.roundSteps()[0]
	}
	t.refreshReadinessLocked()
	return nil
}

// ReportWatermark 上报子任务水位，水位只增不减。
func (t *Trigger) ReportWatermark(subtask int, watermark int64) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if subtask < 0 || subtask >= t.workers {
		return ErrSubtaskOutOfRange
	}
	if t.ended[subtask] {
		return ErrSubtaskEnded
	}
	if t.reported[subtask] && watermark < t.watermarks[subtask] {
		return ErrWatermarkRegression
	}
	t.watermarks[subtask] = watermark
	t.reported[subtask] = true
	t.refreshReadinessLocked()
	return nil
}

// EndSubtask 标记子任务结束，此后其水位视为无穷大。
func (t *Trigger) EndSubtask(subtask int) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if subtask < 0 || subtask >= t.workers {
		return ErrSubtaskOutOfRange
	}
	if t.ended[subtask] {
		return ErrSubtaskAlreadyEnded
	}
	t.ended[subtask] = true
	t.refreshReadinessLocked()
	return nil
}

// ReportStep 由外部对分区当前可执行的步骤上报执行结果。
// success 为 false 时该步失败次数加一，累计达 R 次分区转提交失败。
func (t *Trigger) ReportStep(partitionID int64, step Step, success bool) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	p, ok := t.partitions[partitionID]
	if !ok {
		return ErrPartitionNotFound
	}
	if err := t.executableLocked(p); err != nil {
		return err
	}
	if step != p.nextStep {
		return ErrStepMismatch
	}

	if !success {
		p.failures++
		if p.failures >= t.maxRetries {
			p.state = StateCommitFailed
			p.nextStep = 0
		}
		return nil
	}

	p.failures = 0
	if p.lastStepOfRound(step) {
		// 一轮提交完成：版本加一，分区回到已提交。
		p.version++
		p.state = StateCommitted
		p.nextStep = 0
		return nil
	}
	// 推进到当前轮的下一步。
	steps := p.roundSteps()
	for i, s := range steps {
		if s == step {
			p.nextStep = steps[i+1]
			break
		}
	}
	return nil
}

// Reset 人工重置提交失败的分区：回到就绪、从该轮第一步重来且失败次数清零。
func (t *Trigger) Reset(partitionID int64) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	p, ok := t.partitions[partitionID]
	if !ok {
		return ErrPartitionNotFound
	}
	if p.state != StateCommitFailed {
		return ErrNotCommitFailed
	}
	p.state = StateReady
	p.failures = 0
	p.nextStep = p.roundSteps()[0]
	return nil
}

// GlobalWatermark 返回全局水位；ok 为 false 表示尚有子任务未上报过水位，
// 全局水位不存在。
func (t *Trigger) GlobalWatermark() (watermark int64, ok bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.globalWatermarkLocked()
}

// Partition 返回分区快照；ok 为 false 表示分区不存在。
func (t *Trigger) Partition(partitionID int64) (PartitionInfo, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	p, ok := t.partitions[partitionID]
	if !ok {
		return PartitionInfo{}, false
	}
	return p.info(), true
}

// Partitions 按编号升序返回所有已创建分区的快照。
func (t *Trigger) Partitions() []PartitionInfo {
	t.mu.Lock()
	defer t.mu.Unlock()

	ids := make([]int64, 0, len(t.partitions))
	for id := range t.partitions {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	infos := make([]PartitionInfo, 0, len(ids))
	for _, id := range ids {
		infos = append(infos, t.partitions[id].info())
	}
	return infos
}

// globalWatermarkLocked 计算全局水位：全部 W 个子任务都上报过（或已结束）
// 后才存在，取各子任务水位的最小值；已结束子任务的水位视为无穷大。
func (t *Trigger) globalWatermarkLocked() (int64, bool) {
	wm := int64(math.MaxInt64)
	for i := 0; i < t.workers; i++ {
		if t.ended[i] {
			continue
		}
		if !t.reported[i] {
			return 0, false
		}
		if t.watermarks[i] < wm {
			wm = t.watermarks[i]
		}
	}
	return wm, true
}

// readyLocked 判定分区是否满足水位就绪条件：
// 全局水位 ≥ (k+1)*P + 延迟。
func (t *Trigger) readyLocked(p *partition) bool {
	wm, ok := t.globalWatermarkLocked()
	if !ok {
		return false
	}
	return wm >= (p.id+1)*t.period+t.delay
}

// blockedLocked 判定分区是否被编号更小的已创建分区阻塞：
// 任一更小的已创建分区不处于已提交或待补提交状态时即阻塞。
func (t *Trigger) blockedLocked(p *partition) bool {
	for id, other := range t.partitions {
		if id >= p.id {
			continue
		}
		if other.state != StateCommitted && other.state != StatePendingRecommit {
			return true
		}
	}
	return false
}

// executableLocked 校验分区当前是否存在可执行步骤：
// 未就绪、被阻塞、已提交或提交失败均不可执行。
func (t *Trigger) executableLocked(p *partition) error {
	switch p.state {
	case StateReady, StatePendingRecommit:
		// 每轮第一步开始时，要求所有编号更小的已创建分区
		// 都处于已提交或待补提交状态。
		if p.nextStep == p.roundSteps()[0] && t.blockedLocked(p) {
			return ErrStepNotExecutable
		}
		return nil
	case StateNotReady:
		if !t.readyLocked(p) {
			return ErrStepNotExecutable
		}
		if t.blockedLocked(p) {
			return ErrStepNotExecutable
		}
		// 惰性推进：就绪且未被阻塞，从该轮第一步开始。
		p.state = StateReady
		p.nextStep = p.roundSteps()[0]
		return nil
	default:
		// 提交中但无待执行步骤、已提交、提交失败。
		return ErrStepNotExecutable
	}
}

// refreshReadinessLocked 在水位或写入变化后，将就绪的未就绪分区推进到
// 就绪状态（仅更新状态与待执行步骤，不改变提交进度）。
func (t *Trigger) refreshReadinessLocked() {
	for _, p := range t.partitions {
		if p.state == StateNotReady && t.readyLocked(p) {
			p.state = StateReady
			p.nextStep = p.roundSteps()[0]
		}
	}
}
