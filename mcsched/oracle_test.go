package mcsched

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

type refJob struct {
	taskIndex int
	index     int
	deadline  int
	executed  int
	remaining int
}

type referenceSimulator struct {
	tasks   []Task
	demands map[string]map[int]int
	active  map[int]refJob
	trace   string
	mode    Criticality
	stats   Stats
}

func TestRandomOracleComparison(t *testing.T) {
	for seed := int64(1); seed <= 2000; seed++ {
		tasks, demands, horizon := randomScenario(seed)
		actual := NewMonitor()
		for _, task := range tasks {
			mustAdd(t, actual, task)
		}
		for id, jobDemands := range demands {
			for index, demand := range jobDemands {
				mustSet(t, actual, id, index, demand)
			}
		}
		if err := actual.Step(horizon); err != nil {
			t.Fatalf("seed %d Step: %v", seed, err)
		}

		expected := simulateReference(tasks, demands, horizon)
		actualTrace := fullTrace(actual, horizon)
		basis := "ordered each t as deadline-miss, HI recovery, release, highest-priority execution, then end-of-tick switch/discard"
		t.Logf("seed=%d input_tasks=%v input_demands=%v horizon=%d output_trace=%q output_mode=%v output_stats=%+v oracle_trace=%q oracle_mode=%v oracle_stats=%+v basis=%q",
			seed, tasks, demands, horizon, actualTrace, actual.Mode(), actual.Stats(),
			expected.trace, expected.mode, expected.stats, basis)

		if actualTrace != expected.trace {
			t.Fatalf("seed %d trace mismatch:\nactual   %q\nexpected %q", seed, actualTrace, expected.trace)
		}
		if actual.Mode() != expected.mode || actual.Stats() != expected.stats || actual.CurrentTime() != horizon {
			t.Fatalf("seed %d state mismatch: actual mode=%v stats=%+v time=%d; expected mode=%v stats=%+v time=%d",
				seed, actual.Mode(), actual.Stats(), actual.CurrentTime(), expected.mode, expected.stats, horizon)
		}
	}
}

func simulateReference(tasks []Task, demands map[string]map[int]int, horizon int) referenceSimulator {
	sim := referenceSimulator{
		tasks:   append([]Task(nil), tasks...),
		demands: demands,
		active:  make(map[int]refJob),
		mode:    LO,
	}

	for now := 0; now < horizon; now++ {
		for index := range sim.tasks {
			job, ok := sim.active[index]
			if ok && job.deadline == now {
				delete(sim.active, index)
				if sim.tasks[index].Crit == HI {
					sim.stats.MissedHI++
				} else {
					sim.stats.MissedLO++
				}
			}
		}

		if sim.mode == HI && len(sim.active) == 0 {
			sim.mode = LO
			sim.stats.ModeRecoveries++
		}

		for index, task := range sim.tasks {
			if now < task.Phi || (now-task.Phi)%task.T != 0 {
				continue
			}
			jobIndex := (now - task.Phi) / task.T
			if task.Crit == LO && sim.mode == HI {
				sim.stats.SkippedLO++
				continue
			}
			if _, exists := sim.active[index]; exists {
				continue
			}
			sim.active[index] = refJob{
				taskIndex: index,
				index:     jobIndex,
				deadline:  now + task.T,
				remaining: sim.demand(task.ID, jobIndex),
			}
		}

		selectedTask := -1
		selectedPriority := 0
		for index, current := range sim.active {
			priority := sim.tasks[index].Prio
			if selectedPriority == 0 || priority < selectedPriority {
				selectedTask = index
				selectedPriority = priority
				_ = current
			}
		}

		if selectedTask < 0 {
			sim.trace += "."
			continue
		}

		task := sim.tasks[selectedTask]
		current := sim.active[selectedTask]
		current.executed++
		current.remaining--
		sim.trace += task.ID

		if current.remaining == 0 {
			delete(sim.active, selectedTask)
			sim.stats.Completed++
			continue
		}
		sim.active[selectedTask] = current
		if sim.mode == LO && task.Crit == HI && current.executed == task.CL {
			sim.mode = HI
			sim.stats.ModeSwitches++
			for index := range sim.active {
				if sim.tasks[index].Crit == LO {
					delete(sim.active, index)
					sim.stats.DiscardedLO++
				}
			}
		}
	}

	return sim
}

func (s referenceSimulator) demand(id string, index int) int {
	if jobDemands := s.demands[id]; jobDemands != nil {
		if demand, ok := jobDemands[index]; ok {
			return demand
		}
	}
	for _, task := range s.tasks {
		if task.ID == id {
			return task.CL
		}
	}
	return 0
}

func randomScenario(seed int64) ([]Task, map[string]map[int]int, int) {
	random := rand.New(rand.NewSource(seed))
	count := 1 + random.Intn(16)
	tasks := make([]Task, count)
	priorities := random.Perm(count)

	for i := 0; i < count; i++ {
		period := 1 + random.Intn(8)
		cl := 1 + random.Intn(period)
		crit := LO
		ch := cl
		if random.Intn(2) == 1 {
			crit = HI
			ch = cl + random.Intn(period-cl+1)
		}
		tasks[i] = Task{
			ID:   fmt.Sprintf("%c", 'A'+i),
			Crit: crit,
			CL:   cl,
			CH:   ch,
			T:    period,
			Prio: priorities[i] + 1,
			Phi:  random.Intn(4),
		}
	}

	horizon := 20 + random.Intn(21)
	demands := make(map[string]map[int]int)
	for _, task := range tasks {
		if task.Phi > horizon {
			continue
		}
		lastIndex := (horizon - task.Phi) / task.T
		for index := 0; index <= lastIndex; index++ {
			if random.Intn(10) >= 7 {
				continue
			}
			maxDemand := task.CH
			if demands[task.ID] == nil {
				demands[task.ID] = make(map[int]int)
			}
			demands[task.ID][index] = 1 + random.Intn(maxDemand)
		}
	}

	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })
	return tasks, demands, horizon
}

func randomScenarioMonitor(t *testing.T, seed int64) *Monitor {
	t.Helper()
	tasks, demands, _ := randomScenario(seed)
	monitor := NewMonitor()
	for _, task := range tasks {
		mustAdd(t, monitor, task)
	}
	for id, jobDemands := range demands {
		for index, demand := range jobDemands {
			mustSet(t, monitor, id, index, demand)
		}
	}
	return monitor
}

func fullTrace(monitor *Monitor, ticks int) string {
	result := ""
	for index := 0; index < ticks; index++ {
		if id, ok := monitor.RunAt(index); ok {
			result += id
		} else {
			result += "."
		}
	}
	return result
}
