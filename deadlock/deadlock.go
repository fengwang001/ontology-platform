package deadlock

import "errors"

var (
	ErrInvalidArgs         = errors.New("invalid arguments")
	ErrProcessNotFound     = errors.New("process not found")
	ErrProcessBlocked      = errors.New("process already blocked")
	ErrRequestImpossible   = errors.New("request can never be satisfied")
	ErrReleaseExceedsAlloc = errors.New("release exceeds allocation")
)

type Granted struct {
	AltIndex int
}

type GrantItem struct {
	Pid      int
	AltIndex int
}

type ResolveStep struct {
	Victim    int
	Cost      int64
	Granted   []GrantItem
	Permanent bool
}

type Detector struct {
	mu       chan struct{}
	r        int
	total    []int64
	cost     []int64
	pnum     int
	rollback int

	avail   []int64
	alloc   [][]int64
	rb      []int
	alive   []bool
	blocked []bool
	alts    [][][]int64
	bseq    []int

	nextBSeq int
	checks   int64
}

func New(R int, T, c []int64, P, L int) (*Detector, error) {
	if R < 1 || R > 8 || P < 1 || P > 64 || L < 1 || L > 10 {
		return nil, ErrInvalidArgs
	}
	if len(T) != R || len(c) != R {
		return nil, ErrInvalidArgs
	}
	for _, v := range T {
		if v < 1 || v > 1_000_000 {
			return nil, ErrInvalidArgs
		}
	}
	for _, v := range c {
		if v < 1 || v > 1000 {
			return nil, ErrInvalidArgs
		}
	}
	d := &Detector{
		mu:       make(chan struct{}, 1),
		r:        R,
		total:    append([]int64(nil), T...),
		cost:     append([]int64(nil), c...),
		pnum:     P,
		rollback: L,
		avail:    append([]int64(nil), T...),
		alloc:    make([][]int64, P),
		rb:       make([]int, P),
		alive:    make([]bool, P),
		blocked:  make([]bool, P),
		alts:     make([][][]int64, P),
		bseq:     make([]int, P),
	}
	for i := range d.alloc {
		d.alloc[i] = make([]int64, R)
		d.alive[i] = true
	}
	return d, nil
}

func (d *Detector) Request(p int, alts [][]int64) (Granted, error) {
	if len(alts) < 1 || len(alts) > 3 {
		return Granted{}, ErrInvalidArgs
	}
	norm := make([][]int64, len(alts))
	for i, alt := range alts {
		if len(alt) != d.r {
			return Granted{}, ErrInvalidArgs
		}
		vec := make([]int64, d.r)
		zero := true
		for r, v := range alt {
			if v < 0 {
				return Granted{}, ErrInvalidArgs
			}
			if v != 0 {
				zero = false
			}
			vec[r] = v
		}
		if zero {
			return Granted{}, ErrInvalidArgs
		}
		norm[i] = vec
	}
	d.lock()
	defer d.unlock()
	if p < 0 || p >= d.pnum || !d.alive[p] {
		return Granted{}, ErrProcessNotFound
	}
	if d.blocked[p] {
		return Granted{}, ErrProcessBlocked
	}
	anyFeasible := false
	for _, alt := range norm {
		feasible := true
		for r, v := range alt {
			if d.alloc[p][r]+v > d.total[r] {
				feasible = false
				break
			}
		}
		if feasible {
			anyFeasible = true
		}
	}
	// Alternatives are "grant any one of them": a single oversized alternative
	// can simply never be the granted one, so the request is permanently
	// impossible only when EVERY alternative exceeds total capacity given the
	// process holdings. This matches the worked example, where a process
	// holding all of resource 1 still blocks on a group whose second
	// alternative would exceed T, while its first alternative stays feasible.
	if !anyFeasible {
		return Granted{}, ErrRequestImpossible
	}
	if idx, ok := d.firstFit(norm, d.avail); ok {
		d.grant(p, norm, idx)
		return Granted{AltIndex: idx}, nil
	}
	d.nextBSeq++
	d.blocked[p] = true
	d.alts[p] = norm
	d.bseq[p] = d.nextBSeq
	return Granted{}, nil
}

func (d *Detector) Release(p int, vec []int64) ([]GrantItem, error) {
	if len(vec) != d.r {
		return nil, ErrInvalidArgs
	}
	norm := make([]int64, d.r)
	zero := true
	for r, v := range vec {
		if v < 0 {
			return nil, ErrInvalidArgs
		}
		if v != 0 {
			zero = false
		}
		norm[r] = v
	}
	if zero {
		return nil, ErrInvalidArgs
	}
	d.lock()
	defer d.unlock()
	if p < 0 || p >= d.pnum || !d.alive[p] {
		return nil, ErrProcessNotFound
	}
	if d.blocked[p] {
		return nil, ErrProcessBlocked
	}
	for r, v := range norm {
		if v > d.alloc[p][r] {
			return nil, ErrReleaseExceedsAlloc
		}
	}
	for r, v := range norm {
		d.alloc[p][r] -= v
		d.avail[r] += v
	}
	return d.grantFixpoint(), nil
}

func (d *Detector) lock()   { d.mu <- struct{}{} }
func (d *Detector) unlock() { <-d.mu }

func vecFits(vec, avail []int64) bool {
	for r, v := range vec {
		if v > avail[r] {
			return false
		}
	}
	return true
}

// firstFit returns the index of the first alternative fitting avail,
// in listed order (not the "best" one).
func (d *Detector) firstFit(alts [][]int64, avail []int64) (int, bool) {
	for i, alt := range alts {
		if vecFits(alt, avail) {
			return i, true
		}
	}
	return 0, false
}

func (d *Detector) grant(p int, alts [][]int64, idx int) {
	alt := alts[idx]
	for r, v := range alt {
		d.avail[r] -= v
		d.alloc[p][r] += v
	}
}

// grantFixpoint repeatedly grants the blocked process with the smallest bseq
// that currently has any fitting alternative, until no such process remains.
func (d *Detector) grantFixpoint() []GrantItem {
	var granted []GrantItem
	for {
		bestP := -1
		bestIdx := 0
		bestSeq := 0
		for p := 0; p < d.pnum; p++ {
			if !d.blocked[p] {
				continue
			}
			if idx, ok := d.firstFit(d.alts[p], d.avail); ok {
				if bestP == -1 || d.bseq[p] < bestSeq {
					bestP, bestIdx, bestSeq = p, idx, d.bseq[p]
				}
			}
		}
		if bestP == -1 {
			return granted
		}
		d.grant(bestP, d.alts[bestP], bestIdx)
		d.blocked[bestP] = false
		d.alts[bestP] = nil
		granted = append(granted, GrantItem{Pid: bestP, AltIndex: bestIdx})
	}
}

func (d *Detector) Detect() []int {
	d.lock()
	defer d.unlock()
	return d.detectLocked()
}

// detectLocked performs banker-style graph reduction.
//
// Work starts from avail plus the holdings of every non-blocked process:
// non-blocked processes are treated as always able to finish and release
// everything they hold. A blocked process whose pending request has an
// alternative fitting Work can finish as well; it acquires the alternative
// and later releases both that alternative and its prior holdings, so the net
// addition to Work is exactly its alloc. Processes that cannot be reduced
// away are the deadlocked ones. State is never modified.
func (d *Detector) detectLocked() []int {
	d.checks = 0
	work := append([]int64(nil), d.avail...)
	for p := 0; p < d.pnum; p++ {
		if d.alive[p] && !d.blocked[p] {
			for r, v := range d.alloc[p] {
				work[r] += v
			}
		}
	}
	finish := make([]bool, d.pnum)
	changed := true
	for changed {
		changed = false
		for p := 0; p < d.pnum; p++ {
			if !d.blocked[p] || finish[p] {
				continue
			}
			d.checks++
			if _, ok := d.firstFit(d.alts[p], work); ok {
				finish[p] = true
				changed = true
				for r, v := range d.alloc[p] {
					work[r] += v
				}
			}
		}
	}
	var dead []int
	for p := 0; p < d.pnum; p++ {
		if d.blocked[p] && !finish[p] {
			dead = append(dead, p)
		}
	}
	return dead
}

func (d *Detector) Resolve() []ResolveStep {
	d.lock()
	defer d.unlock()
	var steps []ResolveStep
	for {
		dead := d.detectLocked()
		if len(dead) == 0 {
			return steps
		}
		victim := -1
		var bestCost int64
		for _, p := range dead {
			holds := false
			for _, v := range d.alloc[p] {
				if v > 0 {
					holds = true
					break
				}
			}
			if !holds {
				continue
			}
			cost := d.victimCost(p)
			if victim == -1 || cost < bestCost || (cost == bestCost && p < victim) {
				victim, bestCost = p, cost
			}
		}
		// A non-empty deadlock set always contains a holder: with nobody
		// holding resources every pending request would fit avail, and no
		// blocked process could exist after a grant fixpoint.
		for r, v := range d.alloc[victim] {
			d.avail[r] += v
			d.alloc[victim][r] = 0
		}
		d.blocked[victim] = false
		d.alts[victim] = nil
		d.rb[victim]++
		permanent := false
		if d.rb[victim] >= d.rollback {
			d.alive[victim] = false
			permanent = true
		}
		granted := d.grantFixpoint()
		steps = append(steps, ResolveStep{
			Victim:    victim,
			Cost:      bestCost,
			Granted:   granted,
			Permanent: permanent,
		})
	}
}

func (d *Detector) victimCost(p int) int64 {
	var sum int64
	for r, v := range d.alloc[p] {
		sum += v * d.cost[r]
	}
	return sum * int64(1+d.rb[p])
}

// Checks reports how many blocked processes were inspected during the most
// recent Detect graph reduction. With b blocked processes the value never
// exceeds b(b+1)/2: the k-th successful reduction pass performs at most
// b-k+1 checks.
func (d *Detector) Checks() int64 {
	d.lock()
	defer d.unlock()
	return d.checks
}

// Snapshot is a consistent, locked observation of the detector state, for
// tests and external observers that need holdings without racing.
type Snapshot struct {
	Avail    []int64
	Alloc    [][]int64
	Rollback []int
	Alive    []bool
	Blocked  []bool
	BSeq     []int
	NextBSeq int
}

func (d *Detector) Snapshot() Snapshot {
	d.lock()
	defer d.unlock()
	s := Snapshot{
		Avail:    append([]int64(nil), d.avail...),
		Alloc:    make([][]int64, d.pnum),
		Rollback: append([]int(nil), d.rb...),
		Alive:    append([]bool(nil), d.alive...),
		Blocked:  append([]bool(nil), d.blocked...),
		BSeq:     append([]int(nil), d.bseq...),
		NextBSeq: d.nextBSeq,
	}
	for p := range s.Alloc {
		s.Alloc[p] = append([]int64(nil), d.alloc[p]...)
	}
	return s
}
