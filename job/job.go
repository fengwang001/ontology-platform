// Package job 管理单个转码作业内各档任务的状态与重试。
package job

import (
	"errors"
	"sync"

	"ontology/ladder"
)

var (
	// ErrInvalidArgument 参数非法（空档名、size 越界等）。
	ErrInvalidArgument = errors.New("job: invalid argument")
	// ErrRungNotFound 档不在本作业阶梯内。
	ErrRungNotFound = errors.New("job: rung not in ladder")
	// ErrNotPending Start 要求档为 Pending。
	ErrNotPending = errors.New("job: rung is not pending")
	// ErrNotRunning Finish 要求档为 Running。
	ErrNotRunning = errors.New("job: rung is not running")
	// ErrTerminated 作业已终态（完成或 required 档失败）。
	ErrTerminated = errors.New("job: job terminated")
	// ErrBusy 作业内 Running 档数已达并行上限。
	ErrBusy = errors.New("job: concurrency limit reached")
)

// State 是单档任务状态。
type State int

const (
	Pending State = iota
	Running
	Done
	Failed
)

// Task 是一档任务的运行时状态。
type Task struct {
	Rung    ladder.Rung
	State   State
	Attempt int
	Size    int64
}

// Job 是单个作业。
type Job struct {
	mu     sync.Mutex
	ID     string
	Tasks  []*Task
	maxTry int
	index  map[string]*Task
}

// New 创建作业；maxTry = R+1。
func New(id string, rungs []ladder.Rung, maxTry int) *Job {
	tasks := make([]*Task, len(rungs))
	idx := make(map[string]*Task, len(rungs))
	for i := range rungs {
		t := &Task{Rung: rungs[i]}
		tasks[i] = t
		idx[rungs[i].Name] = t
	}
	return &Job{ID: id, Tasks: tasks, maxTry: maxTry, index: idx}
}

func (j *Job) lockedFailed() bool {
	for _, t := range j.Tasks {
		if t.Rung.Required && t.State == Failed {
			return true
		}
	}
	return false
}

func (j *Job) lockedDone() bool {
	for _, t := range j.Tasks {
		if t.State != Done && t.State != Failed {
			return false
		}
	}
	return !j.lockedFailed()
}

// Failed 报告作业是否已有 required 档终败。
func (j *Job) Failed() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.lockedFailed()
}

// Done 报告作业是否全部档到达终态且无 required 终败。
func (j *Job) Done() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.lockedDone()
}

// Snapshot 返回各档状态的快照（名称、状态、attempt、size、required）。
func (j *Job) Snapshot() []Task {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]Task, len(j.Tasks))
	for i, t := range j.Tasks {
		out[i] = *t
	}
	return out
}

// Start 要求 Pending，置 Running 且 attempt+1；并行上限由 limit（C）给出。
// 调用方须保证作业未终止。
func (j *Job) Start(rung string, limit int) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if rung == "" {
		return ErrInvalidArgument
	}
	t, ok := j.index[rung]
	if !ok {
		return ErrRungNotFound
	}
	if t.State != Pending {
		return ErrNotPending
	}
	running := 0
	for _, x := range j.Tasks {
		if x.State == Running {
			running++
		}
	}
	if running >= limit {
		return ErrBusy
	}
	t.Attempt++
	t.State = Running
	return nil
}

// RunningCount 返回当前 Running 档数。
func (j *Job) RunningCount() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	c := 0
	for _, t := range j.Tasks {
		if t.State == Running {
			c++
		}
	}
	return c
}

// Finish 处理 Running 档的成功或失败结果。
func (j *Job) Finish(rung string, ok bool, size int64) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if rung == "" || size < 0 || size > 1e12 {
		return ErrInvalidArgument
	}
	t, exists := j.index[rung]
	if !exists {
		return ErrRungNotFound
	}
	if t.State != Running {
		return ErrNotRunning
	}
	if ok {
		t.State = Done
		t.Size = size
		return nil
	}
	if t.Attempt >= j.maxTry {
		t.State = Failed
	} else {
		t.State = Pending
	}
	return nil
}
