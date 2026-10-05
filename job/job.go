// Package job 管理单个转码作业内各档任务的状态机、重试与并行上限。
package job

import "errors"

var (
	// ErrTerminated 作业已终止（失败态或完成态），不再接受 Start。
	ErrTerminated = errors.New("job: 作业已终止")
	// ErrRungNotFound 档不在阶梯。
	ErrRungNotFound = errors.New("job: 档不在阶梯")
	// ErrBadState 状态不符（Start 要求 Pending，Finish 要求 Running）。
	ErrBadState = errors.New("job: 状态不符")
	// ErrBusy 同一作业 Running 档数已达并行上限 C。
	ErrBusy = errors.New("job: 繁忙")
)

// State 档位任务状态。
type State int

const (
	Pending State = iota
	Running
	Done
	Failed
)

// Rung 作业视角的阶梯档。
type Rung struct {
	Name     string
	Required bool
}

// Job 单个转码作业。不是并发安全的，由上层串行化。
type Job struct {
	index    map[string]int
	required []bool
	state    []State
	attempt  []int
	size     []int64
	retry    int
	conc     int
	running  int
	open     int // 未到终态的档数
	failed   bool
}

// New 按阶梯（高度升序）创建作业，所有档初始为 Pending。
func New(rungs []Rung, retryLimit, concurrency int) *Job {
	j := &Job{
		index:    make(map[string]int, len(rungs)),
		required: make([]bool, len(rungs)),
		state:    make([]State, len(rungs)),
		attempt:  make([]int, len(rungs)),
		size:     make([]int64, len(rungs)),
		retry:    retryLimit,
		conc:     concurrency,
		open:     len(rungs),
	}
	for i, r := range rungs {
		j.index[r.Name] = i
		j.required[i] = r.Required
	}
	return j
}

// Failed 作业是否因任一 required 档 Failed 而进入失败态。
func (j *Job) Failed() bool { return j.failed }

// Complete 作业是否完成：全部档到达终态且无 required 档 Failed。
func (j *Job) Complete() bool { return !j.failed && j.open == 0 }

// Running 当前 Running 档数。
func (j *Job) Running() int { return j.running }

// StateOf 返回某档状态与是否在阶梯内。
func (j *Job) StateOf(name string) (State, bool) {
	i, ok := j.index[name]
	if !ok {
		return Pending, false
	}
	return j.state[i], true
}

// Attempt 返回某档已接受的 Start 次数（不在阶梯内返回 0）。
func (j *Job) Attempt(name string) int {
	i, ok := j.index[name]
	if !ok {
		return 0
	}
	return j.attempt[i]
}

// Size 返回 Done 档记录的 size。
func (j *Job) Size(name string) (int64, bool) {
	i, ok := j.index[name]
	if !ok {
		return 0, false
	}
	return j.size[i], true
}

func (j *Job) terminated() bool { return j.failed || j.open == 0 }

// Start 拒绝次序：作业已终止 > 档不在阶梯 > 状态不符 > 繁忙。
// 接受时置 Running 并令 attempt 加 1。
func (j *Job) Start(name string) error {
	if j.terminated() {
		return ErrTerminated
	}
	i, ok := j.index[name]
	if !ok {
		return ErrRungNotFound
	}
	if j.state[i] != Pending {
		return ErrBadState
	}
	if j.running >= j.conc {
		return ErrBusy
	}
	j.state[i] = Running
	j.attempt[i]++
	j.running++
	return nil
}

// Finish 拒绝次序：档不在阶梯 > 状态不符。接受时返回该档是否进入终态。
// ok 为真则 Done 并记 size；为假时 attempt <= R 回到 Pending，否则 Failed，
// required 档 Failed 使作业进入失败态（Running 档仍照常接受 Finish）。
func (j *Job) Finish(name string, ok bool, size int64) (terminal bool, err error) {
	i, found := j.index[name]
	if !found {
		return false, ErrRungNotFound
	}
	if j.state[i] != Running {
		return false, ErrBadState
	}
	j.running--
	if ok {
		j.state[i] = Done
		j.size[i] = size
		j.open--
		return true, nil
	}
	if j.attempt[i] <= j.retry {
		j.state[i] = Pending
		return false, nil
	}
	j.state[i] = Failed
	j.open--
	if j.required[i] {
		j.failed = true
	}
	return true, nil
}
