package admission

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"
)

type naiveState struct {
	clock   int64
	jobs    []naiveJob
	value   int64
	evicted []string
}

type naiveJob struct {
	id        string
	remaining int64
	deadline  int64
	tolerance int64
	value     int64
	started   bool
}

type naiveSubmitResult struct {
	accepted bool
	evicted  []string
	reason   error
	basis    string
}

func cloneNaive(state naiveState) naiveState {
	copied := state
	copied.jobs = append([]naiveJob(nil), state.jobs...)
	copied.evicted = append([]string(nil), state.evicted...)
	return copied
}

func naiveEDFLess(left, right naiveJob) bool {
	if left.deadline != right.deadline {
		return left.deadline < right.deadline
	}
	return left.id < right.id
}

func naiveAdvance(state *naiveState, now int64) {
	for state.clock < now {
		if len(state.jobs) == 0 {
			state.clock = now
			return
		}

		sort.SliceStable(state.jobs, func(i, j int) bool {
			return naiveEDFLess(state.jobs[i], state.jobs[j])
		})

		running := &state.jobs[0]
		running.started = true
		running.remaining--
		state.clock++

		if running.remaining == 0 {
			late := state.clock - running.deadline
			if late < 0 {
				late = 0
			}
			denominator := running.tolerance + 1
			state.value += running.value * (denominator - late) / denominator
			state.jobs = state.jobs[1:]
		}
	}
}

func naiveFeasible(jobs []naiveJob, start int64) bool {
	current := start
	for _, pending := range jobs {
		current += pending.remaining
		if current > pending.deadline+pending.tolerance {
			return false
		}
	}
	return true
}

func naiveLowerDensity(candidate, best naiveJob) bool {
	left := candidate.value * best.remaining
	right := best.value * candidate.remaining
	if left != right {
		return left < right
	}
	return candidate.id > best.id
}

func naiveLeastDensity(jobs []naiveJob) int {
	best := -1
	for i, pending := range jobs {
		if pending.started {
			continue
		}
		if best < 0 || naiveLowerDensity(pending, jobs[best]) {
			best = i
		}
	}
	return best
}

func naiveSubmit(base naiveState, id string, now, execution, deadline, tolerance, value int64) (naiveState, naiveSubmitResult) {
	state := cloneNaive(base)
	naiveAdvance(&state, now)

	for _, pending := range state.jobs {
		if pending.id == id {
			return base, naiveSubmitResult{reason: ErrDuplicateID, basis: "post-advance pending id still exists"}
		}
	}
	if now+execution > deadline+tolerance {
		return base, naiveSubmitResult{reason: ErrImpossible, basis: "now+C exceeds d+M on a dedicated processor"}
	}

	state.jobs = append(state.jobs, naiveJob{
		id:        id,
		remaining: execution,
		deadline:  deadline,
		tolerance: tolerance,
		value:     value,
	})
	sort.SliceStable(state.jobs, func(i, j int) bool {
		return naiveEDFLess(state.jobs[i], state.jobs[j])
	})

	evictedNow := make([]string, 0)
	if naiveFeasible(state.jobs, state.clock) {
		return state, naiveSubmitResult{
			accepted: true,
			evicted:  evictedNow,
			basis:    fmt.Sprintf("one EDF scan from clock=%d is feasible", state.clock),
		}
	}

	for !naiveFeasible(state.jobs, state.clock) {
		index := naiveLeastDensity(state.jobs)
		if index < 0 {
			return base, naiveSubmitResult{reason: ErrOverloaded, basis: "no unstarted job can be evicted"}
		}

		removed := state.jobs[index]
		state.jobs = append(state.jobs[:index], state.jobs[index+1:]...)
		if removed.id == id {
			return base, naiveSubmitResult{
				reason:  ErrOverloaded,
				evicted: nil,
				basis:   "new job selected for eviction; all provisional evictions are restored",
			}
		}
		evictedNow = append(evictedNow, removed.id)
	}

	state.evicted = append(state.evicted, evictedNow...)
	return state, naiveSubmitResult{
		accepted: true,
		evicted:  evictedNow,
		basis:    fmt.Sprintf("feasible after evicting %v in density order", evictedNow),
	}
}

func pendingFromNaive(state naiveState) []PendingJob {
	result := make([]PendingJob, 0, len(state.jobs))
	for _, pending := range state.jobs {
		result = append(result, PendingJob{
			ID:        pending.id,
			Remaining: pending.remaining,
			Started:   pending.started,
		})
	}
	return result
}

func sameReason(left, right error) bool {
	return errors.Is(left, right) || errors.Is(right, left)
}

func sameStringSlice(left, right []string) bool {
	if len(left) == 0 && len(right) == 0 {
		return true
	}
	return reflect.DeepEqual(left, right)
}

func TestRandomSequencesMatchNaiveUnitSimulation(t *testing.T) {
	const sequences = 2000
	const operations = 10

	for seed := int64(1); seed <= sequences; seed++ {
		t.Run(fmt.Sprintf("seed_%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			controller := New()
			oracle := naiveState{
				jobs:    make([]naiveJob, 0),
				evicted: make([]string, 0),
			}
			var now int64

			for operation := 0; operation < operations; operation++ {
				if rng.Intn(5) == 0 {
					now += int64(rng.Intn(4))
					input := fmt.Sprintf("Advance(now=%d)", now)
					err := controller.Advance(now)
					naiveAdvance(&oracle, now)
					t.Logf("input=%s output=err=%v pending=%v value=%d basis=run EDF head one unit at a time", input, err, controller.Pending(), controller.Value())
					if err != nil {
						t.Fatalf("Advance returned unexpected error: %v", err)
					}
				} else {
					id := fmt.Sprintf("j%d", rng.Intn(4))
					execution := int64(1 + rng.Intn(6))
					deadline := now + int64(rng.Intn(12))
					tolerance := int64(rng.Intn(5))
					value := int64(1 + rng.Intn(12))

					input := fmt.Sprintf("Submit(id=%s,now=%d,C=%d,d=%d,M=%d,v=%d)", id, now, execution, deadline, tolerance, value)
					result := controller.Submit(id, now, execution, deadline, tolerance, value)
					nextOracle, expected := naiveSubmit(oracle, id, now, execution, deadline, tolerance, value)
					t.Logf("input=%s output=accepted=%v evicted=%v reason=%v pending=%v value=%d basis=%s",
						input, result.Accepted, result.Evicted, result.Reason, controller.Pending(), controller.Value(), expected.basis)

					if result.Accepted != expected.accepted ||
						!sameStringSlice(result.Evicted, expected.evicted) ||
						!sameReason(result.Reason, expected.reason) {
						t.Fatalf("submit mismatch: got=%+v want=%+v", result, expected)
					}
					oracle = nextOracle
				}

				if got, want := controller.Pending(), pendingFromNaive(oracle); !reflect.DeepEqual(got, want) {
					t.Fatalf("pending mismatch: got=%+v want=%+v clock=%d oracleClock=%d", got, want, controller.clock, oracle.clock)
				}
				if controller.Value() != oracle.value {
					t.Fatalf("value = %d, want %d", controller.Value(), oracle.value)
				}
				if got, want := controller.Evicted(), append([]string(nil), oracle.evicted...); !sameStringSlice(got, want) {
					t.Fatalf("evicted = %v, want %v", got, want)
				}
				if controller.clock != oracle.clock {
					t.Fatalf("clock = %d, want %d", controller.clock, oracle.clock)
				}
			}
		})
	}
}

func TestConcurrentOperationsAndQueries(t *testing.T) {
	controller := New()
	var wait sync.WaitGroup

	for worker := 0; worker < 16; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			for step := 0; step < 100; step++ {
				now := int64(worker*100 + step)
				id := fmt.Sprintf("w%d-%d", worker, step%3)
				_ = controller.Submit(id, now, 1, now+10, 2, int64(step+1))
				_ = controller.Pending()
				_ = controller.Value()
				_ = controller.Evicted()
			}
		}(worker)
	}

	wait.Wait()
}
