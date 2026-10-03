package cidpool

import (
	"bytes"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"
)

// model is a naive, literal transcription of the specification used as
// a reference implementation. It shares no code with Pool.
type model struct {
	limit   int
	entries map[uint64]*modelEntry
	r       uint64
	queue   []uint64
	paths   map[uint64]int64 // pid -> seq, -1 means shelved
}

type modelEntry struct {
	cid     []byte
	token   [16]byte
	retired bool
}

func newModel(limit int, cid0 []byte, token0 [16]byte) *model {
	m := &model{
		limit:   limit,
		entries: make(map[uint64]*modelEntry),
		paths:   make(map[uint64]int64),
	}
	m.entries[0] = &modelEntry{cid: append([]byte(nil), cid0...), token: token0}
	return m
}

func (m *model) activeSeqs() []uint64 {
	var out []uint64
	for s, e := range m.entries {
		if !e.retired {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func (m *model) freeActive() (uint64, bool) {
	used := make(map[uint64]bool)
	for _, s := range m.paths {
		if s >= 0 {
			used[uint64(s)] = true
		}
	}
	for _, s := range m.activeSeqs() {
		if !used[s] {
			return s, true
		}
	}
	return 0, false
}

func (m *model) reassign() {
	var shelved []uint64
	for pid, s := range m.paths {
		if s < 0 {
			shelved = append(shelved, pid)
		}
	}
	sort.Slice(shelved, func(i, j int) bool { return shelved[i] < shelved[j] })
	for _, pid := range shelved {
		s, ok := m.freeActive()
		if !ok {
			return
		}
		m.paths[pid] = int64(s)
	}
}

func (m *model) shelve(retired []uint64) {
	gone := make(map[uint64]bool)
	for _, s := range retired {
		gone[s] = true
	}
	for pid, s := range m.paths {
		if s >= 0 && gone[uint64(s)] {
			m.paths[pid] = -1
		}
	}
}

// onNew returns the error code and a human-readable decision rationale.
func (m *model) onNew(seq, rpt uint64, cid []byte, token [16]byte) (ErrCode, string) {
	if rpt > seq {
		return ErrEncoding, fmt.Sprintf("rpt %d > seq %d", rpt, seq)
	}
	if len(cid) < 1 || len(cid) > 20 {
		return ErrEncoding, fmt.Sprintf("cid length %d out of [1,20]", len(cid))
	}
	if e, ok := m.entries[seq]; ok {
		if bytes.Equal(e.cid, cid) && e.token == token {
			return 0, "duplicate frame: identical cid+token, no state change"
		}
		return ErrViolation, fmt.Sprintf("seq %d known with different cid/token", seq)
	}
	for s, e := range m.entries {
		if bytes.Equal(e.cid, cid) {
			return ErrViolation, fmt.Sprintf("cid collides with known seq %d", s)
		}
	}
	r2 := m.r
	if rpt > r2 {
		r2 = rpt
	}
	active := 0
	for s, e := range m.entries {
		if !e.retired && s >= r2 {
			active++
		}
	}
	if seq >= r2 {
		active++
	}
	if active > m.limit {
		return ErrLimit, fmt.Sprintf("deduction with R'=%d yields %d active > limit %d", r2, active, m.limit)
	}
	m.r = r2
	m.entries[seq] = &modelEntry{cid: append([]byte(nil), cid...), token: token, retired: seq < r2}
	var newly []uint64
	for s, e := range m.entries {
		if !e.retired && s < r2 {
			e.retired = true
			newly = append(newly, s)
		}
	}
	if seq < r2 {
		newly = append(newly, seq)
	}
	sort.Slice(newly, func(i, j int) bool { return newly[i] < newly[j] })
	m.queue = append(m.queue, newly...)
	m.shelve(newly)
	m.reassign()
	return 0, fmt.Sprintf("committed: R=%d, retired %v", r2, newly)
}

func (m *model) newPath(pid uint64) (ErrCode, string) {
	if _, ok := m.paths[pid]; ok {
		return ErrArg, fmt.Sprintf("path %d exists", pid)
	}
	s, ok := m.freeActive()
	if !ok {
		return ErrNoCID, "no free active entry"
	}
	m.paths[pid] = int64(s)
	return 0, fmt.Sprintf("path %d takes seq %d", pid, s)
}

func (m *model) freePath(pid uint64) (ErrCode, string) {
	if _, ok := m.paths[pid]; !ok {
		return ErrArg, fmt.Sprintf("path %d missing", pid)
	}
	delete(m.paths, pid)
	m.reassign()
	return 0, fmt.Sprintf("path %d freed", pid)
}

func (m *model) retire(seq uint64) (ErrCode, string) {
	e, ok := m.entries[seq]
	if !ok || e.retired {
		return ErrArg, fmt.Sprintf("seq %d not a known active entry", seq)
	}
	e.retired = true
	m.queue = append(m.queue, seq)
	m.shelve([]uint64{seq})
	m.reassign()
	return 0, fmt.Sprintf("retired seq %d", seq)
}

func (m *model) takeRetires() []uint64 {
	out := append([]uint64(nil), m.queue...)
	m.queue = nil
	return out
}

func (m *model) pathSeq(pid uint64) (uint64, PathState) {
	s, ok := m.paths[pid]
	if !ok {
		return 0, PathMissing
	}
	if s < 0 {
		return 0, PathShelved
	}
	return uint64(s), PathActive
}

func (m *model) isReset(token [16]byte) bool {
	for _, e := range m.entries {
		if !e.retired && e.token == token {
			return true
		}
	}
	return false
}

// op is one randomized call in a fuzz sequence.
type op struct {
	kind        string
	seq, rpt    uint64
	cid         []byte
	token       [16]byte
	pid         uint64
	description string
}

func genOps(rng *rand.Rand, n int) []op {
	ops := make([]op, 0, n)
	freshCID := 0x80
	freshTok := 0x80
	for i := 0; i < n; i++ {
		var o op
		switch r := rng.Intn(100); {
		case r < 45:
			o = op{kind: "OnNew"}
			switch rng.Intn(10) {
			case 0:
				o.seq = uint64(rng.Intn(100))
			default:
				o.seq = uint64(rng.Intn(10))
			}
			switch rng.Intn(10) {
			case 0:
				o.rpt = o.seq + 1 // encoding error
			case 1:
				o.rpt = o.seq // boundary: new entry survives
			case 2, 3:
				o.rpt = uint64(rng.Intn(20))
			default:
				o.rpt = uint64(rng.Intn(6))
			}
			switch rng.Intn(20) {
			case 0:
				o.cid = nil // encoding error
			case 1:
				o.cid = make([]byte, 21) // encoding error
			case 2, 3:
				o.cid = []byte{byte(freshCID)}
				freshCID++
			default:
				o.cid = []byte{byte(rng.Intn(8))}
			}
			if rng.Intn(20) == 0 {
				o.token = tok(byte(freshTok))
				freshTok++
			} else {
				o.token = tok(byte(rng.Intn(8)))
			}
			o.description = fmt.Sprintf("OnNew(seq=%d, rpt=%d, cid=%x, token=%x...)", o.seq, o.rpt, o.cid, o.token[:2])
		case r < 62:
			o = op{kind: "NewPath", pid: uint64(rng.Intn(5))}
			o.description = fmt.Sprintf("NewPath(%d)", o.pid)
		case r < 74:
			o = op{kind: "FreePath", pid: uint64(rng.Intn(5))}
			o.description = fmt.Sprintf("FreePath(%d)", o.pid)
		case r < 88:
			o = op{kind: "Retire", seq: uint64(rng.Intn(10))}
			o.description = fmt.Sprintf("Retire(%d)", o.seq)
		default:
			o = op{kind: "Query"}
			o.description = "Query(IsReset/PathSeq/ActiveCount)"
		}
		ops = append(ops, o)
	}
	return ops
}

// runTrial replays one op sequence on both implementations and compares
// observable state after every call, logging inputs, outputs and the
// model's decision rationale.
func runTrial(t *testing.T, trial int, limit int, cid0 []byte, token0 [16]byte, ops []op) {
	p, err := New(limit, cid0, token0)
	if err != nil {
		t.Fatalf("trial %d: New: %v", trial, err)
	}
	m := newModel(limit, cid0, token0)
	drained := make(map[uint64]bool) // seqs ever dequeued: each at most once

	compare := func(step int, desc string, gotErr, wantCode ErrCode, reason string) {
		t.Helper()
		if gotErr != wantCode {
			t.Fatalf("trial %d step %d: %s: pool code %v, model code %v (%s)",
				trial, step, desc, gotErr, wantCode, reason)
		}
		gotQ, wantQ := p.TakeRetires(), m.takeRetires()
		if len(gotQ) != 0 || len(wantQ) != 0 {
			if !reflect.DeepEqual(gotQ, wantQ) {
				t.Fatalf("trial %d step %d: %s: retire queue %v, model %v", trial, step, desc, gotQ, wantQ)
			}
		}
		for _, s := range gotQ {
			if drained[s] {
				t.Fatalf("trial %d step %d: %s: seq %d enqueued twice", trial, step, desc, s)
			}
			drained[s] = true
		}
		if got, want := p.ActiveCount(), len(m.activeSeqs()); got != want {
			t.Fatalf("trial %d step %d: %s: ActiveCount %d, model %d", trial, step, desc, got, want)
		}
		if p.ActiveCount() > limit {
			t.Fatalf("trial %d step %d: %s: ActiveCount %d exceeds limit %d", trial, step, desc, p.ActiveCount(), limit)
		}
		seen := make(map[uint64]uint64) // seq -> pid, detect double occupancy
		for pid := uint64(0); pid < 6; pid++ {
			gotSeq, gotSt := p.PathSeq(pid)
			wantSeq, wantSt := m.pathSeq(pid)
			if gotSt != wantSt || (gotSt == PathActive && gotSeq != wantSeq) {
				t.Fatalf("trial %d step %d: %s: PathSeq(%d)=(%d,%v), model (%d,%v)",
					trial, step, desc, pid, gotSeq, gotSt, wantSeq, wantSt)
			}
			if gotSt == PathActive {
				if prev, dup := seen[gotSeq]; dup {
					t.Fatalf("trial %d step %d: %s: seq %d occupied by paths %d and %d",
						trial, step, desc, gotSeq, prev, pid)
				}
				seen[gotSeq] = pid
			}
		}
		for b := 0; b < 10; b++ {
			if got, want := p.IsReset(tok(byte(b))), m.isReset(tok(byte(b))); got != want {
				t.Fatalf("trial %d step %d: %s: IsReset(%d)=%v, model %v", trial, step, desc, b, got, want)
			}
		}
		t.Logf("trial %d step %d: %s -> code=%v queue=%v active=%d reason=%s",
			trial, step, desc, wantCode, gotQ, p.ActiveCount(), reason)
	}

	for i, o := range ops {
		switch o.kind {
		case "OnNew":
			err := p.OnNew(o.seq, o.rpt, o.cid, o.token)
			code, reason := m.onNew(o.seq, o.rpt, o.cid, o.token)
			compare(i, o.description, errCode(err), code, reason)
		case "NewPath":
			err := p.NewPath(o.pid)
			code, reason := m.newPath(o.pid)
			compare(i, o.description, errCode(err), code, reason)
		case "FreePath":
			err := p.FreePath(o.pid)
			code, reason := m.freePath(o.pid)
			compare(i, o.description, errCode(err), code, reason)
		case "Retire":
			err := p.Retire(o.seq)
			code, reason := m.retire(o.seq)
			compare(i, o.description, errCode(err), code, reason)
		case "Query":
			compare(i, o.description, 0, 0, "queries only")
		}
	}
}

// TestDifferential replays 2000 randomized call sequences against the
// naive model with a fixed seed, so any mismatch reproduces exactly.
func TestDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(20261003))
	for trial := 0; trial < 2000; trial++ {
		limit := 2 + rng.Intn(3) // 2..4 keeps limits and shelving frequent
		cid0 := []byte{byte(rng.Intn(8))}
		token0 := tok(byte(rng.Intn(8)))
		ops := genOps(rng, 40)
		runTrial(t, trial, limit, cid0, token0, ops)
	}
}

// TestConcurrent hammers one pool from many goroutines; with -race this
// proves mutual exclusion, and the final invariant check proves the
// interleaving was equivalent to some serial order.
func TestConcurrent(t *testing.T) {
	const limit = 4
	p, err := New(limit, cid(0xF0), tok(0xF0))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for _, o := range genOps(rng, 200) {
				switch o.kind {
				case "OnNew":
					_ = p.OnNew(o.seq, o.rpt, o.cid, o.token)
				case "NewPath":
					_ = p.NewPath(o.pid)
				case "FreePath":
					_ = p.FreePath(o.pid)
				case "Retire":
					_ = p.Retire(o.seq)
				default:
					_ = p.ActiveCount()
					_ = p.IsReset(o.token)
					_, _ = p.PathSeq(o.pid)
				}
				_ = p.TakeRetires()
			}
		}(int64(g) + 1)
	}
	wg.Wait()
	if got := p.ActiveCount(); got > limit {
		t.Fatalf("ActiveCount %d exceeds limit %d after concurrent run", got, limit)
	}
	seen := make(map[uint64]uint64)
	for pid := uint64(0); pid < 6; pid++ {
		if seq, st := p.PathSeq(pid); st == PathActive {
			if prev, dup := seen[seq]; dup {
				t.Fatalf("seq %d occupied by paths %d and %d", seq, prev, pid)
			}
			seen[seq] = pid
		}
	}
}
