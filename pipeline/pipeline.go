// Package pipeline 实现按依赖图推进的流水线作业状态机。
//
// 所有操作在单把互斥锁内串行完成，并发调用等价于某个串行顺序；
// 评估采用 BFS 级联，一次状态变更触发的评估次数被该作业的传递
// 下游数限界，与图中无关作业数无关。
package pipeline

import (
	"errors"
	"fmt"
	"sync"

	"ontology/dag"
	"ontology/rule"
)

var (
	ErrInvalidArgument = errors.New("pipeline: invalid argument")
	ErrNotFound        = errors.New("pipeline: job not found")
	ErrInvalidState    = errors.New("pipeline: invalid job state")
)

// State 是单个作业的状态。
type State int

const (
	StateCreated State = iota
	StatePending
	StateRunning
	StateManual
	StateSuccess
	StateFailed
	StateSkipped
	StateCanceled
)

func (s State) String() string {
	switch s {
	case StateCreated:
		return "Created"
	case StatePending:
		return "Pending"
	case StateRunning:
		return "Running"
	case StateManual:
		return "Manual"
	case StateSuccess:
		return "Success"
	case StateFailed:
		return "Failed"
	case StateSkipped:
		return "Skipped"
	case StateCanceled:
		return "Canceled"
	}
	return "unknown"
}

// Terminal 报告状态是否为终态。
func (s State) Terminal() bool {
	switch s {
	case StateSuccess, StateFailed, StateSkipped, StateCanceled:
		return true
	}
	return false
}

// Status 是流水线整体状态。
type Status int

const (
	StatusRunning Status = iota
	StatusBlocked
	StatusFailed
	StatusCanceled
	StatusSuccess
)

func (s Status) String() string {
	switch s {
	case StatusRunning:
		return "Running"
	case StatusBlocked:
		return "Blocked"
	case StatusFailed:
		return "Failed"
	case StatusCanceled:
		return "Canceled"
	case StatusSuccess:
		return "Success"
	}
	return "unknown"
}

type job struct {
	spec    dag.Job
	when    rule.When
	state   State
	retries int
}

// Pipeline 是一条流水线的完整可复现状态。
type Pipeline struct {
	mu    sync.Mutex
	graph *dag.Graph
	jobs  map[string]*job
	order []string
	evals int
}

// New 校验作业规格并构造流水线；无 needs 的作业在返回前完成评估。
func New(specs []dag.Job) (*Pipeline, error) {
	g, err := dag.New(specs)
	if err != nil {
		return nil, err
	}
	p := &Pipeline{graph: g, jobs: make(map[string]*job, len(specs))}
	for _, spec := range g.Jobs() {
		w, _ := rule.Parse(spec.When)
		p.jobs[spec.Name] = &job{spec: spec, when: w, state: StateCreated}
		p.order = append(p.order, spec.Name)
	}
	for _, name := range p.order {
		if len(p.jobs[name].spec.Needs) == 0 {
			p.evaluate(name)
			p.cascade(name)
		}
	}
	return p, nil
}

// settled 报告作业对下游而言是否已定：终态，或非阻塞人工闸。
func (p *Pipeline) settled(name string) bool {
	j := p.jobs[name]
	return j.state.Terminal() || (j.state == StateManual && j.spec.AllowFailure)
}

func (p *Pipeline) allNeedsSettled(name string) bool {
	for _, need := range p.jobs[name].spec.Needs {
		if !p.settled(need) {
			return false
		}
	}
	return true
}

// evaluate 在全部 needs 已定时把 Created 作业评估恰一次。
func (p *Pipeline) evaluate(name string) {
	j := p.jobs[name]
	var bad, skip bool
	for _, need := range j.spec.Needs {
		n := p.jobs[need]
		switch {
		case n.state == StateFailed && !n.spec.AllowFailure:
			bad = true
		case n.state == StateSkipped || n.state == StateCanceled:
			skip = true
		}
	}
	switch rule.Evaluate(j.when, bad, skip) {
	case rule.ToPending:
		j.state = StatePending
	case rule.ToSkipped:
		j.state = StateSkipped
	case rule.ToManual:
		j.state = StateManual
	}
	p.evals++
}

// cascade 从已定的 name 出发，评估因此而全部 needs 已定的下游，如此级联。
func (p *Pipeline) cascade(name string) {
	if !p.settled(name) {
		return
	}
	queue := []string{name}
	seen := map[string]bool{name: true}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, nxt := range p.graph.DirectDownstream(cur) {
			j := p.jobs[nxt]
			if j.state != StateCreated || !p.allNeedsSettled(nxt) {
				continue
			}
			p.evaluate(nxt)
			if p.settled(nxt) && !seen[nxt] {
				seen[nxt] = true
				queue = append(queue, nxt)
			}
		}
	}
}

func (p *Pipeline) lookup(name string) (*job, error) {
	if name == "" {
		return nil, fmt.Errorf("%w: empty job name", ErrInvalidArgument)
	}
	j, ok := p.jobs[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrNotFound, name)
	}
	return j, nil
}

// Start 把 Pending 作业转为 Running。
func (p *Pipeline) Start(name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	j, err := p.lookup(name)
	if err != nil {
		return err
	}
	if j.state != StatePending {
		return fmt.Errorf("%w: Start needs Pending, %q is %s", ErrInvalidState, name, j.state)
	}
	j.state = StateRunning
	return nil
}

// Finish 结束 Running 作业。ok 为假且重试未耗尽时回到 Pending，
// 已用重试数加一，此时不评估下游；否则转为 Failed。
func (p *Pipeline) Finish(name string, ok bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	j, err := p.lookup(name)
	if err != nil {
		return err
	}
	if j.state != StateRunning {
		return fmt.Errorf("%w: Finish needs Running, %q is %s", ErrInvalidState, name, j.state)
	}
	switch {
	case ok:
		j.state = StateSuccess
	case j.retries < j.spec.Retry:
		j.retries++
		j.state = StatePending
		return nil
	default:
		j.state = StateFailed
	}
	p.cascade(name)
	return nil
}

// Play 把等待人工的 Manual 作业转为 Pending。
func (p *Pipeline) Play(name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	j, err := p.lookup(name)
	if err != nil {
		return err
	}
	if j.state != StateManual {
		return fmt.Errorf("%w: Play needs Manual, %q is %s", ErrInvalidState, name, j.state)
	}
	j.state = StatePending
	return nil
}

// Cancel 把所有非终态作业（含 Created 与 Manual）转为 Canceled，之后不再评估。
func (p *Pipeline) Cancel() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, j := range p.jobs {
		if !j.state.Terminal() {
			j.state = StateCanceled
		}
	}
}

// RetryJob 把 Failed 或 Canceled 的作业转回 Pending 并清零已用重试数，
// 其全部传递下游无论原状态一律重置为 Created 并清零重试数。
// 目标的传递下游中存在 Running 时拒绝。
func (p *Pipeline) RetryJob(name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	j, err := p.lookup(name)
	if err != nil {
		return err
	}
	if j.state != StateFailed && j.state != StateCanceled {
		return fmt.Errorf("%w: RetryJob needs Failed or Canceled, %q is %s", ErrInvalidState, name, j.state)
	}
	closure := p.graph.TransitiveDownstream(name)
	for nxt := range closure {
		if p.jobs[nxt].state == StateRunning {
			return fmt.Errorf("%w: downstream %q is Running", ErrInvalidState, nxt)
		}
	}
	j.state = StatePending
	j.retries = 0
	for nxt := range closure {
		p.jobs[nxt].state = StateCreated
		p.jobs[nxt].retries = 0
	}
	// 目标转为 Pending（未定），闭包内每个作业沿依赖路径必有未定的
	// need，因此不存在可立即评估的 Created 作业，无需级联。
	return nil
}

// Status 按 Running > Blocked > Failed > Canceled > Success 的次序
// 取第一个成立者；Success 时附 Warnings（被允许的 Failed 作业数）。
func (p *Pipeline) Status() (Status, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var running, blocked, failed, canceled bool
	var warnings int
	for _, name := range p.order {
		j := p.jobs[name]
		switch j.state {
		case StateRunning, StatePending:
			running = true
		case StateManual:
			if !j.spec.AllowFailure {
				blocked = true
			}
		case StateFailed:
			if j.spec.AllowFailure {
				warnings++
			} else {
				failed = true
			}
		case StateCanceled:
			canceled = true
		}
	}
	switch {
	case running:
		return StatusRunning, 0
	case blocked:
		return StatusBlocked, 0
	case failed:
		return StatusFailed, 0
	case canceled:
		return StatusCanceled, 0
	}
	return StatusSuccess, warnings
}

// State 返回单个作业的当前状态。
func (p *Pipeline) State(name string) (State, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	j, err := p.lookup(name)
	if err != nil {
		return StateCreated, err
	}
	return j.state, nil
}

// Snapshot 返回全部作业状态的副本，按键序与声明次序无关。
func (p *Pipeline) Snapshot() map[string]State {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string]State, len(p.jobs))
	for name, j := range p.jobs {
		out[name] = j.state
	}
	return out
}
