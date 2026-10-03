package scheduler

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

type naiveState struct {
	trace []string
	now   int
	tasks []*naiveTask
	byID  map[string]*naiveTask
}

type naiveTask struct {
	spec      Task
	window    []int
	remaining int
	deadline  int
	hasJob    bool
	met       int
	missed    int
	failures  int
}

func newNaiveState(tasks []Task) *naiveState {
	state := &naiveState{byID: make(map[string]*naiveTask)}
	for _, spec := range tasks {
		entry := &naiveTask{
			spec:   spec,
			window: make([]int, spec.WindowSize),
		}
		for i := range entry.window {
			entry.window[i] = 1
		}
		state.tasks = append(state.tasks, entry)
		state.byID[spec.ID] = entry
	}
	return state
}

func (state *naiveState) step() {
	t := state.now
	ordered := append([]*naiveTask(nil), state.tasks...)
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].spec.ID < ordered[j].spec.ID
	})

	for _, entry := range ordered {
		if entry.hasJob && entry.remaining > entry.deadline-t {
			state.append(entry, 0)
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

	var selected *naiveTask
	for _, entry := range ordered {
		if entry.hasJob && (selected == nil || state.before(entry, selected)) {
			selected = entry
		}
	}

	runID := ""
	if selected != nil {
		runID = selected.spec.ID
		selected.remaining--
		if selected.remaining == 0 {
			state.append(selected, 1)
			selected.hasJob = false
			selected.deadline = 0
		}
	}
	state.trace = append(state.trace, runID)
	state.now++
}

func (state *naiveState) before(candidate, current *naiveTask) bool {
	candidateDistance := naiveDistance(candidate.window, candidate.spec.RequiredHits)
	currentDistance := naiveDistance(current.window, current.spec.RequiredHits)
	if candidateDistance != currentDistance {
		return candidateDistance < currentDistance
	}
	if candidate.deadline != current.deadline {
		return candidate.deadline < current.deadline
	}
	return candidate.spec.ID < current.spec.ID
}

func (state *naiveState) append(entry *naiveTask, met int) {
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

func naiveDistance(window []int, required int) int {
	hits := 0
	for _, value := range window {
		hits += value
	}
	if hits < required {
		return 0
	}
	for dropped := 1; dropped <= len(window); dropped++ {
		remainingHits := 0
		for _, value := range window[dropped:] {
			remainingHits += value
		}
		if remainingHits < required {
			return dropped
		}
	}
	return len(window)
}

func TestRandomDifferential2000(t *testing.T) {
	rng := rand.New(rand.NewSource(20261003))
	for iteration := range 2000 {
		tasks := randomTasks(rng)
		total := 1 + rng.Intn(40)

		scheduler := buildScheduler(t, tasks)
		naive := newNaiveState(tasks)

		elapsed := 0
		for elapsed < total {
			n := 1 + rng.Intn(total-elapsed)
			if err := scheduler.Step(n); err != nil {
				t.Fatalf("iteration=%d Step(%d): %v", iteration, n, err)
			}
			for range n {
				naive.step()
			}
			elapsed += n
			assertNaiveState(t, iteration, total, tasks, scheduler, naive, elapsed)
		}

		t.Logf("用例%04d 输入 tasks=%v total=%d；输出 trace=%s；判定依据：与朴素四步模拟器逐 tick、逐窗口、逐统计一致", iteration, tasks, total, traceString(scheduler, total))
	}
}

func randomTasks(rng *rand.Rand) []Task {
	count := 1 + rng.Intn(16)
	used := make(map[string]bool)
	tasks := make([]Task, 0, count)
	for len(tasks) < count {
		id := randomID(rng, used)
		used[id] = true
		period := 1 + rng.Intn(8)
		windowSize := 1 + rng.Intn(16)
		tasks = append(tasks, Task{
			ID:           id,
			Phase:        rng.Intn(5),
			Period:       period,
			Execution:    1 + rng.Intn(period),
			RequiredHits: 1 + rng.Intn(windowSize),
			WindowSize:   windowSize,
		})
	}
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].ID < tasks[j].ID
	})
	return tasks
}

func randomID(rng *rand.Rand, used map[string]bool) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_"
	for {
		id := fmt.Sprintf("t%c", alphabet[rng.Intn(len(alphabet))])
		if !used[id] {
			return id
		}
	}
}

func assertNaiveState(t *testing.T, iteration, total int, tasks []Task, actual *Scheduler, expected *naiveState, elapsed int) {
	t.Helper()
	actualTrace := traceString(actual, elapsed)
	expectedTrace := ""
	for _, id := range expected.trace {
		if id == "" {
			expectedTrace += "."
		} else {
			expectedTrace += id
		}
	}
	if actualTrace != expectedTrace {
		t.Fatalf("iteration=%d tasks=%v elapsed=%d trace actual=%q expected=%q", iteration, tasks, elapsed, actualTrace, expectedTrace)
	}

	for _, expectedTask := range expected.tasks {
		id := expectedTask.spec.ID
		window := mustWindow(t, actual, id)
		if !reflect.DeepEqual(window, expectedTask.window) {
			t.Fatalf("iteration=%d %s window actual=%v expected=%v", iteration, id, window, expectedTask.window)
		}
		distance := mustDistance(t, actual, id)
		if distance != naiveDistance(expectedTask.window, expectedTask.spec.RequiredHits) {
			t.Fatalf("iteration=%d %s distance actual=%d expected=%d", iteration, id, distance, naiveDistance(expectedTask.window, expectedTask.spec.RequiredHits))
		}
		stats := mustStats(t, actual, id)
		expectedStats := Stats{
			Met:             expectedTask.met,
			Missed:          expectedTask.missed,
			DynamicFailures: expectedTask.failures,
		}
		if stats != expectedStats {
			t.Fatalf("iteration=%d %s stats actual=%+v expected=%+v", iteration, id, stats, expectedStats)
		}
	}
}
