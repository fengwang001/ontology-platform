package tictoc

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// naiveTuple / naiveTxn are an independent, line-by-line transcription of the
// rules in the specification. It shares no code with the production DB.
type naiveTuple struct {
	v, w, r int64
	holder  int64
}

type naiveRead struct {
	k          int
	w0, r0     int64
	firstValue int64
}

type naiveTxn struct {
	id       int64
	status   TxnStatus
	c        int64
	reads    []naiveRead
	readKeys map[int]bool
	writes   map[int]int64
}

type naiveSim struct {
	k      int
	tuples []naiveTuple
	txns   map[int64]*naiveTxn
	nextID int64
	log    []string
}

func newNaive(k int) *naiveSim {
	return &naiveSim{
		k:      k,
		tuples: make([]naiveTuple, k),
		txns:   map[int64]*naiveTxn{},
		nextID: 1,
	}
}

func (s *naiveSim) begin() int64 {
	id := s.nextID
	s.nextID++
	s.txns[id] = &naiveTxn{id: id, status: StatusActive, readKeys: map[int]bool{}, writes: map[int]int64{}}
	s.log = append(s.log, fmt.Sprintf("Begin() -> %d", id))
	return id
}

func (s *naiveSim) read(t int64, key int) (int64, error) {
	tr, ok := s.txns[t]
	if !ok {
		return 0, ErrNoSuchTxnErr
	}
	if tr.status != StatusActive {
		return 0, ErrWrongStateErr
	}
	if key < 0 || key >= s.k {
		return 0, ErrKeyOutOfRangeErr
	}
	if x, wrote := tr.writes[key]; wrote {
		s.log = append(s.log, fmt.Sprintf("Read(%d,%d) -> %d [write buffer]", t, key, x))
		return x, nil
	}
	if tr.readKeys[key] {
		for _, rec := range tr.reads {
			if rec.k == key {
				s.log = append(s.log, fmt.Sprintf("Read(%d,%d) -> %d [cached first read]", t, key, rec.firstValue))
				return rec.firstValue, nil
			}
		}
	}
	tp := s.tuples[key]
	tr.reads = append(tr.reads, naiveRead{k: key, w0: tp.w, r0: tp.r, firstValue: tp.v})
	tr.readKeys[key] = true
	s.log = append(s.log, fmt.Sprintf("Read(%d,%d) -> %d [snapshot w=%d r=%d]", t, key, tp.v, tp.w, tp.r))
	return tp.v, nil
}

func (s *naiveSim) write(t int64, key int, x int64) error {
	tr, ok := s.txns[t]
	if !ok {
		return ErrNoSuchTxnErr
	}
	if tr.status != StatusActive {
		return ErrWrongStateErr
	}
	if key < 0 || key >= s.k {
		return ErrKeyOutOfRangeErr
	}
	tr.writes[key] = x
	s.log = append(s.log, fmt.Sprintf("Write(%d,%d,%d) -> ok", t, key, x))
	return nil
}

func sortedNaiveKeys(m map[int]int64) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

func (s *naiveSim) prepare(t int64) (int64, error) {
	tr, ok := s.txns[t]
	if !ok {
		return 0, ErrNoSuchTxnErr
	}
	if tr.status != StatusActive {
		return 0, ErrWrongStateErr
	}
	writeKeys := sortedNaiveKeys(tr.writes)

	// Step 1: ascending locks.
	locked := []int{}
	for _, key := range writeKeys {
		if h := s.tuples[key].holder; h != 0 && h != t {
			for _, k2 := range locked {
				s.tuples[k2].holder = 0
			}
			tr.status = StatusAborted
			s.log = append(s.log, fmt.Sprintf("Prepare(%d) -> abort lock-conflict key=%d holder=%d", t, key, h))
			return 0, ErrLockConflictErr
		}
		s.tuples[key].holder = t
		locked = append(locked, key)
	}

	// Step 2: commit timestamp.
	var c int64
	for _, key := range writeKeys {
		if x := s.tuples[key].r + 1; x > c {
			c = x
		}
	}
	for _, rec := range tr.reads {
		if rec.w0 > c {
			c = rec.w0
		}
	}

	// Step 3: ascending read-set validation.
	readsByKey := make([]naiveRead, len(tr.reads))
	copy(readsByKey, tr.reads)
	sort.Slice(readsByKey, func(a, b int) bool { return readsByKey[a].k < readsByKey[b].k })
	extended := map[int]int64{}
	undo := func() {
		for _, k2 := range locked {
			s.tuples[k2].holder = 0
		}
		for k2, old := range extended {
			s.tuples[k2].r = old
		}
	}
	for _, rec := range readsByKey {
		if rec.r0 >= c {
			continue
		}
		tp := &s.tuples[rec.k]
		if tp.w != rec.w0 {
			undo()
			tr.status = StatusAborted
			s.log = append(s.log, fmt.Sprintf("Prepare(%d) -> abort version-changed key=%d wNow=%d wSaw=%d", t, rec.k, tp.w, rec.w0))
			return 0, ErrVersionChangedErr
		}
		if tp.r >= c {
			continue
		}
		if tp.holder == t {
			continue
		}
		if tp.holder != 0 {
			undo()
			tr.status = StatusAborted
			s.log = append(s.log, fmt.Sprintf("Prepare(%d) -> abort extension-blocked key=%d holder=%d", t, rec.k, tp.holder))
			return 0, ErrExtensionBlockedErr
		}
		if _, done := extended[rec.k]; !done {
			extended[rec.k] = tp.r
		}
		tp.r = c
	}

	tr.status = StatusPrepared
	tr.c = c
	s.log = append(s.log, fmt.Sprintf("Prepare(%d) -> prepared c=%d locks=%v extensions=%v", t, c, writeKeys, extended))
	return c, nil
}

func (s *naiveSim) finish(t int64) (int64, error) {
	tr, ok := s.txns[t]
	if !ok {
		return 0, ErrNoSuchTxnErr
	}
	if tr.status != StatusPrepared {
		return 0, ErrWrongStateErr
	}
	for key, x := range tr.writes {
		s.tuples[key].v = x
		s.tuples[key].w = tr.c
		s.tuples[key].r = tr.c
		s.tuples[key].holder = 0
	}
	tr.status = StatusCommitted
	s.log = append(s.log, fmt.Sprintf("Finish(%d) -> committed c=%d", t, tr.c))
	return tr.c, nil
}

func (s *naiveSim) abort(t int64) error {
	tr, ok := s.txns[t]
	if !ok {
		return ErrNoSuchTxnErr
	}
	if tr.status != StatusActive && tr.status != StatusPrepared {
		return ErrWrongStateErr
	}
	if tr.status == StatusPrepared {
		for key := range tr.writes {
			s.tuples[key].holder = 0
		}
	}
	tr.status = StatusAborted
	s.log = append(s.log, fmt.Sprintf("Abort(%d) -> aborted", t))
	return nil
}

type callResult struct {
	val int64
	err error
}

func errCode(err error) ErrorCode {
	if err == nil {
		return -1
	}
	var te *TicTocError
	if errors.As(err, &te) {
		return te.Code
	}
	return -2
}

// runOneSequence executes one random call stream against both implementations
// and diffs every result, every tuple, the touch budget and serial replay.
func runOneSequence(t *testing.T, rng *rand.Rand, seq int) {
	t.Helper()
	k := 1 + rng.Intn(5)
	db, err := NewDB(k)
	if err != nil {
		t.Fatal(err)
	}
	sim := newNaive(k)

	const numCalls = 40
	active := []int64{}

	for i := 0; i < numCalls; i++ {
		op := rng.Intn(6)
		var tID int64
		if len(active) > 0 {
			tID = active[rng.Intn(len(active))]
		}

		var got, want callResult
		desc := ""
		switch op {
		case 0:
			g := db.Begin()
			w := sim.begin()
			got.val, want.val = g, w
			active = append(active, g)
			desc = "Begin"
		case 1:
			key := rng.Intn(k + 2)
			got.val, got.err = db.Read(tID, key)
			want.val, want.err = sim.read(tID, key)
			desc = fmt.Sprintf("Read(%d,%d)", tID, key)
		case 2:
			key := rng.Intn(k + 2)
			x := int64(rng.Intn(7)) - 3
			got.err = db.Write(tID, key, x)
			want.err = sim.write(tID, key, x)
			desc = fmt.Sprintf("Write(%d,%d,%d)", tID, key, x)
		case 3:
			got.val, got.err = db.Prepare(tID)
			want.val, want.err = sim.prepare(tID)
			desc = fmt.Sprintf("Prepare(%d)", tID)
		case 4:
			preFinish := db.Snapshot()
			trPre := sim.txns[tID]
			got.val, got.err = db.Finish(tID)
			want.val, want.err = sim.finish(tID)
			desc = fmt.Sprintf("Finish(%d)", tID)
			if got.err == nil && trPre.status == StatusPrepared {
				postFinish := db.Snapshot()
				for key := range trPre.writes {
					if !(postFinish[key].WriteTS > preFinish[key].ReadTS) {
						t.Fatalf("seq %d Finish(%d): installed w=%d not > prior r=%d on key %d",
							seq, tID, postFinish[key].WriteTS, preFinish[key].ReadTS, key)
					}
				}
			}
		case 5:
			got.err = db.Abort(tID)
			want.err = sim.abort(tID)
			desc = fmt.Sprintf("Abort(%d)", tID)
		}

		if got.val != want.val || errCode(got.err) != errCode(want.err) {
			t.Fatalf("seq %d call %d %s diverges:\n got=(v=%d,code=%d)\nwant=(v=%d,code=%d)\nnaive log:\n%s",
				seq, i, desc, got.val, errCode(got.err), want.val, errCode(want.err),
				strings.Join(sim.log, "\n"))
		}

		snap := db.Snapshot()
		for key := 0; key < k; key++ {
			nt := sim.tuples[key]
			if snap[key].Value != nt.v || snap[key].WriteTS != nt.w ||
				snap[key].ReadTS != nt.r || snap[key].Holder != nt.holder {
				t.Fatalf("seq %d after %s tuple %d diverges: db=(%d,%d,%d,%d) naive=(%d,%d,%d,%d)\nlog:\n%s",
					seq, desc, key, snap[key].Value, snap[key].WriteTS, snap[key].ReadTS, snap[key].Holder,
					nt.v, nt.w, nt.r, nt.holder, strings.Join(sim.log, "\n"))
			}
			if snap[key].WriteTS > snap[key].ReadTS {
				t.Fatalf("seq %d after %s: key %d w>r", seq, desc, key)
			}
			if snap[key].Holder != 0 {
				h, ok := sim.txns[snap[key].Holder]
				if !ok || h.status != StatusPrepared {
					t.Fatalf("seq %d after %s: key %d held by %d which is not prepared", seq, desc, key, snap[key].Holder)
				}
				if _, inWriteSet := h.writes[key]; !inWriteSet {
					t.Fatalf("seq %d after %s: key %d held by %d outside its write set", seq, desc, key, snap[key].Holder)
				}
			}
		}
		for _, tr := range sim.txns {
			if tr.status != StatusPrepared {
				continue
			}
			held := 0
			for _, tp := range snap {
				if tp.Holder == tr.id {
					held++
				}
			}
			if held != len(tr.writes) {
				t.Fatalf("seq %d after %s: prepared txn %d holds %d locks but write set has %d keys",
					seq, desc, tr.id, held, len(tr.writes))
			}
		}

		if op == 3 && got.err == nil {
			tr := sim.txns[tID]
			budget := int64(2 * (len(tr.reads) + len(tr.writes)))
			if touches := db.lastPrepareTouches(); touches > budget {
				t.Fatalf("seq %d %s touches=%d > budget=%d", seq, desc, touches, budget)
			}
		}
		t.Logf("seq %4d %-22s -> v=%-3d code=%-2d OK | %s",
			seq, desc, got.val, errCode(got.err), lastLine(sim))
	}

	for id, ntr := range sim.txns {
		gst, gerr := db.Status(id)
		if gerr != nil || gst != ntr.status {
			t.Fatalf("seq %d txn %d status db=(%d,%v) naive=%d", seq, id, gst, gerr, ntr.status)
		}
	}
	verifySerialReplay(t, sim, seq)
}

func lastLine(sim *naiveSim) string {
	if len(sim.log) == 0 {
		return ""
	}
	return sim.log[len(sim.log)-1]
}

// verifySerialReplay re-installs committed transactions in (c, Finish order)
// and checks each transaction's first-read values against the replayed state.
func verifySerialReplay(t *testing.T, sim *naiveSim, seq int) {
	t.Helper()
	finishRank := map[int64]int{}
	rank := 0
	for _, line := range sim.log {
		var id int64
		if n, _ := fmt.Sscanf(line, "Finish(%d) -> committed", &id); n == 1 {
			finishRank[id] = rank
			rank++
		}
	}
	type entry struct {
		id int64
		c  int64
	}
	var order []entry
	for id, tr := range sim.txns {
		if tr.status == StatusCommitted {
			order = append(order, entry{id, tr.c})
		}
	}
	sort.SliceStable(order, func(a, b int) bool {
		if order[a].c != order[b].c {
			return order[a].c < order[b].c
		}
		return finishRank[order[a].id] < finishRank[order[b].id]
	})

	replayed := make([]int64, sim.k)
	for _, e := range order {
		tr := sim.txns[e.id]
		readByKey := map[int]naiveRead{}
		for _, rec := range tr.reads {
			if _, exists := readByKey[rec.k]; !exists {
				readByKey[rec.k] = rec
			}
		}
		keys := make([]int, 0, len(readByKey))
		for key := range readByKey {
			keys = append(keys, key)
		}
		sort.Ints(keys)
		for _, key := range keys {
			if _, wrote := tr.writes[key]; !wrote && readByKey[key].firstValue != replayed[key] {
				t.Fatalf("seq %d serial replay: txn %d first read key %d = %d, replay state = %d (c=%d)",
					seq, e.id, key, readByKey[key].firstValue, replayed[key], e.c)
			}
		}
		for key, x := range tr.writes {
			replayed[key] = x
		}
	}
}

func TestNaiveDifferential2000(t *testing.T) {
	rng := rand.New(rand.NewSource(20261003))
	for seq := 0; seq < 2000; seq++ {
		runOneSequence(t, rng, seq)
	}
	t.Logf("2000 random call sequences match the naive simulation on every result and state")
}

type opSpec struct {
	kind string
	key  int
	val  int64
	pick int // index into the live-txn list at execution time
}

func buildFixedOps(rng *rand.Rand, k, n int) []opSpec {
	ops := make([]opSpec, 0, n)
	live := 0
	for i := 0; i < n; i++ {
		pick := 0
		if live > 0 {
			pick = rng.Intn(live)
		}
		switch rng.Intn(6) {
		case 0:
			ops = append(ops, opSpec{kind: "begin"})
			live++
		case 1:
			ops = append(ops, opSpec{kind: "read", key: rng.Intn(k + 2), pick: pick})
		case 2:
			ops = append(ops, opSpec{kind: "write", key: rng.Intn(k + 2), val: int64(rng.Intn(7)) - 3, pick: pick})
		case 3:
			ops = append(ops, opSpec{kind: "prepare", pick: pick})
		case 4:
			ops = append(ops, opSpec{kind: "finish", pick: pick})
		case 5:
			ops = append(ops, opSpec{kind: "abort", pick: pick})
		}
	}
	return ops
}

func replayFixedOps(t *testing.T, k int, ops []opSpec) ([]SnapshotTuple, []string) {
	t.Helper()
	db, err := NewDB(k)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	out := make([]string, 0, len(ops))
	for _, o := range ops {
		var id int64
		if len(ids) > 0 {
			id = ids[o.pick%len(ids)]
		}
		switch o.kind {
		case "begin":
			id = db.Begin()
			ids = append(ids, id)
			out = append(out, fmt.Sprintf("begin->%d", id))
		case "read":
			v, err := db.Read(id, o.key)
			out = append(out, fmt.Sprintf("read(%d,%d)->%d,%d", id, o.key, v, errCode(err)))
		case "write":
			err := db.Write(id, o.key, o.val)
			out = append(out, fmt.Sprintf("write(%d,%d,%d)->%d", id, o.key, o.val, errCode(err)))
		case "prepare":
			c, err := db.Prepare(id)
			out = append(out, fmt.Sprintf("prepare(%d)->%d,%d", id, c, errCode(err)))
		case "finish":
			c, err := db.Finish(id)
			out = append(out, fmt.Sprintf("finish(%d)->%d,%d", id, c, errCode(err)))
		case "abort":
			err := db.Abort(id)
			out = append(out, fmt.Sprintf("abort(%d)->%d", id, errCode(err)))
		}
	}
	return db.Snapshot(), out
}

func TestDeterministicReplay(t *testing.T) {
	rng := rand.New(rand.NewSource(424242))
	ops := buildFixedOps(rng, 4, 120)
	snapA, outA := replayFixedOps(t, 4, ops)
	snapB, outB := replayFixedOps(t, 4, ops)
	if strings.Join(outA, "|") != strings.Join(outB, "|") {
		t.Fatalf("result sequences differ:\n%v\nvs\n%v", outA, outB)
	}
	for i := range snapA {
		if snapA[i] != snapB[i] {
			t.Fatalf("tuple %d differs between replays: %+v vs %+v", i, snapA[i], snapB[i])
		}
	}
	t.Logf("identical call sequence replayed twice produced identical outputs and tuple state")
}
