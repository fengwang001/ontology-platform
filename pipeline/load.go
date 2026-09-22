package pipeline

import (
	"io"
	"os"
	"path/filepath"

	"ontology/merge"
	"ontology/record"
	"ontology/spill"
)

func joinPath(dir, name string) string { return filepath.Join(dir, name) }

// loadRunSources opens every completed run (1..runs) and returns them
// as merge sources. A prior Open already repaired damaged tails.
func (p *Pipeline) loadRunSources() ([]merge.Source, uint64, error) {
	sources := make([]merge.Source, 0, p.runs)
	var total uint64
	for id := uint64(1); id <= p.runs; id++ {
		rc := spill.Recover(spill.RunPath(p.dir, id))
		if rc.HeaderErr != nil {
			return nil, 0, rc.HeaderErr
		}
		if rc.TailErr != nil && rc.TailErr != io.EOF {
			return nil, 0, rc.TailErr
		}
		sources = append(sources, merge.Source{RunID: id, Records: rc.Records})
		total += uint64(len(rc.Records))
	}
	return sources, total, nil
}

// recover resumes from a checkpointed phase. Damaged runs are repaired
// to their maximum prefix; the merge is idempotent because its output
// is a temp file renamed into place only after a complete, counted run.
func (p *Pipeline) recover(cp checkpoint) error {
	if cp.Phase == PhaseFinalize {
		return nil
	}
	total, err := p.repairRuns()
	if err != nil {
		return err
	}
	if cp.Phase != PhaseIngest && total != cp.Accepted {
		return errRunMismatch
	}
	switch cp.Phase {
	case PhaseSpill, PhaseMerge:
		if err := p.runMerge(); err != nil {
			return err
		}
		return saveCheckpoint(p.dir, checkpoint{
			Phase: PhaseFinalize, Accepted: cp.Accepted, Runs: cp.Runs,
			Comparisons: p.comparisons, PeakResident: cp.PeakResident,
		})
	}
	return nil
}

// OutputPath reports the final sorted output file location.
func (p *Pipeline) OutputPath() string { return p.outputPath() }

// ReadOutput streams the finalized output as record frames.
func (p *Pipeline) ReadOutput() ([]record.Record, error) {
	data, err := os.ReadFile(p.outputPath())
	if err != nil {
		return nil, err
	}
	var out []record.Record
	for len(data) > 0 {
		r, n, err := record.DecodeFrame(data)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
		data = data[n:]
	}
	return out, nil
}
