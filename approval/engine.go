package approval

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

type stageState struct {
	votes     map[string]Vote // 以委托人（成员）为键
	delegate  map[string]string
	delegated map[string]bool // 受托人名 -> true
	approves  int
}

type flow struct {
	id        string
	initiator string
	stages    []Stage
	index     int
	terminal  Terminal
	state     *stageState
}

// Engine 管理全部审批流程；所有方法可并发调用，事件日志全局有序。
type Engine struct {
	mu     sync.Mutex
	now    func() time.Time
	flows  map[string]*flow
	log    []Event
	seq    int
	logger func(string)
}

// NewEngine 创建引擎，clock 为 nil 时使用系统时钟。
func NewEngine(clock Clock) *Engine {
	e := &Engine{flows: map[string]*flow{}}
	if clock != nil {
		e.now = clock.Now
	} else {
		e.now = time.Now
	}
	return e
}

// SetLogger 注册每条事件的同步日志回调（在锁内调用，回调不得重入引擎）。
func (e *Engine) SetLogger(fn func(string)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.logger = fn
}

// Start 创建一个新的审批流程并返回其 ID；非法阶段定义返回空串。
func (e *Engine) Start(initiator string, stages []Stage) string {
	if len(stages) == 0 {
		return ""
	}
	copied := make([]Stage, len(stages))
	for idx, st := range stages {
		if st.Required < 1 || st.Required > len(st.Approvers) {
			return ""
		}
		copied[idx] = Stage{
			Name:      st.Name,
			Approvers: append([]string(nil), st.Approvers...),
			Required:  st.Required,
			Deadline:  st.Deadline,
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	id := fmt.Sprintf("F%03d", len(e.flows)+1)
	e.flows[id] = &flow{
		id:        id,
		initiator: initiator,
		stages:    copied,
		state:     newStageState(),
	}
	e.emitLocked(id, "Started",
		fmt.Sprintf("输入: 发起人=%s 阶段数=%d；输出: 已创建；判定: 首阶段=%s k=%d 截止=%s",
			initiator, len(copied), copied[0].Name, copied[0].Required,
			copied[0].Deadline.Format(time.RFC3339)))
	return id
}

// Vote 处理一次表态（审批人本人或其受托人均可）。at 为表态声称时刻。
func (e *Engine) Vote(flowID, actor string, opinion Vote, at time.Time) Result {
	e.mu.Lock()
	defer e.mu.Unlock()

	f, ok := e.flows[flowID]
	if !ok {
		return rejectLocked(e, flowID, "Vote", actor, opinion, at, RejectFlowNotFound)
	}
	if f.terminal != 0 {
		return rejectLocked(e, flowID, "Vote", actor, opinion, at, RejectAlreadyTerminal)
	}
	e.expireLocked(f, at)
	if f.terminal != 0 {
		return rejectLocked(e, flowID, "Vote", actor, opinion, at, RejectAlreadyTerminal)
	}

	stage := f.stages[f.index]
	member := actor
	if !isApprover(stage.Approvers, actor) {
		principal, isDelegatee := f.state.delegateOf(actor)
		if !isDelegatee {
			return rejectLocked(e, flowID, "Vote", actor, opinion, at, RejectNotParticipant)
		}
		member = principal
	}
	if delegatee, delegated := f.state.delegate[member]; delegated && delegatee != actor {
		return rejectLocked(e, flowID, "Vote", actor, opinion, at, RejectDelegatorVoting)
	}
	if _, voted := f.state.votes[member]; voted {
		return rejectLocked(e, flowID, "Vote", actor, opinion, at, RejectDuplicateVote)
	}

	f.state.votes[member] = opinion
	if opinion == VoteApprove {
		f.state.approves++
	}
	votedBy := member
	if actor != member {
		votedBy = fmt.Sprintf("%s(受托人=%s)", member, actor)
	}
	e.emitLocked(flowID, "Voted",
		fmt.Sprintf("输入: 操作人=%s 意见=%s 时刻=%s；输出: 接受；判定: 计入 %s；阶段=%s 同意=%d/%d 已表态=%d 否决=%d",
			actor, opinion, at.Format(time.RFC3339), votedBy, stage.Name,
			f.state.approves, stage.Required, len(f.state.votes),
			len(f.state.votes)-f.state.approves))

	return e.reevaluateLocked(f, at)
}

// Delegate 处理一次委托：审批人把本阶段表态权委托给一名非本阶段成员。
func (e *Engine) Delegate(flowID, approver, delegatee string, at time.Time) Result {
	e.mu.Lock()
	defer e.mu.Unlock()

	f, ok := e.flows[flowID]
	if !ok {
		return rejectLocked(e, flowID, "Delegate", approver+"->"+delegatee, Vote(0), at, RejectFlowNotFound)
	}
	if f.terminal != 0 {
		return rejectLocked(e, flowID, "Delegate", approver+"->"+delegatee, Vote(0), at, RejectAlreadyTerminal)
	}
	e.expireLocked(f, at)
	if f.terminal != 0 {
		return rejectLocked(e, flowID, "Delegate", approver+"->"+delegatee, Vote(0), at, RejectAlreadyTerminal)
	}

	stage := f.stages[f.index]
	if !isApprover(stage.Approvers, approver) {
		if _, isDelegatee := f.state.delegateOf(approver); isDelegatee {
			return rejectLocked(e, flowID, "Delegate", approver+"->"+delegatee, Vote(0), at, RejectDelegateeRedelegating)
		}
		return rejectLocked(e, flowID, "Delegate", approver+"->"+delegatee, Vote(0), at, RejectNotParticipant)
	}
	if approver == delegatee {
		return rejectLocked(e, flowID, "Delegate", approver+"->"+delegatee, Vote(0), at, RejectDelegateToSelf)
	}
	if isApprover(stage.Approvers, delegatee) || f.state.delegated[delegatee] {
		return rejectLocked(e, flowID, "Delegate", approver+"->"+delegatee, Vote(0), at, RejectDelegateeIsMember)
	}
	if _, voted := f.state.votes[approver]; voted {
		return rejectLocked(e, flowID, "Delegate", approver+"->"+delegatee, Vote(0), at, RejectVoteAfterDelegation)
	}
	if _, exists := f.state.delegate[approver]; exists {
		return rejectLocked(e, flowID, "Delegate", approver+"->"+delegatee, Vote(0), at, RejectAlreadyDelegated)
	}

	f.state.delegate[approver] = delegatee
	f.state.delegated[delegatee] = true
	e.emitLocked(flowID, "Delegated",
		fmt.Sprintf("输入: %s -> %s 时刻=%s；输出: 接受；判定: 委托人为阶段 %s 成员，受托人非本阶段成员且非已受托者，委托人未表态、未委托",
			approver, delegatee, at.Format(time.RFC3339), stage.Name))
	return Result{OK: true}
}

// Withdraw 由发起人在终局前撤回流程。
func (e *Engine) Withdraw(flowID, initiator string, at time.Time) Result {
	e.mu.Lock()
	defer e.mu.Unlock()

	f, ok := e.flows[flowID]
	if !ok {
		return rejectLocked(e, flowID, "Withdraw", initiator, Vote(0), at, RejectFlowNotFound)
	}
	if f.terminal != 0 {
		return rejectLocked(e, flowID, "Withdraw", initiator, Vote(0), at, RejectAlreadyTerminal)
	}
	e.expireLocked(f, at)
	if f.terminal != 0 {
		return rejectLocked(e, flowID, "Withdraw", initiator, Vote(0), at, RejectAlreadyTerminal)
	}
	if initiator != f.initiator {
		return rejectLocked(e, flowID, "Withdraw", initiator, Vote(0), at, RejectNotParticipant)
	}

	f.terminal = TerminalWithdrawn
	e.emitLocked(flowID, "Terminal",
		fmt.Sprintf("输入: 发起人=%s 时刻=%s；输出: 撤回；判定: 发起人本人在终局前撤回",
			initiator, at.Format(time.RFC3339)))
	return Result{OK: true, Terminal: TerminalWithdrawn, Reason: "撤回"}
}

// AdvanceClock 以注入时刻推进时钟，处理所有已到截止的流程（按流程 ID 排序保证确定性）。
func (e *Engine) AdvanceClock(at time.Time) []Event {
	e.mu.Lock()
	defer e.mu.Unlock()

	ids := make([]string, 0, len(e.flows))
	for id := range e.flows {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		f := e.flows[id]
		if f.terminal == 0 {
			e.expireLocked(f, at)
		}
	}
	return e.snapshotLogLocked()
}

// Events 返回事件日志副本。
func (e *Engine) Events() []Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.snapshotLogLocked()
}

// Terminal 返回流程终局；流程不存在或未终局为 Terminal(0)。
func (e *Engine) Terminal(flowID string) Terminal {
	e.mu.Lock()
	defer e.mu.Unlock()
	f, ok := e.flows[flowID]
	if !ok {
		return 0
	}
	return f.terminal
}

func newStageState() *stageState {
	return &stageState{
		votes:     map[string]Vote{},
		delegate:  map[string]string{},
		delegated: map[string]bool{},
	}
}

func (s *stageState) delegateOf(actor string) (string, bool) {
	for member, delegatee := range s.delegate {
		if delegatee == actor {
			return member, true
		}
	}
	return "", false
}

func isApprover(approvers []string, who string) bool {
	for _, member := range approvers {
		if member == who {
			return true
		}
	}
	return false
}

// expireLocked 检查当前阶段截止；时刻不早于截止（含恰在截止）即超时。
func (e *Engine) expireLocked(f *flow, at time.Time) {
	if f.terminal != 0 {
		return
	}
	stage := f.stages[f.index]
	if !at.Before(stage.Deadline) {
		f.terminal = TerminalTimedOut
		e.emitLocked(f.id, "Terminal",
			fmt.Sprintf("输入: 时钟=%s；输出: 超时；判定: 阶段=%s 截止=%s（有效表态须严格早于截止），同意=%d<%d",
				at.Format(time.RFC3339), stage.Name, stage.Deadline.Format(time.RFC3339),
				f.state.approves, stage.Required))
	}
}

// reevaluateLocked 应用推进判定式：
// 推进/通过：approves >= k；提前驳回：approves + (n - voted) < k。
func (e *Engine) reevaluateLocked(f *flow, at time.Time) Result {
	stage := f.stages[f.index]
	n := len(stage.Approvers)
	voted := len(f.state.votes)

	switch {
	case f.state.approves >= stage.Required:
		if f.index == len(f.stages)-1 {
			f.terminal = TerminalApproved
			e.emitLocked(f.id, "Terminal",
				fmt.Sprintf("输出: 通过；判定: 末阶段=%s 同意=%d>=k=%d 时刻=%s",
					stage.Name, f.state.approves, stage.Required, at.Format(time.RFC3339)))
			return Result{OK: true, Terminal: TerminalApproved, Reason: "通过"}
		}
		e.emitLocked(f.id, "Advanced",
			fmt.Sprintf("输出: 阶段推进；判定: 同意=%d>=k=%d，阶段 %s 完成，进入 %s",
				f.state.approves, stage.Required, stage.Name, f.stages[f.index+1].Name))
		f.index++
		f.state = newStageState()
		next := f.stages[f.index]
		e.emitLocked(f.id, "StageEntered",
			fmt.Sprintf("新阶段=%s 审批人=%v k=%d 截止=%s",
				next.Name, next.Approvers, next.Required, next.Deadline.Format(time.RFC3339)))
		e.expireLocked(f, at)
		if f.terminal != 0 {
			return Result{OK: true, Terminal: f.terminal, Reason: f.terminal.String()}
		}
		return Result{OK: true}
	case f.state.approves+(n-voted) < stage.Required:
		f.terminal = TerminalRejected
		e.emitLocked(f.id, "Terminal",
			fmt.Sprintf("输出: 驳回；判定: 阶段=%s 同意=%d+未表态=%d=%d<k=%d，已不可能达到所需同意数",
				stage.Name, f.state.approves, n-voted, f.state.approves+n-voted, stage.Required))
		return Result{OK: true, Terminal: TerminalRejected, Reason: "提前驳回"}
	default:
		return Result{OK: true}
	}
}

// rejectLocked 按错误优先级构造拒绝结果；被拒绝的操作不改变任何状态。
// 流程不存在或已终局时不产生事件（终局后不再产生事件）。
func rejectLocked(e *Engine, flowID, op, input string, opinion Vote, at time.Time, code RejectCode) Result {
	detail := fmt.Sprintf("输入: 操作=%s 参数=%s 意见=%s 时刻=%s；输出: 拒绝；判定依据: %s",
		op, input, opinion, at.Format(time.RFC3339), code)
	if f, ok := e.flows[flowID]; ok && f.terminal == 0 {
		e.emitLocked(flowID, "Rejected", detail)
	}
	return Result{OK: false, RejectCode: code, Reason: code.String()}
}

func (e *Engine) emitLocked(flowID, kind, detail string) {
	e.seq++
	ev := Event{Seq: e.seq, Time: e.now(), FlowID: flowID, Kind: kind, Detail: detail}
	e.log = append(e.log, ev)
	if e.logger != nil {
		e.logger(fmt.Sprintf("#%d [%s] %s %s", ev.Seq, flowID, kind, detail))
	}
}

func (e *Engine) snapshotLogLocked() []Event {
	out := make([]Event, len(e.log))
	copy(out, e.log)
	return out
}
