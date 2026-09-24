package api

import (
	"errors"
	"sort"

	"ontology/order"
)

// SelfCheck replays a built-in sequence and verifies the four invariants
// against an independent naive reference, then exercises all rejection
// paths. It uses fresh local instances and leaves receiver state untouched.
func (r *Reorder) SelfCheck() error {
	ids := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
	tss := []int64{5, 3, 9, 5, 6, 9, 4, 12, 12, 16}
	if e := runReference(3, 10, ids, tss); e != nil {
		return e
	}
	if _, e := New(-1, 10); e != ErrInvalidParam {
		return errors.New("selfcheck: negative delay not rejected")
	}
	if _, e := New(3, 0); e != ErrInvalidParam {
		return errors.New("selfcheck: non-positive capacity not rejected")
	}
	rr, _ := New(3, 1)
	rr.Push("x", 100)
	m0, s0 := rr.Main(), rr.Side()
	badIDs := []string{"", "x", "y"}
	badTS := []int64{1, 1, 1000}
	badErr := []error{ErrEmptyID, ErrDuplicateID, ErrFull}
	for i := range badIDs {
		if _, _, e := rr.Push(badIDs[i], badTS[i]); e != badErr[i] {
			return errors.New("selfcheck: rejection error mismatch")
		}
	}
	if !equalOuts(rr.Main(), m0) || !equalOuts(rr.Side(), s0) {
		return errors.New("selfcheck: rejected call left a trace")
	}
	if _, _, e := rr.Push("z", 0); e != nil { // still usable: z arrives late
		return errors.New("selfcheck: buffer unusable after rejections")
	}
	return nil
}

// runReference feeds one sequence into a fresh Reorder and into the naive
// reference, then verifies invariants 1-3.
func runReference(delay int64, cap int, ids []string, tss []int64) error {
	rr, e := New(delay, cap)
	if e != nil {
		return e
	}
	evs := make([]Out, len(ids))
	for i := range ids {
		if _, _, e := rr.Push(ids[i], tss[i]); e != nil {
			return e
		}
		evs[i] = Out{ids[i], tss[i], int64(i)}
	}
	rr.Flush()
	refMain, refSide := naiveOutputs(delay, evs)
	got := rr.Main()
	if !equalOuts(got, refMain) || !equalOuts(rr.Side(), refSide) {
		return errors.New("reference: outputs differ from naive reference")
	}
	for i := 1; i < len(got); i++ {
		if !order.Less(got[i-1].TS, got[i-1].Seq, got[i].TS, got[i].Seq) {
			return errors.New("reference: main output not strictly ordered")
		}
	}
	n := map[string]int{}
	for _, o := range append(append([]Out{}, got...), rr.Side()...) {
		if n[o.ID]++; n[o.ID] > 1 {
			return errors.New("reference: an event appears more than once")
		}
	}
	if len(n) != len(ids) || rr.buf.Len() != 0 {
		return errors.New("reference: not exactly-once")
	}
	return nil
}

// naiveOutputs is the independent reference: events in arrival order are
// split into the side output (late, kept in arrival order) and the main
// output (the rest, stable-sorted by (TS, Seq)).
func naiveOutputs(delay int64, evs []Out) (main, side []Out) {
	var maxTS int64
	have := false
	for _, o := range evs {
		if have && o.TS <= maxTS-delay {
			side = append(side, o)
		} else {
			main = append(main, o)
		}
		if !have || o.TS > maxTS {
			maxTS, have = o.TS, true
		}
	}
	sort.SliceStable(main, func(i, j int) bool { return main[i].TS < main[j].TS })
	return main, side
}

func equalOuts(a, b []Out) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
