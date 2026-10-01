package cursor

import (
	"math/big"
	"strings"
	"testing"
)

// oracle is the step-by-step naive model written directly from the rules.
// All arithmetic on k and p goes through math/big so int64 extremes are
// handled as exact integers with no wraparound.
type oracle struct {
	n      int64
	pos    *big.Int
	scroll bool
}

func newOracle(n int64, scroll bool) *oracle {
	return &oracle{n: n, pos: big.NewInt(0), scroll: scroll}
}

var (
	bigZero = big.NewInt(0)
	bigOne  = big.NewInt(1)
)

// validate mirrors the ordered rejection rules and returns the matching
// sentinel error, or nil when the operation is legal.
func (m *oracle) validate(op Op, k int64) error {
	switch op {
	case OpNext, OpPrior, OpFirst, OpLast, OpAbsolute, OpRelative, OpForward, OpBackward:
	default:
		return errInvalidOp
	}
	if !m.scroll {
		switch op {
		case OpNext, OpForward:
		case OpRelative:
			if k < 0 {
				return errForwardOnly
			}
		default:
			return errForwardOnly
		}
	}
	if (op == OpForward || op == OpBackward) && k <= 0 {
		return errNonPositiveCount
	}
	return nil
}

// fetch applies one legal operation and returns the row numbers, leaving
// the position in exactly the state prescribed by the rules.
func (m *oracle) fetch(op Op, k int64) []int64 {
	n := big.NewInt(m.n)
	switch op {
	case OpNext, OpPrior, OpFirst, OpLast, OpAbsolute, OpRelative:
		t := new(big.Int)
		switch op {
		case OpNext:
			t.Add(m.pos, bigOne)
		case OpPrior:
			t.Sub(m.pos, bigOne)
		case OpFirst:
			t.SetInt64(1)
		case OpLast:
			t.Set(n)
		case OpAbsolute:
			if k > 0 {
				t.SetInt64(k)
			} else if k < 0 {
				t.Add(new(big.Int).Add(n, bigOne), big.NewInt(k))
			}
		case OpRelative:
			t.Add(m.pos, big.NewInt(k))
		}
		if t.Cmp(bigOne) < 0 {
			m.pos.SetInt64(0)
			return nil
		}
		if t.Cmp(n) > 0 {
			m.pos.Set(new(big.Int).Add(n, bigOne))
			return nil
		}
		m.pos.Set(t)
		return []int64{t.Int64()}
	case OpForward:
		start := new(big.Int).Add(m.pos, bigOne)
		return m.forward(start, big.NewInt(k))
	case OpBackward:
		start := new(big.Int).Sub(m.pos, bigOne)
		return m.backward(start, big.NewInt(k))
	}
	return nil
}

func (m *oracle) forward(start, k *big.Int) []int64 {
	n := big.NewInt(m.n)
	if start.Cmp(bigOne) < 0 || start.Cmp(n) > 0 {
		m.pos.Set(new(big.Int).Add(n, bigOne))
		return nil
	}
	available := new(big.Int).Sub(n, start)
	available.Add(available, bigOne)
	var rows []int64
	if available.Cmp(k) < 0 {
		for r := new(big.Int).Set(start); r.Cmp(n) <= 0; r.Add(r, bigOne) {
			rows = append(rows, r.Int64())
		}
		m.pos.Set(new(big.Int).Add(n, bigOne))
		return rows
	}
	last := new(big.Int).Add(start, k)
	last.Sub(last, bigOne)
	for r := new(big.Int).Set(start); r.Cmp(last) <= 0; r.Add(r, bigOne) {
		rows = append(rows, r.Int64())
	}
	m.pos.Set(last)
	return rows
}

func (m *oracle) backward(start, k *big.Int) []int64 {
	n := big.NewInt(m.n)
	if start.Cmp(bigOne) < 0 || start.Cmp(n) > 0 {
		m.pos.SetInt64(0)
		return nil
	}
	available := new(big.Int).Set(start)
	var rows []int64
	if available.Cmp(k) < 0 {
		for r := new(big.Int).Set(start); r.Cmp(bigOne) >= 0; r.Sub(r, bigOne) {
			rows = append(rows, r.Int64())
		}
		m.pos.SetInt64(0)
		return rows
	}
	last := new(big.Int).Sub(start, k)
	last.Add(last, bigOne)
	for r := new(big.Int).Set(start); r.Cmp(last) >= 0; r.Sub(r, bigOne) {
		rows = append(rows, r.Int64())
	}
	m.pos.Set(last)
	return rows
}

func (m *oracle) position() int64 { return m.pos.Int64() }

func sameRows(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func errKind(err error) string {
	if e, ok := err.(*Error); ok {
		return e.Kind
	}
	if err == nil {
		return ""
	}
	return err.Error()
}

func verdict(ok bool) string {
	if ok {
		return "MATCH"
	}
	return "MISMATCH"
}

// mustFetch drives both the registry cursor and the naive model through one
// operation, logging input, both outputs, positions and the verdict.
func mustFetch(t *testing.T, r *Registry, name string, m *oracle, op Op, k int64, log *strings.Builder) {
	t.Helper()
	gotRows, gotErr := r.Fetch(name, op, k)
	wantErr := m.validate(op, k)
	wantPos := m.position()
	var wantRows []int64
	if wantErr == nil {
		wantRows = m.fetch(op, k)
		wantPos = m.position()
	}
	gotPos, _ := r.Position(name)

	ok := errKind(gotErr) == errKind(wantErr) && sameRows(gotRows, wantRows) && gotPos == wantPos
	if log != nil {
		log.WriteString(formatFetch(name, op, k, gotRows, gotErr, wantRows, wantErr, gotPos, wantPos, ok))
	}
	if !ok {
		t.Fatalf("mismatch for %s %s k=%d: rows got=%v want=%v, err got=%q want=%q, pos got=%d want=%d",
			name, op, k, gotRows, wantRows, errKind(gotErr), errKind(wantErr), gotPos, wantPos)
	}
}

func formatFetch(name string, op Op, k int64, gotRows []int64, gotErr error, wantRows []int64, wantErr error, gotPos, wantPos int64, ok bool) string {
	return "Fetch(name=" + name + ", op=" + string(op) + ", k=" + intToString(k) + ")" +
		" -> rows=" + rowsToString(gotRows) + " err=" + quoteString(errKind(gotErr)) +
		" | model: rows=" + rowsToString(wantRows) + " err=" + quoteString(errKind(wantErr)) +
		" | pos got=" + intToString(gotPos) + " want=" + intToString(wantPos) +
		" | " + verdict(ok) + "\n"
}
