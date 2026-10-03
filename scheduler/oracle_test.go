package scheduler

import (
	"fmt"
	"math/rand"
	"testing"
)

type oracleTask struct {
	spec      TaskSpec
	window    []int
	met       int64
	missed    int64
	dynamic   int64
	running   bool
	remaining int
	deadline  int64
}

type oracle struct {
	tasks []*oracleTask
	trace []string
}

func newOracle(specs []TaskSpec) *oracle {
	result := &oracle{}
	for _, spec := range specs {
		window := make([]int, spec.K)
		for i := range window {
			window[i] = 1
		}
		result.tasks = append(result.tasks, &oracleTask{spec: spec, window: window})
	}
	return result
}

func (sim *oracle) step(t int64) {
	for _, task := range sim.tasks {
		if task.running && int64(task.remaining) > task.deadline-t {
			task.running = false
			task.remaining = 0
			sim.append(task, 0)
		}
	}

	for _, task := range sim.tasks {
		if t >= task.spec.Phi && (t-task.spec.Phi)%int64(task.spec.T) == 0 {
			task.running = true
			task.remaining = task.spec.C
			task.deadline = t + int64(task.spec.T)
		}
	}

	chosen := (*oracleTask)(nil)
	for _, task := range sim.tasks {
		if !task.running {
			continue
		}
		if chosen == nil {
			chosen = task
			continue
		}
		taskDistance := oracleDistance(task.window, task.spec.M)
		chosenDistance := oracleDistance(chosen.window, chosen.spec.M)
		if taskDistance < chosenDistance ||
			(taskDistance == chosenDistance && task.deadline < chosen.deadline) ||
			(taskDistance == chosenDistance && task.deadline == chosen.deadline && compareByteStrings(task.spec.ID, chosen.spec.ID) < 0) {
			chosen = task
		}
	}

	if chosen == nil {
		sim.trace = append(sim.trace, "")
		return
	}

	sim.trace = append(sim.trace, chosen.spec.ID)
	chosen.remaining--
	if chosen.remaining == 0 {
		chosen.running = false
		sim.append(chosen, 1)
	}
}

func (sim *oracle) append(task *oracleTask, outcome int) {
	copy(task.window, task.window[1:])
	task.window[len(task.window)-1] = outcome

	metCount := 0
	for _, value := range task.window {
		metCount += value
	}
	if metCount < task.spec.M {
		task.dynamic++
	}
	if outcome == 1 {
		task.met++
	} else {
		task.missed++
	}
}

func oracleDistance(window []int, required int) int {
	metCount := 0
	for _, value := range window {
		metCount += value
	}
	if metCount < required {
		return 0
	}

	for zeros := 0; zeros <= len(window); zeros++ {
		currentMet := 0
		for i := zeros; i < len(window); i++ {
			currentMet += window[i]
		}
		if currentMet < required {
			return zeros
		}
	}
	return len(window) + 1
}

func TestRandomOracleDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(20261003))

	for iteration := 0; iteration < 2000; iteration++ {
		specs := randomSpecs(rng)
		totalSteps := 1 + rng.Intn(40)
		splitAt := rng.Intn(totalSteps + 1)

		full := NewScheduler()
		split := NewScheduler()
		for _, spec := range specs {
			if err := full.AddTask(spec); err != nil {
				t.Fatalf("case %d full AddTask(%+v): %v", iteration, spec, err)
			}
			if err := split.AddTask(spec); err != nil {
				t.Fatalf("case %d split AddTask(%+v): %v", iteration, spec, err)
			}
		}

		sim := newOracle(specs)
		for tick := int64(0); tick < int64(totalSteps); tick++ {
			sim.step(tick)
		}

		if err := full.Step(totalSteps); err != nil {
			t.Fatalf("case %d full Step(%d): %v", iteration, totalSteps, err)
		}
		if splitAt > 0 {
			if err := split.Step(splitAt); err != nil {
				t.Fatalf("case %d split Step(%d): %v", iteration, splitAt, err)
			}
		}
		if remainder := totalSteps - splitAt; remainder > 0 {
			if err := split.Step(remainder); err != nil {
				t.Fatalf("case %d split Step(%d): %v", iteration, remainder, err)
			}
		}

		t.Logf("case=%d input=%+v steps=%d split=%d output_trace=%v reason=four-step miss/release/select/complete compared with independent oracle",
			iteration, specs, totalSteps, splitAt, sim.trace)
		assertOracleState(t, iteration, full, sim, totalSteps)
		assertOracleState(t, iteration, split, sim, totalSteps)

		for tick := int64(0); tick < int64(totalSteps); tick++ {
			fullID, err := full.RunAt(tick)
			if err != nil {
				t.Fatalf("case %d full RunAt(%d): %v", iteration, tick, err)
			}
			splitID, err := split.RunAt(tick)
			if err != nil {
				t.Fatalf("case %d split RunAt(%d): %v", iteration, tick, err)
			}
			if fullID != sim.trace[tick] || splitID != sim.trace[tick] {
				t.Fatalf("case %d tick %d: full=%q split=%q oracle=%q", iteration, tick, fullID, splitID, sim.trace[tick])
			}
		}
	}
}

func randomSpecs(rng *rand.Rand) []TaskSpec {
	n := 1 + rng.Intn(MaxTasks)
	specs := make([]TaskSpec, 0, n)
	used := make(map[string]bool)
	for i := 0; i < n; i++ {
		id := randomID(rng, used)
		period := 1 + rng.Intn(8)
		k := 1 + rng.Intn(16)
		m := 1 + rng.Intn(k)
		specs = append(specs, TaskSpec{
			ID:  id,
			Phi: int64(rng.Intn(8)),
			T:   period,
			C:   1 + rng.Intn(period),
			M:   m,
			K:   k,
		})
	}
	return specs
}

func randomID(rng *rand.Rand, used map[string]bool) string {
	for {
		length := 1 + rng.Intn(6)
		bytes := make([]byte, length)
		for i := range bytes {
			bytes[i] = byte('a' + rng.Intn(6))
		}
		id := string(bytes)
		if !used[id] {
			used[id] = true
			return id
		}
	}
}

func assertOracleState(t *testing.T, iteration int, s *Scheduler, sim *oracle, steps int) {
	t.Helper()
	for index, expected := range sim.tasks {
		window, err := s.Window(expected.spec.ID)
		if err != nil {
			t.Fatalf("case %d Window(%s): %v", iteration, expected.spec.ID, err)
		}
		distanceValue, err := s.Distance(expected.spec.ID)
		if err != nil {
			t.Fatalf("case %d Distance(%s): %v", iteration, expected.spec.ID, err)
		}
		actualStats, err := s.Stats(expected.spec.ID)
		if err != nil {
			t.Fatalf("case %d Stats(%s): %v", iteration, expected.spec.ID, err)
		}

		expectedStats := Stats{Met: expected.met, Missed: expected.missed, DynamicFailures: expected.dynamic}
		expectedDistance := oracleDistance(expected.window, expected.spec.M)
		if fmt.Sprint(window) != fmt.Sprint(expected.window) ||
			distanceValue != expectedDistance ||
			actualStats != expectedStats {
			t.Fatalf("case %d task %d/%s: window=%v want %v; distance=%d want %d; stats=%+v want %+v; steps=%d",
				iteration, index, expected.spec.ID, window, expected.window,
				distanceValue, expectedDistance, actualStats, expectedStats, steps)
		}
		if actualStats.Met+actualStats.Missed > int64(steps) {
			t.Fatalf("case %d task %s judged more jobs than ticks", iteration, expected.spec.ID)
		}
	}
}
