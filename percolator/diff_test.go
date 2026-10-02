package percolator

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

type opKind int

const (
	opBegin opKind = iota
	opPrewrite
	opCommitPrimary
	opCommitKeys
	opAbort
	opCheck
	opGet
)

type genOp struct {
	kind      opKind
	st        int64
	keys      []string
	primary   string
	ttl       int64
	now       int64
	rts       int64
	writeKind WriteKind
	badArg    bool
	desc      string
}

var diffKeys = []string{"a", "b", "c", "d"}
var diffVals = []string{"x", "y", "z", ""}

// genProgram builds one random, self-describing operation program. Both
// implementations receive byte-identical arguments, including deliberately
// invalid ones that must be rejected without state changes.
func genProgram(rng *rand.Rand, length int) []genOp {
	var ops []genOp
	var live []int64 // allocated start timestamps
	now := int64(0)
	oracle := int64(0)

	pickKey := func() string { return diffKeys[rng.Intn(len(diffKeys))] }
	advance := func() int64 {
		now += int64(rng.Intn(6))
		return now
	}

	for i := 0; i < length; i++ {
		k := opKind(rng.Intn(7))
		switch k {
		case opBegin:
			oracle++
			live = append(live, oracle)
			ops = append(ops, genOp{kind: opBegin, st: oracle, desc: fmt.Sprintf("Begin() -> %d", oracle)})
		case opPrewrite:
			if len(live) == 0 {
				oracle++
				live = append(live, oracle)
				ops = append(ops, genOp{kind: opBegin, st: oracle, desc: fmt.Sprintf("Begin() -> %d", oracle)})
			}
			st := live[len(live)-1]
			// 15% chance of an invalid-argument probe.
			if rng.Intn(100) < 15 {
				ops = append(ops, genOp{
					kind:    opPrewrite,
					st:      st,
					keys:    nil,
					primary: pickKey(),
					ttl:     1,
					now:     advance(),
					badArg:  true,
					desc:    "Prewrite(empty batch)",
				})
				continue
			}
			n := 1 + rng.Intn(3)
			perm := rng.Perm(len(diffKeys))[:n]
			keys := make([]string, n)
			for j, p := range perm {
				keys[j] = diffKeys[p]
			}
			primary := keys[rng.Intn(len(keys))]
			kind := Put
			if rng.Intn(3) == 0 {
				kind = Delete
			}
			ops = append(ops, genOp{
				kind:      opPrewrite,
				st:        st,
				keys:      keys,
				primary:   primary,
				ttl:       int64(1 + rng.Intn(12)),
				now:       advance(),
				writeKind: kind,
				desc:      fmt.Sprintf("Prewrite(st=%d,keys=%v,primary=%s)", st, keys, primary),
			})
			// Occasional ttl/range probes using a fresh future st.
			if rng.Intn(100) < 10 {
				oracle++
				live = append(live, oracle)
				ops = append(ops, genOp{kind: opBegin, st: oracle, desc: fmt.Sprintf("Begin() -> %d", oracle)})
				mode := rng.Intn(3)
				op := genOp{kind: opPrewrite, st: oracle, keys: []string{pickKey()},
					primary: pickKey(), now: advance(), writeKind: Put}
				switch mode {
				case 0:
					op.ttl = 0 // invalid ttl
				case 1:
					op.ttl = 1_000_000_001 // ttl out of range
				case 2:
					op.now = maxTime + 1 // now out of range
				}
				op.desc = fmt.Sprintf("Prewrite-invalid(st=%d,ttl=%d,now=%d)", oracle, op.ttl, op.now)
				ops = append(ops, op)
			}
		case opCommitPrimary:
			if len(live) == 0 {
				continue
			}
			st := live[rng.Intn(len(live))]
			ops = append(ops, genOp{kind: opCommitPrimary, st: st, desc: fmt.Sprintf("CommitPrimary(%d)", st)})
		case opCommitKeys:
			if len(live) == 0 {
				continue
			}
			st := live[rng.Intn(len(live))]
			keys := []string{pickKey()}
			if rng.Intn(2) == 0 {
				keys = append(keys, pickKey())
			}
			ops = append(ops, genOp{kind: opCommitKeys, st: st, keys: keys, desc: fmt.Sprintf("CommitKeys(%d,%v)", st, keys)})
		case opAbort:
			if len(live) == 0 {
				continue
			}
			st := live[rng.Intn(len(live))]
			ops = append(ops, genOp{kind: opAbort, st: st, desc: fmt.Sprintf("Abort(%d)", st)})
		case opCheck:
			st := int64(1 + rng.Intn(8))
			primary := pickKey()
			cnow := advance()
			if rng.Intn(12) == 0 {
				cnow = now - int64(1+rng.Intn(3)) // deliberate clock skew
				if cnow < 0 {
					cnow = 0
				}
			}
			if rng.Intn(20) == 0 {
				primary = ""
			}
			ops = append(ops, genOp{kind: opCheck, st: st, primary: primary, now: cnow,
				desc: fmt.Sprintf("CheckTxnStatus(%s,%d)", primary, st)})
		case opGet:
			key := pickKey()
			rts := int64(rng.Intn(12))
			gnow := advance()
			if rng.Intn(20) == 0 {
				key = ""
			}
			if rng.Intn(20) == 0 {
				rts = -1
			}
			if rng.Intn(20) == 0 {
				gnow = maxTime + 1
			}
			ops = append(ops, genOp{kind: opGet, keys: []string{key}, rts: rts, now: gnow,
				desc: fmt.Sprintf("Get(%s,rts=%d)", key, rts)})
		}
	}

	return ops
}

type outcomes struct {
	begin  int64
	err    error
	status TxnStatusResult
	get    GetResult
	snap   Snapshot
}

func runReal(s *Store, o genOp) outcomes {
	r := outcomes{}
	switch o.kind {
	case opBegin:
		r.begin = s.Begin()
	case opPrewrite:
		muts := make([]Mutation, len(o.keys))
		for i, key := range o.keys {
			muts[i] = Mutation{Key: key, Kind: o.writeKind, Value: diffVals[i%len(diffVals)]}
		}
		r.err = s.Prewrite(o.st, muts, o.primary, o.ttl, o.now)
	case opCommitPrimary:
		r.begin, r.err = s.CommitPrimary(o.st)
	case opCommitKeys:
		r.err = s.CommitKeys(o.st, o.keys)
	case opAbort:
		r.err = s.Abort(o.st)
	case opCheck:
		r.status, r.err = s.CheckTxnStatus(o.primary, o.st, o.now)
	case opGet:
		r.get, r.err = s.Get(o.keys[0], o.rts, o.now)
	}
	r.snap = s.Snapshot()
	return r
}

func runNaive(n *naiveStore, o genOp) outcomes {
	r := outcomes{}
	switch o.kind {
	case opBegin:
		r.begin = n.begin()
	case opPrewrite:
		muts := make([]Mutation, len(o.keys))
		for i, key := range o.keys {
			muts[i] = Mutation{Key: key, Kind: o.writeKind, Value: diffVals[i%len(diffVals)]}
		}
		r.err = n.prewrite(o.st, muts, o.primary, o.ttl, o.now)
	case opCommitPrimary:
		r.begin, r.err = n.commitPrimary(o.st)
	case opCommitKeys:
		r.err = n.commitKeys(o.st, o.keys)
	case opAbort:
		r.err = n.abort(o.st)
	case opCheck:
		r.status, r.err = n.checkTxnStatus(o.primary, o.st, o.now)
	case opGet:
		r.get, r.err = n.get(o.keys[0], o.rts, o.now)
	}
	r.snap = n.snapshot()
	return r
}

func outcomesEqual(a, b outcomes) (bool, string) {
	if a.begin != b.begin {
		return false, fmt.Sprintf("begin %d != %d", a.begin, b.begin)
	}
	if !errors.Is(a.err, b.err) || (a.err == nil) != (b.err == nil) {
		return false, fmt.Sprintf("err %v != %v", a.err, b.err)
	}
	if a.status != b.status {
		return false, fmt.Sprintf("status %+v != %+v", a.status, b.status)
	}
	if a.get != b.get {
		return false, fmt.Sprintf("get %+v != %+v", a.get, b.get)
	}
	if !snapshotsEqual(a.snap, b.snap) {
		return false, fmt.Sprintf("snapshot\nreal =%+v\nnaive=%+v", a.snap, b.snap)
	}
	return true, ""
}

func TestDifferential2000(t *testing.T) {
	for seed := int64(1); seed <= 2000; seed++ {
		rng := rand.New(rand.NewSource(seed))
		ops := genProgram(rng, 60)
		real := NewStore()
		naive := newNaive()
		t.Logf("seed=%d program length=%d", seed, len(ops))
		for i, o := range ops {
			rr := runReal(real, o)
			nr := runNaive(naive, o)
			if ok, why := outcomesEqual(rr, nr); !ok {
				t.Fatalf("seed=%d step=%d %s\n%s\nreason: %s", seed, i, o.desc,
					"decision basis: real outcome vs naive outcome", why)
			}
			t.Logf("  step=%d %s => %s", i, o.desc, summarizeOutcome(o, rr))
		}
	}
}

func summarizeOutcome(o genOp, r outcomes) string {
	if r.err != nil {
		return "reject(" + r.err.Error() + ")"
	}
	if r.begin != 0 {
		return fmt.Sprintf("ok begin=%d", r.begin)
	}
	if o.kind == opCheck {
		return fmt.Sprintf("ok status=%+v", r.status)
	}
	if o.kind == opGet {
		return fmt.Sprintf("ok get=%+v", r.get)
	}
	if o.kind == opCommitPrimary {
		return fmt.Sprintf("ok commitTs=%d", r.begin)
	}
	return "ok"
}
