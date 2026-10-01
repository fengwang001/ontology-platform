package plancache

import (
	"fmt"
	"math/big"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
)

type opKind int

const (
	opPrepare opKind = iota
	opNext
	opReportCustom
	opReportGeneric
	opDoneGeneric
	opBump
	opDrop
)

type op struct {
	kind opKind
	name string
	cost int64
	v    uint64
}

func (o op) String() string {
	switch o.kind {
	case opPrepare:
		return fmt.Sprintf("Prepare(%q)", o.name)
	case opNext:
		return fmt.Sprintf("Next(%q)", o.name)
	case opReportCustom:
		return fmt.Sprintf("ReportCustom(%q,%d)", o.name, o.cost)
	case opReportGeneric:
		return fmt.Sprintf("ReportGeneric(%q,%d)", o.name, o.cost)
	case opDoneGeneric:
		return fmt.Sprintf("DoneGeneric(%q)", o.name)
	case opBump:
		return fmt.Sprintf("Bump(%d)", o.v)
	case opDrop:
		return fmt.Sprintf("Drop(%q)", o.name)
	default:
		return "?"
	}
}

type result struct {
	errName string
	dec     Decision
}

func errName(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func applyOp(s *Selector, o op) result {
	switch o.kind {
	case opPrepare:
		return result{errName: errName(s.Prepare(o.name))}
	case opNext:
		d, err := s.Next(o.name)
		return result{errName: errName(err), dec: d}
	case opReportCustom:
		return result{errName: errName(s.ReportCustom(o.name, o.cost))}
	case opReportGeneric:
		return result{errName: errName(s.ReportGeneric(o.name, o.cost))}
	case opDoneGeneric:
		return result{errName: errName(s.DoneGeneric(o.name))}
	case opBump:
		return result{errName: errName(s.Bump(o.v))}
	case opDrop:
		return result{errName: errName(s.Drop(o.name))}
	default:
		panic("bad op")
	}
}

func generateOps(rng *rand.Rand) (int, int64, []op) {
	k := 1 + rng.Intn(5)
	p := int64(rng.Intn(20))
	names := []string{"a", "b", "c", "d"}
	n := 200 + rng.Intn(400)
	ops := make([]op, 0, n)
	nextVersion := uint64(1)
	for i := 0; i < n; i++ {
		r := rng.Intn(100)
		name := names[rng.Intn(len(names))]
		switch {
		case r < 8:
			ops = append(ops, op{kind: opPrepare, name: name})
		case r < 62:
			ops = append(ops, op{kind: opNext, name: name})
		case r < 77:
			cost := rng.Int63n(MaxCost + 1)
			switch rng.Intn(14) {
			case 0:
				cost = -1
			case 1:
				cost = MaxCost + 1
			case 2:
				cost = MaxCost
			case 3:
				cost = 0
			}
			ops = append(ops, op{kind: opReportCustom, name: name, cost: cost})
		case r < 85:
			cost := rng.Int63n(MaxCost + 1)
			if rng.Intn(10) == 0 {
				cost = MaxCost + 7
			}
			ops = append(ops, op{kind: opReportGeneric, name: name, cost: cost})
		case r < 94:
			ops = append(ops, op{kind: opDoneGeneric, name: name})
		case r < 97:
			v := nextVersion + 1 + uint64(rng.Intn(3))
			ops = append(ops, op{kind: opBump, v: v})
			nextVersion = v
		default:
			ops = append(ops, op{kind: opDrop, name: name})
		}
	}
	return k, p, ops
}

func joinLines(lines []string) string {
	out := ""
	for _, l := range lines {
		out += l + "\n"
	}
	return out
}

// decideBasis prints the statistics backing a successful Next verdict, taken
// from the reference model state *before* the pending flag is set.
func decideBasis(k, p *big.Int, st *modelStmt) string {
	gStr := "unknown"
	left, right := "-", "-"
	if st.g != nil {
		gStr = st.g.String()
		left = new(big.Int).Mul(st.g, st.c).String()
		right = new(big.Int).Add(st.sum, new(big.Int).Mul(p, st.c)).String()
	}
	return fmt.Sprintf("basis[c=%s<%s? sum=%s g=%s g*c=%s vs sum+P*c=%s]",
		st.c, k, st.sum, gStr, left, right)
}

func TestDifferentialRandom2000(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 2000-sequence differential test in -short mode")
	}
	const seeds = 2000
	for seed := int64(1); seed <= seeds; seed++ {
		rng := rand.New(rand.NewSource(seed))
		k, p, ops := generateOps(rng)
		s, err := New(k, p)
		if err != nil {
			t.Fatalf("seed=%d New: %v", seed, err)
		}
		m := newModel(k, p)

		first := make([]result, len(ops))
		var logBuf []string
		logBuf = append(logBuf, fmt.Sprintf("seed=%d K=%d P=%d ops=%d", seed, k, p, len(ops)))
		for i, o := range ops {
			var basis string
			if o.kind == opNext {
				if st, ok := m.stmts[o.name]; ok && st.pending < 0 {
					basis = decideBasis(m.k, m.p, st)
				}
			}
			g := applyOp(s, o)
			w := m.apply(o)
			if g != w {
				t.Fatalf("seed=%d op#%d %s\n got=%+v\nwant=%+v\nlog:\n%s",
					seed, i, o, g, w, joinLines(logBuf))
			}
			first[i] = g
			logBuf = append(logBuf, fmt.Sprintf("%-26s -> err=%q dec=%s %s",
				o.String(), g.errName, g.dec, basis))
		}

		// Identical replay must reproduce every decision and error.
		s2, err := New(k, p)
		if err != nil {
			t.Fatal(err)
		}
		for i, o := range ops {
			if g := applyOp(s2, o); g != first[i] {
				t.Fatalf("seed=%d replay differs at op#%d %s: got=%+v want=%+v",
					seed, i, o, g, first[i])
			}
		}

		if seed == 1 || seed == 500 || seed == 1000 || seed == 2000 {
			end := 26
			if len(logBuf) < end {
				end = len(logBuf)
			}
			t.Logf("\n--- input/output/basis log seed=%d ---\n%s",
				seed, joinLines(logBuf[:end]))
		}
	}
}

// Concurrent Next on the same statement leaves exactly one winner; the rest
// observe ErrPendingExists.
func TestConcurrentNextSameStatement(t *testing.T) {
	s, _ := New(3, 1)
	_ = s.Prepare("q")
	const n = 64
	var wg sync.WaitGroup
	var wins, busy int64
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			d, err := s.Next("q")
			switch {
			case err == nil && d == Custom:
				atomic.AddInt64(&wins, 1)
			case errorsIs(err, ErrPendingExists):
				atomic.AddInt64(&busy, 1)
			default:
				t.Errorf("unexpected Next outcome: d=%d err=%v", d, err)
			}
		}()
	}
	wg.Wait()
	if wins != 1 || busy != n-1 {
		t.Fatalf("wins=%d busy=%d, want 1 and %d", wins, busy, n-1)
	}
	// Winner still owns the pending flag; another Next loses.
	if _, err := s.Next("q"); !errorsIs(err, ErrPendingExists) {
		t.Fatalf("post-race Next: got %v", err)
	}
	if err := s.ReportCustom("q", 5); err != nil {
		t.Fatalf("winner report: %v", err)
	}
}

// Different statements advance in parallel and every pending decision is
// pairable exactly once.
func TestConcurrentDifferentStatements(t *testing.T) {
	s, _ := New(2, 1)
	const namesN = 16
	var names []string
	for i := 0; i < namesN; i++ {
		name := fmt.Sprintf("s%d", i)
		if err := s.Prepare(name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	var wg sync.WaitGroup
	for round := 0; round < 50; round++ {
		for _, name := range names {
			wg.Add(1)
			go func(name string) {
				defer wg.Done()
				d, err := s.Next(name)
				if err != nil {
					t.Errorf("Next(%s): %v", name, err)
					return
				}
				switch d {
				case Custom:
					if err := s.ReportCustom(name, 7); err != nil {
						t.Errorf("ReportCustom(%s): %v", name, err)
					}
				case BuildGeneric:
					if err := s.ReportGeneric(name, 3); err != nil {
						t.Errorf("ReportGeneric(%s): %v", name, err)
					}
				case UseGeneric:
					if err := s.DoneGeneric(name); err != nil {
						t.Errorf("DoneGeneric(%s): %v", name, err)
					}
				}
			}(name)
		}
		wg.Wait()
	}

	// No pending decisions may remain; statistics reflect exactly 50 rounds.
	total := new(big.Int)
	for _, name := range names {
		st := s.stmts[name]
		if st.pending >= 0 {
			t.Fatalf("%s still pending: %d", name, st.pending)
		}
		total.Add(total, st.c)
	}
	// Every round each statement either did Custom (c+1) or BuildGeneric/
	// UseGeneric (c unchanged). c <= 50 per statement, and total >= 2.
	if total.Sign() == 0 {
		t.Fatal("expected some custom executions")
	}
}

func errorsIs(err, target error) bool {
	if err == nil {
		return false
	}
	return err.Error() == target.Error()
}

// Naive reference model implemented independently with big.Int.
type modelStmt struct {
	version uint64
	c       *big.Int
	sum     *big.Int
	g       *big.Int
	pending Decision
}

type model struct {
	k       *big.Int
	p       *big.Int
	version uint64
	stmts   map[string]*modelStmt
}

func newModel(k int, p int64) *model {
	return &model{
		k:       big.NewInt(int64(k)),
		p:       big.NewInt(p),
		version: 0,
		stmts:   map[string]*modelStmt{},
	}
}

func modelDecide(k, p *big.Int, st *modelStmt) Decision {
	if st.c.Cmp(k) < 0 {
		return Custom
	}
	if st.g == nil {
		return BuildGeneric
	}
	left := new(big.Int).Mul(st.g, st.c)
	right := new(big.Int).Add(st.sum, new(big.Int).Mul(p, st.c))
	if left.Cmp(right) < 0 {
		return UseGeneric
	}
	return Custom
}

func (m *model) apply(o op) result {
	switch o.kind {
	case opPrepare:
		if o.name == "" {
			return result{errName: ErrEmptyName.Error()}
		}
		if _, ok := m.stmts[o.name]; ok {
			return result{errName: ErrNameExists.Error()}
		}
		m.stmts[o.name] = &modelStmt{
			version: m.version,
			c:       new(big.Int),
			sum:     new(big.Int),
			pending: -1,
		}
		return result{}
	case opNext:
		st, ok := m.stmts[o.name]
		if !ok {
			return result{errName: ErrStatementNotFound.Error()}
		}
		if st.pending >= 0 {
			return result{errName: ErrPendingExists.Error()}
		}
		d := modelDecide(m.k, m.p, st)
		st.pending = d
		return result{dec: d}
	case opReportCustom:
		st, ok := m.stmts[o.name]
		if !ok {
			return result{errName: ErrStatementNotFound.Error()}
		}
		if st.pending != Custom {
			return result{errName: ErrPendingMismatch.Error()}
		}
		if o.cost < 0 || o.cost > MaxCost {
			return result{errName: ErrCostOutOfRange.Error()}
		}
		st.c.Add(st.c, big.NewInt(1))
		st.sum.Add(st.sum, big.NewInt(o.cost))
		st.pending = -1
		return result{}
	case opReportGeneric:
		st, ok := m.stmts[o.name]
		if !ok {
			return result{errName: ErrStatementNotFound.Error()}
		}
		if st.pending != BuildGeneric {
			return result{errName: ErrPendingMismatch.Error()}
		}
		if o.cost < 0 || o.cost > MaxCost {
			return result{errName: ErrCostOutOfRange.Error()}
		}
		st.g = big.NewInt(o.cost)
		st.pending = -1
		return result{}
	case opDoneGeneric:
		st, ok := m.stmts[o.name]
		if !ok {
			return result{errName: ErrStatementNotFound.Error()}
		}
		if st.pending != UseGeneric {
			return result{errName: ErrPendingMismatch.Error()}
		}
		st.pending = -1
		return result{}
	case opBump:
		if o.v <= m.version {
			return result{errName: ErrVersionNotGreater.Error()}
		}
		for _, st := range m.stmts {
			if st.version < o.v {
				st.c.SetInt64(0)
				st.sum.SetInt64(0)
				st.g = nil
				st.pending = -1
				st.version = o.v
			}
		}
		m.version = o.v
		return result{}
	case opDrop:
		if _, ok := m.stmts[o.name]; !ok {
			return result{errName: ErrStatementNotFound.Error()}
		}
		delete(m.stmts, o.name)
		return result{}
	default:
		panic("bad op")
	}
}
