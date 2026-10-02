package changebuffer

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// Naive differential oracle implementing the spec independently.

type naiveEntry struct {
	size   int64
	marked bool
}

type naivePage struct {
	inPool bool
	entry  map[string]*naiveEntry
	used   int64
	queue  []QueuedOp
	buf    int64
}

type naiveModel struct {
	s, kp, g int64
	pages    map[int]*naivePage
	global   int64
}

func newNaive(s, kp, g int64) *naiveModel {
	return &naiveModel{s: s, kp: kp, g: g, pages: make(map[int]*naivePage)}
}

func (m *naiveModel) page(p int) *naivePage {
	pg, ok := m.pages[p]
	if !ok {
		pg = &naivePage{entry: map[string]*naiveEntry{}}
		m.pages[p] = pg
	}
	return pg
}

func naiveApply(en map[string]*naiveEntry, used *int64, s int64, kind Kind, key string, e int64) bool {
	switch kind {
	case Insert:
		if x, ok := en[key]; ok {
			x.marked = false
			return true
		}
		if s-*used < e {
			return false
		}
		en[key] = &naiveEntry{size: e}
		*used += e
		return true
	case DeleteMark:
		if x, ok := en[key]; ok {
			x.marked = true
		}
		return true
	default:
		if x, ok := en[key]; ok && x.marked {
			delete(en, key)
			*used -= x.size
		}
		return true
	}
}

func cloneEntries(en map[string]*naiveEntry) map[string]*naiveEntry {
	c := make(map[string]*naiveEntry, len(en))
	for k, v := range en {
		c[k] = &naiveEntry{size: v.size, marked: v.marked}
	}
	return c
}

func (m *naiveModel) op(p int, kind Kind, key string, e int64) (Decision, error) {
	pg := m.page(p)
	if pg.inPool {
		if !naiveApply(pg.entry, &pg.used, m.s, kind, key, e) {
			return 0, ErrPageSpace
		}
		return DecisionAppliedDirect, nil
	}

	can := int64(len(pg.queue)) < m.kp
	if can && kind == Insert {
		lb := lowerBoundOf(m.s, bucketOf(m.s, m.s-pg.used))
		if pg.buf+e > lb || m.global+e > m.g {
			can = false
		}
	}
	if can {
		pg.queue = append(pg.queue, QueuedOp{Kind: kind, Key: key, Size: e})
		if kind == Insert {
			pg.buf += e
			m.global += e
		}
		return DecisionBuffered, nil
	}

	en := cloneEntries(pg.entry)
	used := pg.used
	for _, q := range pg.queue {
		if !naiveApply(en, &used, m.s, q.Kind, q.Key, q.Size) {
			return 0, ErrPageSpace
		}
	}
	if !naiveApply(en, &used, m.s, kind, key, e) {
		return 0, ErrPageSpace
	}
	pg.entry = en
	pg.used = used
	m.global -= pg.buf
	pg.buf = 0
	pg.queue = nil
	pg.inPool = true
	return DecisionForceMerged, nil
}

func (m *naiveModel) load(p int) {
	pg := m.page(p)
	if pg.inPool {
		return
	}
	for _, q := range pg.queue {
		naiveApply(pg.entry, &pg.used, m.s, q.Kind, q.Key, q.Size)
	}
	m.global -= pg.buf
	pg.buf = 0
	pg.queue = nil
	pg.inPool = true
}

func (m *naiveModel) evict(p int) error {
	pg := m.page(p)
	if !pg.inPool {
		return ErrPageNotInPool
	}
	pg.inPool = false
	return nil
}

func (m *naiveModel) view(p int) []Entry {
	pg := m.page(p)
	en := cloneEntries(pg.entry)
	var used int64 = pg.used
	for _, q := range pg.queue {
		naiveApply(en, &used, m.s, q.Kind, q.Key, q.Size)
	}
	keys := make([]string, 0, len(en))
	for k := range en {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]Entry, 0, len(keys))
	for _, k := range keys {
		out = append(out, Entry{Key: k, Size: en[k].size, Marked: en[k].marked})
	}
	return out
}

func sameErr(a, b error) bool { return (a == nil) == (b == nil) }

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

func queueEqual(a, b []QueuedOp) bool {
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

type loggedCall struct {
	text   string
	d      Decision
	err    error
	reason string
}

func kindName(k Kind) string {
	switch k {
	case Insert:
		return "Insert"
	case DeleteMark:
		return "DeleteMark"
	default:
		return "Purge"
	}
}

func admissionReason(m *naiveModel, p int, kind Kind, e int64) string {
	pg := m.page(p)
	if pg.inPool {
		return "[in pool -> direct]"
	}
	if int64(len(pg.queue)) >= m.kp {
		return fmt.Sprintf("[queue=%d==Kp -> force merge]", len(pg.queue))
	}
	if kind != Insert {
		return fmt.Sprintf("[%s: no space/global check -> buffer]", kindName(kind))
	}
	free := m.s - pg.used
	bucket := bucketOf(m.s, free)
	lb := lowerBoundOf(m.s, bucket)
	local := pg.buf + e
	switch {
	case local > lb:
		return fmt.Sprintf("[F=%d b=%d lb=%d: %d>%d -> force merge]", free, bucket, lb, local, lb)
	case m.global+e > m.g:
		return fmt.Sprintf("[local %d<=%d but global %d+%d>G=%d -> force merge]", local, lb, m.global, e, m.g)
	default:
		return fmt.Sprintf("[F=%d b=%d lb=%d: %d<=%d, global %d+%d<=%d -> buffer]",
			free, bucket, lb, local, lb, m.global, e, m.g)
	}
}

// TestRandomDifferential replays 2000 random sequences through both the
// production ChangeBuffer and the naive oracle, comparing every answer and
// all observable state after each step. Inputs, outputs and the buffering
// decision basis are written to the test log.
func TestRandomDifferential(t *testing.T) {
	const sequences = 2000
	var logBuf strings.Builder

	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)*7919 + 17))
		s := int64(64 + rng.Intn(960)) // exercises lb flooring
		kp := int64(1 + rng.Intn(6))
		g := int64(1 + rng.Intn(400))
		steps := 20 + rng.Intn(40)
		pageCount := 1 + rng.Intn(5)

		cb, err := New(s, kp, g)
		if err != nil {
			t.Fatalf("seq %d: New: %v", seq, err)
		}
		nm := newNaive(s, kp, g)
		calls := make([]loggedCall, 0, steps)
		fmt.Fprintf(&logBuf, "seq %d: S=%d Kp=%d G=%d steps=%d pages=%d\n", seq, s, kp, g, steps, pageCount)

		for st := 0; st < steps; st++ {
			page := rng.Intn(pageCount)
			switch rng.Intn(10) {
			case 0:
				errC := cb.Load(page)
				nm.load(page)
				calls = append(calls, loggedCall{text: fmt.Sprintf("Load(%d)", page), err: errC})
			case 1:
				errC := cb.Evict(page)
				errN := nm.evict(page)
				if !sameErr(errC, errN) {
					t.Fatalf("seq %d step %d Evict(%d): cb=%v naive=%v\n%s", seq, st, page, errC, errN, logBuf.String())
				}
				calls = append(calls, loggedCall{text: fmt.Sprintf("Evict(%d)", page), err: errC})
			default:
				kinds := []Kind{Insert, Insert, Insert, Insert, DeleteMark, DeleteMark, Purge, Purge}
				kind := kinds[rng.Intn(len(kinds))]
				key := fmt.Sprintf("k%d", rng.Intn(6))
				var e int64
				if kind == Insert {
					switch rng.Intn(8) {
					case 0:
						e = s / 32
					case 1:
						e = s/32 + 1
					case 2:
						e = s / 8
					case 3:
						e = g
					default:
						e = 1 + rng.Int63n(s)
					}
					if e < 1 {
						e = 1
					}
					if e > s {
						e = s
					}
				}
				reason := admissionReason(nm, page, kind, e)
				dC, errC := cb.Op(page, kind, key, e)
				dN, errN := nm.op(page, kind, key, e)
				if dC != dN || !sameErr(errC, errN) {
					t.Fatalf("seq %d step %d Op(%d,%s,%q,%d): cb=(%d,%v) naive=(%d,%v)\n%s",
						seq, st, page, kindName(kind), key, e, dC, errC, dN, errN, logBuf.String())
				}
				calls = append(calls, loggedCall{
					text:   fmt.Sprintf("Op(%d,%s,%q,%d)", page, kindName(kind), key, e),
					d:      dC,
					err:    errC,
					reason: reason,
				})
			}

			for p := 0; p < pageCount; p++ {
				vc, _ := cb.View(p)
				if !entriesEqual(vc, nm.view(p)) {
					t.Fatalf("seq %d step %d View(%d): cb=%v na=%v\n%s", seq, st, p, vc, nm.view(p), logBuf.String())
				}
				qc, _ := cb.Queue(p)
				var qn []QueuedOp
				if pg := nm.pages[p]; pg != nil {
					qn = pg.queue
				}
				if !queueEqual(qc, qn) {
					t.Fatalf("seq %d step %d Queue(%d): cb=%v na=%v", seq, st, p, qc, qn)
				}
				inC, _ := cb.InPool(p)
				inN := nm.pages[p] != nil && nm.pages[p].inPool
				if inC != inN {
					t.Fatalf("seq %d step %d InPool(%d): cb=%v na=%v", seq, st, p, inC, inN)
				}
				uC, _ := cb.Used(p)
				var uN int64
				if pg := nm.pages[p]; pg != nil {
					uN = pg.used
				}
				if uC != uN {
					t.Fatalf("seq %d step %d Used(%d): cb=%d na=%d", seq, st, p, uC, uN)
				}
				bC, _ := cb.BufBytes(p)
				var bN int64
				if pg := nm.pages[p]; pg != nil {
					bN = pg.buf
				}
				if bC != bN || cb.GlobalBufBytes() != nm.global {
					t.Fatalf("seq %d step %d buf page %d: cb=(%d,%d) na=(%d,%d)",
						seq, st, p, bC, cb.GlobalBufBytes(), bN, nm.global)
				}
			}
			if err := cb.CheckInvariants(); err != nil {
				t.Fatalf("seq %d step %d invariants: %v", seq, st, err)
			}
		}

		for i, c := range calls {
			out := "ok"
			if c.err != nil {
				out = c.err.Error()
			}
			dec := ""
			switch c.d {
			case DecisionAppliedDirect:
				dec = "direct"
			case DecisionBuffered:
				dec = "buffered"
			case DecisionForceMerged:
				dec = "force-merged"
			}
			fmt.Fprintf(&logBuf, "  %2d %-32s -> %-12s %-24s %s\n", i, c.text, dec, out, c.reason)
		}
	}

	t.Log("\n" + logBuf.String())
}
