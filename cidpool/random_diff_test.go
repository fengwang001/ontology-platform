package cidpool

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

type diffOp struct {
	kind  string
	a, b  uint64
	cidN  int
	tokN  int
	bad   int // 0 none, 1 empty cid, 2 long cid, 3 short token, 4 rpt>seq
	query int
}

func reason(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrEncoding):
		return "ErrEncoding"
	case errors.Is(err, ErrViolation):
		return "ErrViolation"
	case errors.Is(err, ErrLimit):
		return "ErrLimit"
	case errors.Is(err, ErrArg):
		return "ErrArg"
	case errors.Is(err, ErrNoCID):
		return "ErrNoCID"
	default:
		return err.Error()
	}
}

// compareStates checks every observable field of Pool against the naive model.
func compareStates(m *naiveModel, p *Pool) error {
	if m.R != p.R() {
		return fmt.Errorf("R: model=%d pool=%d", m.R, p.R())
	}
	if m.activeCount() != p.ActiveCount() {
		return fmt.Errorf("active count: model=%d pool=%d", m.activeCount(), p.ActiveCount())
	}
	if len(m.entries) != len(p.entries) {
		return fmt.Errorf("entry table size: model=%d pool=%d", len(m.entries), len(p.entries))
	}
	for s, me := range m.entries {
		pe, ok := p.EntryAt(s)
		if !ok {
			return fmt.Errorf("seq %d missing from pool", s)
		}
		if string(me.cid) != string(pe.CID) || me.token != pe.Token || me.retired != pe.Retired {
			return fmt.Errorf("seq %d entry mismatch: model=%+v pool=%+v", s, me, pe)
		}
	}
	if fmt.Sprint(m.retires) != fmt.Sprint(peekRetires(p)) {
		return fmt.Errorf("retire queue: model=%v pool=%v", m.retires, peekRetires(p))
	}
	if len(m.paths) != len(pathSnapshot(p)) {
		return fmt.Errorf("path count: model=%d pool=%d", len(m.paths), len(pathSnapshot(p)))
	}
	for pid, ms := range m.paths {
		ps, err := p.PathSeq(pid)
		if err != nil {
			return fmt.Errorf("path %d missing from pool", pid)
		}
		if ms.parked != ps.Parked || (!ms.parked && ms.seq != ps.Seq) {
			return fmt.Errorf("path %d mismatch: model=%+v pool=%+v", pid, ms, ps)
		}
	}
	// Every token number in play must agree on IsReset, including retired
	// tokens and tokens never advertised.
	for n := 0; n < 40; n++ {
		want := false
		for _, e := range m.entries {
			if !e.retired && e.token == toToken(tok(byte(n))) {
				want = true
			}
		}
		if got := p.IsReset(tok(byte(n))); got != want {
			return fmt.Errorf("IsReset(token%d)=%v, want %v", n, got, want)
		}
	}
	// No two paths may share an active seq.
	seen := map[uint64]bool{}
	for pid, ms := range m.paths {
		if ms.parked {
			continue
		}
		if seen[ms.seq] {
			return fmt.Errorf("duplicate occupation of seq %d", ms.seq)
		}
		seen[ms.seq] = true
		if e, ok := m.entries[ms.seq]; !ok || e.retired {
			return fmt.Errorf("path %d holds missing/retired seq %d", pid, ms.seq)
		}
	}
	if m.activeCount() > m.limit {
		return fmt.Errorf("active count %d exceeds limit %d", m.activeCount(), m.limit)
	}
	return nil
}

func peekRetires(p *Pool) []uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]uint64(nil), p.retires...)
}

func pathSnapshot(p *Pool) map[uint64]PathSlot {
	out := map[uint64]PathSlot{}
	for pid := uint64(0); pid < 8; pid++ {
		if s, err := p.PathSeq(pid); err == nil {
			out[pid] = s
		}
	}
	return out
}

func genOp(rng *rand.Rand, step int) diffOp {
	kinds := []string{"onnew", "newpath", "freepath", "retire", "takeretires"}
	kind := kinds[rng.Intn(len(kinds))]
	op := diffOp{kind: kind}
	switch kind {
	case "onnew":
		// Bias seqs small so resends and conflicts recur; occasionally a
		// genuinely new high seq appears.
		if rng.Intn(5) == 0 {
			op.a = uint64(20 + rng.Intn(20))
		} else {
			op.a = uint64(rng.Intn(10))
		}
		switch rng.Intn(6) {
		case 0:
			op.b = 0
		case 1:
			op.b = op.a // boundary: entry survives
		case 2:
			op.b = uint64(rng.Intn(int(op.a) + 1))
		default:
			op.b = uint64(rng.Intn(12))
		}
		op.cidN = rng.Intn(30)
		op.tokN = rng.Intn(30)
		op.bad = rng.Intn(12)
		if op.bad > 4 {
			op.bad = 0
		}
	case "newpath":
		op.a = uint64(rng.Intn(6))
	case "freepath":
		op.a = uint64(rng.Intn(6))
	case "retire":
		op.a = uint64(rng.Intn(12))
	}
	return op
}

// TestRandomDifferential replays 2000 random call sequences against both
// Pool and the naive step-by-step model, asserting identical results and
// state after every single call.
func TestRandomDifferential(t *testing.T) {
	const cases = 2000
	for tc := 0; tc < cases; tc++ {
		rng := rand.New(rand.NewSource(int64(1000 + tc)))
		limit := 2 + rng.Intn(15)
		m := newNaiveModel(limit)
		p := mustPool(t, limit)
		var log []string
		log = append(log, fmt.Sprintf("case %d: L=%d", tc, limit))

		steps := 30 + rng.Intn(60)
		for step := 0; step < steps; step++ {
			op := genOp(rng, step)
			var mErr, pErr error
			switch op.kind {
			case "onnew":
				mErr = applyOnNewNaive(m, op)
				pErr = applyOnNewPool(p, op)
			case "newpath":
				mErr = m.newPath(op.a)
				pErr = p.NewPath(op.a)
			case "freepath":
				mErr = m.freePath(op.a)
				pErr = p.FreePath(op.a)
			case "retire":
				mErr = m.retire(op.a)
				pErr = p.Retire(op.a)
			case "takeretires":
				mq := append([]uint64(nil), m.retires...)
				m.retires = nil
				pq := p.TakeRetires()
				if fmt.Sprint(mq) != fmt.Sprint(pq) {
					t.Fatalf("case %d step %d TakeRetires model=%v pool=%v\n%s",
						tc, step, mq, pq, joinLog(log))
				}
			}
			log = append(log, fmt.Sprintf("step %d %s -> %s", step, opString(op), reason(mErr)))
			if reason(mErr) != reason(pErr) {
				t.Fatalf("case %d step %d %s: model=%s pool=%s\n%s",
					tc, step, opString(op), reason(mErr), reason(pErr), joinLog(log))
			}
			if err := compareStates(m, p); err != nil {
				t.Fatalf("case %d step %d %s: state diverged: %v\n%s\nmodel=%s\npool =%s",
					tc, step, opString(op), err, joinLog(log), dumpNaive(m), dumpPool(p))
			}
		}
		t.Logf("case %d replayed %d steps, inputs/outputs/reasons matched", tc, steps)
	}
}

func joinLog(log []string) string {
	out := ""
	for _, l := range log {
		out += l + "\n"
	}
	return out
}

func opString(op diffOp) string {
	switch op.kind {
	case "onnew":
		return fmt.Sprintf("OnNew(seq=%d rpt=%d cid=%d token=%d bad=%d)",
			op.a, op.b, op.cidN, op.tokN, op.bad)
	case "newpath":
		return fmt.Sprintf("NewPath(%d)", op.a)
	case "freepath":
		return fmt.Sprintf("FreePath(%d)", op.a)
	case "retire":
		return fmt.Sprintf("Retire(%d)", op.a)
	default:
		return "TakeRetires()"
	}
}

func applyOnNewNaive(m *naiveModel, op diffOp) error {
	c, tk, rpt := opArgs(op)
	return m.onNew(op.a, rpt, c, tk)
}

func applyOnNewPool(p *Pool, op diffOp) error {
	c, tk, rpt := opArgs(op)
	return p.OnNew(op.a, rpt, c, tk)
}

func opArgs(op diffOp) ([]byte, []byte, uint64) {
	c := cid(byte(op.cidN))
	tk := tok(byte(op.tokN))
	switch op.bad {
	case 1:
		c = []byte{}
	case 2:
		c = make([]byte, 21)
	case 3:
		tk = make([]byte, 15)
	}
	rpt := op.b
	if op.bad == 4 {
		rpt = op.a + 1
	}
	return c, tk, rpt
}

func dumpNaive(m *naiveModel) string {
	return fmt.Sprintf("R=%d active=%d retires=%v entries=%v paths=%v",
		m.R, m.activeCount(), m.retires, naiveEntriesString(m), m.paths)
}

func naiveEntriesString(m *naiveModel) string {
	out := ""
	for s, e := range m.entries {
		label := -1
		if len(e.cid) == 1 && e.cid[0] >= 0xC0 {
			label = int(e.cid[0] - 0xC0)
		}
		out += fmt.Sprintf("{%d cid=%d ret=%v} ", s, label, e.retired)
	}
	return out
}

func dumpPool(p *Pool) string {
	return fmt.Sprintf("R=%d active=%d paths=%v", p.R(), p.ActiveCount(), pathSnapshot(p))
}
