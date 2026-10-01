package ontology

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"testing"
)

// naiveEntry is a deliberately straightforward, independently written
// reference model following the folding table from the specification.
type naiveEntry struct {
	kind  NetKind
	first int64
	last  int64
}

type naiveDebouncer struct {
	q, w, cap int64
	pending   map[string]naiveEntry
	maxNow    int64
	hasTime   bool
}

func newNaive(q, w, c int64) *naiveDebouncer {
	return &naiveDebouncer{q: q, w: w, cap: c, pending: map[string]naiveEntry{}}
}

func naiveValidPath(p string) bool {
	if p == "" || p[0] == '/' || p[len(p)-1] == '/' {
		return false
	}
	return !strings.Contains(p, "//")
}

// foldTable applies the specification folding table.
func foldTable(cur NetKind, has bool, ev EventKind) (NetKind, bool) {
	if !has {
		switch ev {
		case Create:
			return NetCreate, true
		case Modify:
			return NetModify, true
		case Delete, DeleteDir:
			return NetDelete, true
		}
	}
	switch ev {
	case Create:
		switch cur {
		case NetCreate:
			return NetCreate, true
		case NetModify:
			return NetModify, true
		case NetDelete:
			return NetModify, true
		}
	case Modify:
		return cur, true
	case Delete, DeleteDir:
		switch cur {
		case NetCreate:
			return 0, false // cancel
		case NetModify, NetDelete:
			return NetDelete, true
		}
	}
	return 0, false
}

func (n *naiveDebouncer) add(now int64, ev Event) error {
	if !(ev.Kind >= Create && ev.Kind <= DeleteDir) {
		return ErrUnknownKind
	}
	if !naiveValidPath(ev.Path) {
		return ErrInvalidPath
	}
	if n.hasTime && now < n.maxNow {
		return ErrClockRewind
	}
	_, exists := n.pending[ev.Path]
	if !exists && int64(len(n.pending)) >= n.cap {
		return ErrCapacityExceeded
	}
	n.maxNow, n.hasTime = now, true

	if ev.Kind == DeleteDir {
		prefix := ev.Path + "/"
		for p := range n.pending {
			if strings.HasPrefix(p, prefix) {
				delete(n.pending, p)
			}
		}
	}
	cur, has := n.pending[ev.Path]
	kind, keep := foldTable(cur.kind, has, ev.Kind)
	if !keep {
		delete(n.pending, ev.Path)
		return nil
	}
	if !has {
		n.pending[ev.Path] = naiveEntry{kind: kind, first: now, last: now}
	} else {
		cur.kind, cur.last = kind, now
		n.pending[ev.Path] = cur
	}
	return nil
}

func (n *naiveDebouncer) flush(now int64) ([]Entry, error) {
	if n.hasTime && now < n.maxNow {
		return nil, ErrClockRewind
	}
	n.maxNow, n.hasTime = now, true
	var out []Entry
	for p, e := range n.pending {
		if now-e.last >= n.q || now-e.first >= n.w {
			out = append(out, Entry{Path: p, Kind: e.kind, First: e.first, Last: e.last})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	for _, e := range out {
		delete(n.pending, e.Path)
	}
	return out, nil
}

func (n *naiveDebouncer) nextDue() (int64, bool) {
	var best int64
	found := false
	for _, e := range n.pending {
		t := e.last + n.q
		if e.first+n.w < t {
			t = e.first + n.w
		}
		if !found || t < best {
			best, found = t, true
		}
	}
	return best, found
}

// TestDifferentialAgainstNaive replays 2000 random (now, event) sequences
// against both implementations and requires identical output. Input, output
// and the decision basis are logged for the first sequence of every seed.
func TestDifferentialAgainstNaive(t *testing.T) {
	const sequences = 2000
	for seed := int64(0); seed < sequences; seed++ {
		rng := rand.New(rand.NewSource(seed))
		q := int64(1 + rng.Intn(5))
		w := q + int64(rng.Intn(5))
		cap := int64(1 + rng.Intn(4))
		real, _ := NewDebouncer(q, w, cap)
		ref := newNaive(q, w, cap)

		paths := []string{"a", "b", "a/b", "a/c", "a/b/c", "ab/c", "d", "d/e", "f"}
		kinds := []EventKind{Create, Modify, Delete, DeleteDir}
		now := int64(0)
		var log []string
		var basis []string

		for step := 0; step < 40; step++ {
			isFlush := rng.Intn(4) == 0
			now += int64(rng.Intn(3)) // monotonic; accepted ops never exceed it
			if isFlush {
				flushNow := now
				if rng.Intn(5) == 0 {
					flushNow = now - int64(1+rng.Intn(3))
					if flushNow < 0 {
						flushNow = 0
					}
				}
				g1, e1 := real.Flush(flushNow)
				g2, e2 := ref.flush(flushNow)
				if errS(e1) != errS(e2) || !entriesEqual(g1, g2) {
					t.Fatalf("seed=%d Flush(%d): real=(%v,%v) ref=(%v,%v)\nlog:\n%s",
						seed, flushNow, g1, e1, g2, e2, strings.Join(log, "\n"))
				}
				decision := "accepted"
				if e1 != nil {
					decision = "rejected: " + errS(e1)
				}
				log = append(log, fmt.Sprintf("Flush@%d -> %s out=%v", flushNow, decision, g1))
			} else {
				ev := Event{Kind: kinds[rng.Intn(len(kinds))], Path: paths[rng.Intn(len(paths))]}
				at := now
				switch rng.Intn(12) {
				case 0:
					ev.Kind = EventKind(99)
				case 1:
					ev.Path = ""
				case 2:
					ev.Path = "/x"
				case 3:
					ev.Path = "x/"
				case 4:
					ev.Path = "x//y"
				case 5:
					at = now - int64(1+rng.Intn(3)) // clock rewind attempt
					if at < 0 {
						at = 0
					}
				}
				e1 := real.Add(at, ev)
				e2 := ref.add(at, ev)
				if errS(e1) != errS(e2) {
					t.Fatalf("seed=%d step=%d Add(%d,%+v): real err=%v ref err=%v\nlog:\n%s",
						seed, step, at, ev, e1, e2, strings.Join(log, "\n"))
				}
				decision := "rejected: " + errS(e1)
				if e2 == nil {
					decision = "accepted and folded"
					basis = append(basis, fmt.Sprintf("accepted Add@%d %+v", at, ev))
				}
				log = append(log, fmt.Sprintf("Add@%d ev=%+v -> %s", at, ev, decision))
			}

			nd1, ok1 := real.NextDue()
			nd2, ok2 := ref.nextDue()
			if ok1 != ok2 || (ok1 && nd1 != nd2) {
				t.Fatalf("seed=%d NextDue: real=(%d,%v) ref=(%d,%v)\nlog:\n%s",
					seed, nd1, ok1, nd2, ok2, strings.Join(log, "\n"))
			}
		}

		// Final quiescent flush drains everything.
		final := now + w + q
		g1, e1 := real.Flush(final)
		g2, e2 := ref.flush(final)
		if e1 != e2 || !entriesEqual(g1, g2) {
			t.Fatalf("seed=%d final flush: real=(%v,%v) ref=(%v,%v)\nlog:\n%s",
				seed, g1, e1, g2, e2, strings.Join(log, "\n"))
		}
		if n := pendingCount(real); n != 0 {
			t.Fatalf("seed=%d pending not empty after final flush: %d", seed, n)
		}
		if seed < 3 {
			t.Logf("seed=%d params Q=%d W=%d Cap=%d\n  input/decisions:\n  %s\n  final output@%d: %v",
				seed, q, w, cap, strings.Join(append(log, basis...), "\n  "), final, g1)
		}
	}
}

// TestConcurrentAccess verifies calls are serializable under -race: after a
// fixed closed input set, a quiescent flush emits exactly one folded entry.
func TestConcurrentAccess(t *testing.T) {
	d := mustNew(t, 50, 100000, 4)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = d.Add(int64(i+1), Event{Kind: Modify, Path: "f"})
		}(i)
	}
	wg.Wait()

	// Readers must be safe to call concurrently with each other.
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = d.Flush(10) // never due: quiet 50, huge W
			_, _ = d.NextDue()
		}()
	}
	wg.Wait()

	got := mustFlush(t, d, 100)
	if len(got) != 1 || got[0].Path != "f" || got[0].Kind != NetModify ||
		got[0].Last != 20 || got[0].First < 1 || got[0].First > 20 {
		t.Fatalf("want one M entry with last=20 and first in 1..20, got %v", got)
	}
	t.Logf("20 concurrent Add@1..20 + Flush/NextDue; some serial order; output=%v", got)
}

func errS(e error) string {
	if e == nil {
		return "<nil>"
	}
	return e.Error()
}

func entriesEqual(a, b []Entry) bool {
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
