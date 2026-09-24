package applier

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"

	"ontology/kq"
)

func recsOf(rs []Record) string {
	b := strings.Builder{}
	for _, r := range rs {
		c := byte('+')
		if r.Kind == DeadLettered {
			c = 'x'
		}
		fmt.Fprintf(&b, "%s%d%c", r.Key, r.Seq, c)
	}
	return b.String()
}

// snap encodes blocked heads/attempts, per-key FIFO buffers, dead letters.
func snap(a *Applier) string {
	ks := make([]string, 0, len(a.blocked))
	for k := range a.blocked {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	ps := make([]string, 0, len(ks))
	for _, k := range ks {
		h, n := a.keys[k].Head()
		s := fmt.Sprintf("%s#%d/%d", k, h.Seq, n)
		if bf := a.keys[k].Buffered(); len(bf) > 0 {
			is := make([]string, len(bf))
			for i, e := range bf {
				is[i] = strconv.FormatInt(e.Seq, 10)
			}
			s += ":[" + strings.Join(is, ",") + "]"
		}
		ps = append(ps, s)
	}
	dl := make([]string, 0, len(a.dead))
	for _, e := range a.dead {
		dl = append(dl, fmt.Sprintf("%s%d", e.Key, e.Seq))
	}
	return strings.Join(ps, " ") + "|" + strings.Join(dl, ",")
}

// TestOrderedTrace drives the mandated eleven operations and pins every
// step's finalized records plus heads/buffers/dead list (NOTES table).
func TestOrderedTrace(t *testing.T) {
	a := New(3, 8, map[kq.Event]int{{Key: "A", Seq: 2}: 1, {Key: "B", Seq: 1}: 99, {Key: "B", Seq: 3}: 1})
	rows := []struct {
		e    kq.Event
		tick bool
		recs string
		full string
	}{
		{ev("A", 1), false, "A1+", "|"},
		{ev("A", 2), false, "", "A#2/1|"},
		{ev("B", 1), false, "", "A#2/1 B#1/1|"},
		{ev("A", 3), false, "", "A#2/1:[3] B#1/1|"},
		{ev("C", 1), false, "C1+", "A#2/1:[3] B#1/1|"},
		{ev("B", 2), false, "", "A#2/1:[3] B#1/1:[2]|"},
		{kq.Event{}, true, "A2+A3+", "B#1/2:[2]|"},
		{ev("B", 3), false, "", "B#1/2:[2,3]|"},
		{ev("A", 4), false, "A4+", "B#1/2:[2,3]|"},
		{kq.Event{}, true, "B1xB2+", "B#3/1|B1"},
		{kq.Event{}, true, "B3+", "|B1"},
	}
	for i, r := range rows {
		var rs []Record
		if r.tick {
			rs = a.Tick()
		} else {
			rs, _ = a.Submit(r.e)
		}
		if recsOf(rs) != r.recs || snap(a) != r.full {
			t.Fatalf("step %d: recs=%q snap=%q want %q / %q", i+1, recsOf(rs), snap(a), r.recs, r.full)
		}
	}
}

func ev(k string, s int64) kq.Event { return kq.Event{Key: k, Seq: s} }

// TestRejectedOpsLeaveNoTrace pins invariant 4: three distinct error
// classes, and a rejected op changes no state including retry schedules.
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	a := New(3, 1, map[kq.Event]int{ev("z", 1): 5})
	a.Submit(ev("z", 1))
	a.Submit(ev("z", 2)) // sole buffer slot full
	for _, cs := range [][2]any{{ev("", 1), ErrInvalidArgument}, {ev("z", 1), ErrSeqNotIncreasing}, {ev("z", 3), ErrBufferFull}} {
		if _, got := a.Submit(cs[0].(kq.Event)); !errors.Is(got, cs[1].(error)) {
			t.Fatalf("%v err=%v", cs[0], got)
		}
	}
	if len(a.Applied()) != 0 || len(a.DeadLetters()) != 0 {
		t.Fatal("rejection changed finalized lists")
	}
	// z1 has 1 attempt and rejects added none: tick1 (attempt 2) stays
	// blocked, tick2 (attempt 3) dead-letters z1 and drains z2.
	if rs := a.Tick(); len(rs) != 0 {
		t.Fatalf("tick1 after rejects=%v", rs)
	} else if rs := a.Tick(); len(rs) != 2 || rs[0].Kind != DeadLettered || rs[1].Kind != Applied {
		t.Fatalf("tick2 after rejects=%v", rs)
	}
	if rs, _ := a.Submit(ev("w", 1)); len(rs) != 1 { // still usable
		t.Fatalf("fresh key after rejects=%v", rs)
	}
}

// TestTickInspectsOnlyBlocked pins the complexity guarantee: the internal
// tickChecked counter equals the blocked-set size at Tick start, independent
// of the number m of known but non-blocked keys.
func TestTickInspectsOnlyBlocked(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		a := New(3, m+10, map[kq.Event]int{})
		for i := 0; i < m; i++ {
			if _, err := a.Submit(ev(fmt.Sprintf("k%05d", i), 1)); err != nil {
				t.Fatal(err)
			}
		}
		a.fails[ev("zzz", 1)] = 5
		if _, err := a.Submit(ev("zzz", 1)); err != nil {
			t.Fatal(err)
		}
		a.Tick()
		if a.tickChecked != 1 { // constant in m despite m settled keys
			t.Fatalf("m=%d tickChecked=%d want 1", m, a.tickChecked)
		}
		for _, k := range []string{"yyy0", "yyy1"} {
			a.fails[ev(k, 1)] = 5
			if _, err := a.Submit(ev(k, 1)); err != nil {
				t.Fatal(err)
			}
		}
		a.Tick()
		if a.tickChecked != 3 { // blocked set at Tick start: zzz, yyy0, yyy1
			t.Fatalf("m=%d tickChecked=%d want 3", m, a.tickChecked)
		}
		if len(a.applied) != m { // settled keys are never revisited
			t.Fatalf("m=%d applied=%d want %d", m, len(a.applied), m)
		}
	}
}
