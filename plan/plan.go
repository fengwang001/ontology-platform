package plan

import (
	"sort"

	"ontology/ledger"
	"ontology/tree"
)

// Victim is one task selected for preemption.
type Victim struct {
	TaskID string
	Leaf   string
	Size   int64
}

// Plan is a read-only allocation decision.
type Plan struct {
	Immediate bool     // the request can be committed now
	Need      int64    // capacity that must be reclaimed (0 for immediate)
	Free      int64    // usable free at build time
	Victims   []Victim // atomic, complete set when !Immediate
}

// Builder is stateless; it only reads tree and ledger.
type Builder struct {
	tr *tree.Tree
	lg *ledger.Ledger

	// NodesChecked is reset per Build: borrowers examined during selection.
	NodesChecked int
}

func NewBuilder(tr *tree.Tree, lg *ledger.Ledger) *Builder {
	return &Builder{tr: tr, lg: lg}
}

// Build computes the plan for a new request on leaf of size r, given the
// already-frozen in-flight capacity. It never mutates the ledger.
func (b *Builder) Build(leaf string, r, inFlight int64) Plan {
	b.NodesChecked = 0
	free := b.tr.C - b.lg.TotalUsed() - inFlight
	if free < 0 {
		free = 0
	}
	n, _ := b.tr.Node(leaf)
	guaranteeGap := n.Min - b.lg.Used(leaf)
	if guaranteeGap < 0 {
		guaranteeGap = 0
	}
	need := r - free
	if need > guaranteeGap {
		need = guaranteeGap
	}
	if need <= 0 {
		return Plan{Immediate: true, Free: free}
	}
	borrowers := b.lg.Borrowers()
	sort.Slice(borrowers, func(i, j int) bool {
		si, sj := b.tr.Siblings(leaf, borrowers[i]), b.tr.Siblings(leaf, borrowers[j])
		if si != sj {
			return si
		}
		bi, bj := b.lg.Borrowed(borrowers[i]), b.lg.Borrowed(borrowers[j])
		if bi != bj {
			return bi > bj
		}
		return borrowers[i] < borrowers[j]
	})
	p := Plan{Immediate: false, Need: need, Free: free}
	var got int64
	for _, bID := range borrowers {
		b.NodesChecked++
		if got >= need {
			break
		}
		room := b.lg.Borrowed(bID)
		tasks := feasibleTasks(b.lg.TasksOn(bID), room)
		for _, tk := range tasks {
			if got >= need {
				break
			}
			p.Victims = append(p.Victims, Victim{TaskID: tk.ID, Leaf: bID, Size: tk.Size})
			got += tk.Size
		}
	}
	if got < need {
		return Plan{Immediate: false, Need: need, Free: free} // atomic: notify nobody
	}
	return p
}

// feasibleTasks returns tasks greedily by descending size (earlier start on
	tie) while cumulative size never exceeds room: no victim drops below Min.
func feasibleTasks(all []ledger.Task, room int64) []ledger.Task {
	can := make([]ledger.Task, 0, len(all))
	for _, tk := range all {
		if tk.Size <= room {
			can = append(can, tk)
		}
	}
	sort.Slice(can, func(i, j int) bool {
		if can[i].Size != can[j].Size {
			return can[i].Size > can[j].Size
		}
		return can[i].Start < can[j].Start
	})
	out := make([]ledger.Task, 0, len(can))
	var used int64
	for _, tk := range can {
		if used+tk.Size > room {
			continue
		}
		out = append(out, tk)
		used += tk.Size
	}
	return out
}
