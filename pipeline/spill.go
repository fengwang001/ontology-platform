package pipeline

import (
	"ontology/record"
	"ontology/spill"
)

// spillOnce drains the resident batch into one durable run file. The
// batch's bytes remain charged on the budget until the file rename is
// durable, then are released in one step: that release is the only
// point at which blocked ingesters may resume, so the resident total
// never crosses the limit even with a concurrent writer.
//
// Called either by the coordinator (background pressure spills) or by
// Close (final flush); flushMu serializes the two.
func (p *Pipeline) spillOnce(final bool) error {
	p.flushMu.Lock()
	defer p.flushMu.Unlock()

	p.mu.Lock()
	if !final && len(p.resident) == 0 {
		p.mu.Unlock()
		return nil
	}
	batch := p.resident
	charged := p.resBytes
	p.resident = nil
	p.resBytes = 0
	runID := p.runs + 1
	p.mu.Unlock()

	record.Sort(batch)
	w, err := spill.Create(p.dir, runID, batch)
	if err != nil {
		return err
	}
	cut := -1
	if p.faults != nil && p.faults.TruncateRun != nil {
		cut = p.faults.TruncateRun(runID)
	}
	if err := w.Close(cut); err != nil {
		return err
	}

	p.mu.Lock()
	p.runs = runID
	p.mu.Unlock()
	p.budget.Release(charged)
	return nil
}
