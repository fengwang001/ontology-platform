package sla

import (
	"errors"
	"sort"
	"sync"
)

var (
	// ErrInvalidArgument indicates a constructor, fee, or interval argument is outside its domain.
	ErrInvalidArgument = errors.New("非法参数")
	// ErrMonthAlreadyOpen indicates that NewMonth was called before the current month closed.
	ErrMonthAlreadyOpen = errors.New("月已开放")
	// ErrNoOpenMonth indicates that a month operation was called without an open month.
	ErrNoOpenMonth = errors.New("无开放月")
	// ErrExcludeLimitExceeded indicates that an exclusion window exceeds the monthly union limit.
	ErrExcludeLimitExceeded = errors.New("排除超限")
	// ErrNotFound indicates that Revoke did not find an identical registered failure interval.
	ErrNotFound = errors.New("不存在")
)

// Config defines the month length, availability tiers, escalation rules, and annual limits.
type Config struct {
	LengthMinutes       int
	JitterMinutes       int
	Tier1Availability   int64
	Tier2Availability   int64
	Tier3Availability   int64
	Tier1Credit         int
	Tier2Credit         int
	Tier3Credit         int
	EscalationStep      int
	EscalationCapMonths int
	AnnualCreditLimit   int64
	MonthlyExcludeLimit int
}

type interval struct {
	start int
	end   int
}

// Settlement is a concurrency-safe monthly service-credit state machine.
type Settlement struct {
	mu                sync.Mutex
	config            Config
	month             int
	open              bool
	fee               int64
	reports           []interval
	excludes          []interval
	streak            int
	annualPaidCredits map[int]int64
}

// CloseResult contains the deterministic settlement values for one month.
type CloseResult struct {
	DowntimeMinutes int
	Availability    int64
	BasePercent     int
	AppliedPercent  int
	Credit          int64
}

// New validates the configuration and creates an empty settlement state.
func New(config Config) (*Settlement, error) {
	if config.LengthMinutes < 1 || config.LengthMinutes > 1_000_000 ||
		config.JitterMinutes < 0 || config.JitterMinutes > config.LengthMinutes ||
		config.Tier1Availability < 1 || config.Tier1Availability > 1_000_000 ||
		config.Tier2Availability < 1 || config.Tier2Availability > 1_000_000 ||
		config.Tier3Availability < 1 || config.Tier3Availability > 1_000_000 ||
		!(config.Tier1Availability > config.Tier2Availability &&
			config.Tier2Availability > config.Tier3Availability) ||
		!validCreditPoint(config.Tier1Credit) ||
		!validCreditPoint(config.Tier2Credit) ||
		!validCreditPoint(config.Tier3Credit) ||
		!(config.Tier1Credit < config.Tier2Credit &&
			config.Tier2Credit < config.Tier3Credit) ||
		config.EscalationStep < 0 || config.EscalationStep > 100 ||
		config.EscalationCapMonths < 0 || config.EscalationCapMonths > 12 ||
		config.AnnualCreditLimit < 0 || config.AnnualCreditLimit > 1_000_000_000_000 ||
		config.MonthlyExcludeLimit < 0 ||
		config.MonthlyExcludeLimit > config.LengthMinutes {
		return nil, ErrInvalidArgument
	}

	return &Settlement{
		config:            config,
		annualPaidCredits: make(map[int]int64),
	}, nil
}

// NewMonth opens the next month with the supplied monthly fee in cents.
func (s *Settlement) NewMonth(fee int64) error {
	if fee < 1 || fee > 1_000_000_000_000 {
		return ErrInvalidArgument
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.open {
		return ErrMonthAlreadyOpen
	}

	s.open = true
	s.fee = fee
	s.reports = nil
	s.excludes = nil
	return nil
}

// Report registers one half-open failure interval [start,end).
func (s *Settlement) Report(start, end int) error {
	if !validInterval(start, end, s.config.LengthMinutes) {
		return ErrInvalidArgument
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.open {
		return ErrNoOpenMonth
	}

	s.reports = append(s.reports, interval{start: start, end: end})
	return nil
}

// AddExclude registers one half-open exclusion interval [start,end).
func (s *Settlement) AddExclude(start, end int) error {
	if !validInterval(start, end, s.config.LengthMinutes) {
		return ErrInvalidArgument
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.open {
		return ErrNoOpenMonth
	}

	candidate := append(append([]interval(nil), s.excludes...), interval{start: start, end: end})
	merged := mergeIntervals(candidate)
	if totalIntervalLength(merged) > s.config.MonthlyExcludeLimit {
		return ErrExcludeLimitExceeded
	}

	s.excludes = merged
	return nil
}

// Revoke removes one previously registered interval exactly equal to [start,end).
func (s *Settlement) Revoke(start, end int) error {
	if !validInterval(start, end, s.config.LengthMinutes) {
		return ErrInvalidArgument
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.open {
		return ErrNoOpenMonth
	}

	for i, reported := range s.reports {
		if reported.start == start && reported.end == end {
			s.reports = append(s.reports[:i], s.reports[i+1:]...)
			return nil
		}
	}

	return ErrNotFound
}

// Close settles the currently open month and advances the month index.
func (s *Settlement) Close() (CloseResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.open {
		return CloseResult{}, ErrNoOpenMonth
	}

	mergedFailures := mergeIntervals(s.reports)
	remainingFailures := subtractIntervals(mergedFailures, s.excludes)

	downtime := 0
	for _, segment := range remainingFailures {
		length := segment.end - segment.start
		if length >= s.config.JitterMinutes {
			downtime += length
		}
	}

	availability := ((int64(s.config.LengthMinutes - downtime)) * 1_000_000) /
		int64(s.config.LengthMinutes)

	result := CloseResult{
		DowntimeMinutes: downtime,
		Availability:    availability,
	}

	switch {
	case availability >= s.config.Tier1Availability:
		result.BasePercent = 0
	case availability >= s.config.Tier2Availability:
		result.BasePercent = s.config.Tier1Credit
	case availability >= s.config.Tier3Availability:
		result.BasePercent = s.config.Tier2Credit
	default:
		result.BasePercent = s.config.Tier3Credit
	}

	year := s.month / 12
	if result.BasePercent == 0 {
		s.streak = 0
		result.AppliedPercent = 0
		result.Credit = 0
	} else {
		escalatedMonths := s.streak
		if escalatedMonths > s.config.EscalationCapMonths {
			escalatedMonths = s.config.EscalationCapMonths
		}
		result.AppliedPercent = result.BasePercent +
			s.config.EscalationStep*escalatedMonths
		if result.AppliedPercent > 100 {
			result.AppliedPercent = 100
		}

		credit := (s.fee*int64(result.AppliedPercent) + 99) / 100
		remaining := s.config.AnnualCreditLimit - s.annualPaidCredits[year]
		if credit > remaining {
			credit = remaining
		}

		result.Credit = credit
		s.annualPaidCredits[year] += credit
		s.streak++
	}

	s.open = false
	s.fee = 0
	s.reports = nil
	s.excludes = nil
	s.month++

	return result, nil
}

func validCreditPoint(value int) bool {
	return value >= 1 && value <= 100
}

func validInterval(start, end, length int) bool {
	return start >= 0 && start < end && end <= length
}

func mergeIntervals(intervals []interval) []interval {
	if len(intervals) == 0 {
		return nil
	}

	sorted := append([]interval(nil), intervals...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].start == sorted[j].start {
			return sorted[i].end < sorted[j].end
		}
		return sorted[i].start < sorted[j].start
	})

	merged := []interval{sorted[0]}
	for _, current := range sorted[1:] {
		last := &merged[len(merged)-1]
		if current.start <= last.end {
			if current.end > last.end {
				last.end = current.end
			}
		} else {
			merged = append(merged, current)
		}
	}

	return merged
}

func subtractIntervals(intervals, exclusions []interval) []interval {
	mergedExclusions := mergeIntervals(exclusions)
	var result []interval

	for _, segment := range intervals {
		cutStart := segment.start
		for _, excluded := range mergedExclusions {
			if excluded.end <= cutStart {
				continue
			}
			if excluded.start >= segment.end {
				break
			}
			if excluded.start > cutStart {
				result = append(result, interval{start: cutStart, end: excluded.start})
			}
			if excluded.end > cutStart {
				cutStart = excluded.end
			}
			if cutStart >= segment.end {
				break
			}
		}
		if cutStart < segment.end {
			result = append(result, interval{start: cutStart, end: segment.end})
		}
	}

	return result
}

func totalIntervalLength(intervals []interval) int {
	total := 0
	for _, segment := range intervals {
		total += segment.end - segment.start
	}
	return total
}
