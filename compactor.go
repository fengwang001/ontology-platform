package ontology

import (
	"errors"
	"sync"
)

var (
	ErrParam     = errors.New("ontology: invalid parameter")
	ErrClock     = errors.New("ontology: clock moved backwards")
	ErrBusy      = errors.New("ontology: too many unfinished plans")
	ErrUnknown   = errors.New("ontology: unknown or finished plan")
	ErrNotNeeded = errors.New("ontology: no compaction rule applies")
)

// Config configures the Universal-style compaction selector.
type Config struct {
	MinRuns  int
	MaxRuns  int
	A        int64 // space amplification percentage
	Rho      int64 // size ratio percentage
	MinMerge int
	MaxMerge int
	Cmax     int   // maximum number of concurrently unfinished plans
	P        int64 // period; 0 disables the periodic rule
}

// Run is a single run of the ordered run set.
type Run struct {
	ID      int64
	Size    int64
	Created int64
	Busy    bool
	Fails   int
}

// Plan is a compaction plan returned by Pick.
type Plan struct {
	ID     int64
	Reason string
	Runs   []int64
	Total  int64
}

const (
	ReasonSpaceAmp    = "SpaceAmp"
	ReasonSizeRatio   = "SizeRatio"
	ReasonCountReduce = "CountReduce"
	ReasonPeriodic    = "Periodic"
)

// Compactor selects compaction plans over an ordered run set.
type Compactor struct {
	mu sync.Mutex

	cfg Config

	runs []*Run // newest first, oldest last

	nextRunID int64
	nextPlan  int64
	lastNow   int64

	// active maps an unfinished plan ID to its run IDs (newest first).
	active map[int64][]int64
	// ended records plan IDs that have already terminated.
	ended map[int64]bool

	// probes counts SizeRatio examinations during the last Pick.
	probes int
}

// New creates a Compactor. It returns ErrParam when cfg is invalid.
func New(cfg Config) (*Compactor, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	c := &Compactor{
		cfg:       cfg,
		nextRunID: 1,
		nextPlan:  1,
		active:    make(map[int64][]int64),
		ended:     make(map[int64]bool),
	}
	return c, nil
}

func validateConfig(cfg Config) error {
	if cfg.MinRuns < 2 || cfg.MinRuns > cfg.MaxRuns {
		return ErrParam
	}
	if cfg.A < 1 || cfg.A > 1_000_000 {
		return ErrParam
	}
	if cfg.Rho < 0 || cfg.Rho > 10_000 {
		return ErrParam
	}
	if cfg.MinMerge < 2 || cfg.MinMerge > cfg.MaxMerge {
		return ErrParam
	}
	if cfg.Cmax < 1 {
		return ErrParam
	}
	if cfg.P < 0 {
		return ErrParam
	}
	return nil
}

// AddRun appends a new run at the newest end and returns its ID.
func (c *Compactor) AddRun(now, size int64) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if size < 1 || size > 1_000_000_000_000 {
		return 0, ErrParam
	}
	if now < 0 {
		return 0, ErrParam
	}
	if now < c.lastNow {
		return 0, ErrClock
	}
	id := c.nextRunID
	c.nextRunID++
	newRun := &Run{
		ID:      id,
		Size:    size,
		Created: now,
	}
	c.runs = append([]*Run{newRun}, c.runs...)
	c.lastNow = now
	return id, nil
}

// Pick returns the first compaction plan whose rule holds at now.
func (c *Compactor) Pick(now int64) (Plan, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if now < 0 {
		return Plan{}, ErrParam
	}
	if now < c.lastNow {
		return Plan{}, ErrClock
	}
	if len(c.active) >= c.cfg.Cmax {
		return Plan{}, ErrBusy
	}

	n := len(c.runs)

	c.probes = 0

	// Rule 1: SpaceAmp.
	if p := c.pickSpaceAmp(n); p != nil {
		c.commit(now, p)
		return *p, nil
	}

	// Rule 2: SizeRatio.
	if p := c.pickSizeRatio(n); p != nil {
		c.commit(now, p)
		return *p, nil
	}

	// Rule 3: CountReduce.
	if p := c.pickCountReduce(n); p != nil {
		c.commit(now, p)
		return *p, nil
	}

	// Rule 4: Periodic.
	if p := c.pickPeriodic(now, n); p != nil {
		c.commit(now, p)
		return *p, nil
	}

	return Plan{}, ErrNotNeeded
}

// pickSpaceAmp selects every run when none is busy and the total size of all
// runs except the oldest is at least A percent of the oldest run's size.
func (c *Compactor) pickSpaceAmp(n int) *Plan {
	if n < c.cfg.MinRuns {
		return nil
	}
	var others int64
	for i := 0; i < n; i++ {
		if c.runs[i].Busy {
			return nil
		}
		if i < n-1 {
			others += c.runs[i].Size
		}
	}
	oldest := c.runs[n-1].Size
	if others*100 < c.cfg.A*oldest {
		return nil
	}
	return c.planFor(ReasonSpaceAmp, 0, n)
}

// pickSizeRatio greedily extends runs toward the old end from each eligible
// starting point, comparing against the accumulated size of the segment.
func (c *Compactor) pickSizeRatio(n int) *Plan {
	if n < c.cfg.MinRuns {
		return nil
	}
	for i := 0; i < n; i++ {
		c.probes++
		start := c.runs[i]
		if start.Busy || start.Fails >= 2 {
			continue
		}
		acc := start.Size
		count := 1
		j := i + 1
		examined := 1
		for j < n && count < c.cfg.MaxMerge && examined < c.cfg.MaxMerge {
			c.probes++
			examined++
			next := c.runs[j]
			if next.Busy || next.Fails >= 2 {
				break
			}
			if next.Size*100 > acc*(100+c.cfg.Rho) {
				break
			}
			acc += next.Size
			count++
			j++
		}
		if count >= c.cfg.MinMerge {
			return c.planFor(ReasonSizeRatio, i, j)
		}
	}
	return nil
}

// pickCountReduce selects the first window (from the newest end) of c
// consecutive non-busy runs when n exceeds MaxRuns.
func (c *Compactor) pickCountReduce(n int) *Plan {
	if n <= c.cfg.MaxRuns {
		return nil
	}
	cnt := n - c.cfg.MaxRuns + 1
	if cnt > c.cfg.MaxMerge {
		cnt = c.cfg.MaxMerge
	}
	for i := 0; i+cnt <= n; i++ {
		ok := true
		for k := 0; k < cnt; k++ {
			if c.runs[i+k].Busy {
				ok = false
				break
			}
		}
		if ok {
			return c.planFor(ReasonCountReduce, i, i+cnt)
		}
	}
	return nil
}

// pickPeriodic selects the oldest non-busy run whose age is at least P.
func (c *Compactor) pickPeriodic(now int64, n int) *Plan {
	if c.cfg.P <= 0 {
		return nil
	}
	for i := n - 1; i >= 0; i-- {
		r := c.runs[i]
		if !r.Busy && now-r.Created >= c.cfg.P {
			return c.planFor(ReasonPeriodic, i, i+1)
		}
	}
	return nil
}

// planFor builds a plan over the half-open slice range [lo, hi).
func (c *Compactor) planFor(reason string, lo, hi int) *Plan {
	ids := make([]int64, 0, hi-lo)
	var total int64
	for i := lo; i < hi; i++ {
		ids = append(ids, c.runs[i].ID)
		total += c.runs[i].Size
	}
	return &Plan{Reason: reason, Runs: ids, Total: total}
}

// commit assigns plan bookkeeping, marks runs busy and advances the clock.
func (c *Compactor) commit(now int64, p *Plan) {
	p.ID = c.nextPlan
	c.nextPlan++
	for _, r := range c.runs {
		for _, id := range p.Runs {
			if r.ID == id {
				r.Busy = true
			}
		}
	}
	ids := append([]int64(nil), p.Runs...)
	c.active[p.ID] = ids
	c.lastNow = now
}

// Done finishes a plan and replaces its runs with a fresh run of size out.
func (c *Compactor) Done(now, planID, out int64) (Run, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if out < 1 || out > 1_000_000_000_000 {
		return Run{}, ErrParam
	}
	if now < 0 {
		return Run{}, ErrParam
	}
	if now < c.lastNow {
		return Run{}, ErrClock
	}
	ids, ok := c.active[planID]
	if !ok {
		return Run{}, ErrUnknown
	}

	lo, hi := c.findSegment(ids)
	if lo < 0 {
		return Run{}, ErrUnknown
	}

	newID := c.nextRunID
	c.nextRunID++
	replacement := &Run{
		ID:      newID,
		Size:    out,
		Created: now,
	}

	updated := make([]*Run, 0, len(c.runs)-len(ids)+1)
	updated = append(updated, c.runs[:lo]...)
	updated = append(updated, replacement)
	updated = append(updated, c.runs[hi:]...)
	c.runs = updated

	delete(c.active, planID)
	c.ended[planID] = true
	c.lastNow = now
	return *replacement, nil
}

// Abort cancels a plan, clears busy flags and increments fails.
func (c *Compactor) Abort(planID int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	ids, ok := c.active[planID]
	if !ok {
		return ErrUnknown
	}
	want := make(map[int64]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	for _, r := range c.runs {
		if want[r.ID] {
			r.Busy = false
			r.Fails++
		}
	}
	delete(c.active, planID)
	c.ended[planID] = true
	return nil
}

// findSegment returns the half-open slice range of the contiguous run IDs.
func (c *Compactor) findSegment(ids []int64) (int, int) {
	if len(ids) == 0 {
		return -1, -1
	}
	want := make(map[int64]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	lo := -1
	for i, r := range c.runs {
		if want[r.ID] {
			if lo < 0 {
				lo = i
			}
		} else if lo >= 0 {
			return lo, i
		}
	}
	if lo >= 0 {
		return lo, len(c.runs)
	}
	return -1, -1
}

// Runs returns a copy of the current run set (newest first).
func (c *Compactor) Runs() []Run {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Run, len(c.runs))
	for i, r := range c.runs {
		out[i] = *r
	}
	return out
}
