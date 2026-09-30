package qualitygate

// Batch is a submitted data batch.
type Batch struct {
	Seq   int
	Rows  int
	Nulls int
}

// Status of a known batch.
type Status string

const (
	StatusPassed      Status = "passed"
	StatusWarning     Status = "warning"
	StatusReleased    Status = "released"
	StatusQuarantined Status = "quarantined"
	StatusDiscarded   Status = "discarded"
)

// Snapshot is the query result for one batch.
type Snapshot struct {
	Seq        int
	Rows       int
	Nulls      int
	Status     Status
	Violations []Rule
}

// Submit accepts a new batch and adjudicates it.
func (g *Gate) Submit(b Batch) (*AdjudicationResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.paused {
		g.logf("input=Submit %+v -> reject: %v", b, ErrPaused)
		return nil, ErrPaused
	}
	if !g.validBatch(b) {
		g.logf("input=Submit %+v -> reject: %v", b, ErrInvalidBatch)
		return nil, ErrInvalidBatch
	}
	if _, ok := g.recs[b.Seq]; ok {
		g.logf("input=Submit %+v -> reject: %v", b, ErrSeqExists)
		return nil, ErrSeqExists
	}

	r := &record{seq: b.Seq}
	g.recs[b.Seq] = r
	res := g.adjudicate(b)
	g.commitResult(r, res)
	g.logf("input=Submit %+v baseline=%v seqs=%v violations=%v -> output=%+v reason=%s",
		b, res.Baseline, res.BaselineSeqs, res.Violations, res, res.Decision)
	return res, nil
}

// Resubmit retries a quarantined batch with new measurements; its baseline is
// still computed as of its seq position.
func (g *Gate) Resubmit(b Batch) (*AdjudicationResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.paused {
		g.logf("input=Resubmit %+v -> reject: %v", b, ErrPaused)
		return nil, ErrPaused
	}
	if !g.validBatch(b) {
		g.logf("input=Resubmit %+v -> reject: %v", b, ErrInvalidBatch)
		return nil, ErrInvalidBatch
	}
	r, ok := g.recs[b.Seq]
	if !ok {
		g.logf("input=Resubmit %+v -> reject: %v", b, ErrSeqNotFound)
		return nil, ErrSeqNotFound
	}
	if r.status != StatusQuarantined {
		err := notQuarantineError(r)
		g.logf("input=Resubmit %+v -> reject: %v", b, err)
		return nil, err
	}

	res := g.adjudicate(b)
	g.commitResult(r, res)
	g.logf("input=Resubmit %+v baseline=%v seqs=%v violations=%v -> output=%+v reason=%s",
		b, res.Baseline, res.BaselineSeqs, res.Violations, res, res.Decision)
	return res, nil
}

// ManuallyRelease moves a quarantined batch into the baseline set.
func (g *Gate) ManuallyRelease(seq int) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	r, ok := g.recs[seq]
	if !ok {
		g.logf("input=Release seq=%d -> reject: %v", seq, ErrSeqNotFound)
		return ErrSeqNotFound
	}
	if r.status != StatusQuarantined {
		err := notQuarantineError(r)
		g.logf("input=Release seq=%d -> reject: %v", seq, err)
		return err
	}
	r.status = StatusReleased
	g.logf("input=Release seq=%d -> output=released; streak unchanged=%d (not counted, not cleared)", seq, g.streak)
	return nil
}

// Discard drops a quarantined batch; it accepts no further operations.
func (g *Gate) Discard(seq int) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	r, ok := g.recs[seq]
	if !ok {
		g.logf("input=Discard seq=%d -> reject: %v", seq, ErrSeqNotFound)
		return ErrSeqNotFound
	}
	if r.status != StatusQuarantined {
		err := notQuarantineError(r)
		g.logf("input=Discard seq=%d -> reject: %v", seq, err)
		return err
	}
	r.status = StatusDiscarded
	g.logf("input=Discard seq=%d -> output=discarded; streak unchanged=%d (not counted, not cleared)", seq, g.streak)
	return nil
}

// Resume clears the pause and the consecutive-block counter.
func (g *Gate) Resume() error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if !g.paused {
		g.logf("input=Resume -> reject: %v", ErrNotPaused)
		return ErrNotPaused
	}
	g.paused = false
	g.streak = 0
	g.logf("input=Resume -> output=resumed; streak cleared=0")
	return nil
}

// Query returns the snapshot of a known batch.
func (g *Gate) Query(seq int) (*Snapshot, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	r, ok := g.recs[seq]
	if !ok {
		return nil, ErrSeqNotFound
	}
	violations := append([]Rule(nil), r.violations...)
	s := &Snapshot{Seq: r.seq, Rows: r.rows, Nulls: r.nulls, Status: r.status, Violations: violations}
	g.logf("input=Query seq=%d -> output=%+v", seq, s)
	return s, nil
}

// Paused reports whether the channel is currently paused.
func (g *Gate) Paused() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.paused
}

// BlockStreak reports the current consecutive blocked count.
func (g *Gate) BlockStreak() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.streak
}
