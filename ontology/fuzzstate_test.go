package ontology

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"reflect"
	"slices"
	"testing"
)

var fuzzTerms = []string{"", "a", "b", "aa", "ab", "x", "z", "中", "\x00", "long-term-name"}

type fuzzMerge struct {
	handle   int
	finished bool
}

type fuzzState struct {
	rng     *rand.Rand
	m       *Merger
	orc     *naiveOracle
	allIDs  []int
	liveIDs []int
	merges  []*fuzzMerge
	everKey map[string]bool
	log     *os.File
	seq     int
}

func newFuzzState(rng *rand.Rand, seq int, logFile *os.File) *fuzzState {
	return &fuzzState{
		rng:     rng,
		m:       NewMerger(),
		orc:     newNaiveOracle(),
		everKey: map[string]bool{},
		log:     logFile,
		seq:     seq,
	}
}

func (fs *fuzzState) aliveKeys() []string {
	keys := make([]string, 0, len(fs.orc.live))
	for k := range fs.orc.live {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func (fs *fuzzState) deadKeys() []string {
	var out []string
	for k := range fs.everKey {
		if _, alive := fs.orc.live[k]; !alive {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

func (fs *fuzzState) freshKey() string {
	for {
		k := fmt.Sprintf("k%06d", fs.rng.IntN(1_000_000))
		if !fs.everKey[k] {
			fs.everKey[k] = true
			return k
		}
	}
}

func (fs *fuzzState) pickKey() string {
	alive := fs.aliveKeys()
	dead := fs.deadKeys()
	r := fs.rng.Float64()
	switch {
	case len(alive) > 0 && r < 0.5:
		return alive[fs.rng.IntN(len(alive))]
	case len(dead) > 0 && r < 0.8:
		return dead[fs.rng.IntN(len(dead))]
	default:
		return fs.freshKey()
	}
}

func (fs *fuzzState) randomTerms() []string {
	n := fs.rng.IntN(5)
	out := make([]string, n)
	for i := range n {
		out[i] = fuzzTerms[fs.rng.IntN(len(fuzzTerms))]
	}
	return out
}

func (fs *fuzzState) genBatch() []Doc {
	if fs.rng.IntN(12) == 0 {
		return nil
	}
	size := 1 + fs.rng.IntN(3)
	batch := make([]Doc, size)
	for i := range size {
		key := fs.pickKey()
		if fs.rng.IntN(15) == 0 {
			key = ""
		}
		batch[i] = Doc{Key: key, Terms: fs.randomTerms()}
	}
	return batch
}

func (fs *fuzzState) mergeIDs() []int {
	avail := append([]int(nil), fs.liveIDs...)
	if len(avail) < 2 || fs.rng.IntN(6) == 0 {
		switch fs.rng.IntN(3) {
		case 0:
			if len(fs.allIDs) > 0 {
				return []int{fs.allIDs[fs.rng.IntN(len(fs.allIDs))]}
			}
			return []int{9999}
		case 1:
			if len(avail) > 0 {
				id := avail[fs.rng.IntN(len(avail))]
				return []int{id, id}
			}
			return []int{42, 42}
		default:
			ghost := 7_000_000 + fs.rng.IntN(1000)
			if len(avail) > 0 {
				return []int{avail[fs.rng.IntN(len(avail))], ghost}
			}
			return []int{ghost, ghost + 1}
		}
	}
	fs.rng.Shuffle(len(avail), func(i, j int) { avail[i], avail[j] = avail[j], avail[i] })
	n := 2
	if len(avail) >= 3 && fs.rng.IntN(2) == 0 {
		n = 3
	}
	return avail[:min(n, len(avail))]
}

func (fs *fuzzState) pendingHandles() []int {
	var out []int
	for _, mg := range fs.merges {
		if !mg.finished {
			out = append(out, mg.handle)
		}
	}
	return out
}

func (fs *fuzzState) pickHandle() int {
	pending := fs.pendingHandles()
	if len(pending) > 0 && fs.rng.IntN(4) != 0 {
		return pending[fs.rng.IntN(len(pending))]
	}
	if len(fs.merges) > 0 && fs.rng.IntN(2) == 0 {
		return fs.merges[fs.rng.IntN(len(fs.merges))].handle
	}
	return 9_000_000 + fs.rng.IntN(1000)
}

func (fs *fuzzState) pickSegmentID() int {
	r := fs.rng.Float64()
	switch {
	case len(fs.liveIDs) > 0 && r < 0.7:
		return fs.liveIDs[fs.rng.IntN(len(fs.liveIDs))]
	case len(fs.allIDs) > 0 && r < 0.9:
		return fs.allIDs[fs.rng.IntN(len(fs.allIDs))]
	default:
		return 8_000_000 + fs.rng.IntN(1000)
	}
}

func (fs *fuzzState) recordOp(opIdx int, in, out, mismatch string) {
	verdict := "MATCH"
	if mismatch != "" {
		verdict = "MISMATCH: " + mismatch
	}
	fmt.Fprintf(fs.log, "[seq %04d op %03d] IN  %s\n                 OUT %s\n                 => %s\n",
		fs.seq, opIdx, in, out, verdict)
}

// fullCheck 逐段对照 Stats 与全部词项倒排表，并校验存活键集合与全局不变量。
func (fs *fuzzState) fullCheck(t *testing.T, opIdx int, in, out string) {
	t.Helper()
	failf := func(format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		fs.recordOp(opIdx, in, out, msg)
		t.Fatalf("seq=%d op=%d %s", fs.seq, opIdx, msg)
	}
	if len(fs.m.segments) != len(fs.orc.segments) {
		failf("segment count impl=%d naive=%d", len(fs.m.segments), len(fs.orc.segments))
	}
	termSet := map[string]struct{}{"__never__": {}}
	for id, seg := range fs.m.segments {
		oseg, ok := fs.orc.segments[id]
		if !ok {
			failf("segment %d absent in naive", id)
		}
		if seg.busy != oseg.busy {
			failf("segment %d busy impl=%v naive=%v", id, seg.busy, oseg.busy)
		}
		for _, ts := range seg.terms {
			for _, term := range ts {
				termSet[term] = struct{}{}
			}
		}
		for _, ts := range oseg.terms {
			for _, term := range ts {
				termSet[term] = struct{}{}
			}
		}
	}
	for id, seg := range fs.m.segments {
		oseg := fs.orc.segments[id]
		if got, want := seg.stats(), refStats(oseg); got != want {
			failf("segment %d stats impl=%+v naive=%+v", id, got, want)
		}
		for term := range termSet {
			if got, want := seg.postings(term), refPostings(oseg, term); !reflect.DeepEqual(got, want) {
				failf("segment %d term %q postings impl=%v naive=%v", id, term, got, want)
			}
		}
	}
	if len(fs.m.liveKeys) != len(fs.orc.live) {
		failf("live key count impl=%d naive=%d", len(fs.m.liveKeys), len(fs.orc.live))
	}
	for key, seg := range fs.m.liveKeys {
		oseg, ok := fs.orc.live[key]
		if !ok || oseg.id != seg.id {
			failf("live key %q location impl=seg%d naive=seg%v", key, seg.id, ok)
		}
	}
	total := 0
	for _, seg := range fs.m.segments {
		total += seg.numDocs()
	}
	if total != len(fs.m.liveKeys) {
		failf("invariant broken: sum(numDocs)=%d live=%d", total, len(fs.m.liveKeys))
	}
	fs.recordOp(opIdx, in, out, "")
}

func errName(err error) string {
	if err == nil {
		return "nil"
	}
	switch {
	case errorIs(err, ErrEmptyBatch):
		return "ErrEmptyBatch"
	case errorIs(err, ErrEmptyKey):
		return "ErrEmptyKey"
	case errorIs(err, ErrEmptyTerm):
		return "ErrEmptyTerm"
	case errorIs(err, ErrInvalidMerge):
		return "ErrInvalidMerge"
	case errorIs(err, ErrDuplicateKey):
		return "ErrDuplicateKey"
	case errorIs(err, ErrSegmentNotFound):
		return "ErrSegmentNotFound"
	case errorIs(err, ErrSegmentBusy):
		return "ErrSegmentBusy"
	case errorIs(err, ErrKeyNotFound):
		return "ErrKeyNotFound"
	case errorIs(err, ErrInvalidHandle):
		return "ErrInvalidHandle"
	default:
		return err.Error()
	}
}

func errorIs(err, target error) bool { return err == target || errors.Is(err, target) }
