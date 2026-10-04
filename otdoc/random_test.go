package otdoc

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
	"unicode/utf8"
)

var randAlphabet = []rune("abXYé界🙂")

func randRunes(rng *rand.Rand, n int) []rune {
	out := make([]rune, n)
	for i := range out {
		out[i] = randAlphabet[rng.Intn(len(randAlphabet))]
	}
	return out
}

func randInsert(rng *rand.Rand) string {
	return string(randRunes(rng, 1+rng.Intn(3)))
}

// randOp builds a random (not necessarily canonical) operation whose base
// length is exactly baseLen.
func randOp(rng *rand.Rand, baseLen int) Operation {
	if baseLen == 0 {
		return op(Insert(randInsert(rng)))
	}
	var o Operation
	pos := 0
	for pos < baseLen {
		switch rng.Intn(4) {
		case 0, 1:
			n := 1 + rng.Intn(baseLen-pos)
			o = append(o, Retain(n))
			pos += n
		case 2:
			n := 1 + rng.Intn(baseLen-pos)
			o = append(o, Delete(n))
			pos += n
		case 3:
			o = append(o, Insert(randInsert(rng)))
		}
	}
	if rng.Intn(3) == 0 {
		o = append(o, Insert(randInsert(rng)))
	}
	return o
}

// oracleSite mirrors the server-side per-site dedup state.
type oracleSite struct {
	lastSeq int
	res     Result
	hasRes  bool
}

// oracle is an independent, deliberately naive model of the Document. It
// keeps every revision of the document and transforms operations by mapping
// each character gap of the base revision, one stored operation at a time,
// into the current revision.
type oracle struct {
	maxLen int
	floor  int
	revs   [][]rune    // revs[i] is the document at revision floor+i
	hist   []Operation // hist[i] transforms revision floor+i into floor+i+1
	sites  map[string]*oracleSite
}

func newOracle(maxLen int) *oracle {
	return &oracle{
		maxLen: maxLen,
		revs:   [][]rune{{}},
		sites:  make(map[string]*oracleSite),
	}
}

func (o *oracle) rev() int { return o.floor + len(o.revs) - 1 }

func (o *oracle) doc() []rune { return o.revs[len(o.revs)-1] }

func (o *oracle) compact(newFloor int) error {
	if newFloor < o.floor || newFloor > o.rev() {
		return ErrBadFloor
	}
	drop := newFloor - o.floor
	o.revs = append([][]rune(nil), o.revs[drop:]...)
	o.hist = append([]Operation(nil), o.hist[drop:]...)
	o.floor = newFloor
	return nil
}

// mapGap maps a character gap x of the document before h to the corresponding
// gap after h. h must be canonical. Gaps at an insert point move right of the
// inserted text (the stored operation is ordered left); gaps inside a deleted
// run collapse to the deletion point.
func mapGap(h Operation, x int) int {
	p, out := 0, 0
	for _, comp := range h {
		switch comp.Kind {
		case OpRetain:
			if x < p+comp.N {
				return out + (x - p)
			}
			p += comp.N
			out += comp.N
		case OpInsert:
			out += utf8.RuneCountInString(comp.S)
		case OpDelete:
			if x <= p+comp.N {
				return out
			}
			p += comp.N
		}
	}
	return out + (x - p)
}

// naiveTransform transforms c (based on revision from) to the current
// revision by mapping every character gap of the base document through each
// stored operation, then rebuilding an operation gap by gap.
func (o *oracle) naiveTransform(c Operation, from int) Operation {
	base := o.revs[from-o.floor]
	n := len(base)
	// Track every base character (alive flag + current position) and every
	// base character gap (current position, for placing inserts) as each
	// stored operation is applied one at a time.
	alive := make([]bool, n)
	charPos := make([]int, n)
	gapPos := make([]int, n+1)
	for i := 0; i < n; i++ {
		alive[i] = true
		charPos[i] = i
	}
	for g := 0; g <= n; g++ {
		gapPos[g] = g
	}
	for k := from; k < o.rev(); k++ {
		h := o.hist[k-o.floor]
		deleted := map[int]bool{}
		p := 0
		for _, comp := range h {
			switch comp.Kind {
			case OpRetain:
				p += comp.N
			case OpDelete:
				for x := p; x < p+comp.N; x++ {
					deleted[x] = true
				}
				p += comp.N
			}
		}
		for i := 0; i < n; i++ {
			if !alive[i] {
				continue
			}
			if deleted[charPos[i]] {
				alive[i] = false
				continue
			}
			charPos[i] = mapGap(h, charPos[i])
		}
		for g := 0; g <= n; g++ {
			gapPos[g] = mapGap(h, gapPos[g])
		}
	}

	inserts := map[int][]string{}
	deletes := map[int]bool{}
	pos := 0
	for _, comp := range c {
		switch comp.Kind {
		case OpInsert:
			g := gapPos[pos]
			inserts[g] = append(inserts[g], comp.S)
		case OpRetain:
			pos += comp.N
		case OpDelete:
			for i := 0; i < comp.N; i++ {
				// Delete only characters that survived; characters already
				// deleted by stored operations are removed only once.
				if alive[pos+i] {
					deletes[charPos[pos+i]] = true
				}
			}
			pos += comp.N
		}
	}

	var out Operation
	cur := o.doc()
	for g := 0; g <= len(cur); g++ {
		for _, s := range inserts[g] {
			out = append(out, Insert(s))
		}
		if g < len(cur) {
			if deletes[g] {
				out = append(out, Delete(1))
			} else {
				out = append(out, Retain(1))
			}
		}
	}
	return Normalize(out)
}

// submit mirrors the specified server semantics, including the rejection
// priority, using only the naive position-mapping model.
func (o *oracle) submit(site string, seq, baseRev int, c Operation) (Result, error) {
	if site == "" || seq < 1 || baseRev < 0 {
		return Result{}, ErrInvalid
	}
	if err := validate(c); err != nil {
		return Result{}, err
	}
	norm := Normalize(c)

	st := o.sites[site]
	if st == nil {
		st = &oracleSite{}
		o.sites[site] = st
	}
	switch {
	case seq == st.lastSeq && st.hasRes:
		return st.res, nil
	case seq < st.lastSeq:
		return Result{}, ErrStaleSeq
	case seq > st.lastSeq+1:
		return Result{}, ErrSeqGap
	}

	if baseRev > o.rev() {
		return Result{}, ErrFuture
	}
	if baseRev < o.floor {
		return Result{}, ErrTooOld
	}
	if baseLen(norm) != len(o.revs[baseRev-o.floor]) {
		return Result{}, ErrLength
	}

	transformed := o.naiveTransform(norm, baseRev)
	newLen := len(o.doc()) + insertRunes(transformed) - deleteRunes(transformed)
	if newLen > o.maxLen {
		return Result{}, ErrTooLarge
	}

	res := Result{Rev: o.rev(), Applied: false, Op: transformed}
	if !isNoop(transformed) {
		o.hist = append(o.hist, transformed)
		o.revs = append(o.revs, apply(o.doc(), transformed))
		res.Rev = o.rev()
		res.Applied = true
	}
	st.lastSeq = seq
	st.res = res
	st.hasRes = true
	return res, nil
}

// TestRandomAgainstNaiveOracle replays 2000 random multi-site submit
// sequences against both the Document and the naive position-mapping oracle,
// requiring identical results, documents, revisions and histories.
func TestRandomAgainstNaiveOracle(t *testing.T) {
	for trial := 0; trial < 2000; trial++ {
		t.Run(fmt.Sprintf("trial-%d", trial), func(t *testing.T) {
			rng := rand.New(rand.NewSource(20261003 + int64(trial)))
			runRandomTrial(t, rng, trial)
		})
	}
}

func runRandomTrial(t *testing.T, rng *rand.Rand, trial int) {
	maxLen := 1 + rng.Intn(20)
	d, err := New(maxLen)
	if err != nil {
		t.Fatalf("New(%d): %v", maxLen, err)
	}
	o := newOracle(maxLen)
	sites := []string{"alice", "bob", "carol"}
	nextSeq := map[string]int{}

	steps := 1 + rng.Intn(25)
	for step := 0; step < steps; step++ {
		if rng.Intn(12) == 0 {
			// Occasionally compact, sometimes with an invalid floor.
			newFloor := o.floor - 1 + rng.Intn(o.rev()-o.floor+3)
			wantErr := o.compact(newFloor)
			gotErr := d.Compact(newFloor)
			if !errors.Is(gotErr, wantErr) {
				t.Fatalf("Compact(%d): got %v, oracle %v", newFloor, gotErr, wantErr)
			}
			t.Logf("step=%d compact(%d) => err=%v (floor=%d rev=%d)", step, newFloor, gotErr, o.floor, o.rev())
			continue
		}

		site := sites[rng.Intn(len(sites))]
		seq := nextSeq[site] + 1
		switch r := rng.Intn(10); {
		case r == 0 && seq > 1:
			seq-- // duplicate of the last accepted submit
		case r == 1:
			seq++ // gap
		}
		baseRev := o.floor
		if o.rev() > o.floor {
			baseRev = o.floor + rng.Intn(o.rev()-o.floor+1)
		}
		var c Operation
		if rng.Intn(20) == 0 {
			baseRev = o.rev() + 1 // future base revision
			c = randOp(rng, 1+rng.Intn(4))
		} else {
			base := len(o.revs[baseRev-o.floor])
			c = randOp(rng, base)
			if rng.Intn(20) == 0 {
				c = append(c, Retain(1)) // corrupt the base length
			}
		}

		wantRes, wantErr := o.submit(site, seq, baseRev, c)
		gotRes, gotErr := d.Submit(site, seq, baseRev, c)
		t.Logf("step=%d submit(site=%q seq=%d baseRev=%d op=%v) => res=%+v err=%v | oracle res=%+v err=%v (naive position-map; doc=%q rev=%d floor=%d)",
			step, site, seq, baseRev, c, gotRes, gotErr, wantRes, wantErr, o.doc(), o.rev(), o.floor)

		if !errors.Is(gotErr, wantErr) {
			t.Fatalf("err mismatch: got %v, oracle %v", gotErr, wantErr)
		}
		if gotErr == nil {
			if !reflect.DeepEqual(gotRes, wantRes) {
				t.Fatalf("result mismatch:\n got %+v\noracle %+v", gotRes, wantRes)
			}
			if seq == nextSeq[site]+1 {
				nextSeq[site] = seq
			}
		}
		if got := d.Doc(); got != string(o.doc()) {
			t.Fatalf("doc mismatch: got %q, oracle %q", got, o.doc())
		}
		if d.Rev() != o.rev() || d.Floor() != o.floor {
			t.Fatalf("rev/floor mismatch: got %d/%d, oracle %d/%d", d.Rev(), d.Floor(), o.rev(), o.floor)
		}
	}

	// Final state must match completely, including the stored history.
	h, err := d.History(d.Floor(), d.Rev())
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(h) != len(o.hist) {
		t.Fatalf("history length mismatch: got %d, oracle %d", len(h), len(o.hist))
	}
	for i := range h {
		if !reflect.DeepEqual(h[i], o.hist[i]) {
			t.Fatalf("history[%d] mismatch:\n got %v\noracle %v", i, h[i], o.hist[i])
		}
	}
	if d.Len() != len(o.doc()) {
		t.Fatalf("len mismatch: got %d, oracle %d", d.Len(), len(o.doc()))
	}
}

// TestReplayDeterministic feeds identical submit sequences to two fresh
// documents and requires identical per-submit results, histories and docs.
func TestReplayDeterministic(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	for trial := 0; trial < 200; trial++ {
		maxLen := 1 + rng.Intn(30)
		type cmd struct {
			site         string
			seq, baseRev int
			o            Operation
		}
		var cmds []cmd
		gen, _ := New(maxLen)
		nextSeq := map[string]int{}
		sites := []string{"a", "b", "c"}
		for i, n := 0, 1+rng.Intn(20); i < n; i++ {
			site := sites[rng.Intn(len(sites))]
			seq := nextSeq[site] + 1
			baseRev := gen.Rev()
			c := randOp(rng, gen.Len())
			cmds = append(cmds, cmd{site, seq, baseRev, c})
			if _, err := gen.Submit(site, seq, baseRev, c); err == nil {
				nextSeq[site] = seq
			}
		}

		run := func() ([]Result, []error, string, []Operation) {
			d, _ := New(maxLen)
			ress := make([]Result, 0, len(cmds))
			errs := make([]error, 0, len(cmds))
			for _, cm := range cmds {
				res, err := d.Submit(cm.site, cm.seq, cm.baseRev, cm.o)
				ress = append(ress, res)
				errs = append(errs, err)
			}
			h, _ := d.History(0, d.Rev())
			return ress, errs, d.Doc(), h
		}
		res1, err1, doc1, h1 := run()
		res2, err2, doc2, h2 := run()
		if !reflect.DeepEqual(res1, res2) || !reflect.DeepEqual(err1, err2) ||
			doc1 != doc2 || !reflect.DeepEqual(h1, h2) {
			t.Fatalf("trial %d: replay diverged", trial)
		}
	}
}

// TestConcurrentSubmit hammers the document from many goroutines. The final
// state must equal some serial execution: every accepted submit created
// exactly one revision, and replaying the stored history from the empty
// document reproduces the final content.
func TestConcurrentSubmit(t *testing.T) {
	d := mustNew(t, 1_000_000)
	const numSites = 8
	const perSite = 50
	var wg sync.WaitGroup
	lastRes := make([]Result, numSites)
	for s := 0; s < numSites; s++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			site := fmt.Sprintf("site-%d", idx)
			for seq := 1; seq <= perSite; seq++ {
				for {
					rev := d.Rev()
					l := d.Len()
					o := Operation{Insert("x")}
					if l > 0 {
						o = Operation{Retain(l), Insert("x")}
					}
					res, err := d.Submit(site, seq, rev, o)
					if err == nil {
						if !res.Applied {
							t.Errorf("%s seq=%d: insert must apply", site, seq)
						}
						lastRes[idx] = res
						break
					}
					if errors.Is(err, ErrLength) {
						continue // raced with another submit; retry
					}
					t.Errorf("%s seq=%d: unexpected error %v", site, seq, err)
					return
				}
			}
		}(s)
	}
	wg.Wait()

	if d.Rev() != numSites*perSite {
		t.Fatalf("rev = %d, want %d", d.Rev(), numSites*perSite)
	}
	if d.Len() != numSites*perSite {
		t.Fatalf("len = %d, want %d", d.Len(), numSites*perSite)
	}
	h, err := d.History(0, d.Rev())
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	doc := []rune{}
	for _, ho := range h {
		doc = apply(doc, ho)
	}
	if string(doc) != d.Doc() {
		t.Fatalf("history replay %q != doc %q", doc, d.Doc())
	}
	// The recorded per-site results are stable: re-submitting the last seq
	// of each site returns them verbatim.
	for idx := 0; idx < numSites; idx++ {
		site := fmt.Sprintf("site-%d", idx)
		res, err := d.Submit(site, perSite, 0, Operation{Insert("ignored")})
		if err != nil {
			t.Fatalf("%s duplicate: %v", site, err)
		}
		if !reflect.DeepEqual(res, lastRes[idx]) {
			t.Fatalf("%s duplicate = %+v, want recorded %+v", site, res, lastRes[idx])
		}
	}
	if d.Rev() != numSites*perSite {
		t.Fatalf("duplicates changed rev: %d", d.Rev())
	}
}
