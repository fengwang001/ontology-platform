package scheduler

import (
	"errors"
	"sync"
)

var (
	ErrStarted      = errors.New("scheduler already started")
	ErrInvalidTask  = errors.New("invalid task parameters")
	ErrDuplicateID  = errors.New("duplicate task id")
	ErrTaskLimit    = errors.New("task limit reached")
	ErrInvalidStep  = errors.New("invalid step count")
	ErrTaskNotFound = errors.New("task not found")
	ErrTickNotFound = errors.New("tick not found")
)

const MaxTasks = 16

type TaskSpec struct {
	ID  string
	Phi int64
	T   int
	C   int
	M   int
	K   int
}

type Stats struct {
	Met             int64
	Missed          int64
	DynamicFailures int64
}

type Scheduler struct {
	mu      sync.Mutex
	started bool
	tasks   []*taskState
	byID    map[string]*taskState
	time    int64
	trace   []int
}

type taskState struct {
	spec            TaskSpec
	window          []int
	met             int64
	missed          int64
	dynamicFailures int64
	remaining       int
	deadline        int64
	hasJob          bool
}

func NewScheduler() *Scheduler {
	return &Scheduler{byID: make(map[string]*taskState)}
}

func (s *Scheduler) AddTask(spec TaskSpec) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.started {
		return ErrStarted
	}
	if !validSpec(spec) {
		return ErrInvalidTask
	}
	if _, exists := s.byID[spec.ID]; exists {
		return ErrDuplicateID
	}
	if len(s.tasks) == MaxTasks {
		return ErrTaskLimit
	}

	window := make([]int, spec.K)
	for i := range window {
		window[i] = 1
	}
	task := &taskState{spec: spec, window: window}
	s.byID[spec.ID] = task
	s.tasks = append(s.tasks, task)

	return nil
}

func (s *Scheduler) Step(n int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if n < 1 || n > 1_000_000 {
		return ErrInvalidStep
	}
	s.started = true

	for range n {
		s.stepOnce()
	}

	return nil
}

func (s *Scheduler) Window(id string) ([]int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	task, ok := s.byID[id]
	if !ok {
		return nil, ErrTaskNotFound
	}

	return append([]int(nil), task.window...), nil
}

func (s *Scheduler) Distance(id string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	task, ok := s.byID[id]
	if !ok {
		return 0, ErrTaskNotFound
	}

	return distance(task.window, task.spec.M), nil
}

func (s *Scheduler) Stats(id string) (Stats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	task, ok := s.byID[id]
	if !ok {
		return Stats{}, ErrTaskNotFound
	}

	return Stats{Met: task.met, Missed: task.missed, DynamicFailures: task.dynamicFailures}, nil
}

func (s *Scheduler) RunAt(tick int64) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if tick < 0 || tick >= int64(len(s.trace)) {
		return "", ErrTickNotFound
	}

	index := s.trace[tick]
	if index < 0 {
		return "", nil
	}

	return s.tasks[index].spec.ID, nil
}

func validSpec(spec TaskSpec) bool {
	if len(spec.ID) == 0 || len(spec.ID) > 32 {
		return false
	}
	if spec.Phi < 0 || spec.Phi > 1_000_000 {
		return false
	}
	if spec.T < 1 || spec.T > 1000 || spec.C < 1 || spec.C > spec.T {
		return false
	}

	return spec.M >= 1 && spec.M <= spec.K && spec.K <= 16
}

func (s *Scheduler) stepOnce() {
	t := s.time

	for _, task := range s.tasks {
		if task.hasJob && int64(task.remaining) > task.deadline-t {
			task.hasJob = false
			task.remaining = 0
			task.appendOutcome(0)
		}
	}

	for _, task := range s.tasks {
		if t >= task.spec.Phi && (t-task.spec.Phi)%int64(task.spec.T) == 0 {
			task.hasJob = true
			task.remaining = task.spec.C
			task.deadline = t + int64(task.spec.T)
		}
	}

	chosen := -1
	for i, task := range s.tasks {
		if !task.hasJob {
			continue
		}
		if chosen < 0 {
			chosen = i
			continue
		}

		current := s.tasks[chosen]
		currentKey := priorityKey{
			distance: distance(current.window, current.spec.M),
			deadline: current.deadline,
			id:       current.spec.ID,
		}
		candidateKey := priorityKey{
			distance: distance(task.window, task.spec.M),
			deadline: task.deadline,
			id:       task.spec.ID,
		}
		if candidateKey.less(currentKey) {
			chosen = i
		}
	}

	s.trace = append(s.trace, chosen)
	if chosen >= 0 {
		task := s.tasks[chosen]
		task.remaining--
		if task.remaining == 0 {
			task.hasJob = false
			task.appendOutcome(1)
		}
	}

	s.time++
}

type priorityKey struct {
	distance int
	deadline int64
	id       string
}

func (key priorityKey) less(other priorityKey) bool {
	if key.distance != other.distance {
		return key.distance < other.distance
	}
	if key.deadline != other.deadline {
		return key.deadline < other.deadline
	}

	return compareByteStrings(key.id, other.id) < 0
}

func compareByteStrings(left, right string) int {
	maxLength := len(left)
	if len(right) < maxLength {
		maxLength = len(right)
	}
	for i := 0; i < maxLength; i++ {
		if left[i] != right[i] {
			return int(left[i]) - int(right[i])
		}
	}

	return len(left) - len(right)
}

func (task *taskState) appendOutcome(outcome int) {
	k := len(task.window)
	copy(task.window, task.window[1:])
	task.window[k-1] = outcome

	metCount := 0
	for _, value := range task.window {
		metCount += value
	}
	if metCount < task.spec.M {
		task.dynamicFailures++
	}
	if outcome == 1 {
		task.met++
	} else {
		task.missed++
	}
}

func distance(window []int, required int) int {
	metCount := 0
	for _, value := range window {
		metCount += value
	}
	if metCount < required {
		return 0
	}

	copied := append([]int(nil), window...)
	for zeros := 0; zeros <= len(window); zeros++ {
		currentMet := 0
		start := zeros
		if start > len(copied) {
			start = len(copied)
		}
		for _, value := range copied[start:] {
			currentMet += value
		}
		if currentMet < required {
			return zeros
		}
	}

	return len(window) + 1
}
