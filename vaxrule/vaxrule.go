// Package vaxrule implements the pure vaccination-series rule engine:
// series definitions, per-record validity evaluation and earliest-next-dose.
package vaxrule

import (
	"errors"
	"sort"
)

const (
	// GraceDays is the 4-day grace applied to minimum age and intervals.
	GraceDays = 4
	// LiveInterval is the required separation between different live series.
	LiveInterval = 28
	// MaxN is the maximum number of doses in one series.
	MaxN = 6
	// MaxDay is the upper bound of every date.
	MaxDay = 1_000_000
	// MaxParam is the upper bound of age/interval/revacc parameters.
	MaxParam = 10_000
)

// Package-level errors.
var (
	ErrInvalidSeries = errors.New("vaxrule: invalid series definition")
	ErrSeriesExists  = errors.New("vaxrule: series already exists")
	ErrUnknownSeries = errors.New("vaxrule: unknown series")
)

// Reason is the first failed rule for an invalid record.
type Reason int

const (
	ReasonNone Reason = iota
	ReasonAge
	ReasonInterval
	ReasonRevacc
	ReasonLive
)

// String returns the stable machine-readable reason name.
func (r Reason) String() string {
	switch r {
	case ReasonAge:
		return "age"
	case ReasonInterval:
		return "interval"
	case ReasonRevacc:
		return "revacc"
	case ReasonLive:
		return "live"
	default:
		return ""
	}
}

// Series is a vaccine series definition (1..6 doses).
// MinAge[k] is the minimum age of dose k+1; MinInt[k] is the minimum
// interval since the previous valid dose of dose k+1 (MinInt[0] must be 0).
type Series struct {
	Name   string
	Live   bool
	N      int
	MinAge []int
	MinInt []int
	R      int
}

// Entry is one vaccination record for a patient.
type Entry struct {
	Series string
	Date   int
}

// Verdict is the evaluation result for one Entry.
type Verdict struct {
	Valid  bool
	Extra  bool
	Dose   int
	Reason Reason
}

// Result is the full evaluation for one patient.
type Result struct {
	Order    []int // original entry indices in evaluation order
	Verdicts []Verdict
}

// LiveRec is one record belonging to a live series.
type LiveRec struct {
	Series string
	Date   int
}

// Books maps series name to its definition.
type Books struct {
	series map[string]Series
}

// NewBooks creates an empty rule book.
func NewBooks() *Books {
	return &Books{series: map[string]Series{}}
}

// Add validates and registers a series.
func (b *Books) Add(s Series) error {
	if s.Name == "" || s.N < 1 || s.N > MaxN || len(s.MinAge) != s.N || len(s.MinInt) != s.N {
		return ErrInvalidSeries
	}
	if s.R < 0 || s.R > MaxParam {
		return ErrInvalidSeries
	}
	for k := 0; k < s.N; k++ {
		if s.MinAge[k] < 0 || s.MinAge[k] > MaxParam || s.MinInt[k] < 0 || s.MinInt[k] > MaxParam {
			return ErrInvalidSeries
		}
		if k == 0 && s.MinInt[k] != 0 {
			return ErrInvalidSeries
		}
	}
	if _, ok := b.series[s.Name]; ok {
		return ErrSeriesExists
	}
	b.series[s.Name] = s
	return nil
}

// Get returns a registered series.
func (b *Books) Get(name string) (Series, bool) {
	s, ok := b.series[name]
	return s, ok
}

// Names returns all registered series names in byte order.
func (b *Books) Names() []string {
	out := make([]string, 0, len(b.series))
	for name := range b.series {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

type seriesState struct {
	validCount  int
	prevValid   int
	hasPrev     bool
	lastInvalid int
	hasInvalid  bool
	lastDate    int
	lastValid   bool
	hasLast     bool
}

// sortEntries returns original indices ordered by (date, series bytes).
func sortEntries(entries []Entry) []int {
	order := make([]int, len(entries))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, b := entries[order[i]], entries[order[j]]
		if a.Date != b.Date {
			return a.Date < b.Date
		}
		return a.Series < b.Series
	})
	return order
}

func liveConflict(lives []LiveRec, self string, d int) bool {
	for _, l := range lives {
		if l.Series != self && l.Date < d && d-l.Date < LiveInterval {
			return true
		}
	}
	return false
}

// Evaluate judges all entries of one patient according to the rules.
func Evaluate(books *Books, birth int, entries []Entry) (Result, error) {
	order := sortEntries(entries)
	states := map[string]*seriesState{}
	lives := []LiveRec{}
	res := Result{Order: order, Verdicts: make([]Verdict, len(entries))}
	for _, idx := range order {
		e := entries[idx]
		s, ok := books.Get(e.Series)
		if !ok {
			return Result{}, ErrUnknownSeries
		}
		st := states[e.Series]
		if st == nil {
			st = &seriesState{}
			states[e.Series] = st
		}
		k := st.validCount + 1
		v := Verdict{Dose: k}
		switch {
		case k > s.N:
			v.Valid = true
			v.Extra = true
		case e.Date-birth < s.MinAge[k-1]-GraceDays:
			v.Reason = ReasonAge
		case k > 1 && (!st.hasPrev || e.Date-st.prevValid < s.MinInt[k-1]-GraceDays):
			v.Reason = ReasonInterval
		case st.hasLast && !st.lastValid && e.Date-st.lastDate < s.R:
			v.Reason = ReasonRevacc
		case s.Live && liveConflict(lives, s.Name, e.Date):
			v.Reason = ReasonLive
		default:
			v.Valid = true
		}
		if v.Valid {
			if !v.Extra {
				st.validCount++
				st.prevValid = e.Date
				st.hasPrev = true
			}
		} else {
			st.lastInvalid = e.Date
			st.hasInvalid = true
		}
		st.lastDate = e.Date
		st.lastValid = v.Valid
		st.hasLast = true
		if s.Live {
			lives = append(lives, LiveRec{Series: s.Name, Date: e.Date})
		}
		res.Verdicts[idx] = v
	}
	return res, nil
}

// Summary is the per-series cached state needed by the clinic.
type Summary struct {
	ValidCount  int
	PrevValid   int
	HasPrev     bool
	LastInvalid int
	HasInvalid  bool
	LastDate    int
	LastValid   bool
	HasLast     bool
}

// State aggregates summaries and live-record history for one patient.
type State struct {
	Sums  map[string]Summary
	Lives []LiveRec // in evaluation order (date, series)
}

// Recompute rebuilds a patient state by replaying every record and
// returns the matching full evaluation result.
func Recompute(books *Books, birth int, entries []Entry) (State, Result, error) {
	res, err := Evaluate(books, birth, entries)
	if err != nil {
		return State{}, Result{}, err
	}
	states := map[string]*seriesState{}
	lives := []LiveRec{}
	for _, idx := range res.Order {
		e := entries[idx]
		s, _ := books.Get(e.Series)
		st := states[e.Series]
		if st == nil {
			st = &seriesState{}
			states[e.Series] = st
		}
		v := res.Verdicts[idx]
		if v.Valid {
			if !v.Extra {
				st.validCount++
				st.prevValid = e.Date
				st.hasPrev = true
			}
		} else {
			st.lastInvalid = e.Date
			st.hasInvalid = true
		}
		st.lastDate = e.Date
		st.lastValid = v.Valid
		st.hasLast = true
		if s.Live {
			lives = append(lives, LiveRec{Series: s.Name, Date: e.Date})
		}
	}
	st := State{Sums: map[string]Summary{}, Lives: lives}
	for name, x := range states {
		st.Sums[name] = Summary{
			ValidCount:  x.validCount,
			PrevValid:   x.prevValid,
			HasPrev:     x.hasPrev,
			LastInvalid: x.lastInvalid,
			HasInvalid:  x.hasInvalid,
			LastDate:    x.lastDate,
			LastValid:   x.lastValid,
			HasLast:     x.hasLast,
		}
	}
	return st, res, nil
}

func intervalLower(s Series, sum Summary, k int) (int, bool) {
	if k <= 1 || !sum.HasPrev {
		return 0, false
	}
	return sum.PrevValid + s.MinInt[k-1] - GraceDays, true
}

// TodayReason returns the first rule failing if series s is administered
// exactly at date d, given the patient summary and live-record history.
func TodayReason(s Series, birth, d int, sum Summary, lives []LiveRec) Reason {
	k := sum.ValidCount + 1
	if d-birth < s.MinAge[k-1]-GraceDays {
		return ReasonAge
	}
	if low, ok := intervalLower(s, sum, k); ok && d < low {
		return ReasonInterval
	}
	if sum.HasLast && !sum.LastValid && d-sum.LastDate < s.R {
		return ReasonRevacc
	}
	if s.Live && liveConflict(lives, s.Name, d) {
		return ReasonLive
	}
	return ReasonNone
}

// Earliest returns the smallest date >= now at which series s would be
// valid, plus the first rule failing for an attempt exactly at now.
func Earliest(s Series, birth, now int, sum Summary, lives []LiveRec) (date int, reason Reason) {
	reason = TodayReason(s, birth, now, sum, lives)
	k := sum.ValidCount + 1
	d := now
	if low := birth + s.MinAge[k-1] - GraceDays; d < low {
		d = low
	}
	if low, ok := intervalLower(s, sum, k); ok && d < low {
		d = low
	}
	if sum.HasLast && !sum.LastValid {
		if low := sum.LastDate + s.R; d < low {
			d = low
		}
	}
	if s.Live {
		for {
			raised := false
			for _, l := range lives {
				if l.Series == s.Name {
					continue
				}
				if l.Date < d && d-l.Date < LiveInterval {
					d = l.Date + LiveInterval
					raised = true
				}
			}
			if !raised {
				break
			}
		}
	}
	return d, reason
}
