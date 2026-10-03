package scheduler

import (
	"bytes"
	"errors"
	"sort"
	"sync"
)

var (
	ErrStarted      = errors.New("scheduler has started")
	ErrInvalidTask  = errors.New("invalid task parameters")
	ErrDuplicateID  = errors.New("duplicate task id")
	ErrTaskLimit    = errors.New("task limit reached")
	ErrInvalidStep  = errors.New("step count must be between 1 and 1000000")
	ErrTaskNotFound = errors.New("task not found")
)

type Task struct {
	ID           string
	Phase        int
	Period       int
	Execution    int
	RequiredHits int
	WindowSize   int
}

type Stats struct {
	Met             int
	Missed          int
	DynamicFailures int
}

type task struct {
	spec      Task
	window    []int
	remaining int
	deadline  int
	hasJob    bool
	met       int
	missed    int
	failures  int
}

type Scheduler struct {
	mu      sync.RWMutex
	started bool
	tasks   []*task
	byID    map[string]*task
	now     int
	trace   []string
}

func New() *Scheduler {
	return &Scheduler{}
}

func (s *Scheduler) AddTask(spec Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.started {
		return ErrStarted
	}
	if !validTask(spec) {
		return ErrInvalidTask
	}
	if s.byID == nil {
		s.byID = make(map[string]*task)
	}
	if _, exists := s.byID[spec.ID]; exists {
		return ErrDuplicateID
	}
	if len(s.tasks) >= 16 {
		return ErrTaskLimit
	}

	entry := &task{
		spec:   spec,
		window: make([]int, spec.WindowSize),
	}
	for i := range entry.window {
		entry.window[i] = 1
	}
	s.tasks = append(s.tasks, entry)
	s.byID[spec.ID] = entry
	return nil
}

func (s *Scheduler) Step(n int) error {
	if n < 1 || n > 1_000_000 {
		return ErrInvalidStep
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.byID == nil {
		s.byID = make(map[string]*task)
	}
	s.started = true

	for range n {
		s.stepOnce()
	}
	return nil
}

func (s *Scheduler) Window(id string) ([]int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, err := s.lookup(id)
	if err != nil {
		return nil, err
	}
	window := append([]int(nil), entry.window...)
	return window, nil
}

func (s *Scheduler) Distance(id string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, err := s.lookup(id)
	if err != nil {
		return 0, err
	}
	return distance(entry.window, entry.spec.RequiredHits), nil
}

func (s *Scheduler) Stats(id string) (Stats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, err := s.lookup(id)
	if err != nil {
		return Stats{}, err
	}
	return Stats{
		Met:             entry.met,
		Missed:          entry.missed,
		DynamicFailures: entry.failures,
	}, nil
}

func (s *Scheduler) RunAt(tick int) string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if tick < 0 || tick >= len(s.trace) {
		return ""
	}
	return s.trace[tick]
}

func validTask(task Task) bool {
	if task.ID == "" || len(task.ID) > 32 {
		return false
	}
	if task.Phase < 0 || task.Phase > 1_000_000 {
		return false
	}
	if task.Period < 1 || task.Period > 1000 {
		return false
	}
	if task.Execution < 1 || task.Execution > task.Period {
		return false
	}
	return task.RequiredHits >= 1 &&
		task.RequiredHits <= task.WindowSize &&
		task.WindowSize <= 16
}

func (s *Scheduler) lookup(id string) (*task, error) {
	entry, ok := s.byID[id]
	if !ok {
		return nil, ErrTaskNotFound
	}
	return entry, nil
}

func (s *Scheduler) stepOnce() {
	t := s.now
	ordered := append([]*task(nil), s.tasks...)
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].spec.ID < ordered[j].spec.ID
	})

	for _, entry := range ordered {
		if entry.hasJob && entry.remaining > entry.deadline-t {
			s.judge(entry, 0)
			entry.hasJob = false
			entry.remaining = 0
		}
	}

	for _, entry := range ordered {
		if t >= entry.spec.Phase && (t-entry.spec.Phase)%entry.spec.Period == 0 && !entry.hasJob {
			entry.hasJob = true
			entry.remaining = entry.spec.Execution
			entry.deadline = t + entry.spec.Period
		}
	}

	var selected *task
	for _, entry := range ordered {
		if !entry.hasJob {
			continue
		}
		if selected == nil || scheduledBefore(entry, selected) {
			selected = entry
		}
	}

	runID := ""
	if selected != nil {
		runID = selected.spec.ID
		selected.remaining--
		if selected.remaining == 0 {
			s.judge(selected, 1)
			selected.hasJob = false
			selected.deadline = 0
		}
	}
	s.trace = append(s.trace, runID)
	s.now++
}

func scheduledBefore(candidate, current *task) bool {
	candidateDistance := distance(candidate.window, candidate.spec.RequiredHits)
	currentDistance := distance(current.window, current.spec.RequiredHits)
	if candidateDistance != currentDistance {
		return candidateDistance < currentDistance
	}
	if candidate.deadline != current.deadline {
		return candidate.deadline < current.deadline
	}
	return bytes.Compare([]byte(candidate.spec.ID), []byte(current.spec.ID)) < 0
}

func (s *Scheduler) judge(entry *task, met int) {
	entry.window = append(entry.window[1:], met)
	if met == 1 {
		entry.met++
	} else {
		entry.missed++
	}

	hits := 0
	for _, value := range entry.window {
		hits += value
	}
	if hits < entry.spec.RequiredHits {
		entry.failures++
	}
}

func distance(window []int, required int) int {
	if hits(window) < required {
		return 0
	}
	for dropped := 1; dropped <= len(window); dropped++ {
		if hits(window[dropped:]) < required {
			return dropped
		}
	}
	return len(window)
}

func hits(window []int) int {
	total := 0
	for _, value := range window {
		total += value
	}
	return total
}
