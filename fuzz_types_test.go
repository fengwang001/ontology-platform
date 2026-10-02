package ontology

import "fmt"

type opKind int

const (
	opAdd opKind = iota
	opPick
	opDone
	opAbort
)

type op struct {
	kind opKind
	now  int64
	size int64
	pid  int64
}

type runSnap struct {
	id      int64
	size    int64
	created int64
	busy    bool
	fails   int
}

// step is the normalized, comparable result of one operation.
type step struct {
	kind   string
	err    string
	id     int64
	reason string
	sel    []int64
	total  int64
	probes int
	runs   []runSnap
	basis  string
}

func (c *Compactor) snapshotRuns() []runSnap {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]runSnap, len(c.runs))
	for i, r := range c.runs {
		out[i] = runSnap{r.ID, r.Size, r.Created, r.Busy, r.Fails}
	}
	return out
}

func (s *naiveSim) snapshotRuns() []runSnap {
	out := make([]runSnap, len(s.runs))
	for i, r := range s.runs {
		out[i] = runSnap{r.id, r.size, r.created, r.busy, r.fails}
	}
	return out
}

func (p step) String() string {
	if p.err != "nil" {
		return fmt.Sprintf("%s -> %s; runs=%s", p.kind, p.err, formatSnaps(p.runs))
	}
	switch p.kind {
	case "Add", "Done":
		return fmt.Sprintf("%s -> id=%d; runs=%s", p.kind, p.id, formatSnaps(p.runs))
	case "Abort":
		return fmt.Sprintf("Abort -> ok; runs=%s", formatSnaps(p.runs))
	default:
		return fmt.Sprintf("Pick -> #%d %s ids=%v total=%d probes=%d | %s; runs=%s",
			p.id, p.reason, p.sel, p.total, p.probes, p.basis, formatSnaps(p.runs))
	}
}

func formatSnaps(rs []runSnap) string {
	out := "["
	for i, r := range rs {
		if i > 0 {
			out += " "
		}
		out += fmt.Sprintf("%d(sz=%d,cr=%d,busy=%t,fails=%d)",
			r.id, r.size, r.created, r.busy, r.fails)
	}
	return out + "]"
}

func sameStep(a, b step) bool {
	if a.kind != b.kind || a.err != b.err || a.id != b.id ||
		a.reason != b.reason || a.total != b.total || a.probes != b.probes {
		return false
	}
	if len(a.sel) != len(b.sel) {
		return false
	}
	for i := range a.sel {
		if a.sel[i] != b.sel[i] {
			return false
		}
	}
	if len(a.runs) != len(b.runs) {
		return false
	}
	for i := range a.runs {
		if a.runs[i] != b.runs[i] {
			return false
		}
	}
	return true
}
