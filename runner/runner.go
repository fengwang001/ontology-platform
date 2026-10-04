// Package runner 管理工作流实例、全局时钟与审计日志。
package runner

import (
	"errors"
	"sync"

	"ontology/flow"
	"ontology/grants"
)

// 拒绝类别，按优先级排列：ErrParam（含 flow.ErrDef）< ErrClock <
// ErrNotFound/ErrExists < ErrState < ErrSelf < ErrNoAuthority。
var (
	ErrParam       = errors.New("runner: invalid parameter")
	ErrClock       = errors.New("runner: clock moved backwards")
	ErrNotFound    = errors.New("runner: definition or instance not found")
	ErrExists      = errors.New("runner: instance already exists")
	ErrState       = errors.New("runner: action conflicts with instance state")
	ErrSelf        = errors.New("runner: approver must not be the launcher")
	ErrNoAuthority = errors.New("runner: approver lacks the approve bit")
)

// MaxNow 是 now 的合法上界；下界为 0。
const MaxNow = int64(1e15)

const maxTimeout = int64(1e9)

// InstState 是实例状态。
type InstState int

const (
	StateActive InstState = iota
	StateSuspended
	StateCompleted
	StateFailed
)

// StepState 是步骤状态。
type StepState int

const (
	StepPending StepState = iota
	StepRunning
	StepDone
)

// TermKind 是终局类别。
type TermKind int

const (
	TermNone TermKind = iota
	TermRejected
	TermExpired
)

// EvKind 是审计事件类别。
type EvKind int

const (
	EvAllow EvKind = iota
	EvDeny
	EvOverride
	EvReject
	EvExpire
)

// Event 是一条审计记录；Seq 在实例内从 1 连续编号。
type Event struct {
	Seq   int
	Kind  EvKind
	Step  int
	Miss  uint64
	Actor string
	At    int64
}

// StepOutcome 是 StartStep 的结果；Allowed 为 false 表示 Deny
// （Deny 是成功的结果，不是错误），Miss 为缺失位。
type StepOutcome struct {
	Index   int
	Allowed bool
	Miss    uint64
}

// StatusView 是 Status 返回的只读视图。
type StatusView struct {
	State    InstState
	Steps    []StepState
	Miss     uint64
	Deadline int64
	Term     TermKind
	TermAt   int64
}

type instance struct {
	principal string
	snapshot  uint64
	reqs      []uint64
	steps     []StepState
	running   int
	state     InstState
	miss      uint64
	deadline  int64
	term      TermKind
	termAt    int64
	audit     []Event
}

func (in *instance) record(kind EvKind, step int, miss uint64, actor string, at int64) {
	in.audit = append(in.audit, Event{
		Seq:   len(in.audit) + 1,
		Kind:  kind,
		Step:  step,
		Miss:  miss,
		Actor: actor,
		At:    at,
	})
}

func (in *instance) nextPending() int {
	for i, s := range in.steps {
		if s == StepPending {
			return i
		}
	}
	return -1
}

// expireIfDue 落实到期处理；调用方须持有 Runner 锁。
func (in *instance) expireIfDue(now int64) {
	if in.state == StateSuspended && in.deadline <= now {
		in.state = StateFailed
		in.term = TermExpired
		in.termAt = in.deadline
		in.record(EvExpire, in.nextPending(), 0, "", in.deadline)
	}
}

// Runner 管实例与审计日志；所有带 now 的操作在单锁下串行化。
type Runner struct {
	mu      sync.Mutex
	grants  *grants.Registry
	flows   *flow.Store
	timeout int64
	clock   int64
	insts   map[string]*instance
}

// New 构造 Runner；timeout 为挂起时限 T，合法范围 1 到 1e9。
func New(g *grants.Registry, f *flow.Store, timeout int64) (*Runner, error) {
	if g == nil || f == nil || timeout < 1 || timeout > maxTimeout {
		return nil, ErrParam
	}
	return &Runner{grants: g, flows: f, timeout: timeout, insts: make(map[string]*instance)}, nil
}

// checkNow 校验时钟参数：范围越界为 ErrParam，回退为 ErrClock。
func (r *Runner) checkNow(now int64) error {
	if now < 0 || now > MaxNow {
		return ErrParam
	}
	if now < r.clock {
		return ErrClock
	}
	return nil
}
