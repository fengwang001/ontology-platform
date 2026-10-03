package ontology

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

type opKind int

const (
	opAdd opKind = iota
	opClean
	opPin
	opUnpin
	opUndelete
)

type op struct {
	kind opKind
	now  int64
	file string
	t    int64
	size int64
}

func TestRandomDifferential(t *testing.T) {
	const sequences = 2000
	const opsPerSequence = 60

	for seq := 0; seq < sequences; seq++ {
		seed := int64(1_000_000 + seq)
		rng := rand.New(rand.NewSource(seed))

		// Generate a configuration; tier steps are deliberately allowed to be
		// non-monotonic so thinning is history-dependent.
		var rules []Rule
		until := int64(0)
		for i := 0; i < 1+rng.Intn(3); i++ {
			until += int64(1 + rng.Intn(40))
			rules = append(rules, Rule{Until: until, Step: int64(1 + rng.Intn(30))})
		}
		maxCount := int64(1 + rng.Intn(6))
		maxBytes := int64(1 + rng.Intn(40))
		trashTTL := int64(1 + rng.Intn(8))

		c, err := New(rules, maxCount, maxBytes, trashTTL)
		if err != nil {
			t.Fatalf("seq %d: New: %v", seq, err)
		}
		n := newNaive(rules, maxCount, maxBytes, trashTTL)

		fileNames := []string{"a", "b"}
		var log strings.Builder
		fmt.Fprintf(&log, "seed=%d rules=%v maxCount=%d maxBytes=%d trashTTL=%d\n",
			seed, rules, maxCount, maxBytes, trashTTL)

		var clock int64
		// Track candidate timestamps per file to produce meaningful duplicate
		// and pin/unpin/undelete calls.
		known := map[string][]int64{}

		for step := 0; step < opsPerSequence; step++ {
			file := fileNames[rng.Intn(len(fileNames))]
			kind := opKind(rng.Intn(5))
			// Clock is non-decreasing; sometimes it stands still.
			clock += int64(rng.Intn(3))
			o := op{kind: kind, now: clock, file: file}
			switch kind {
			case opAdd:
				o.size = int64(1 + rng.Intn(12))
				if rng.Intn(4) == 0 && len(known[file]) > 0 {
					o.t = known[file][rng.Intn(len(known[file]))]
				} else {
					o.t = rng.Int63n(clock + 1)
					known[file] = append(known[file], o.t)
				}
			case opPin, opUnpin, opUndelete:
				if len(known[file]) == 0 {
					o.t = rng.Int63n(clock + 1)
				} else {
					o.t = known[file][rng.Intn(len(known[file]))]
				}
			}

			runAndCompare(t, seq, step, c, n, o, fileNames, &log)
		}

		// Final accounting comparison.
		for _, f := range fileNames {
			compareQueries(t, seq, c, n, f, &log)
		}
		if t.Failed() {
			return
		}
	}
}

func runAndCompare(t *testing.T, seq, step int, c *Cleaner, n *naiveCleaner, o op, files []string, log *strings.Builder) {
	t.Helper()

	switch o.kind {
	case opAdd:
		errC := c.Add(o.now, o.file, o.t, o.size)
		errN := n.add(o.now, o.file, o.t, o.size)
		fmt.Fprintf(log, "step %d Add(now=%d file=%q t=%d size=%d) -> %v\n",
			step, o.now, o.file, o.t, o.size, errC)
		if !sameErr(errC, errN) {
			t.Fatalf("seq %d step %d Add mismatch: impl=%v naive=%v\n%s", seq, step, errC, errN, log.String())
		}
	case opClean:
		resC := c.Clean(o.now)
		purgedN, deletedN := n.clean(o.now)
		fmt.Fprintf(log, "step %d Clean(now=%d) -> deleted=%v purged=%v [exams=%d]\n",
			step, o.now, resC.Deleted, resC.Purged, c.thinnedExams)
		if fmt.Sprint(resC.Deleted) != fmt.Sprint(deletedN) {
			t.Fatalf("seq %d step %d Clean deleted mismatch:\n impl=%v\n naive=%v\n%s",
				seq, step, resC.Deleted, deletedN, log.String())
		}
		if fmt.Sprint(resC.Purged) != fmt.Sprint(purgedN) {
			t.Fatalf("seq %d step %d Clean purged mismatch:\n impl=%v\n naive=%v\n%s",
				seq, step, resC.Purged, purgedN, log.String())
		}
		if c.thinnedExams != n.thinnedExams {
			t.Fatalf("seq %d step %d thinnedExams mismatch: impl=%d naive=%d\n%s",
				seq, step, c.thinnedExams, n.thinnedExams, log.String())
		}
	case opPin, opUnpin:
		pinned := o.kind == opPin
		errC := c.Pin(o.file, o.t)
		errN := n.pin(o.file, o.t, true)
		if !pinned {
			errC = c.Unpin(o.file, o.t)
			errN = n.pin(o.file, o.t, false)
		}
		verb := "Pin"
		if !pinned {
			verb = "Unpin"
		}
		fmt.Fprintf(log, "step %d %s(file=%q t=%d) -> %v\n", step, verb, o.file, o.t, errC)
		if !sameErr(errC, errN) {
			t.Fatalf("seq %d step %d %s mismatch: impl=%v naive=%v\n%s",
				seq, step, verb, errC, errN, log.String())
		}
	case opUndelete:
		errC := c.Undelete(o.now, o.file, o.t)
		errN := n.undelete(o.now, o.file, o.t)
		fmt.Fprintf(log, "step %d Undelete(now=%d file=%q t=%d) -> %v\n",
			step, o.now, o.file, o.t, errC)
		if !sameErr(errC, errN) {
			t.Fatalf("seq %d step %d Undelete mismatch: impl=%v naive=%v\n%s",
				seq, step, errC, errN, log.String())
		}
	}

	// After every operation the complete observable state must agree.
	for _, f := range files {
		compareQueries(t, seq, c, n, f, log)
	}
}

func compareQueries(t *testing.T, seq int, c *Cleaner, n *naiveCleaner, file string, log *strings.Builder) {
	t.Helper()
	vc, vn := c.Versions(file), n.versions(file)
	if fmt.Sprint(vc) != fmt.Sprint(vn) {
		t.Fatalf("seq %d Versions(%q) mismatch:\n impl=%v\n naive=%v\n%s",
			seq, file, vc, vn, log.String())
	}
	nc, bc := c.Totals(file)
	nn, bn := n.totals(file)
	if nc != nn || bc != bn {
		t.Fatalf("seq %d Totals(%q) mismatch: impl=(%d,%d) naive=(%d,%d)\n%s",
			seq, file, nc, bc, nn, bn, log.String())
	}
	tc, tn := c.Trash(file), n.trashList(file)
	if fmt.Sprint(tc) != fmt.Sprint(tn) {
		t.Fatalf("seq %d Trash(%q) mismatch:\n impl=%v\n naive=%v\n%s",
			seq, file, tc, tn, log.String())
	}
}

func sameErr(a, b error) bool {
	if a == nil || b == nil {
		return a == b
	}
	return errors.Is(a, b)
}

// TestRandomDifferentialLogSample emits the full input/output/decision log for
// one short random sequence (visible with `go test -v`).
func TestRandomDifferentialLogSample(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	rules := []Rule{{Until: 8, Step: 5}, {Until: 30, Step: 14}}
	c, _ := New(rules, 3, 20, 3)
	n := newNaive(rules, 3, 20, 3)
	var log strings.Builder
	files := []string{"a"}
	var clock int64
	for step := 0; step < 25; step++ {
		clock += int64(rng.Intn(3))
		o := op{kind: opKind(rng.Intn(5)), now: clock, file: "a"}
		switch o.kind {
		case opAdd:
			o.size = int64(1 + rng.Intn(10))
			o.t = rng.Int63n(clock + 1)
		default:
			o.t = rng.Int63n(clock + 1)
		}
		t.Logf("--- seq-sample step %d ---", step)
		runAndCompare(t, 0, step, c, n, o, files, &log)
		t.Log(strings.TrimRight(log.String(), "\n"))
	}
}

// TestDeterministicReplay verifies that replaying the exact same call
// sequence yields byte-for-byte identical returns and counters.
func TestDeterministicReplay(t *testing.T) {
	build := func() (*Cleaner, []op) {
		c, _ := New([]Rule{{Until: 5, Step: 3}, {Until: 20, Step: 12}}, 3, 25, 4)
		ops := []op{
			{kind: opAdd, now: 1, file: "a", t: 0, size: 10},
			{kind: opAdd, now: 1, file: "a", t: 1, size: 10},
			{kind: opPin, file: "a", t: 0},
			{kind: opClean, now: 2},
			{kind: opAdd, now: 3, file: "b", t: 3, size: 30},
			{kind: opClean, now: 6},
			{kind: opUndelete, now: 6, file: "a", t: 1},
			{kind: opClean, now: 7},
		}
		return c, ops
	}
	type snapshot struct {
		deleted []Deletion
		purged  []PurgedVersion
		exams   int
		err     error
		ver     []Version
		trash   []TrashItem
	}
	play := func() []snapshot {
		c, ops := build()
		var snaps []snapshot
		for _, o := range ops {
			s := snapshot{}
			switch o.kind {
			case opAdd:
				s.err = c.Add(o.now, o.file, o.t, o.size)
			case opClean:
				res := c.Clean(o.now)
				s.deleted, s.purged, s.exams = res.Deleted, res.Purged, c.thinnedExams
			case opPin:
				s.err = c.Pin(o.file, o.t)
			case opUnpin:
				s.err = c.Unpin(o.file, o.t)
			case opUndelete:
				s.err = c.Undelete(o.now, o.file, o.t)
			}
			for _, f := range []string{"a", "b"} {
				s.ver = append(s.ver, c.Versions(f)...)
				s.trash = append(s.trash, c.Trash(f)...)
			}
			snaps = append(snaps, s)
		}
		return snaps
	}
	first, second := play(), play()
	if fmt.Sprint(first) != fmt.Sprint(second) {
		t.Fatalf("replay differs:\n first=%v\n second=%v", first, second)
	}
}
