package pipeline

import (
	"errors"
	"io"

	"ontology/record"
	"ontology/spill"
)

// repairRuns validates every recorded run and rewrites a damaged one
// in place with its maximum recoverable prefix. A truncated run can
// only be the last run (runs before it were checkpointed durably), so
// rewriting it never changes run ids used for equal-key tie-breaking.
func (p *Pipeline) repairRuns() (uint64, error) {
	var accepted uint64
	for id := uint64(1); id <= p.runs; id++ {
		rc := spill.Recover(spill.RunPath(p.dir, id))
		if rc.HeaderErr != nil {
			if errors.Is(rc.HeaderErr, spill.ErrEmptyFile) && id == p.runs {
				// Zero-length last run: treat as zero-record run.
				if err := rewriteRun(p.dir, id, nil); err != nil {
					return 0, err
				}
				continue
			}
			return 0, rc.HeaderErr
		}
		if rc.TailErr != nil && rc.TailErr != io.EOF {
			if err := rewriteRun(p.dir, id, rc.Records); err != nil {
				return 0, err
			}
		} else if rc.TailErr == io.EOF {
			// nothing
		}
		accepted += uint64(len(rc.Records))
	}
	return accepted, nil
}

func rewriteRun(dir string, id uint64, records []record.Record) error {
	w, err := spill.Create(dir, id, records)
	if err != nil {
		return err
	}
	return w.Close(-1)
}
