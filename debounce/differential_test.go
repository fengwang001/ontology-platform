package debounce

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// naiveDebouncer 是按规格逐条直译的朴素参考实现，用于对拍。
type naiveDebouncer struct {
	q, w    int64
	cap     int
	maxNow  int64
	hasNow  bool
	pending map[string]Entry
}

func newNaive(q, w int64, cap int) (*naiveDebouncer, error) {
	if q < 1 {
		return nil, ErrQuietTooSmall
	}
	if w < q {
		return nil, ErrMaxWaitTooSmall
	}
	if cap < 1 {
		return nil, ErrCapTooSmall
	}
	return &naiveDebouncer{q: q, w: w, cap: cap, pending: map[string]Entry{}}, nil
}

func naiveValidPath(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.HasSuffix(p, "/") {
		return false
	}
	return !strings.Contains(p, "//")
}

func (n *naiveDebouncer) add(now int64, ev Event) error {
	if ev.Kind != Create && ev.Kind != Modify && ev.Kind != Delete && ev.Kind != DeleteDir {
		return ErrUnknownKind
	}
	if !naiveValidPath(ev.Path) {
		return ErrInvalidPath
	}
	if n.hasNow && now < n.maxNow {
		return ErrClockBackwards
	}
	_, has := n.pending[ev.Path]
	if !has && len(n.pending) >= n.cap {
		return ErrCapFull
	}
	if !n.hasNow || now > n.maxNow {
		n.maxNow, n.hasNow = now, true
	}
	if ev.Kind == DeleteDir {
		prefix := ev.Path + "/"
		for p := range n.pending {
			if strings.HasPrefix(p, prefix) {
				delete(n.pending, p)
			}
		}
	}
	e, has := n.pending[ev.Path]
	if !has {
		var net NetKind
		switch ev.Kind {
		case Create:
			net = NetCreate
		case Modify:
			net = NetModify
		default:
			net = NetDelete
		}
		n.pending[ev.Path] = Entry{Path: ev.Path, Net: net, First: now, Last: now}
		return nil
	}
	switch {
	case e.Net == NetCreate && (ev.Kind == Delete || ev.Kind == DeleteDir):
		delete(n.pending, ev.Path)
		return nil
	case e.Net == NetModify && (ev.Kind == Delete || ev.Kind == DeleteDir):
		e.Net = NetDelete
	case e.Net == NetDelete && ev.Kind == Create:
		e.Net = NetModify
	}
	e.Last = now
	n.pending[ev.Path] = e
	return nil
}

func (n *naiveDebouncer) flush(now int64) ([]Entry, error) {
	if n.hasNow && now < n.maxNow {
		return nil, ErrClockBackwards
	}
	if !n.hasNow || now > n.maxNow {
		n.maxNow, n.hasNow = now, true
	}
	var out []Entry
	for p, e := range n.pending {
		if now-e.Last >= n.q || now-e.First >= n.w {
			out = append(out, e)
			delete(n.pending, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func (n *naiveDebouncer) nextDue() (int64, bool) {
	if len(n.pending) == 0 {
		return 0, false
	}
	first := true
	var best int64
	for _, e := range n.pending {
		due := e.Last + n.q
		if t := e.First + n.w; t < due {
			due = t
		}
		if first || due < best {
			best, first = due, false
		}
	}
	return best, true
}

// op 是一步随机操作。
type op struct {
	kind string // "add" / "flush" / "nextdue"
	now  int64
	ev   Event
}

func (o op) String() string {
	if o.kind == "add" {
		return fmt.Sprintf("Add(%d, %v %q)", o.now, o.ev.Kind, o.ev.Path)
	}
	return fmt.Sprintf("%s(%d)", o.kind, o.now)
}

var pathPool = []string{
	"a", "b", "a/b", "a/c", "ab", "ab/c", "a/b/c", "d/e/f",
	"", "/a", "a/", "a//b", "/",
}

func genSequence(r *rand.Rand) (int64, int64, int, []op) {
	q := int64(1 + r.Intn(8))
	w := q + int64(r.Intn(30))
	cap := 1 + r.Intn(6)
	n := 5 + r.Intn(25)
	ops := make([]op, 0, n)
	now := int64(0)
	for i := 0; i < n; i++ {
		now += int64(r.Intn(12))
		tnow := now
		if r.Intn(20) == 0 {
			tnow -= int64(1 + r.Intn(5)) // 制造时钟回拨
		}
		switch r.Intn(10) {
		case 0, 1:
			ops = append(ops, op{kind: "flush", now: tnow})
		case 2:
			ops = append(ops, op{kind: "nextdue", now: tnow})
		default:
			k := Kind(r.Intn(5)) // 含非法种类 4
			if r.Intn(30) == 0 {
				k = Kind(-1)
			}
			p := pathPool[r.Intn(len(pathPool))]
			ops = append(ops, op{kind: "add", now: tnow, ev: Event{Kind: k, Path: p}})
		}
	}
	return q, w, cap, ops
}

func TestDifferentialAgainstNaive(t *testing.T) {
	const sequences = 2000
	rng := rand.New(rand.NewSource(20261002))
	for seq := 0; seq < sequences; seq++ {
		seed := rng.Int63()
		r := rand.New(rand.NewSource(seed))
		q, w, cap, ops := genSequence(r)

		real, errR := New(q, w, cap)
		naive, errN := newNaive(q, w, cap)
		if (errR == nil) != (errN == nil) {
			t.Fatalf("seq %d seed %d: constructor mismatch: %v vs %v", seq, seed, errR, errN)
		}

		var log strings.Builder
		fmt.Fprintf(&log, "seq=%d seed=%d Q=%d W=%d Cap=%d\n", seq, seed, q, w, cap)
		mismatch := ""
		for i, o := range ops {
			fmt.Fprintf(&log, "  in[%d]: %s\n", i, o)
			switch o.kind {
			case "add":
				eR := real.Add(o.now, o.ev)
				eN := naive.add(o.now, o.ev)
				fmt.Fprintf(&log, "  out[%d]: err=%v | naive err=%v\n", i, eR, eN)
				if (eR == nil) != (eN == nil) || (eR != nil && eR != eN) {
					mismatch = fmt.Sprintf("op %d add: err %v vs naive %v", i, eR, eN)
				}
			case "flush":
				oR, eR := real.Flush(o.now)
				oN, eN := naive.flush(o.now)
				fmt.Fprintf(&log, "  out[%d]: entries=%v err=%v | naive entries=%v err=%v\n", i, oR, eR, oN, eN)
				if (eR == nil) != (eN == nil) || (eR != nil && eR != eN) {
					mismatch = fmt.Sprintf("op %d flush: err %v vs naive %v", i, eR, eN)
				} else if !reflect.DeepEqual(oR, oN) && !(len(oR) == 0 && len(oN) == 0) {
					mismatch = fmt.Sprintf("op %d flush: %v vs naive %v", i, oR, oN)
				}
			case "nextdue":
				dR, okR := real.NextDue()
				dN, okN := naive.nextDue()
				fmt.Fprintf(&log, "  out[%d]: due=(%d,%v) | naive due=(%d,%v)\n", i, dR, okR, dN, okN)
				if dR != dN || okR != okN {
					mismatch = fmt.Sprintf("op %d nextdue: (%d,%v) vs naive (%d,%v)", i, dR, okR, dN, okN)
				}
			}
			if mismatch != "" {
				break
			}
		}
		if mismatch != "" {
			fmt.Fprintf(&log, "  verdict: MISMATCH: %s\n", mismatch)
			t.Fatalf("differential mismatch:\n%s", log.String())
		}
		t.Logf("%s  verdict: OK (%d ops identical)", log.String(), len(ops))
	}
}
