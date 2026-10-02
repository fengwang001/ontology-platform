package ontology

import (
	"errors"
	"math/big"
	"sort"
	"sync"
)

var (
	ErrInvalidConfig = errors.New("invalid outlier ejector configuration")
	ErrInvalidHost   = errors.New("host index out of range")
	ErrInvalidTime   = errors.New("time must be between 0 and 1000000000000000")
	ErrClockRollback = errors.New("time must not be earlier than the latest accepted time")
)

type Result string

const (
	ResultRecorded Result = "recorded"
	ResultIgnored  Result = "ignored"
	ResultEjected  Result = "ejected"
	ResultBlocked  Result = "blocked_by_limit"
)

type Event struct {
	Host int
	OK   bool
	Now  int64
}

type OutlierEjector struct {
	mu     sync.Mutex
	hosts  []hostState
	n      int
	k      int
	b      int64
	cap    int64
	p      int
	wn     int
	q      int
	maxNow int64
}

type hostState struct {
	consecutiveFailures int
	ejectionCount       int
	ejectionEnd         int64
	window              []bool
}

func NewOutlierEjector(hostCount int, failureThreshold int, baseDuration int64, durationCap int64, maxEjectionPercent int, windowSize int, failureRateThreshold int) (*OutlierEjector, error) {
	switch {
	case hostCount < 1,
		failureThreshold < 1,
		baseDuration < 1,
		baseDuration > 1_000_000_000,
		durationCap < baseDuration,
		durationCap > 1_000_000_000,
		maxEjectionPercent < 0,
		maxEjectionPercent > 100,
		windowSize < 1,
		windowSize > 64,
		failureRateThreshold < 1,
		failureRateThreshold > 100:
		return nil, ErrInvalidConfig
	}

	return &OutlierEjector{
		hosts: make([]hostState, hostCount),
		n:     hostCount,
		k:     failureThreshold,
		b:     baseDuration,
		cap:   durationCap,
		p:     maxEjectionPercent,
		wn:    windowSize,
		q:     failureRateThreshold,
	}, nil
}

func (e *OutlierEjector) Report(host int, ok bool, now int64) (Result, error) {
	if err := validateHostTime(host, now, e.n); err != nil {
		return "", err
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if now < e.maxNow {
		return "", ErrClockRollback
	}

	result := e.reportLocked(host, ok, now)
	e.maxNow = now
	return result, nil
}

func (e *OutlierEjector) ReportBatch(events []Event) ([]Result, error) {
	if len(events) == 0 {
		return []Result{}, nil
	}

	minNow := events[0].Now
	for _, event := range events {
		if err := validateHostTime(event.Host, event.Now, e.n); err != nil {
			return nil, err
		}
		if event.Now < minNow {
			minNow = event.Now
		}
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if minNow < e.maxNow {
		return nil, ErrClockRollback
	}

	order := make([]int, len(events))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool {
		return events[order[i]].Now < events[order[j]].Now
	})

	results := make([]Result, len(events))
	var batchMaxNow int64
	for _, inputIndex := range order {
		event := events[inputIndex]
		results[inputIndex] = e.reportLocked(event.Host, event.OK, event.Now)
		if event.Now > batchMaxNow {
			batchMaxNow = event.Now
		}
	}
	e.maxNow = batchMaxNow

	return results, nil
}

func (e *OutlierEjector) Ejected(host int, now int64) (bool, error) {
	if err := validateHostTime(host, now, e.n); err != nil {
		return false, err
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if now < e.maxNow {
		return false, ErrClockRollback
	}
	return e.ejectedLocked(host, now), nil
}

func (e *OutlierEjector) Healthy(now int64) ([]int, error) {
	if now < 0 || now > 1_000_000_000_000_000 {
		return nil, ErrInvalidTime
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if now < e.maxNow {
		return nil, ErrClockRollback
	}

	healthy := make([]int, 0, e.n)
	for host := range e.hosts {
		if !e.ejectedLocked(host, now) {
			healthy = append(healthy, host)
		}
	}
	return healthy, nil
}

func (e *OutlierEjector) reportLocked(host int, ok bool, now int64) Result {
	state := &e.hosts[host]
	if e.ejectedLocked(host, now) {
		return ResultIgnored
	}

	e.appendWindowLocked(state, ok)
	if ok {
		state.consecutiveFailures = 0
		if state.ejectionCount > 0 {
			state.ejectionCount--
		}
		return ResultRecorded
	}

	state.consecutiveFailures++
	failureCount := 0
	for _, success := range state.window {
		if !success {
			failureCount++
		}
	}

	consecutiveTrigger := state.consecutiveFailures >= e.k
	windowTrigger := len(state.window) == e.wn && failureCount*100 >= e.q*e.wn
	if !consecutiveTrigger && !windowTrigger {
		return ResultRecorded
	}

	ejectedCount := 0
	for candidate := range e.hosts {
		if e.ejectedLocked(candidate, now) {
			ejectedCount++
		}
	}
	allowedLimit := new(big.Int).Mul(big.NewInt(int64(e.p)), big.NewInt(int64(e.n)))
	requested := new(big.Int).Mul(big.NewInt(int64(ejectedCount+1)), big.NewInt(100))
	if requested.Cmp(allowedLimit) > 0 {
		return ResultBlocked
	}

	state.ejectionCount++
	state.ejectionEnd = now + e.ejectionDurationLocked(state.ejectionCount)
	state.consecutiveFailures = 0
	state.window = state.window[:0]
	return ResultEjected
}

func (e *OutlierEjector) appendWindowLocked(state *hostState, ok bool) {
	if len(state.window) == e.wn {
		copy(state.window, state.window[1:])
		state.window[e.wn-1] = ok
		return
	}
	state.window = append(state.window, ok)
}

func (e *OutlierEjector) ejectionDurationLocked(ejectionCount int) int64 {
	count := int64(ejectionCount)
	minimumCountForCap := (e.cap + e.b - 1) / e.b
	if count >= minimumCountForCap {
		return e.cap
	}
	return e.b * count
}

func (e *OutlierEjector) ejectedLocked(host int, now int64) bool {
	return e.hosts[host].ejectionEnd > now
}

func validateHostTime(host int, now int64, hostCount int) error {
	if host < 0 || host >= hostCount {
		return ErrInvalidHost
	}
	if now < 0 || now > 1_000_000_000_000_000 {
		return ErrInvalidTime
	}
	return nil
}
