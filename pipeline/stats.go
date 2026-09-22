package pipeline

import "ontology/merge"

// Stats exposes counters for acceptance checks and the demo.
type Stats struct {
	Accepted     uint64
	Runs         uint64
	PeakResident uint64
	Limit        uint64
	Comparisons  uint64
}

// Stats returns a consistent snapshot of pipeline counters.
func (p *Pipeline) Stats() Stats {
	p.mu.Lock()
	defer p.mu.Unlock()
	return Stats{
		Accepted:     p.nextSeq,
		Runs:         p.runs,
		PeakResident: maxU64(p.peakResident, p.budget.Peak()),
		Limit:        p.budget.Limit(),
		Comparisons:  p.comparisons,
	}
}

// IsFinalized reports whether the output has reached the final phase.
func (p *Pipeline) IsFinalized() bool {
	cp, found, err := loadCheckpoint(p.dir)
	return err == nil && found && cp.Phase == PhaseFinalize
}

var _ = merge.Source{}
