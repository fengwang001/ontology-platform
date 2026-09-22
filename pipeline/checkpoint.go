package pipeline

import (
	"encoding/json"
	"os"
	"path/filepath"

	"ontology/merge"
	"ontology/record"
	"ontology/spill"
)

// Stage is a state-machine phase.
type Stage string

// State-machine stages in order.
const (
	StageIngest   Stage = "ingest"
	StageSpill    Stage = "spill"
	StageMerge    Stage = "merge"
	StageFinalize Stage = "finalize"
	StageDone     Stage = "done"
)

type checkpoint struct {
	Stage    Stage    `json:"stage"`
	Runs     []string `json:"runs"`
	Ingested int64    `json:"ingested"`
}

func (p *Pipeline) checkpointPath() string {
	return filepath.Join(p.dir, "checkpoint.json")
}

// saveCheckpointLocked persists the state atomically. Caller holds stateMu.
func (p *Pipeline) saveCheckpointLocked() error {
	cp := checkpoint{Stage: p.stage, Runs: p.runs, Ingested: p.ingested}
	data, err := json.Marshal(cp)
	if err != nil {
		return err
	}
	tmp := p.checkpointPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p.checkpointPath())
}

func (p *Pipeline) loadCheckpoint() error {
	data, err := os.ReadFile(p.checkpointPath())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var cp checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return err
	}
	p.stage, p.runs, p.ingested = cp.Stage, cp.Runs, cp.Ingested
	return nil
}

func (p *Pipeline) setStage(s Stage) {
	p.stateMu.Lock()
	p.stage = s
	_ = p.saveCheckpointLocked()
	p.stateMu.Unlock()
}

// runStages advances the idempotent stage machine to Done.
func (p *Pipeline) runStages() error {
	for {
		p.stateMu.Lock()
		stage := p.stage
		p.stateMu.Unlock()
		switch stage {
		case StageIngest, StageSpill:
			if p.crash == CrashAfterSpill {
				return ErrCrashed
			}
			p.setStage(StageMerge)
		case StageMerge:
			if err := p.mergeRuns(); err != nil {
				return err
			}
			p.setStage(StageFinalize)
		case StageFinalize:
			if p.crash == CrashBeforeFinalize {
				return ErrCrashed
			}
			if err := os.Rename(p.tmpOutput(), p.OutputPath()); err != nil {
				return err
			}
			p.setStage(StageDone)
		case StageDone:
			return nil
		}
	}
}

func (p *Pipeline) tmpOutput() string { return p.OutputPath() + ".tmp" }

// mergeRuns streams all runs through the K-way merger into output.tmp.
// It is idempotent: the temp output is rewritten from scratch each time.
func (p *Pipeline) mergeRuns() error {
	p.stateMu.Lock()
	runs := append([]string(nil), p.runs...)
	total := p.ingested
	p.stateMu.Unlock()

	files := make([]*os.File, 0, len(runs))
	sources := make([]merge.Source, 0, len(runs))
	for _, name := range runs {
		f, err := os.Open(filepath.Join(p.dir, name))
		if err != nil {
			return err
		}
		rr, err := spill.NewRunReader(f)
		if err != nil {
			f.Close()
			return err
		}
		files = append(files, f)
		sources = append(sources, rr)
	}
	defer func() {
		for _, f := range files {
			f.Close()
		}
	}()

	out, err := os.Create(p.tmpOutput())
	if err != nil {
		return err
	}
	w := spill.NewRunWriter(out)
	m := merge.New(sources...)
	var written int64
	err = m.Merge(func(r record.Record) error {
		if p.crash == CrashMidMerge && total > 1 && written == total/2 {
			out.Close()
			return ErrCrashed
		}
		written++
		return w.Add(r)
	})
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		out.Close()
		return err
	}
	p.mergeCmp = m.Comparisons()
	return out.Close()
}
