// Package qualitygate implements a data-batch quality gate that decides
// whether an out-of-order arriving batch may pass, pass with a warning, or be
// blocked, based on fixed rules and a baseline built only from passed batches.
package qualitygate

import (
	"errors"
	"fmt"
	"log"
	"sort"
	"sync"
)

// Decision is the verdict of adjudicating one batch submission/resubmission.
type Decision string

const (
	DecisionPassed  Decision = "passed"
	DecisionWarning Decision = "warning"
	DecisionBlocked Decision = "blocked"
)

// Rule identifies a gate rule.
type Rule string

const (
	RuleRowsBelowMin Rule = "rows_below_min"
	RuleNullRatio    Rule = "null_ratio_exceeded"
	RuleBaselineDev  Rule = "baseline_deviation"
)

// Config holds the gate parameters. Percentages are integer percents and the
// null ratio is a fraction so that every comparison is an exact integer
// cross multiplication (no floating point rounding on the boundaries).
type Config struct {
	MinRows    int // rule one: rows must be >= MinRows
	MaxNullNum int // rule two: nulls/rows must be <= MaxNullNum/MaxNullDen
	MaxNullDen int //
	BaselineK  int // at most K seq-largest eligible batches form the baseline
	LoPercent  int // rule three: rows*100 <  baseline*(100-LoPercent) warns
	HiPercent  int // rule three: rows*100 >  baseline*(100+HiPercent) warns
	MaxBlocks  int // M: consecutive blocks that pause the channel
}

// AdjudicationResult is the outcome for one submission/resubmission.
type AdjudicationResult struct {
	Seq          int
	Rows         int
	Nulls        int
	Decision     Decision
	Violations   []Rule // all failed rules, in rule order
	Baseline     int    // median used by rule three; 0 when not evaluated
	BaselineSeqs []int  // seq numbers of the baseline samples, ascending
	BlockStreak  int    // consecutive blocked count after this adjudication
	Paused       bool   // whether the channel is paused afterwards
}

// Gate is the concurrency-safe quality gate. A single mutex makes every
// concurrent set of calls equivalent to some serial order.
type Gate struct {
	mu     sync.Mutex
	cfg    Config
	log    Logger
	recs   map[int]*record
	streak int
	paused bool
}

// Logger receives structured decision/audit lines.
type Logger interface {
	Printf(format string, args ...any)
}

type record struct {
	seq        int
	rows       int
	nulls      int
	status     Status
	violations []Rule
}

// New creates a Gate. A nil logger writes through the standard logger.
func New(cfg Config, logger Logger) (*Gate, error) {
	if cfg.MinRows < 0 || cfg.MaxNullDen <= 0 || cfg.MaxNullNum < 0 ||
		cfg.BaselineK <= 0 || cfg.MaxBlocks <= 0 {
		return nil, errors.New("qualitygate: invalid config")
	}
	if logger == nil {
		logger = log.Default()
	}
	return &Gate{cfg: cfg, log: logger, recs: map[int]*record{}}, nil
}

func (g *Gate) logf(format string, args ...any) {
	g.log.Printf(format, args...)
}

type baselineSample struct {
	seq  int
	rows int
}

// computeBaseline must be called with g.mu held. Eligible batches have seq
// smaller than curSeq and are released into the baseline set (ordinary
// passes, warning passes and manual releases).
func (g *Gate) computeBaseline(curSeq int) ([]baselineSample, int) {
	samples := make([]baselineSample, 0)
	for _, r := range g.recs {
		if r.seq < curSeq && r.status.inBaseline() {
			samples = append(samples, baselineSample{r.seq, r.rows})
		}
	}
	if len(samples) == 0 {
		return nil, 0
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i].seq > samples[j].seq })
	if g.cfg.BaselineK < len(samples) {
		samples = samples[:g.cfg.BaselineK]
	}
	rows := make([]int, len(samples))
	for i, s := range samples {
		rows[i] = s.rows
	}
	sort.Ints(rows)
	return samples, rows[(len(rows)-1)/2]
}

func (s Status) inBaseline() bool {
	return s == StatusPassed || s == StatusWarning || s == StatusReleased
}

// adjudicate applies the three rules in order and returns the result without
// mutating any state. Must be called with g.mu held.
func (g *Gate) adjudicate(b Batch) *AdjudicationResult {
	samples, median := g.computeBaseline(b.Seq)
	seqs := make([]int, 0, len(samples))
	for i := len(samples) - 1; i >= 0; i-- {
		seqs = append(seqs, samples[i].seq)
	}

	var violations []Rule
	if b.Rows < g.cfg.MinRows {
		violations = append(violations, RuleRowsBelowMin)
	}
	// Rule two is skipped when rows == 0. Cross multiplication:
	// nulls/rows > num/den  <=>  nulls*den > rows*num.
	if b.Rows > 0 && b.Nulls*g.cfg.MaxNullDen > b.Rows*g.cfg.MaxNullNum {
		violations = append(violations, RuleNullRatio)
	}
	if len(samples) > 0 {
		low := b.Rows*100 < median*(100-g.cfg.LoPercent)
		high := b.Rows*100 > median*(100+g.cfg.HiPercent)
		if low || high {
			violations = append(violations, RuleBaselineDev)
		}
	}

	blocked := false
	for _, v := range violations {
		if v == RuleRowsBelowMin || v == RuleNullRatio {
			blocked = true
		}
	}
	decision := DecisionPassed
	if blocked {
		decision = DecisionBlocked
	} else if len(violations) > 0 {
		decision = DecisionWarning
	}

	return &AdjudicationResult{
		Seq:          b.Seq,
		Rows:         b.Rows,
		Nulls:        b.Nulls,
		Decision:     decision,
		Violations:   violations,
		Baseline:     median,
		BaselineSeqs: seqs,
	}
}

// commitResult applies an adjudication to a record and updates the streak
// and pause flag. Must be called with g.mu held.
func (g *Gate) commitResult(r *record, res *AdjudicationResult) {
	r.rows = res.Rows
	r.nulls = res.Nulls
	r.violations = res.Violations
	if res.Decision == DecisionBlocked {
		r.status = StatusQuarantined
		g.streak++
		if g.streak >= g.cfg.MaxBlocks {
			g.paused = true
		}
	} else {
		g.streak = 0
		if res.Decision == DecisionWarning {
			r.status = StatusWarning
		} else {
			r.status = StatusPassed
		}
	}
	res.BlockStreak = g.streak
	res.Paused = g.paused
}

func (g *Gate) validBatch(b Batch) bool {
	return b.Seq > 0 && b.Rows >= 0 && b.Nulls >= 0 && b.Nulls <= b.Rows
}

// notQuarantineError turns a known record's status into the distinguishable
// rejection reason. Must be called with g.mu held.
func notQuarantineError(r *record) error {
	if r.status == StatusDiscarded {
		return fmt.Errorf("%w: %w", ErrNotQuarantined, ErrAlreadyDiscarded)
	}
	return fmt.Errorf("%w: %w", ErrNotQuarantined, ErrAlreadyPassed)
}
