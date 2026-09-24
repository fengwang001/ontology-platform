package api

import (
	"fmt"
	"sort"

	"ontology/aentry"
)

// naive is the brute-force reference: after every step it re-scans the whole input and emits whatever the rules allow, in rule order.
type naive struct {
	mode   Mode
	capN   int
	ents   []aentry.Entry // accepted, input order
	seq    aentry.Sequencer
	occ    int
	lastT  int64
	hasWM  bool
	curSeg int            // current (last) segment index
	open   int            // number of open segments
	idx    map[string]int // element id -> ents index
	segOf  map[string]int // element id -> segment index
}

func newNaive(m Mode, c int) *naive {
	return &naive{mode: m, capN: c, open: 1, idx: map[string]int{}, segOf: map[string]int{}}
}

func (n *naive) step(s Step) ([]aentry.Entry, error) {
	switch s.Op {
	case "in":
		if n.occ >= n.capN {
			return nil, aentry.ErrFull
		}
		if err := aentry.CheckID(s.ID, func(id string) bool { _, ok := n.idx[id]; return ok }); err != nil {
			return nil, err
		}
		n.idx[s.ID] = len(n.ents)
		n.segOf[s.ID] = n.curSeg
		n.ents = append(n.ents, aentry.Elem(s.ID))
		n.occ++
		return nil, nil
	case "wm":
		if err := aentry.CheckWatermark(s.T, n.lastT, n.hasWM); err != nil {
			return nil, err
		}
		n.lastT, n.hasWM = s.T, true
		n.curSeg++
		n.ents = append(n.ents, aentry.WM(s.T))
		return n.release(""), nil
	default:
		i, ok := n.idx[s.ID]
		if !ok || n.ents[i].Done {
			return nil, aentry.ErrUnknown
		}
		n.ents[i].Done, n.ents[i].Seq = true, n.seq.Next()
		return n.release(s.ID), nil
	}
}

// release re-scans the full input, emitting everything the rules allow; trig is the just-completed id ("" if none).
func (n *naive) release(trig string) []aentry.Entry {
	var out []aentry.Entry
	emit := func(i int) {
		n.ents[i].Out = true
		out = append(out, n.ents[i])
		if n.ents[i].Kind == aentry.Element {
			n.occ--
		}
	}
	if n.mode == Ordered { // emit the prefix while the head is ready
		for i := 0; i < len(n.ents) && (n.ents[i].Out || n.ents[i].Kind == aentry.Watermark || n.ents[i].Done); i++ {
			if !n.ents[i].Out {
				emit(i)
			}
		}
		return out
	}
	if trig != "" { // an open segment emits on completion
		if i := n.idx[trig]; n.segOf[trig] < n.open && !n.ents[i].Out {
			emit(i)
		}
	}
	for { // cascade: first pending watermark with all prior elements out
		w := -1
		for i, e := range n.ents {
			if e.Kind == aentry.Watermark && !e.Out {
				w = i
				break
			}
		}
		if w < 0 {
			return out
		}
		blocked := false
		for i := 0; i < w; i++ {
			blocked = blocked || n.ents[i].Kind == aentry.Element && !n.ents[i].Out
		}
		if blocked {
			return out
		}
		emit(w)
		n.open++
		var held []int // done elements of the newly opened segment
		for i := w + 1; i < len(n.ents) && n.ents[i].Kind == aentry.Element; i++ {
			if !n.ents[i].Out && n.ents[i].Done {
				held = append(held, i)
			}
		}
		sort.Slice(held, func(a, b int) bool { return n.ents[held[a]].Seq < n.ents[held[b]].Seq })
		for _, i := range held {
			emit(i)
		}
	}
}

// runSteps replays steps in the given mode, comparing every step against the naive reference (invariants 1, 4).
func runSteps(mode Mode, capN int, steps []Step) error {
	a, n := New(mode, capN), newNaive(mode, capN)
	for i, s := range steps {
		wantOut, wantErr := n.step(s)
		gotOut, gotErr := a.step(s)
		if gotErr != wantErr || fmt.Sprint(gotOut) != fmt.Sprint(wantOut) || a.Occupancy() != n.occ {
			return fmt.Errorf("step %d %+v: got (%v,%v,%d), want (%v,%v,%d)",
				i, s, gotOut, gotErr, a.Occupancy(), wantOut, wantErr, n.occ)
		}
	}
	return nil
}

// SelfCheck replays built-in scenarios in both modes against the naive reference, verifying the four invariants.
func SelfCheck() error {
	scens := [][]Step{
		{{"in", "e1", 0}, {"in", "e2", 0}, {"wm", "", 10}, {"in", "e3", 0}, {"complete", "e2", 0}, {"in", "e4", 0},
			{"complete", "e4", 0}, {"complete", "e3", 0}, {"in", "e5", 0}, {"complete", "e1", 0}, {"complete", "e5", 0}},
		{{"in", "a", 0}, {"complete", "a", 0}, {"wm", "", 5}, {"in", "b", 0}, {"in", "c", 0}, {"in", "d", 0},
			{"in", "e", 0}, {"in", "f", 0}, {"in", "b", 0}, {"wm", "", 5}, {"complete", "x", 0}, {"complete", "c", 0},
			{"wm", "", 7}, {"complete", "b", 0}, {"complete", "d", 0}, {"complete", "e", 0}, {"complete", "d", 0},
			{"in", "f", 0}, {"complete", "f", 0}},
	}
	for i, sc := range scens {
		for _, m := range []Mode{Ordered, Unordered} {
			if err := runSteps(m, 4, sc); err != nil {
				return fmt.Errorf("scenario %d mode %d: %w", i, m, err)
			}
		}
	}
	return nil
}
