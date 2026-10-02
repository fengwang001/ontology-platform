package ontology

import (
	"fmt"
	"math/rand"
	"os"
	"strings"
	"sync"
	"testing"
)

// refEntry is one page in the naive reference simulation.
type refEntry struct {
	page  int
	first int64
	hasF  bool
	pin   int
}

// refPool is a deliberately slow, literal transcription of the specification:
// two slices (young then old, head to tail) with explicit loops.
type refPool struct {
	n, rho, tol int
	t           int64
	young, old  []refEntry
	now         int64
	evictions   []int
}

func newRef(n, rho, tol int, t int64) *refPool {
	return &refPool{n: n, rho: rho, tol: tol, t: t}
}

func (r *refPool) len() int { return len(r.young) + len(r.old) }

func (r *refPool) find(page int) (seg segment, idx int) {
	for i, e := range r.young {
		if e.page == page {
			return segYoung, i
		}
	}
	for i, e := range r.old {
		if e.page == page {
			return segOld, i
		}
	}
	return 255, -1
}

// rebalance: literal spec loops.
func (r *refPool) rebalance() {
	tgt := (r.len() * r.rho) / 100
	for len(r.old) < tgt && len(r.young) > 0 {
		e := r.young[len(r.young)-1] // Y tail
		r.young = r.young[:len(r.young)-1]
		r.old = append([]refEntry{e}, r.old...) // to O head
	}
	for len(r.old) > tgt+r.tol {
		e := r.old[0] // O head
		r.old = r.old[1:]
		r.young = append(r.young, e) // to Y tail
	}
}

func (r *refPool) victim() (seg segment, idx int) {
	for i := len(r.old) - 1; i >= 0; i-- {
		if r.old[i].pin == 0 {
			return segOld, i
		}
	}
	for i := len(r.young) - 1; i >= 0; i-- {
		if r.young[i].pin == 0 {
			return segYoung, i
		}
	}
	return 255, -1
}

type refResult struct {
	hit, evicted bool
	page         int
}

// miss: evict if full, insert at O head with first optionally set, rebalance.
func (r *refPool) miss(page int, now int64, setFirst bool) (refResult, error) {
	res := refResult{}
	if r.len() >= r.n {
		seg, idx := r.victim()
		if idx < 0 {
			return refResult{}, ErrAllPinned
		}
		var v refEntry
		if seg == segOld {
			v = r.old[idx]
			r.old = append(r.old[:idx], r.old[idx+1:]...)
		} else {
			v = r.young[idx]
			r.young = append(r.young[:idx], r.young[idx+1:]...)
		}
		res.evicted, res.page = true, v.page
		r.evictions = append(r.evictions, v.page)
	}
	e := refEntry{page: page, first: now, hasF: setFirst}
	r.old = append([]refEntry{e}, r.old...)
	r.rebalance()
	return res, nil
}

func (r *refPool) access(page int, now int64) (refResult, error) {
	if page < 0 || page > maxPage || now < 0 || now > maxNow {
		return refResult{}, ErrInvalidArgument
	}
	if now < r.now {
		return refResult{}, ErrClockSkew
	}
	seg, idx := r.find(page)
	if idx < 0 {
		if r.len() >= r.n {
			if s, i := r.victim(); i < 0 {
				_ = s
				return refResult{}, ErrAllPinned
			}
		}
		r.now = now
		return r.miss(page, now, true)
	}
	r.now = now
	if seg == segYoung {
		e := r.young[idx]
		r.young = append(r.young[:idx], r.young[idx+1:]...)
		r.young = append([]refEntry{e}, r.young...)
		return refResult{hit: true}, nil
	}
	e := &r.old[idx]
	if !e.hasF {
		e.hasF, e.first = true, now
		return refResult{hit: true}, nil
	}
	if now-e.first >= r.t {
		en := *e
		r.old = append(r.old[:idx], r.old[idx+1:]...)
		r.young = append([]refEntry{en}, r.young...)
		r.rebalance()
	}
	return refResult{hit: true}, nil
}

func (r *refPool) prefetch(page int, now int64) (refResult, error) {
	if page < 0 || page > maxPage || now < 0 || now > maxNow {
		return refResult{}, ErrInvalidArgument
	}
	if now < r.now {
		return refResult{}, ErrClockSkew
	}
	if _, idx := r.find(page); idx >= 0 {
		r.now = now
		return refResult{hit: true}, nil
	}
	if r.len() >= r.n {
		if s, i := r.victim(); i < 0 {
			_ = s
			return refResult{}, ErrAllPinned
		}
	}
	r.now = now
	return r.miss(page, now, false)
}

func (r *refPool) pin(page int, delta int) error {
	if page < 0 || page > maxPage {
		return ErrInvalidArgument
	}
	seg, idx := r.find(page)
	if idx < 0 {
		return ErrNotFound
	}
	if seg == segYoung {
		if delta < 0 && r.young[idx].pin == 0 {
			return ErrPinUnderflow
		}
		r.young[idx].pin += delta
	} else {
		if delta < 0 && r.old[idx].pin == 0 {
			return ErrPinUnderflow
		}
		r.old[idx].pin += delta
	}
	return nil
}

func (r *refPool) snapshot() ([]int, []int, map[int]refEntry) {
	y := make([]int, len(r.young))
	o := make([]int, len(r.old))
	state := make(map[int]refEntry, r.len())
	for i, e := range r.young {
		y[i] = e.page
		state[e.page] = e
	}
	for i, e := range r.old {
		o[i] = e.page
		state[e.page] = e
	}
	return y, o, state
}

type opKind uint8

const (
	opAccess opKind = iota
	opPrefetch
	opPin
	opUnpin
)

type fuzzOp struct {
	kind opKind
	page int
	now  int64
}

// genOps builds one random operation stream. Small pages (relative to N) make
// hits and evictions frequent; occasional out-of-range pages/now exercise
// validation; non-monotonic now exercises clock-skew rejection.
func genOps(rng *rand.Rand, n, count int) []fuzzOp {
	ops := make([]fuzzOp, 0, count)
	var lastNow int64
	pageCount := n * 2
	if pageCount < 8 {
		pageCount = 8
	}
	for i := 0; i < count; i++ {
		page := rng.Intn(pageCount)
		if rng.Intn(20) == 0 {
			page = maxPage + 1 + rng.Intn(3) // invalid pages sometimes
		}
		var now int64
		switch {
		case rng.Intn(8) == 0:
			now = lastNow - int64(rng.Intn(5)) - 1 // deliberate skew
			if now < 0 {
				now = 0
			}
		case rng.Intn(30) == 0:
			now = maxNow + 1
		default:
			now = lastNow + int64(rng.Intn(4))
			if now > maxNow {
				now = maxNow
			}
		}
		var kind opKind
		switch rng.Intn(10) {
		case 0, 1, 2, 3, 4:
			kind = opAccess
		case 5, 6:
			kind = opPrefetch
		case 7, 8:
			kind = opPin
		default:
			kind = opUnpin
		}
		ops = append(ops, fuzzOp{kind: kind, page: page, now: now})
		if now <= maxNow && (kind == opAccess || kind == opPrefetch) && now >= lastNow && page <= maxPage {
			// Only accepted clock-bearing ops advance the reference clock;
			// acceptance itself depends on pool state, so keep the loose bound
			// here and let decideBasis do the precise work.
			lastNow = now
		}
	}
	return ops
}

func errName(err error) string {
	switch {
	case err == nil:
		return "-"
	case errorIs(err, ErrInvalidArgument):
		return "ErrInvalidArgument"
	case errorIs(err, ErrClockSkew):
		return "ErrClockSkew"
	case errorIs(err, ErrNotFound):
		return "ErrNotFound"
	case errorIs(err, ErrPinUnderflow):
		return "ErrPinUnderflow"
	case errorIs(err, ErrAllPinned):
		return "ErrAllPinned"
	default:
		return err.Error()
	}
}

func errorIs(err, target error) bool { return err == target }

// TestDifferentialNaive runs 2000 random operation sequences against the
// Pool and the naive refPool, printing every input, output and the decision
// basis into lru_difftest.log (and into the test log with -v).
func TestDifferentialNaive(t *testing.T) {
	logf, err := os.Create("lru_difftest.log")
	if err != nil {
		t.Fatal(err)
	}
	defer logf.Close()

	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)*1_000_003 + 77))
		n := 4 + rng.Intn(9)    // 4..12
		rho := 5 + rng.Intn(91) // 5..95
		tol := rng.Intn(n + 1)  // 0..n
		var T int64
		switch rng.Intn(3) {
		case 0:
			T = 0
		case 1:
			T = rng.Int63n(30)
		default:
			T = rng.Int63n(100)
		}
		ops := genOps(rng, n, 30+rng.Intn(51))

		var trace strings.Builder
		fmt.Fprintf(&trace, "===== seq %d: N=%d rho=%d tol=%d T=%d ops=%d =====\n",
			seq, n, rho, tol, T, len(ops))

		p, perr := New(n, rho, tol, T)
		r := newRef(n, rho, tol, T)
		if perr != nil {
			t.Fatalf("seq %d valid params rejected: %v", seq, perr)
		}
		var reads int

		for step, op := range ops {
			basis := decideBasis(r, op)
			var got AccessResult
			var gerr error
			switch op.kind {
			case opAccess:
				got, gerr = p.Access(op.page, op.now)
			case opPrefetch:
				got, gerr = p.Prefetch(op.page, op.now)
			case opPin:
				gerr = p.Pin(op.page)
			case opUnpin:
				gerr = p.Unpin(op.page)
			}
			var want refResult
			var rerr error
			switch op.kind {
			case opAccess:
				want, rerr = r.access(op.page, op.now)
			case opPrefetch:
				want, rerr = r.prefetch(op.page, op.now)
			case opPin:
				rerr = r.pin(op.page, +1)
			case opUnpin:
				rerr = r.pin(op.page, -1)
			}
			if gerr == nil && op.kind <= opPrefetch && !got.Hit {
				reads++
			}
			fmt.Fprintf(&trace, "step %d %s page=%d now=%d -> got{hit=%v ev=%v ep=%d err=%s} want{hit=%v ev=%v ep=%d err=%s} | %s\n",
				step, kindName(op.kind), op.page, op.now,
				got.Hit, got.Evicted, got.Page, errName(gerr),
				want.hit, want.evicted, want.page, errName(rerr), basis)
			t.Logf("seq %d step %d %s p=%d now=%d => %s basis=%s",
				seq, step, kindName(op.kind), op.page, op.now, errName(gerr), basis)

			if errName(gerr) != errName(rerr) {
				dumpAndFail(t, logf, trace, seq, "error mismatch at step %d", step)
			}
			if gerr == nil && op.kind <= opPrefetch {
				if got.Hit != want.hit || got.Evicted != want.evicted ||
					(got.Evicted && got.Page != want.page) {
					dumpAndFail(t, logf, trace, seq, "result mismatch at step %d", step)
				}
			}
			compareState(t, logf, &trace, seq, step, p, r)
			checkInvariants(t, logf, &trace, seq, step, p, reads)
		}

		// Deterministic replay of the identical op sequence.
		p2, _ := New(n, rho, tol, T)
		for _, op := range ops {
			switch op.kind {
			case opAccess:
				p2.Access(op.page, op.now)
			case opPrefetch:
				p2.Prefetch(op.page, op.now)
			case opPin:
				p2.Pin(op.page)
			case opUnpin:
				p2.Unpin(op.page)
			}
		}
		y1, o1 := p.Lists()
		y2, o2 := p2.Lists()
		if !eq(y1, y2) || !eq(o1, o2) || !eq(p.Evictions(), p2.Evictions()) {
			t.Fatalf("seq %d replay differs:\nrun1 Y=%v O=%v ev=%v\nrun2 Y=%v O=%v ev=%v",
				seq, y1, o1, p.Evictions(), y2, o2, p2.Evictions())
		}
		if _, err := logf.WriteString(trace.String()); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("differential fuzz: %d sequences passed; trace in ontology/lru_difftest.log", sequences)
}

// TestConcurrentSmoke hammers one pool from many goroutines; correctness of
// interleaving is covered by -race plus post-run invariants.
func TestConcurrentSmoke(t *testing.T) {
	p := mustNew(t, 64, 60, 2, 5)
	const goroutines = 8
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			var now int64
			for i := 0; i < 2000; i++ {
				page := rng.Intn(200)
				now += int64(rng.Intn(3))
				switch rng.Intn(6) {
				case 0:
					p.Prefetch(page, now)
				case 1:
					p.Pin(page)
				case 2:
					p.Unpin(page)
				default:
					p.Access(page, now)
				}
				if i%16 == 0 {
					p.Lists()
					p.Evictions()
					p.Len()
				}
			}
		}(int64(g)*99 + 1)
	}
	wg.Wait()
	y, o := p.Lists()
	total := len(y) + len(o)
	if total != p.Len() || total > 64 {
		t.Fatalf("post-concurrency size: %d %d Len=%d", len(y), len(o), p.Len())
	}
	tgt := (total * 60) / 100
	if len(o) < tgt || len(o) > tgt+2 {
		t.Fatalf("post-concurrency balance: |O|=%d tgt=%d", len(o), tgt)
	}
}

func kindName(k opKind) string {
	return [...]string{"Access", "Prefetch", "Pin", "Unpin"}[k]
}

// decideBasis inspects the pre-op reference state to explain the decision.
func decideBasis(r *refPool, op fuzzOp) string {
	if op.kind == opPin || op.kind == opUnpin {
		if op.page < 0 || op.page > maxPage {
			return "bad page"
		}
		_, idx := r.find(op.page)
		if idx < 0 {
			return "page absent -> ErrNotFound"
		}
		if op.kind == opUnpin {
			seg, i := r.find(op.page)
			var e refEntry
			if seg == segYoung {
				e = r.young[i]
			} else {
				e = r.old[i]
			}
			if e.pin == 0 {
				return "pin already 0 -> ErrPinUnderflow"
			}
		}
		return "adjust pin"
	}
	if op.page < 0 || op.page > maxPage || op.now < 0 || op.now > maxNow {
		return "bad argument"
	}
	if op.now < r.now {
		return fmt.Sprintf("now %d < max %d -> ErrClockSkew", op.now, r.now)
	}
	seg, idx := r.find(op.page)
	if idx < 0 {
		if r.len() >= r.n {
			vs, vi := r.victim()
			if vi < 0 {
				return "full and all pinned -> ErrAllPinned"
			}
			var v refEntry
			if vs == segOld {
				v = r.old[vi]
			} else {
				v = r.young[vi]
			}
			return fmt.Sprintf("fault: evict victim %d from %s tail-scan, insert O head(first=%v), rebalance",
				v.page, segName(vs), op.kind == opAccess)
		}
		return fmt.Sprintf("fault: insert O head(first=%v), rebalance", op.kind == opAccess)
	}
	if seg == segYoung {
		return "hit in Y -> move to Y head"
	}
	e := r.old[idx]
	if !e.hasF {
		return "hit in O, first empty -> record first only"
	}
	if d := op.now - e.first; d >= r.t {
		return fmt.Sprintf("hit in O, %d-%d=%d >= T=%d -> promote to Y head, rebalance",
			op.now, e.first, d, r.t)
	} else {
		return fmt.Sprintf("hit in O, %d-%d=%d < T=%d -> stay", op.now, e.first, d, r.t)
	}
}

func segName(s segment) string {
	if s == segOld {
		return "O"
	}
	return "Y"
}

func dumpAndFail(t *testing.T, logf *os.File, trace strings.Builder, seq int, format string, args ...any) {
	t.Helper()
	logf.WriteString(trace.String())
	t.Fatalf("seq %d: %s\n%s", seq, fmt.Sprintf(format, args...), trace.String())
}

func compareState(t *testing.T, logf *os.File, trace *strings.Builder, seq, step int, p *Pool, r *refPool) {
	t.Helper()
	ry, ro, rstate := r.snapshot()
	py, po := p.Lists()
	if !eq(py, ry) || !eq(po, ro) {
		dumpAndFail(t, logf, *trace, seq, "lists mismatch at step %d: got Y=%v O=%v want Y=%v O=%v",
			step, py, po, ry, ro)
	}
	if p.now != r.now {
		dumpAndFail(t, logf, *trace, seq, "clock mismatch step %d: %d != %d", step, p.now, r.now)
	}
	if !eq(p.evictions, r.evictions) {
		dumpAndFail(t, logf, *trace, seq, "eviction log mismatch step %d: %v != %v",
			step, p.evictions, r.evictions)
	}
	for page, re := range rstate {
		pe, ok := p.index[page]
		if !ok {
			dumpAndFail(t, logf, *trace, seq, "page %d missing at step %d", page, step)
		}
		if pe.hasF != re.hasF || (pe.hasF && pe.first != re.first) || pe.pin != re.pin {
			dumpAndFail(t, logf, *trace, seq,
				"page %d state mismatch step %d: got(first=%v,%d,pin=%d) want(first=%v,%d,pin=%d)",
				page, step, pe.hasF, pe.first, pe.pin, re.hasF, re.first, re.pin)
		}
	}
	if len(p.index) != len(rstate) {
		dumpAndFail(t, logf, *trace, seq, "index size mismatch step %d", step)
	}
}

func checkInvariants(t *testing.T, logf *os.File, trace *strings.Builder, seq, step int, p *Pool, reads int) {
	t.Helper()
	total := p.young.Len() + p.old.Len()
	if total > p.n || total != len(p.index) {
		dumpAndFail(t, logf, *trace, seq, "size invariant step %d: total=%d index=%d N=%d",
			step, total, len(p.index), p.n)
	}
	seen := make(map[int]bool, total)
	for el := p.young.Front(); el != nil; el = el.Next() {
		e := el.Value.(*entry)
		if e.seg != segYoung || seen[e.page] {
			dumpAndFail(t, logf, *trace, seq, "young integrity step %d page %d", step, e.page)
		}
		seen[e.page] = true
	}
	for el := p.old.Front(); el != nil; el = el.Next() {
		e := el.Value.(*entry)
		if e.seg != segOld || seen[e.page] {
			dumpAndFail(t, logf, *trace, seq, "old integrity step %d page %d", step, e.page)
		}
		seen[e.page] = true
	}
	tgt := (total * p.rho) / 100
	ol := p.old.Len()
	if ol < tgt || ol > tgt+p.tol {
		dumpAndFail(t, logf, *trace, seq,
			"balance invariant step %d: |O|=%d not in [%d,%d]", step, ol, tgt, tgt+p.tol)
	}
	if reads != len(p.evictions)+total {
		dumpAndFail(t, logf, *trace, seq,
			"read accounting step %d: reads=%d evictions=%d held=%d",
			step, reads, len(p.evictions), total)
	}
}
