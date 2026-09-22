package pipeline

import (
	"os"

	"ontology/merge"
	"ontology/record"
	"ontology/spill"
)

const outputName = "output.dat"

// Close ends ingestion: stops accepting records, flushes the final
// resident batch as the last run, merges all runs and finalizes the
// output file. It is idempotent after crash recovery.
func (p *Pipeline) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return p.ensureFinalized()
	}
	p.closed = true
	p.stopping = true
	p.mu.Unlock()

	p.budget.Close()
	close(p.stopSig)
	<-p.doneSig
	p.mu.Lock()
	hasResident := len(p.resident) > 0 || len(p.waiting) > 0
	p.mu.Unlock()
	if len(p.waiting) > 0 {
		// Parked jobs had charged nothing; they were never accepted.
		// They cannot exist at Close because Close blocked new sends.
	}

	// Boundary: spill. A zero-record input still writes an empty
	// (header-only) run so downstream phases stay uniform.
	if hasResident || p.runs == 0 {
		if err := p.spillOnce(true); err != nil {
			return err
		}
	}
	if err := p.savePhase(PhaseSpill, checkpoint{
		Phase: PhaseSpill, Accepted: p.nextSeq, Runs: p.runs,
		PendingSpill: false,
	}); err != nil {
		return err
	}
	if err := p.maybeCrash("spill"); err != nil {
		return err
	}

	if err := p.runMerge(); err != nil {
		return err
	}
	if err := p.savePhase(PhaseMerge, checkpoint{
		Phase: PhaseMerge, Accepted: p.nextSeq, Runs: p.runs,
		Comparisons: p.comparisons, PeakResident: p.budget.Peak(),
	}); err != nil {
		return err
	}
	if err := p.maybeCrash("merge"); err != nil {
		return err
	}
	if err := p.savePhase(PhaseFinalize, checkpoint{
		Phase: PhaseFinalize, Accepted: p.nextSeq, Runs: p.runs,
		Comparisons: p.comparisons, PeakResident: p.budget.Peak(),
	}); err != nil {
		return err
	}
	return p.maybeCrash("finalize")
}

func (p *Pipeline) savePhase(phase Phase, cp checkpoint) error {
	cp.Phase = phase
	cp.Accepted = p.nextSeq
	cp.Runs = p.runs
	return saveCheckpoint(p.dir, cp)
}

// runMerge streams every run through a heap merge and writes the final
// record frames to output.dat.tmp, then atomically renames it.
func (p *Pipeline) runMerge() error {
	total, err := p.repairRuns()
	if err != nil {
		return err
	}
	sources, _, err := p.loadRunSources()
	if err != nil {
		return err
	}
	m := merge.New(sources)
	out, err := os.Create(p.outputPath() + ".tmp")
	if err != nil {
		return err
	}
	buf := make([]byte, 0, 1<<16)
	var n uint64
	for {
		rec, ok := m.Next()
		if !ok {
			break
		}
		buf = record.AppendFrame(buf[:0], rec)
		if _, err := out.Write(buf); err != nil {
			out.Close()
			return err
		}
		n++
		if n == total/2+1 && p.faults != nil && p.faults.CrashAfter != nil &&
			p.faults.CrashAfter("merge-mid") {
			out.Close()
			return errCrashInjected
		}
	}
	if n != total {
		out.Close()
		return errCountMismatch
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	p.comparisons = m.Comparisons()
	return os.Rename(p.outputPath()+".tmp", p.outputPath())
}

func (p *Pipeline) outputPath() string { return joinPath(p.dir, outputName) }

var _ = spill.HeaderSize
