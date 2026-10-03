package mcsched

import (
	"errors"
	"math"
	"sort"
	"sync"
)

var (
	ErrStarted       = errors.New("monitor has started")
	ErrInvalidTask   = errors.New("invalid task")
	ErrDuplicateID   = errors.New("duplicate task id")
	ErrDuplicatePrio = errors.New("duplicate priority")
	ErrTaskLimit     = errors.New("task limit reached")
	ErrUnknownTask   = errors.New("unknown task id")
	ErrInvalidDemand = errors.New("invalid demand")
	ErrJobReleased   = errors.New("job already released")
	ErrInvalidStep   = errors.New("invalid step count")
)

type Criticality int

const (
	LO Criticality = iota
	HI
)

type Task struct {
	ID   string
	Crit Criticality
	CL   int
	CH   int
	T    int
	Prio int
	Phi  int
}

type Stats struct {
	ModeSwitches   int
	ModeRecoveries int
	DiscardedLO    int
	SkippedLO      int
	MissedLO       int
	MissedHI       int
	Completed      int
}

type job struct {
	taskID    string
	index     int
	deadline  int
	executed  int
	remaining int
}

type Monitor struct {
	mu      sync.Mutex
	tasks   map[string]Task
	prios   map[int]string
	demands map[string]map[int]int
	active  map[string]job
	trace   []string
	time    int
	mode    Criticality
	started bool
	stats   Stats
}

func NewMonitor() *Monitor {
	return &Monitor{
		tasks:   make(map[string]Task),
		prios:   make(map[int]string),
		demands: make(map[string]map[int]int),
		active:  make(map[string]job),
		mode:    LO,
	}
}

func (m *Monitor) AddTask(task Task) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.started {
		return ErrStarted
	}
	if !validTask(task) {
		return ErrInvalidTask
	}
	if _, exists := m.tasks[task.ID]; exists {
		return ErrDuplicateID
	}
	if _, exists := m.prios[task.Prio]; exists {
		return ErrDuplicatePrio
	}
	if len(m.tasks) == 16 {
		return ErrTaskLimit
	}

	m.tasks[task.ID] = task
	m.prios[task.Prio] = task.ID
	return nil
}

func (m *Monitor) SetDemand(id string, jobIndex, demand int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	task, exists := m.tasks[id]
	if !exists {
		return ErrUnknownTask
	}

	release, valid := jobRelease(task, jobIndex)
	maxDemand := task.CL
	if task.Crit == HI {
		maxDemand = task.CH
	}
	if !valid || demand < 1 || demand > maxDemand {
		return ErrInvalidDemand
	}
	if release < m.time {
		return ErrJobReleased
	}

	if m.demands[id] == nil {
		m.demands[id] = make(map[int]int)
	}
	m.demands[id][jobIndex] = demand
	return nil
}

func (m *Monitor) Step(ticks int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if ticks < 1 || ticks > 1_000_000 {
		return ErrInvalidStep
	}

	m.started = true
	for range ticks {
		m.stepOnce()
	}
	return nil
}

func (m *Monitor) Mode() Criticality {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mode
}

func (m *Monitor) Stats() Stats {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stats
}

func (m *Monitor) RunAt(tick int) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if tick < 0 || tick >= len(m.trace) || m.trace[tick] == "" {
		return "", false
	}
	return m.trace[tick], true
}

func (m *Monitor) CurrentTime() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.time
}

func validTask(task Task) bool {
	if len(task.ID) == 0 || len(task.ID) > 32 || task.T < 1 || task.T > 1000 {
		return false
	}
	if task.Prio <= 0 || task.Phi < 0 || task.Phi > 1_000_000 {
		return false
	}
	if task.CL < 1 || task.CH < task.CL || task.CH > task.T {
		return false
	}
	switch task.Crit {
	case LO:
		return task.CH == task.CL
	case HI:
		return true
	default:
		return false
	}
}

func jobRelease(task Task, index int) (int, bool) {
	if index < 0 || index > (math.MaxInt-task.Phi)/task.T {
		return 0, false
	}
	return task.Phi + index*task.T, true
}

func (m *Monitor) stepOnce() {
	now := m.time
	m.handleMissedDeadlines(now)

	if m.mode == HI && len(m.active) == 0 {
		m.mode = LO
		m.stats.ModeRecoveries++
	}

	m.releaseJobs(now)

	current := m.selectedJob()
	if current.taskID == "" {
		m.trace = append(m.trace, "")
		m.time = now + 1
		return
	}

	task := m.tasks[current.taskID]
	current.executed++
	current.remaining--
	m.trace = append(m.trace, current.taskID)

	if current.remaining == 0 {
		delete(m.active, current.taskID)
		m.stats.Completed++
	} else {
		m.active[current.taskID] = current
		if m.mode == LO && task.Crit == HI && current.executed == task.CL {
			m.mode = HI
			m.stats.ModeSwitches++
			m.discardLOJobs()
		}
	}

	m.time = now + 1
}

func (m *Monitor) handleMissedDeadlines(now int) {
	for _, task := range m.orderedTasks() {
		current, ok := m.active[task.ID]
		if ok && current.deadline == now {
			delete(m.active, task.ID)
			if task.Crit == HI {
				m.stats.MissedHI++
			} else {
				m.stats.MissedLO++
			}
		}
	}
}

func (m *Monitor) releaseJobs(now int) {
	for _, task := range m.orderedTasks() {
		if now < task.Phi || (now-task.Phi)%task.T != 0 {
			continue
		}
		index := (now - task.Phi) / task.T
		if task.Crit == LO && m.mode == HI {
			m.stats.SkippedLO++
			continue
		}
		if _, exists := m.active[task.ID]; exists {
			continue
		}
		m.active[task.ID] = job{
			taskID:    task.ID,
			index:     index,
			deadline:  now + task.T,
			remaining: m.demand(task, index),
		}
	}
}

func (m *Monitor) selectedJob() job {
	var selected job
	bestPrio := 0
	for _, current := range m.active {
		task := m.tasks[current.taskID]
		if bestPrio == 0 || task.Prio < bestPrio {
			selected = current
			bestPrio = task.Prio
		}
	}
	return selected
}

func (m *Monitor) discardLOJobs() {
	for id := range m.active {
		if m.tasks[id].Crit == LO {
			delete(m.active, id)
			m.stats.DiscardedLO++
		}
	}
}

func (m *Monitor) demand(task Task, index int) int {
	if overrides := m.demands[task.ID]; overrides != nil {
		if demand, ok := overrides[index]; ok {
			return demand
		}
	}
	return task.CL
}

func (m *Monitor) orderedTasks() []Task {
	tasks := make([]Task, 0, len(m.tasks))
	for _, task := range m.tasks {
		tasks = append(tasks, task)
	}
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].Prio < tasks[j].Prio
	})
	return tasks
}
