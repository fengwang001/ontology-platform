package pipeline

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// checkpoint 是落盘的状态机快照，原子替换写盘。
type checkpoint struct {
	State    Stage    `json:"state"`
	Runs     []string `json:"runs"`
	Ingested int64    `json:"ingested"`
	NextSeq  uint64   `json:"next_seq"`
}

func (p *Pipeline) checkpointPath() string {
	return filepath.Join(p.dir, "checkpoint.json")
}

// saveCheckpointLocked 调用方需持有 p.mu。
func (p *Pipeline) saveCheckpointLocked() error {
	cp := checkpoint{State: p.state, Runs: p.runs, Ingested: p.ingested, NextSeq: p.nextSeq}
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

func (p *Pipeline) loadCheckpoint() (checkpoint, error) {
	var cp checkpoint
	data, err := os.ReadFile(p.checkpointPath())
	if err != nil {
		return cp, err
	}
	if err := json.Unmarshal(data, &cp); err != nil {
		return cp, err
	}
	return cp, nil
}
