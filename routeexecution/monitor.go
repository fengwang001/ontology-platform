package routeexecution

import (
	"fmt"
	"sync"
)

type Monitor struct {
	mu      sync.Mutex
	config  Config
	clock   uint64
	stops   []plannedStop
	results []nodeResult
	travel  map[string]map[string]uint64
	indexes map[string]int
	logger  func(OperationLog)
}

type Option func(*Monitor)

func WithOperationLogger(logger func(OperationLog)) Option {
	return func(monitor *Monitor) {
		monitor.logger = logger
	}
}

func NewMonitor(config Config, stops []Stop, durations []TravelDuration, options ...Option) (*Monitor, error) {
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	if len(stops) == 0 {
		return nil, fmt.Errorf("%w: at least one stop is required", ErrInvalidArgument)
	}

	ids := make(map[string]struct{}, len(stops)+1)
	plannedStops := make([]plannedStop, 0, len(stops))
	for index, stop := range stops {
		if err := validateStop(stop); err != nil {
			return nil, err
		}
		if _, exists := ids[stop.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate stop id %q", ErrInvalidArgument, stop.ID)
		}
		ids[stop.ID] = struct{}{}
		plannedStops = append(plannedStops, plannedStop{stop: stop})

		if index > 0 {
			continue
		}
		if config.OriginID != "" {
			if _, exists := ids[config.OriginID]; exists {
				return nil, fmt.Errorf("%w: origin id must not be a stop id", ErrInvalidArgument)
			}
			ids[config.OriginID] = struct{}{}
		}
	}

	travel, err := buildTravelTable(config, stops, durations, ids)
	if err != nil {
		return nil, err
	}

	monitor := &Monitor{
		config:  config,
		clock:   config.DepartureSeconds,
		stops:   plannedStops,
		travel:  travel,
		indexes: buildStopIndex(plannedStops),
	}
	for _, option := range options {
		option(monitor)
	}
	monitor.results = simulateSuffix(monitor.stops, travel, config, simulationSeed{index: -1}, 0)
	monitor.publishFrom(0, monitor.clock)
	return monitor, nil
}

func (m *Monitor) ReportArrival(stopID string, arrivalAt uint64, operatedAt uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if operatedAt < m.clock {
		err := ErrClockRollback
		m.logRejected(OperationReportArrival, stopID, arrivalAt, operatedAt, err, "operation time is before accepted clock")
		return err
	}
	index := m.indexOf(stopID)
	if index < 0 {
		err := ErrStopNotFound
		m.logRejected(OperationReportArrival, stopID, arrivalAt, operatedAt, err, "stop id does not exist")
		return err
	}
	current := m.results[index].result
	if current.Status == StatusCanceled {
		err := fmt.Errorf("%w: stop %q was canceled", ErrInvalidState, stopID)
		m.logRejected(OperationReportArrival, stopID, arrivalAt, operatedAt, err, "stop was canceled")
		return err
	}
	if current.Status == StatusSkipped {
		err := fmt.Errorf("%w: stop %q was skipped", ErrInvalidState, stopID)
		m.logRejected(OperationReportArrival, stopID, arrivalAt, operatedAt, err, "stop was skipped")
		return err
	}
	if current.Reported {
		err := fmt.Errorf("%w: stop %q was already reported", ErrInvalidState, stopID)
		m.logRejected(OperationReportArrival, stopID, arrivalAt, operatedAt, err, "arrival was already reported")
		return err
	}

	if previous := m.previousReported(index); previous != nil && arrivalAt <= previous.DepartureSeconds {
		err := ErrInvalidOrder
		m.logRejected(OperationReportArrival, stopID, arrivalAt, operatedAt, err, "actual arrival must be strictly after previous actual departure")
		return err
	}

	updatedStops := clonePlannedStops(m.stops)
	updatedStops[index].reported = true
	updatedStops[index].arrival = arrivalAt
	anchor := m.seedBeforeIn(m.results, updatedStops, index)
	simulationStart := anchor.index + 1
	updatedResults := m.resimulate(updatedStops, index, simulationStart, anchor)
	publishStart := index
	if updatedResults[index].result.Status == StatusSkipped {
		err := fmt.Errorf("%w: stop %q was skipped", ErrInvalidState, stopID)
		m.logRejected(OperationReportArrival, stopID, arrivalAt, operatedAt, err, "hard-window stop remains skipped with actual arrival")
		return err
	}
	if err := validateReportedOrder(updatedStops, updatedResults); err != nil {
		m.logRejected(OperationReportArrival, stopID, arrivalAt, operatedAt, err, "actual arrivals are not in strict departure order")
		return err
	}

	m.stops = updatedStops
	m.results = updatedResults
	m.clock = operatedAt
	m.publishFrom(publishStart, operatedAt)
	m.logAccepted(OperationReportArrival, stopID, arrivalAt, operatedAt)
	return nil
}

func (m *Monitor) CancelStop(stopID string, operatedAt uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if operatedAt < m.clock {
		err := ErrClockRollback
		m.logRejected(OperationCancelStop, stopID, 0, operatedAt, err, "operation time is before accepted clock")
		return err
	}
	index := m.indexOf(stopID)
	if index < 0 {
		err := ErrStopNotFound
		m.logRejected(OperationCancelStop, stopID, 0, operatedAt, err, "stop id does not exist")
		return err
	}
	current := m.results[index].result
	if current.Status == StatusCanceled || current.Status == StatusSkipped || current.Reported {
		err := fmt.Errorf("%w: stop %q cannot be canceled", ErrInvalidState, stopID)
		m.logRejected(OperationCancelStop, stopID, 0, operatedAt, err, "only unreached, unreported, unskipped stops can be canceled")
		return err
	}

	updatedStops := clonePlannedStops(m.stops)
	updatedStops[index].canceled = true
	anchor := m.seedBeforeIn(m.results, updatedStops, index)
	start := anchor.index + 1
	updatedResults := m.resimulate(updatedStops, index, start, anchor)

	m.stops = updatedStops
	m.results = updatedResults
	m.clock = operatedAt
	m.publishFrom(index, operatedAt)
	m.logAccepted(OperationCancelStop, stopID, 0, operatedAt)
	return nil
}

func (m *Monitor) logRejected(kind OperationKind, stopID string, arrival uint64, operatedAt uint64, err error, reason string) {
	if m.logger == nil {
		return
	}
	m.logger(OperationLog{
		Kind:           kind,
		StopID:         stopID,
		ArrivalSeconds: arrival,
		OperatedAt:     operatedAt,
		Accepted:       false,
		Error:          err,
		Reason:         reason,
		ClockSeconds:   m.clock,
		Snapshot:       m.snapshotLocked(),
	})
}

func (m *Monitor) logAccepted(kind OperationKind, stopID string, arrival uint64, operatedAt uint64) {
	if m.logger == nil {
		return
	}
	m.logger(OperationLog{
		Kind:           kind,
		StopID:         stopID,
		ArrivalSeconds: arrival,
		OperatedAt:     operatedAt,
		Accepted:       true,
		Reason:         "operation accepted; suffix resimulated and ETA policy applied",
		ClockSeconds:   m.clock,
		Snapshot:       m.snapshotLocked(),
	})
}

func (m *Monitor) snapshotLocked() Snapshot {
	stops := make([]StopResult, len(m.results))
	for index := range m.results {
		stops[index] = m.results[index].result
	}
	return Snapshot{ClockSeconds: m.clock, Stops: stops}
}

func (m *Monitor) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()

	stops := make([]StopResult, len(m.results))
	for index := range m.results {
		stops[index] = m.results[index].result
	}
	return Snapshot{ClockSeconds: m.clock, Stops: stops}
}

func (m *Monitor) indexOf(stopID string) int {
	if index, ok := m.indexes[stopID]; ok {
		return index
	}
	return -1
}

func (m *Monitor) seedBeforeIn(results []nodeResult, stops []plannedStop, index int) simulationSeed {
	if index == 0 {
		return simulationSeed{index: -1}
	}
	previous := results[index-1]
	if previous.prevPhysicalIndex < 0 {
		return simulationSeed{index: -1}
	}
	seed := results[previous.prevPhysicalIndex].physicalSeed
	seed.index = previous.prevPhysicalIndex
	return seed
}

func buildStopIndex(stops []plannedStop) map[string]int {
	indexes := make(map[string]int, len(stops))
	for index := range stops {
		indexes[stops[index].stop.ID] = index
	}
	return indexes
}

func (m *Monitor) previousReported(index int) *StopResult {
	for previous := index - 1; previous >= 0; previous-- {
		if !m.stops[previous].reported {
			continue
		}
		result := m.results[previous].result
		if result.Status != StatusCanceled && result.Status != StatusSkipped {
			return &result
		}
	}
	return nil
}

func (m *Monitor) resimulate(updatedStops []plannedStop, pivot int, start int, seed simulationSeed) []nodeResult {
	results := make([]nodeResult, len(m.results))
	copy(results, m.results)

	suffix := simulateSuffix(updatedStops, m.travel, m.config, seed, start)
	for index := start; index < len(results); index++ {
		previousPublication := results[index].result
		results[index] = suffix[index]
		results[index].result.PublishedETA = previousPublication.PublishedETA
		results[index].result.HasPublishedETA = previousPublication.HasPublishedETA
		if !results[index].result.HasPublishedETA {
			results[index].result.PublishedETA = 0
		}
	}
	return results
}

func (m *Monitor) publishFrom(start int, clock uint64) {
	for index := start; index < len(m.results); index++ {
		result := &m.results[index].result
		if !result.Active || result.Status == StatusCanceled || result.Status == StatusSkipped || result.Reported {
			result.PublishedETA = 0
			result.HasPublishedETA = false
			continue
		}

		nextETA := result.ArrivalSeconds
		if !result.HasPublishedETA {
			result.PublishedETA = nextETA
			result.HasPublishedETA = true
			continue
		}
		if withinLockWindow(nextETA, clock, m.config.LockWindowSeconds) {
			continue
		}
		difference := nextETA - result.PublishedETA
		if result.PublishedETA > nextETA {
			difference = result.PublishedETA - nextETA
		}
		if difference > m.config.DebounceSeconds {
			result.PublishedETA = nextETA
		}
	}
}

func withinLockWindow(eta uint64, clock uint64, lockWindow uint64) bool {
	if eta <= clock {
		return true
	}
	return eta-clock <= lockWindow
}

func validateReportedOrder(stops []plannedStop, results []nodeResult) error {
	for index := range stops {
		if !stops[index].reported {
			continue
		}
		arrival := results[index].result.ArrivalSeconds
		for previous := index - 1; previous >= 0; previous-- {
			if !stops[previous].reported {
				continue
			}
			result := results[previous].result
			if result.Status == StatusCanceled || result.Status == StatusSkipped {
				continue
			}
			if arrival <= result.DepartureSeconds {
				return ErrInvalidOrder
			}
		}
	}
	return nil
}

func clonePlannedStops(stops []plannedStop) []plannedStop {
	clone := make([]plannedStop, len(stops))
	copy(clone, stops)
	return clone
}

func validateConfig(config Config) error {
	if config.OriginID == "" {
		return fmt.Errorf("%w: origin id is required", ErrInvalidArgument)
	}
	if config.MaxContinuousDrivingSeconds == 0 {
		return fmt.Errorf("%w: maximum continuous driving must be positive", ErrInvalidArgument)
	}
	return nil
}

func validateStop(stop Stop) error {
	if stop.ID == "" {
		return fmt.Errorf("%w: stop id is required", ErrInvalidArgument)
	}
	if stop.Type != HardWindow && stop.Type != SoftWindow {
		return fmt.Errorf("%w: invalid window type for stop %q", ErrInvalidArgument, stop.ID)
	}
	if stop.Window.RightSeconds < stop.Window.LeftSeconds {
		return fmt.Errorf("%w: invalid time window for stop %q", ErrInvalidArgument, stop.ID)
	}
	return nil
}

func buildTravelTable(
	config Config,
	stops []Stop,
	durations []TravelDuration,
	ids map[string]struct{},
) (map[string]map[string]uint64, error) {
	travel := make(map[string]map[string]uint64)
	for _, duration := range durations {
		if duration.From == "" || duration.To == "" || !duration.Known {
			return nil, fmt.Errorf("%w: invalid travel duration %q -> %q", ErrInvalidArgument, duration.From, duration.To)
		}
		if _, ok := ids[duration.From]; !ok {
			return nil, fmt.Errorf("%w: unknown travel origin %q", ErrInvalidArgument, duration.From)
		}
		if _, ok := ids[duration.To]; !ok {
			return nil, fmt.Errorf("%w: unknown travel destination %q", ErrInvalidArgument, duration.To)
		}
		if duration.From == duration.To {
			return nil, fmt.Errorf("%w: self travel duration %q", ErrInvalidArgument, duration.From)
		}
		if travel[duration.From] == nil {
			travel[duration.From] = make(map[string]uint64)
		}
		if _, exists := travel[duration.From][duration.To]; exists {
			return nil, fmt.Errorf("%w: duplicate travel duration %q -> %q", ErrInvalidArgument, duration.From, duration.To)
		}
		travel[duration.From][duration.To] = duration.Seconds
	}

	required := make(map[string]map[string]struct{})
	addRequired := func(from string, to string) {
		if from == to {
			return
		}
		if required[from] == nil {
			required[from] = make(map[string]struct{})
		}
		required[from][to] = struct{}{}
	}
	for _, destination := range stops {
		addRequired(config.OriginID, destination.ID)
	}
	for _, origin := range stops {
		for _, destination := range stops {
			addRequired(origin.ID, destination.ID)
		}
	}

	for from, destinations := range required {
		for to := range destinations {
			if _, ok := travel[from][to]; !ok {
				return nil, fmt.Errorf("%w: missing travel duration %q -> %q", ErrInvalidArgument, from, to)
			}
		}
	}
	return travel, nil
}
