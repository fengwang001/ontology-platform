package routeexecution

import (
	"errors"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"testing"
)

type naiveState struct {
	config   Config
	clock    uint64
	stops    []Stop
	travel   map[string]map[string]uint64
	reported map[int]uint64
	canceled map[int]bool
	results  []naiveResult
}

type naiveResult struct {
	StopResult
	driving uint64
	seed    simulationSeed
}

type operation struct {
	kind       string
	stopIndex  int
	arrival    uint64
	operatedAt uint64
}

func newNaiveState(t *testing.T, config Config, stops []Stop, durations []TravelDuration) *naiveState {
	t.Helper()
	travel := make(map[string]map[string]uint64)
	for _, duration := range durations {
		if travel[duration.From] == nil {
			travel[duration.From] = make(map[string]uint64)
		}
		travel[duration.From][duration.To] = duration.Seconds
	}
	state := &naiveState{
		config:   config,
		clock:    config.DepartureSeconds,
		stops:    stops,
		travel:   travel,
		reported: make(map[int]uint64),
		canceled: make(map[int]bool),
	}
	state.recompute(0, 0)
	return state
}

func (s *naiveState) apply(op operation) error {
	switch op.kind {
	case "report":
		return s.report(op.stopIndex, op.arrival, op.operatedAt)
	case "cancel":
		return s.cancel(op.stopIndex, op.operatedAt)
	default:
		panic("unknown operation")
	}
}

func (s *naiveState) report(index int, arrival uint64, operatedAt uint64) error {
	if operatedAt < s.clock {
		return ErrClockRollback
	}
	if index < 0 || index >= len(s.stops) {
		return ErrStopNotFound
	}
	if s.canceled[index] {
		return ErrInvalidState
	}
	if s.results[index].Status == StatusSkipped {
		return ErrInvalidState
	}
	if _, exists := s.reported[index]; exists {
		return ErrInvalidState
	}

	results := s.plannedWith(func(planned []plannedStop) {
		planned[index].reported = true
		planned[index].arrival = arrival
	})
	if !naiveOrderValid(results) {
		return ErrInvalidOrder
	}

	s.reported[index] = arrival
	s.clock = operatedAt
	s.recompute(index, index)
	return nil
}

func (s *naiveState) cancel(index int, operatedAt uint64) error {
	if operatedAt < s.clock {
		return ErrClockRollback
	}
	if index < 0 || index >= len(s.stops) {
		return ErrStopNotFound
	}
	if s.canceled[index] || s.results[index].Status == StatusSkipped {
		return ErrInvalidState
	}
	if _, reported := s.reported[index]; reported {
		return ErrInvalidState
	}

	s.canceled[index] = true
	s.clock = operatedAt
	s.recompute(index, index)
	return nil
}

func (s *naiveState) plannedWith(mutate func([]plannedStop)) []nodeResult {
	planned := make([]plannedStop, len(s.stops))
	for index := range s.stops {
		planned[index] = plannedStop{stop: s.stops[index]}
		if arrival, reported := s.reported[index]; reported {
			planned[index].reported = true
			planned[index].arrival = arrival
		}
		planned[index].canceled = s.canceled[index]
	}
	mutate(planned)
	return simulateSuffix(planned, s.travel, s.config, simulationSeed{}, 0)
}

func naiveOrderValid(results []nodeResult) bool {
	for index := range results {
		node := results[index]
		if !node.result.Reported {
			continue
		}
		arrival := node.result.ArrivalSeconds
		for previous := index - 1; previous >= 0; previous-- {
			prior := results[previous].result
			if !prior.Reported || prior.Status == StatusCanceled || prior.Status == StatusSkipped {
				continue
			}
			if arrival <= prior.DepartureSeconds {
				return false
			}
		}
	}
	return true
}

func (s *naiveState) recompute(publishPivot int, simulationPivot int) {
	planned := make([]plannedStop, len(s.stops))
	for index := range s.stops {
		planned[index] = plannedStop{stop: s.stops[index]}
		if arrival, reported := s.reported[index]; reported {
			planned[index].reported = true
			planned[index].arrival = arrival
		}
		planned[index].canceled = s.canceled[index]
	}

	nodes := simulateSuffix(planned, s.travel, s.config, simulationSeed{}, 0)
	published := make(map[int]uint64)
	hasPublished := make(map[int]bool)
	for index := range s.results {
		if s.results[index].HasPublishedETA {
			published[index] = s.results[index].PublishedETA
			hasPublished[index] = true
		}
	}

	results := make([]naiveResult, len(nodes))
	for index, node := range nodes {
		result := node.result
		eligible := result.Active && result.Status != StatusCanceled && result.Status != StatusSkipped && !result.Reported
		if index < publishPivot {
			if old := s.results[index]; old.HasPublishedETA {
				result.HasPublishedETA = true
				result.PublishedETA = old.PublishedETA
			}
		} else if eligible {
			nextETA := result.ArrivalSeconds
			if !hasPublished[index] {
				result.PublishedETA = nextETA
				result.HasPublishedETA = true
			} else if !withinLockWindow(nextETA, s.clock, s.config.LockWindowSeconds) {
				old := published[index]
				difference := nextETA - old
				if old > nextETA {
					difference = old - nextETA
				}
				if difference > s.config.DebounceSeconds {
					result.PublishedETA = nextETA
				} else {
					result.PublishedETA = old
				}
				result.HasPublishedETA = true
			} else {
				result.PublishedETA = published[index]
				result.HasPublishedETA = true
			}
		}
		results[index] = naiveResult{
			StopResult: result,
			driving:    node.driving,
			seed:       node.seed,
		}
	}
	s.results = results
}

func randomScenario(r *rand.Rand) (Config, []Stop, []TravelDuration) {
	stopCount := 2 + r.Intn(6)
	config := Config{
		OriginID:                    "depot",
		DepartureSeconds:            uint64(r.Intn(20)),
		InitialContinuousSeconds:    uint64(r.Intn(7)),
		MaxContinuousDrivingSeconds: uint64(5 + r.Intn(11)),
		RestSeconds:                 uint64(2 + r.Intn(6)),
		DebounceSeconds:             uint64(1 + r.Intn(5)),
		LockWindowSeconds:           uint64(r.Intn(8)),
	}

	stops := make([]Stop, stopCount)
	for index := range stops {
		left := config.DepartureSeconds + uint64(r.Intn(40))
		right := left + uint64(r.Intn(25))
		kind := HardWindow
		if r.Intn(2) == 1 {
			kind = SoftWindow
		}
		stops[index] = Stop{
			ID:             string(rune('A' + index)),
			Window:         TimeWindow{LeftSeconds: left, RightSeconds: right},
			ServiceSeconds: uint64(r.Intn(6)),
			Type:           kind,
		}
	}

	durations := make([]TravelDuration, 0, (stopCount+1)*stopCount)
	ids := []string{config.OriginID}
	for _, stop := range stops {
		ids = append(ids, stop.ID)
	}
	for _, from := range ids {
		for _, to := range ids {
			if from == to {
				continue
			}
			durations = append(durations, TravelDuration{
				From:    from,
				To:      to,
				Seconds: uint64(r.Intn(13)),
				Known:   true,
			})
		}
	}
	return config, stops, durations
}

func TestRandomOperationsMatchNaiveModel(t *testing.T) {
	for seedValue := int64(0); seedValue < 300; seedValue++ {
		r := rand.New(rand.NewSource(seedValue))
		config, stops, durations := randomScenario(r)
		monitor, err := NewMonitor(config, stops, durations)
		if err != nil {
			t.Fatalf("seed %d: construction failed: %v", seedValue, err)
		}
		naive := newNaiveState(t, config, stops, durations)
		var log strings.Builder
		log.WriteString("seed=")
		log.WriteString(strconv.FormatInt(seedValue, 10))
		log.WriteString(" config=")
		log.WriteString(fmt.Sprintf("%+v", config))
		log.WriteString(" stops=")
		log.WriteString(fmt.Sprintf("%+v", stops))
		log.WriteString(" durations=")
		log.WriteString(fmt.Sprintf("%+v", durations))

		clock := config.DepartureSeconds
		for operationIndex := 0; operationIndex < 24; operationIndex++ {
			index := r.Intn(len(stops))
			if clock < 100 {
				clock += uint64(r.Intn(8))
			}
			kind := "report"
			if r.Intn(3) == 0 {
				kind = "cancel"
			}
			arrival := uint64(r.Intn(90))
			op := operation{
				kind:       kind,
				stopIndex:  index,
				arrival:    arrival,
				operatedAt: clock,
			}
			naiveErr := naive.apply(op)
			var monitorErr error
			if kind == "report" {
				monitorErr = monitor.ReportArrival(stops[index].ID, arrival, clock)
			} else {
				monitorErr = monitor.CancelStop(stops[index].ID, clock)
			}
			if naiveErr != nil || monitorErr != nil {
				log.WriteString(";")
				log.WriteString(kind)
			}
			log.WriteString(";")
			log.WriteString(kind)
			log.WriteString("(")
			log.WriteString(stops[index].ID)
			log.WriteString(",a=")
			log.WriteString(strconv.FormatUint(arrival, 10))
			log.WriteString(",op=")
			log.WriteString(strconv.FormatUint(clock, 10))
			log.WriteString(")->")
			if naiveErr != nil {
				log.WriteString(naiveErr.Error())
			} else {
				log.WriteString("ok")
			}
			log.WriteString("/")
			if monitorErr != nil {
				log.WriteString(monitorErr.Error())
			} else {
				log.WriteString("ok")
			}
			assertSameErrorWithLog(t, naiveErr, monitorErr, log.String())
			assertSnapshotsEqualWithLog(t, snapshotFromNaive(naive), monitor.Snapshot(), log.String())
		}
	}
}

func assertSameError(t *testing.T, want error, got error) {
	t.Helper()
	if !errorIs(want, got) {
		t.Fatalf("error mismatch: want %v got %v", want, got)
	}
}

func assertSameErrorWithLog(t *testing.T, want error, got error, log string) {
	t.Helper()
	if !errorIs(want, got) {
		t.Fatalf("error mismatch: want %v got %v\nlog: %s", want, got, log)
	}
}

func errorIs(want error, got error) bool {
	if want == nil || got == nil {
		return want == got
	}
	switch want {
	case ErrClockRollback:
		return errors.Is(got, ErrClockRollback)
	case ErrStopNotFound:
		return errors.Is(got, ErrStopNotFound)
	case ErrInvalidState:
		return errors.Is(got, ErrInvalidState)
	case ErrInvalidOrder:
		return errors.Is(got, ErrInvalidOrder)
	default:
		return want.Error() == got.Error()
	}
}

func snapshotFromNaive(state *naiveState) Snapshot {
	results := make([]StopResult, len(state.results))
	for index := range state.results {
		results[index] = state.results[index].StopResult
	}
	return Snapshot{ClockSeconds: state.clock, Stops: results}
}

func assertSnapshotsEqual(t *testing.T, want Snapshot, got Snapshot) {
	t.Helper()
	if want.ClockSeconds != got.ClockSeconds || len(want.Stops) != len(got.Stops) {
		t.Fatalf("snapshot mismatch\nwant=%+v\ngot =%+v", want, got)
	}
	for index := range want.Stops {
		if want.Stops[index] != got.Stops[index] {
			t.Fatalf("stop %d mismatch\nwant=%+v\ngot =%+v", index, want.Stops[index], got.Stops[index])
		}
	}
}

func assertSnapshotsEqualWithLog(t *testing.T, want Snapshot, got Snapshot, log string) {
	t.Helper()
	if want.ClockSeconds != got.ClockSeconds || len(want.Stops) != len(got.Stops) {
		t.Fatalf("snapshot mismatch\nwant=%+v\ngot =%+v\nlog: %s", want, got, log)
	}
	for index := range want.Stops {
		if want.Stops[index] != got.Stops[index] {
			t.Fatalf("stop %d mismatch\nwant=%+v\ngot =%+v\nlog: %s", index, want.Stops[index], got.Stops[index], log)
		}
	}
}
