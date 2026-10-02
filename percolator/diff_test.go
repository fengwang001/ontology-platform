package percolator

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// opKind enumerates the replayed operations.
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

type op struct {
	kind    opKind
	primary string
	st      uint64
	now     uint64
	rts     int64
	ttl     uint64
	key     string
	keys    []string
	muts    []Mutation
}

func (o op) String() string {
	switch o.kind {
	case opBegin:
		return "Begin()"
	case opPrewrite:
		return fmt.Sprintf("Prewrite(st=%d, muts=%v, primary=%q, ttl=%d, now=%d)", o.st, o.muts, o.primary, o.ttl, o.now)
	case opCommitPrimary:
		return fmt.Sprintf("CommitPrimary(st=%d)", o.st)
	case opCommitKeys:
		return fmt.Sprintf("CommitKeys(st=%d, keys=%v)", o.st, o.keys)
	case opAbort:
		return fmt.Sprintf("Abort(st=%d)", o.st)
	case opCheck:
		return fmt.Sprintf("CheckTxnStatus(primary=%q, st=%d, now=%d)", o.key, o.st, o.now)
	default:
		return fmt.Sprintf("Get(key=%q, rts=%d, now=%d)", o.key, o.rts, o.now)
	}
}

type outcome struct {
	u      uint64
	b      bool
	s      string
	status TxnStatus
	code   ErrorCode
}

func (o op) runReal(s *Store) outcome {
	switch o.kind {
	case opBegin:
		return outcome{u: s.Begin()}
	case opPrewrite:
		return outcome{code: codeOf(s.Prewrite(o.st, o.muts, o.primary, o.ttl, o.now))}
	case opCommitPrimary:
		ct, err := s.CommitPrimary(o.st)
		return outcome{u: ct, code: codeOf(err)}
	case opCommitKeys:
		return outcome{code: codeOf(s.CommitKeys(o.st, o.keys))}
	case opAbort:
		return outcome{code: codeOf(s.Abort(o.st))}
	case opCheck:
		st, ct, err := s.CheckTxnStatus(o.key, o.st, o.now)
		return outcome{u: ct, status: st, code: codeOf(err)}
	default:
		found, val, err := s.Get(o.key, o.rts, o.now)
		return outcome{b: found, s: val, code: codeOf(err)}
	}
}

func (o op) runNaive(n *naiveStore) outcome {
	switch o.kind {
	case opBegin:
		return outcome{u: n.Begin()}
	case opPrewrite:
		return outcome{code: n.Prewrite(o.st, o.muts, o.primary, o.ttl, o.now)}
	case opCommitPrimary:
		ct, c := n.CommitPrimary(o.st)
		return outcome{u: ct, code: c}
	case opCommitKeys:
		return outcome{code: n.CommitKeys(o.st, o.keys)}
	case opAbort:
		return outcome{code: n.Abort(o.st)}
	case opCheck:
		st, ct, c := n.CheckTxnStatus(o.key, o.st, o.now)
		return outcome{u: ct, status: st, code: c}
	default:
		found, val, c := n.Get(o.key, o.rts, o.now)
		return outcome{b: found, s: val, code: c}
	}
}

// genProgram builds a random legal-ish sequence; the engine itself rejects
// whatever is invalid, which is part of what the differential test covers.
func genProgram(rng *rand.Rand, maxOps int) []op {
	const nkeys = 4
	keyName := func(i int) string { return string(rune('a' + i)) }

	var ops []op
	now := uint64(0)
	allocated := uint64(0)

	bumpNow := func() uint64 {
		switch rng.Intn(3) {
		case 0:
			now += uint64(rng.Intn(3))
		case 1:
			now += uint64(rng.Intn(20))
		default:
			now += uint64(rng.Intn(100))
		}
		if now > maxNow {
			now = maxNow
		}
		return now
	}

	// mutateTime occasionally injects an out-of-range or backwards timestamp.
	mutateTime := func(candidate uint64) uint64 {
		switch rng.Intn(20) {
		case 0:
			return maxNow + 1
		case 1:
			if candidate > 0 {
				return candidate - 1
			}
		}
		return candidate
	}

	pickSt := func() uint64 {
		if allocated == 0 {
			return 0
		}
		if rng.Intn(5) == 0 {
			return allocated + uint64(rng.Intn(3)) + 1 // unknown txn
		}
		return uint64(rng.Intn(int(allocated))) + 1
	}

	for len(ops) < maxOps {
		kind := opKind(rng.Intn(7))
		switch kind {
		case opBegin:
			ops = append(ops, op{kind: opBegin})
			allocated++
		case opPrewrite:
			st := pickSt()
			count := 1 + rng.Intn(3)
			perm := rng.Perm(nkeys)[:count]
			muts := make([]Mutation, count)
			for i, ki := range perm {
				tp := Put
				if rng.Intn(3) == 0 {
					tp = Delete
				}
				muts[i] = Mutation{Key: keyName(ki), Type: tp, Value: fmt.Sprintf("v%d-%d", st, i)}
			}
			sort.Slice(muts, func(i, j int) bool { return muts[i].Key < muts[j].Key })
			primary := muts[rng.Intn(count)].Key
			ttl := uint64(1 + rng.Intn(25))
			switch rng.Intn(10) {
			case 0:
				ttl = 0 // invalid
			case 1:
				ttl = maxTTL + 1 // invalid
			}
			opNow := bumpNow()
			if count > 1 {
				switch rng.Intn(20) {
				case 0:
					muts[1] = muts[0] // duplicate key -> invalid
				case 1:
					muts[0].Key = "" // empty key -> invalid
				}
			}
			if rng.Intn(20) == 0 {
				primary = "z-not-in-set" // invalid primary
			}
			ops = append(ops, op{kind: opPrewrite, st: st, primary: primary, muts: muts, ttl: ttl, now: mutateTime(opNow)})
		case opCommitPrimary:
			ops = append(ops, op{kind: opCommitPrimary, st: pickSt()})
		case opCommitKeys:
			st := pickSt()
			kcount := rng.Intn(3)
			keys := make([]string, kcount)
			for i := range keys {
				keys[i] = keyName(rng.Intn(nkeys))
			}
			ops = append(ops, op{kind: opCommitKeys, st: st, keys: keys})
		case opAbort:
			ops = append(ops, op{kind: opAbort, st: pickSt()})
		case opCheck:
			st := pickSt()
			k := keyName(rng.Intn(nkeys))
			if rng.Intn(8) == 0 {
				st = 0 // invalid st
			}
			opNow := bumpNow()
			if rng.Intn(20) == 0 {
				k = ""
			}
			ops = append(ops, op{kind: opCheck, key: k, st: st, now: mutateTime(opNow)})
		default:
			k := keyName(rng.Intn(nkeys))
			if rng.Intn(30) == 0 {
				k = ""
			}
			rts := int64(rng.Intn(int(allocated) + 3))
			if rng.Intn(15) == 0 {
				rts = -1 // invalid
			}
			ops = append(ops, op{kind: opGet, key: k, rts: rts, now: mutateTime(bumpNow())})
		}
	}
	return ops
}

func TestRandomDifferential(t *testing.T) {
	const runs = 2000
	rng := rand.New(rand.NewSource(20261002))
	var logLines []string

	for run := 0; run < runs; run++ {
		length := 1 + rng.Intn(60)
		program := genProgram(rng, length)
		real := NewStore()
		naive := newNaive()
		var runLog []string
		runLog = append(runLog, fmt.Sprintf("=== run %d (seed fixed, %d ops) ===", run, len(program)))

		for step, o := range program {
			// Normalize prewrite primary: generator always put valid primary;
			// occasionally inject an invalid one to exercise param ordering.
			if o.kind == opPrewrite && rng.Intn(40) == 0 {
				o.ttl = 0
			}
			got := o.runReal(real)
			want := o.runNaive(naive)
			reason := ""
			if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(real.Snapshot(), naive.snapshot()) {
				reason = "MISMATCH"
			}
			runLog = append(runLog, fmt.Sprintf("  [%d] in : %s", step, o.String()))
			runLog = append(runLog, fmt.Sprintf("       out: real=%+v naive=%+v %s", got, want, reason))
			if reason == "MISMATCH" {
				runLog = append(runLog, fmt.Sprintf("       real snap : %+v", real.Snapshot()))
				runLog = append(runLog, fmt.Sprintf("       naive snap: %+v", naive.snapshot()))
				logLines = append(logLines, runLog...)
				t.Fatalf("differential mismatch at run %d step %d\n%s", run, step, strings.Join(logLines, "\n"))
			}
		}
		// Keep logs bounded but always record inputs/outputs of a sample.
		if run < 3 || rng.Intn(100) == 0 {
			logLines = append(logLines, runLog...)
		}
	}
	t.Logf("differential runs: %d\n%s", runs, strings.Join(logLines, "\n"))
}
