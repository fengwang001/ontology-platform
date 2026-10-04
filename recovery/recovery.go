package recovery

import "ontology/history"

type Mode int

const (
	OpsBased Mode = iota + 1
	FileBased
)

type LiveDoc struct {
	ID  []byte
	Seq int64
}

type Plan struct {
	Mode   Mode
	Ops    []history.Op
	Docs   []LiveDoc
	MaxSeq int64
}

type Planner struct {
	hist *history.History
}

func NewPlanner(hist *history.History) *Planner {
	return &Planner{hist: hist}
}

func (p *Planner) Plan(c int64) (Plan, error) {
	p.hist.RLock()
	defer p.hist.RUnlock()
	maxSeq := p.hist.MaxSeqLocked()
	if c < 0 || c > maxSeq {
		return Plan{}, history.ErrInvalidArgument
	}
	h := p.hist.HLocked()
	if c+1 >= h {
		// OpsBased：seq>c 的操作全部现存且连续（c+1>=H 保证无洞）。
		snaps := p.hist.OpsAfterLocked(c)
		ops := make([]history.Op, len(snaps))
		copy(ops, snaps)
		return Plan{Mode: OpsBased, Ops: ops, MaxSeq: maxSeq}, nil
	}
	// FileBased：历史在 c 之后有缺口，直接拷贝当前存活文档快照。
	snaps := p.hist.AliveDocsLocked()
	docs := make([]LiveDoc, len(snaps))
	for i, sn := range snaps {
		docs[i] = LiveDoc{ID: sn.ID, Seq: sn.Seq}
	}
	return Plan{Mode: FileBased, Docs: docs, MaxSeq: maxSeq}, nil
}
