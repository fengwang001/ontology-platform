package netting

import (
	"errors"
	"reflect"
	"testing"
)

func mustReg(t *testing.T, e *Engine, id string, cap int64) {
	t.Helper()
	if err := e.Register(id, cap); err != nil {
		t.Fatalf("Register(%s,%d): %v", id, cap, err)
	}
}

func mustSubmit(t *testing.T, e *Engine, oid, from, to string, amt int64) {
	t.Helper()
	if err := e.Submit(oid, from, to, amt); err != nil {
		t.Fatalf("Submit(%s,%s,%s,%d): %v", oid, from, to, amt, err)
	}
}

func posMap(ps []Position) map[string]int64 {
	m := make(map[string]int64, len(ps))
	for _, p := range ps {
		m[p.ID] = p.Net
	}
	return m
}

func nonzeroParties(ps []Position) int {
	n := 0
	for _, p := range ps {
		if p.Net != 0 {
			n++
		}
	}
	return n
}

func TestSpecExample(t *testing.T) {
	e := NewEngine()
	mustReg(t, e, "A", 50)
	mustReg(t, e, "B", 100)
	mustReg(t, e, "C", 0)
	mustSubmit(t, e, "o1", "A", "B", 80)
	mustSubmit(t, e, "o2", "B", "C", 30)
	mustSubmit(t, e, "o3", "C", "A", 20)

	r := e.Close()
	if r.Cycle != 1 {
		t.Fatalf("cycle = %d, want 1", r.Cycle)
	}
	if !reflect.DeepEqual(r.Defaulters, [][]string{{"A"}}) {
		t.Fatalf("defaulters = %v, want [[A]]", r.Defaulters)
	}
	if !reflect.DeepEqual(r.Revoked, []string{"o1", "o3"}) {
		t.Fatalf("revoked = %v, want [o1 o3]", r.Revoked)
	}
	wantNet := map[string]int64{"A": 0, "B": -30, "C": 30}
	if !reflect.DeepEqual(posMap(r.Positions), wantNet) {
		t.Fatalf("positions = %v, want %v", posMap(r.Positions), wantNet)
	}
	if !reflect.DeepEqual(r.Instructions, []Instruction{{"B", "C", 30}}) {
		t.Fatalf("instructions = %v, want B->C 30", r.Instructions)
	}
}

func TestCapBoundary(t *testing.T) {
	e := NewEngine()
	mustReg(t, e, "A", 50)
	mustReg(t, e, "B", 0)
	mustSubmit(t, e, "x", "A", "B", 50)
	r := e.Close()
	if len(r.Defaulters) != 0 {
		t.Fatalf("equal cap: unexpected defaulters %v", r.Defaulters)
	}
	if got := posMap(r.Positions); got["A"] != -50 || got["B"] != 50 {
		t.Fatalf("equal cap positions = %v", got)
	}
	if !reflect.DeepEqual(r.Instructions, []Instruction{{"A", "B", 50}}) {
		t.Fatalf("equal cap instructions = %v", r.Instructions)
	}

	e = NewEngine()
	mustReg(t, e, "A", 50)
	mustReg(t, e, "B", 0)
	mustSubmit(t, e, "x", "A", "B", 51)
	r = e.Close()
	if !reflect.DeepEqual(r.Defaulters, [][]string{{"A"}}) {
		t.Fatalf("cap+1 defaulters = %v", r.Defaulters)
	}
	if !reflect.DeepEqual(r.Revoked, []string{"x"}) {
		t.Fatalf("cap+1 revoked = %v", r.Revoked)
	}
	for _, p := range r.Positions {
		if p.Net != 0 {
			t.Fatalf("cap+1 positions = %v, all want 0", r.Positions)
		}
	}
	if len(r.Instructions) != 0 {
		t.Fatalf("cap+1 instructions = %v, want none", r.Instructions)
	}
}

func TestCascade(t *testing.T) {
	e := NewEngine()
	mustReg(t, e, "A", 0)
	mustReg(t, e, "B", 20)
	mustReg(t, e, "C", 0)
	mustSubmit(t, e, "a1", "A", "B", 50)
	mustSubmit(t, e, "a2", "B", "A", 30)
	mustSubmit(t, e, "b1", "B", "C", 40)

	r := e.Close()
	if !reflect.DeepEqual(r.Defaulters, [][]string{{"A"}, {"B"}}) {
		t.Fatalf("defaulter rounds = %v, want [[A] [B]]", r.Defaulters)
	}
	if !reflect.DeepEqual(r.Revoked, []string{"a1", "a2", "b1"}) {
		t.Fatalf("revoked = %v", r.Revoked)
	}
	for _, p := range r.Positions {
		if p.Net != 0 {
			t.Fatalf("final positions = %v, all want 0", r.Positions)
		}
	}
	if len(r.Instructions) != 0 {
		t.Fatalf("instructions = %v, want none", r.Instructions)
	}
}

// TestSimultaneousRound proves all round-1 defaulters are identified together,
// not one by one. Setup (A cap 0, B cap 10, C cap 100):
//
//	o1 A->C 100, o2 B->A 30, o3 C->B 10
//	round-1 nets: A=-100+30=-70 breach; B=-30+10=-20 breach; C=+90 safe.
//
// Simultaneous semantics: {A,B} default in a single round and all three
// obligations are revoked; final nets are all zero.
// One-at-a-time semantics: revoke A first (o1,o2) -> only o3 survives,
// B becomes +10 and survives; so only A defaults and C->B 10 is settled.
func TestSimultaneousRound(t *testing.T) {
	parties := map[string]int64{"A": 0, "B": 10, "C": 100}
	obl := []Obligation{
		{OID: "o1", From: "A", To: "C", Amount: 100},
		{OID: "o2", From: "B", To: "A", Amount: 30},
		{OID: "o3", From: "C", To: "B", Amount: 10},
	}

	e := NewEngine()
	for _, id := range []string{"A", "B", "C"} {
		mustReg(t, e, id, parties[id])
	}
	for _, o := range obl {
		mustSubmit(t, e, o.OID, o.From, o.To, o.Amount)
	}
	r := e.Close()

	if !reflect.DeepEqual(r.Defaulters, [][]string{{"A", "B"}}) {
		t.Fatalf("engine rounds = %v, want simultaneous [A B]", r.Defaulters)
	}
	if !reflect.DeepEqual(r.Revoked, []string{"o1", "o2", "o3"}) {
		t.Fatalf("revoked = %v", r.Revoked)
	}
	for _, p := range r.Positions {
		if p.Net != 0 {
			t.Fatalf("positions = %v, all want 0", r.Positions)
		}
	}

	simRounds, simRev, simPos, simInst := naiveSettle(parties, obl, true)
	if !reflect.DeepEqual(simRounds, r.Defaulters) || !reflect.DeepEqual(simRev, r.Revoked) ||
		!reflect.DeepEqual(simPos, r.Positions) || !reflect.DeepEqual(simInst, r.Instructions) {
		t.Fatalf("engine vs simultaneous naive mismatch:\nengine rounds=%v rev=%v pos=%v inst=%v\nnaive  rounds=%v rev=%v pos=%v inst=%v",
			r.Defaulters, r.Revoked, r.Positions, r.Instructions,
			simRounds, simRev, simPos, simInst)
	}

	seqRounds, seqRev, _, seqInst := naiveSettle(parties, obl, false)
	if reflect.DeepEqual(seqRounds, r.Defaulters) {
		t.Fatalf("one-at-a-time rounds %v unexpectedly equal simultaneous", seqRounds)
	}
	if !reflect.DeepEqual(seqRounds, [][]string{{"A"}}) {
		t.Fatalf("one-at-a-time rounds = %v, want [[A]]", seqRounds)
	}
	if !reflect.DeepEqual(seqRev, []string{"o1", "o2"}) {
		t.Fatalf("one-at-a-time rev=%v, want [o1 o2]", seqRev)
	}
	if !reflect.DeepEqual(seqInst, []Instruction{{"C", "B", 10}}) {
		t.Fatalf("one-at-a-time inst=%v, want C->B 10", seqInst)
	}
}

func TestTieBreaks(t *testing.T) {
	e := NewEngine()
	for _, id := range []string{"A", "B", "C", "D"} {
		mustReg(t, e, id, 1000)
	}
	mustSubmit(t, e, "o1", "D", "B", 10)
	mustSubmit(t, e, "o2", "C", "A", 10)

	r := e.Close()
	want := []Instruction{
		{Payer: "C", Payee: "A", Amount: 10},
		{Payer: "D", Payee: "B", Amount: 10},
	}
	if !reflect.DeepEqual(r.Instructions, want) {
		t.Fatalf("instructions = %v, want %v", r.Instructions, want)
	}

	var ids []string
	for _, p := range r.Positions {
		ids = append(ids, p.ID)
	}
	if !reflect.DeepEqual(ids, []string{"A", "B", "C", "D"}) {
		t.Fatalf("position ids = %v", ids)
	}
}

func TestSinglePayerSplit(t *testing.T) {
	e := NewEngine()
	mustReg(t, e, "P", 1000)
	mustReg(t, e, "X", 1000)
	mustReg(t, e, "Y", 1000)
	mustReg(t, e, "Z", 1000)
	mustSubmit(t, e, "p1", "P", "X", 60)
	mustSubmit(t, e, "p2", "P", "Y", 30)
	mustSubmit(t, e, "p3", "P", "Z", 10)

	r := e.Close()
	want := []Instruction{
		{Payer: "P", Payee: "X", Amount: 60},
		{Payer: "P", Payee: "Y", Amount: 30},
		{Payer: "P", Payee: "Z", Amount: 10},
	}
	if !reflect.DeepEqual(r.Instructions, want) {
		t.Fatalf("instructions = %v, want %v", r.Instructions, want)
	}
	if n := nonzeroParties(r.Positions); len(r.Instructions) > n-1 {
		t.Fatalf("instruction count %d exceeds nonzero parties - 1 = %d", len(r.Instructions), n-1)
	}
}

func TestEmptyCycle(t *testing.T) {
	e := NewEngine()
	mustReg(t, e, "A", 10)
	r := e.Close()
	assertEmptyCycle(t, r, 1)
	r = e.Close()
	assertEmptyCycle(t, r, 2)
}

func assertEmptyCycle(t *testing.T, r CycleResult, wantCycle int64) {
	t.Helper()
	if r.Cycle != wantCycle {
		t.Fatalf("cycle = %d, want %d", r.Cycle, wantCycle)
	}
	if len(r.Defaulters) != 0 || len(r.Revoked) != 0 || len(r.Instructions) != 0 {
		t.Fatalf("empty cycle produced non-empty result: %+v", r)
	}
	if len(r.Positions) != 1 || r.Positions[0] != (Position{ID: "A", Net: 0}) {
		t.Fatalf("empty cycle positions = %v", r.Positions)
	}
}

func TestCycleRollAndOIDReuse(t *testing.T) {
	e := NewEngine()
	mustReg(t, e, "A", 1000)
	mustReg(t, e, "B", 1000)
	mustSubmit(t, e, "same", "A", "B", 5)
	r1 := e.Close()
	if r1.Cycle != 1 {
		t.Fatalf("first cycle = %d", r1.Cycle)
	}
	if e.CycleNo() != 2 {
		t.Fatalf("cycle no after close = %d, want 2", e.CycleNo())
	}

	// Same oid can be reused in the new cycle.
	mustSubmit(t, e, "same", "B", "A", 7)
	r2 := e.Close()
	if r2.Cycle != 2 {
		t.Fatalf("second cycle = %d", r2.Cycle)
	}
	want := map[string]int64{"A": 7, "B": -7}
	if !reflect.DeepEqual(posMap(r2.Positions), want) {
		t.Fatalf("cycle 2 positions = %v, want %v", posMap(r2.Positions), want)
	}
	if !reflect.DeepEqual(r2.Instructions, []Instruction{{"B", "A", 7}}) {
		t.Fatalf("cycle 2 instructions = %v", r2.Instructions)
	}

	// Duplicate oid within the same cycle is still rejected.
	if err := e.Submit("dup", "A", "B", 1); err != nil {
		t.Fatal(err)
	}
	if err := e.Submit("dup", "A", "B", 1); !errors.Is(err, ErrDuplicateObligation) {
		t.Fatalf("dup oid err = %v, want ErrDuplicateObligation", err)
	}
}

func TestRejections(t *testing.T) {
	t.Run("register invalid", func(t *testing.T) {
		e := NewEngine()
		cases := []struct {
			id  string
			cap int64
		}{
			{"", 0}, {"", -1}, {"A", -1}, {"A", MaxCap + 1},
		}
		for _, c := range cases {
			if err := e.Register(c.id, c.cap); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("Register(%q,%d) err=%v, want invalid", c.id, c.cap, err)
			}
		}
		if err := e.Register("A", MaxCap); err != nil {
			t.Fatalf("cap at bound: %v", err)
		}
		if err := e.Register("A", 0); !errors.Is(err, ErrDuplicateParty) {
			t.Fatalf("dup register err=%v, want duplicate", err)
		}
	})

	t.Run("submit precedence", func(t *testing.T) {
		e := NewEngine()
		mustReg(t, e, "A", 100)
		mustReg(t, e, "B", 100)

		// 1. invalid argument (empty oid, bad amount) before anything else.
		if err := e.Submit("", "A", "B", 1); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("empty oid err=%v", err)
		}
		if err := e.Submit("z", "A", "B", 0); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("amt 0 err=%v", err)
		}
		if err := e.Submit("z", "A", "B", MaxAmount+1); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("amt too large err=%v", err)
		}

		// 2. duplicate oid beats unknown party / self / full.
		mustSubmit(t, e, "d", "A", "B", 1)
		_ = e.Submit("self", "A", "A", 1) // rejected self; oid "self" free
		if err := e.Submit("d", "ZZ", "A", 1); !errors.Is(err, ErrDuplicateObligation) {
			t.Fatalf("precedence dup err=%v", err)
		}
		// A rejected self-counterparty submit must not reserve its oid:
		// reusing "self" with valid parties now succeeds.
		if err := e.Submit("self", "A", "B", 1); err != nil {
			t.Fatalf("rejected self op must not reserve oid, got %v", err)
		}

		// 3. unknown from, then unknown to.
		if err := e.Submit("x1", "ZZ", "A", 1); !errors.Is(err, ErrUnknownParty) {
			t.Fatalf("unknown from err=%v", err)
		}
		if err := e.Submit("x2", "A", "ZZ", 1); !errors.Is(err, ErrUnknownParty) {
			t.Fatalf("unknown to err=%v", err)
		}

		// 4. self counterparty.
		if err := e.Submit("x3", "A", "A", 1); !errors.Is(err, ErrSelfCounterparty) {
			t.Fatalf("self err=%v", err)
		}
	})

	t.Run("cycle full", func(t *testing.T) {
		e := NewEngine()
		mustReg(t, e, "A", 1e15)
		mustReg(t, e, "B", 1e15)
		// Shrink the limit for the test by building via the package constant
		// would be too slow; instead verify the guard path with a small limit.
		old := maxObligationsForTest
		maxObligationsForTest = 3
		defer func() { maxObligationsForTest = old }()
		mustSubmit(t, e, "k1", "A", "B", 1)
		mustSubmit(t, e, "k2", "A", "B", 1)
		mustSubmit(t, e, "k3", "A", "B", 1)
		if err := e.Submit("k4", "A", "B", 1); !errors.Is(err, ErrCycleFull) {
			t.Fatalf("full err=%v, want cycle full", err)
		}
	})

	t.Run("rejected ops do not mutate", func(t *testing.T) {
		e := NewEngine()
		mustReg(t, e, "A", 10)
		mustReg(t, e, "B", 10)
		mustSubmit(t, e, "g", "A", "B", 4)
		_ = e.Register("", 0)
		_ = e.Register("A", 1)
		_ = e.Submit("", "A", "B", 1)
		_ = e.Submit("g", "A", "B", 1)
		_ = e.Submit("q", "X", "B", 1)
		_ = e.Submit("q", "A", "X", 1)
		_ = e.Submit("q", "A", "A", 1)
		r := e.Close()
		if !reflect.DeepEqual(r.Instructions, []Instruction{{"A", "B", 4}}) {
			t.Fatalf("state changed after rejected ops: %v", r.Instructions)
		}
		if !reflect.DeepEqual(posMap(r.Positions), map[string]int64{"A": -4, "B": 4}) {
			t.Fatalf("positions after rejected ops: %v", posMap(r.Positions))
		}
	})
}
