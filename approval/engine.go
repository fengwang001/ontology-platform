// Package approval 实现多阶段会签审批流引擎。
package approval

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// State 是流程的运行/终局状态。
type State int

const (
	StateActive State = iota
	StateApproved
	StateRejected
	StateTimeout
	StateWithdrawn
)

func (s State) String() string {
	switch s {
	case StateActive:
		return "进行中"
	case StateApproved:
		return "通过"
	case StateRejected:
		return "驳回"
	case StateTimeout:
		return "超时"
	case StateWithdrawn:
		return "撤回"
	default:
		return "未知"
	}
}

// Terminal 报告状态是否为终局。
func (s State) Terminal() bool { return s != StateActive }

// 拒绝原因。检查顺序即下列声明顺序：只报告第一个命中的原因，
// 被拒绝的操作不改变任何状态。
var (
	ErrProcessNotFound       = errors.New("流程不存在")
	ErrAlreadyTerminal       = errors.New("流程已终局")
	ErrNotStageMember        = errors.New("既非当前阶段审批人也非其受托人")
	ErrDelegatorVoted        = errors.New("已委托者本人不得表态")
	ErrDuplicateVote         = errors.New("重复表态")
	ErrDelegateToSelf        = errors.New("不能委托给自己")
	ErrDelegateTargetInvalid = errors.New("受托人不能是本阶段成员或已受托者")
	ErrTrusteeRedelegate     = errors.New("受托人不得再转委托")
	ErrDelegateAfterVote     = errors.New("已表态后不得再委托")
	ErrAlreadyDelegated      = errors.New("已存在有效委托，不得重复委托")
	ErrNotInitiator          = errors.New("仅发起人可撤回")
	ErrProcessExists         = errors.New("流程 ID 已存在")
	ErrInvalidStage          = errors.New("阶段配置非法")
	ErrClockBackward         = errors.New("时钟不能回拨")
)

// StageSpec 描述一个阶段的静态配置。
type StageSpec struct {
	Name      string
	Approvers []string
	K         int
	Deadline  time.Time
}

// EventType 是事件类型。
type EventType int

const (
	EventProcessCreated EventType = iota
	EventVoteCast
	EventDelegated
	EventStageAdvanced
	EventApproved
	EventRejected
	EventTimeout
	EventWithdrawn
)

func (t EventType) String() string {
	switch t {
	case EventProcessCreated:
		return "流程创建"
	case EventVoteCast:
		return "表态"
	case EventDelegated:
		return "委托"
	case EventStageAdvanced:
		return "阶段推进"
	case EventApproved:
		return "通过"
	case EventRejected:
		return "驳回"
	case EventTimeout:
		return "超时"
	case EventWithdrawn:
		return "撤回"
	default:
		return "未知事件"
	}
}

// Event 是流程产生的一条不可变事件记录。
type Event struct {
	Seq       int       // 全局单调递增序号
	ProcessID string    // 所属流程
	Time      time.Time // 产生时的引擎时钟
	Type      EventType
	Actor     string // 触发者（表态人/委托人/发起人；系统事件为空）
	Detail    string // 判定依据
}

// stageRuntime 是单个阶段的运行时状态，进入下一阶段时整体重置。
type stageRuntime struct {
	votes      map[string]bool // 审批人 -> 决定（受托人的票记到委托人名下）
	delegateTo map[string]string
	trusteeOf  map[string]string
	approvals  int
	vetoes     int
}

func newStageRuntime() *stageRuntime {
	return &stageRuntime{
		votes:      make(map[string]bool),
		delegateTo: make(map[string]string),
		trusteeOf:  make(map[string]string),
	}
}

// process 是单个流程的内部状态。
type process struct {
	id        string
	initiator string
	stages    []StageSpec
	stageIdx  int
	state     State
	rt        *stageRuntime
}

func (p *process) stage() *StageSpec { return &p.stages[p.stageIdx] }

func (p *process) isApprover(who string) bool {
	for _, a := range p.stage().Approvers {
		if a == who {
			return true
		}
	}
	return false
}

// Engine 是会签审批流引擎。所有公开方法均可并发调用。
type Engine struct {
	mu     sync.Mutex
	now    time.Time
	procs  map[string]*process
	events []Event
}

// NewEngine 创建引擎，start 为注入时钟的初始时刻。
func NewEngine(start time.Time) *Engine {
	return &Engine{
		now:   start,
		procs: make(map[string]*process),
	}
}

// Now 返回引擎当前时钟。
func (e *Engine) Now() time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.now
}

// CreateProcess 创建流程并记录创建事件。
func (e *Engine) CreateProcess(id, initiator string, stages []StageSpec) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.createProcessLocked(id, initiator, stages)
}

// Vote 在当前阶段表态。approve 为 true 表示同意，false 表示否决。
func (e *Engine) Vote(processID, voter string, approve bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.voteLocked(processID, voter, approve)
}

// Delegate 把当前阶段的表态权委托给 trustee。
func (e *Engine) Delegate(processID, delegator, trustee string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.delegateLocked(processID, delegator, trustee)
}

// Withdraw 由发起人在终局前撤回流程。
func (e *Engine) Withdraw(processID, actor string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.withdrawLocked(processID, actor)
}

// AdvanceClock 把引擎时钟推进到 to，并对所有未终局流程做超时判定。
func (e *Engine) AdvanceClock(to time.Time) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.advanceClockLocked(to)
}

// State 返回流程当前状态。
func (e *Engine) State(processID string) (State, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	p, ok := e.procs[processID]
	if !ok {
		return StateActive, ErrProcessNotFound
	}
	return p.state, nil
}

// Events 返回指定流程的事件日志副本；processID 为空时返回全部事件。
func (e *Engine) Events(processID string) []Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []Event
	for _, ev := range e.events {
		if processID == "" || ev.ProcessID == processID {
			out = append(out, ev)
		}
	}
	return out
}

// emit 追加一条事件。调用方须持有锁。
func (e *Engine) emit(p *process, typ EventType, actor, detail string) {
	e.events = append(e.events, Event{
		Seq:       len(e.events),
		ProcessID: p.id,
		Time:      e.now,
		Type:      typ,
		Actor:     actor,
		Detail:    detail,
	})
}

func (e *Engine) createProcessLocked(id, initiator string, stages []StageSpec) error {
	if id == "" {
		return ErrProcessNotFound
	}
	if _, ok := e.procs[id]; ok {
		return ErrProcessExists
	}
	if len(stages) == 0 {
		return fmt.Errorf("%w: 至少需要一个阶段", ErrInvalidStage)
	}
	copied := make([]StageSpec, len(stages))
	for i, st := range stages {
		if len(st.Approvers) == 0 {
			return fmt.Errorf("%w: 阶段 %d 审批人集合为空", ErrInvalidStage, i)
		}
		if st.K < 1 || st.K > len(st.Approvers) {
			return fmt.Errorf("%w: 阶段 %d 所需同意数 k=%d 不在 [1, %d] 内", ErrInvalidStage, i, st.K, len(st.Approvers))
		}
		seen := make(map[string]bool, len(st.Approvers))
		for _, a := range st.Approvers {
			if a == "" || seen[a] {
				return fmt.Errorf("%w: 阶段 %d 审批人重复或为空", ErrInvalidStage, i)
			}
			seen[a] = true
		}
		copied[i] = st
		copied[i].Approvers = append([]string(nil), st.Approvers...)
	}
	p := &process{
		id:        id,
		initiator: initiator,
		stages:    copied,
		state:     StateActive,
		rt:        newStageRuntime(),
	}
	e.procs[id] = p
	e.emit(p, EventProcessCreated, initiator,
		fmt.Sprintf("发起人=%s 阶段数=%d 当前阶段=%s 审批人=%v k=%d 截止=%s",
			initiator, len(copied), copied[0].Name, copied[0].Approvers, copied[0].K,
			copied[0].Deadline.Format(time.RFC3339Nano)))
	return nil
}

// checkTimeoutLocked 做惰性超时判定：时钟到达或越过当前阶段截止且仍未达到 k，则超时。
// 调用方须持有锁。
func (e *Engine) checkTimeoutLocked(p *process) {
	if p.state != StateActive {
		return
	}
	st := p.stage()
	if e.now.Before(st.Deadline) {
		return
	}
	if p.rt.approvals >= st.K {
		return // 已达到 k 的阶段在表态时已推进，此处仅为防御
	}
	p.state = StateTimeout
	unvoted := len(st.Approvers) - len(p.rt.votes)
	e.emit(p, EventTimeout, "",
		fmt.Sprintf("时钟 %s 到达截止 %s：同意 %d < 所需 k=%d（否决 %d，未表态 %d），判定超时",
			e.now.Format(time.RFC3339Nano), st.Deadline.Format(time.RFC3339Nano),
			p.rt.approvals, st.K, p.rt.vetoes, unvoted))
}

func (e *Engine) voteLocked(processID, voter string, approve bool) error {
	p, ok := e.procs[processID]
	if !ok {
		return ErrProcessNotFound
	}
	e.checkTimeoutLocked(p)
	if p.state.Terminal() {
		return ErrAlreadyTerminal
	}
	// 解析表决权归属：审批人本人，或某审批人的受托人（票记到委托人名下）。
	owner := voter
	isApprover := p.isApprover(voter)
	if !isApprover {
		delegator, isTrustee := p.rt.trusteeOf[voter]
		if !isTrustee {
			return ErrNotStageMember
		}
		owner = delegator
	}
	if isApprover {
		if _, delegated := p.rt.delegateTo[voter]; delegated {
			return ErrDelegatorVoted
		}
	}
	if _, voted := p.rt.votes[owner]; voted {
		return ErrDuplicateVote
	}
	st := p.stage()
	p.rt.votes[owner] = approve
	decision := "否决"
	if approve {
		p.rt.approvals++
		decision = "同意"
	} else {
		p.rt.vetoes++
	}
	via := ""
	if owner != voter {
		via = fmt.Sprintf("（受托人 %s 代委托人 %s 表决）", voter, owner)
	}
	e.emit(p, EventVoteCast, voter,
		fmt.Sprintf("阶段=%s 表决人=%s 归属=%s%s 决定=%s 时刻=%s < 截止=%s 计票：同意 %d/所需 %d，否决 %d",
			st.Name, voter, owner, via, decision,
			e.now.Format(time.RFC3339Nano), st.Deadline.Format(time.RFC3339Nano),
			p.rt.approvals, st.K, p.rt.vetoes))
	e.evaluateLocked(p)
	return nil
}

// evaluateLocked 在每次有效表态后判定推进、通过或提前驳回。
// 推进式：同意数 >= k；提前驳回式：同意数 + 未表态人数 < k。
func (e *Engine) evaluateLocked(p *process) {
	st := p.stage()
	total := len(st.Approvers)
	unvoted := total - len(p.rt.votes)
	switch {
	case p.rt.approvals >= st.K:
		if p.stageIdx == len(p.stages)-1 {
			p.state = StateApproved
			e.emit(p, EventApproved, "",
				fmt.Sprintf("末阶段=%s 同意 %d >= 所需 k=%d，判定通过", st.Name, p.rt.approvals, st.K))
			return
		}
		next := p.stages[p.stageIdx+1]
		e.emit(p, EventStageAdvanced, "",
			fmt.Sprintf("阶段=%s 同意 %d >= 所需 k=%d，推进到阶段=%s（审批人=%v k=%d 截止=%s）",
				st.Name, p.rt.approvals, st.K, next.Name, next.Approvers, next.K,
				next.Deadline.Format(time.RFC3339Nano)))
		p.stageIdx++
		p.rt = newStageRuntime()
	case p.rt.approvals+unvoted < st.K:
		p.state = StateRejected
		e.emit(p, EventRejected, "",
			fmt.Sprintf("阶段=%s 同意 %d + 未表态 %d = %d < 所需 k=%d，已不可能达到，提前驳回",
				st.Name, p.rt.approvals, unvoted, p.rt.approvals+unvoted, st.K))
	}
}

func (e *Engine) delegateLocked(processID, delegator, trustee string) error {
	p, ok := e.procs[processID]
	if !ok {
		return ErrProcessNotFound
	}
	e.checkTimeoutLocked(p)
	if p.state.Terminal() {
		return ErrAlreadyTerminal
	}
	_, delegatorIsTrustee := p.rt.trusteeOf[delegator]
	if !p.isApprover(delegator) && !delegatorIsTrustee {
		return ErrNotStageMember
	}
	if trustee == delegator {
		return ErrDelegateToSelf
	}
	if p.isApprover(trustee) {
		return ErrDelegateTargetInvalid
	}
	if _, alreadyTrustee := p.rt.trusteeOf[trustee]; alreadyTrustee {
		return ErrDelegateTargetInvalid
	}
	if delegatorIsTrustee {
		return ErrTrusteeRedelegate
	}
	if _, voted := p.rt.votes[delegator]; voted {
		return ErrDelegateAfterVote
	}
	if _, delegated := p.rt.delegateTo[delegator]; delegated {
		return ErrAlreadyDelegated
	}
	p.rt.delegateTo[delegator] = trustee
	p.rt.trusteeOf[trustee] = delegator
	e.emit(p, EventDelegated, delegator,
		fmt.Sprintf("阶段=%s 委托人=%s 受托人=%s（非本阶段成员），受托人的表态计作委托人的一票",
			p.stage().Name, delegator, trustee))
	return nil
}

func (e *Engine) withdrawLocked(processID, actor string) error {
	p, ok := e.procs[processID]
	if !ok {
		return ErrProcessNotFound
	}
	e.checkTimeoutLocked(p)
	if p.state.Terminal() {
		return ErrAlreadyTerminal
	}
	if actor != p.initiator {
		return ErrNotInitiator
	}
	p.state = StateWithdrawn
	e.emit(p, EventWithdrawn, actor,
		fmt.Sprintf("发起人=%s 在终局前撤回，当前阶段=%s", actor, p.stage().Name))
	return nil
}

func (e *Engine) advanceClockLocked(to time.Time) error {
	if to.Before(e.now) {
		return ErrClockBackward
	}
	e.now = to
	// 按流程 ID 排序遍历，保证串行重放时事件顺序确定。
	ids := make([]string, 0, len(e.procs))
	for id := range e.procs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		e.checkTimeoutLocked(e.procs[id])
	}
	return nil
}
