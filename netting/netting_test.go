package netting

import (
	"errors"
	"fmt"
	"testing"
)

func mustRegister(t *testing.T, e *Engine, id string, cap int64) {
	t.Helper()
	if err := e.Register(id, cap); err != nil {
		t.Fatalf("Register(%q, %d) unexpected error: %v", id, cap, err)
	}
}

func mustSubmit(t *testing.T, e *Engine, oid, from, to string, amount int64) {
	t.Helper()
	if err := e.Submit(oid, from, to, amount); err != nil {
		t.Fatalf("Submit(%q,%q,%q,%d) unexpected error: %v", oid, from, to, amount, err)
	}
}

func positionsMap(result CloseResult) map[string]int64 {
	out := make(map[string]int64, len(result.Positions))
	for _, p := range result.Positions {
		out[p.Party] = p.Net
	}
	return out
}

func logResult(t *testing.T, name string, result CloseResult) {
	t.Helper()
	t.Logf("[%s] cycle=%d defaulters=%v revoked=%v positions=%v instructions=%v",
		name, result.Cycle, result.Defaulters, result.RevokedOIDs, result.Positions, result.Instructions)
}

// TestExample 覆盖题面示例：A 违约撤销 o1/o3，第二轮 B 付 C 30。
func TestExample(t *testing.T) {
	e := NewEngine()
	mustRegister(t, e, "A", 50)
	mustRegister(t, e, "B", 100)
	mustRegister(t, e, "C", 0)
	mustSubmit(t, e, "o1", "A", "B", 80)
	mustSubmit(t, e, "o2", "B", "C", 30)
	mustSubmit(t, e, "o3", "C", "A", 20)

	r := e.Close()
	logResult(t, "example", r)

	if r.Cycle != 1 {
		t.Fatalf("cycle = %d, want 1", r.Cycle)
	}
	if fmt.Sprint(r.Defaulters) != "[A]" {
		t.Fatalf("defaulters = %v, want [A]", r.Defaulters)
	}
	if fmt.Sprint(r.RevokedOIDs) != "[o1 o3]" {
		t.Fatalf("revoked = %v, want [o1 o3]", r.RevokedOIDs)
	}
	net := positionsMap(r)
	if net["A"] != 0 || net["B"] != -30 || net["C"] != 30 {
		t.Fatalf("positions = %v, want A=0 B=-30 C=30", net)
	}
	if len(r.Instructions) != 1 || r.Instructions[0] != (Instruction{"B", "C", 30}) {
		t.Fatalf("instructions = %v, want [B->C 30]", r.Instructions)
	}
}

// TestCapBoundary 覆盖 -net 恰等于 cap 不违约、大 1 违约。
func TestCapBoundary(t *testing.T) {
	t.Run("equal_cap_not_default", func(t *testing.T) {
		e := NewEngine()
		mustRegister(t, e, "A", 50)
		mustRegister(t, e, "B", 0)
		mustSubmit(t, e, "x", "A", "B", 50)
		r := e.Close()
		logResult(t, "equal", r)
		if len(r.Defaulters) != 0 || len(r.RevokedOIDs) != 0 {
			t.Fatalf("want no default/revoke, got def=%v revoked=%v", r.Defaulters, r.RevokedOIDs)
		}
		if len(r.Instructions) != 1 || r.Instructions[0] != (Instruction{"A", "B", 50}) {
			t.Fatalf("instructions = %v, want [A->B 50]", r.Instructions)
		}
	})

	t.Run("one_over_cap_defaults", func(t *testing.T) {
		e := NewEngine()
		mustRegister(t, e, "A", 50)
		mustRegister(t, e, "B", 0)
		mustSubmit(t, e, "x", "A", "B", 51)
		r := e.Close()
		logResult(t, "one-over", r)
		if fmt.Sprint(r.Defaulters) != "[A]" || fmt.Sprint(r.RevokedOIDs) != "[x]" {
			t.Fatalf("want def=[A] revoked=[x], got def=%v revoked=%v", r.Defaulters, r.RevokedOIDs)
		}
		net := positionsMap(r)
		if net["A"] != 0 || net["B"] != 0 || len(r.Instructions) != 0 {
			t.Fatalf("want all-zero result, got net=%v instr=%v", net, r.Instructions)
		}
	})
}

// TestCascade 覆盖违约撤销使另一方因而超限的级联。
// 第一轮 A=-60 > cap10 违约并撤销 o；第二轮 X 由 +50 转为 -10，
// -10 > cap5，X 在第二轮违约并撤销 p；Y 最终回到 0。
func TestCascade(t *testing.T) {
	e := NewEngine()
	mustRegister(t, e, "A", 10)
	mustRegister(t, e, "X", 5)
	mustRegister(t, e, "Y", 0)
	mustSubmit(t, e, "o", "A", "X", 60)
	mustSubmit(t, e, "p", "X", "Y", 10)

	r := e.Close()
	logResult(t, "cascade", r)

	if fmt.Sprint(r.Defaulters) != "[A X]" {
		t.Fatalf("defaulters = %v, want [A X] (round1 A, round2 X)", r.Defaulters)
	}
	if fmt.Sprint(r.RevokedOIDs) != "[o p]" {
		t.Fatalf("revoked = %v, want [o p]", r.RevokedOIDs)
	}
	net := positionsMap(r)
	if net["A"] != 0 || net["X"] != 0 || net["Y"] != 0 || len(r.Instructions) != 0 {
		t.Fatalf("want all-zero result, got net=%v instr=%v", net, r.Instructions)
	}
}

// TestSimultaneousRound 覆盖同一轮多个违约方同时认定而不是逐个认定。
// 第一轮快照：o1 A->C 60、o2 B->A 10；净头寸 A=-50（cap40）、
// B=-10（cap5）、C=+70，A 与 B 在同一快照上同时违约。
// 同时口径：defaulters=[A B]，o1/o2 全撤销，C 最终为 0。
// 逐个口径（按 id 先认定 A 并立即撤销重算）：B 失去义务变安全，
// defaulters 仅 [A]。两种口径结果不同。
func TestSimultaneousRound(t *testing.T) {
	e := NewEngine()
	mustRegister(t, e, "A", 40)
	mustRegister(t, e, "B", 5)
	mustRegister(t, e, "C", 0)
	mustSubmit(t, e, "o1", "A", "C", 60)
	mustSubmit(t, e, "o2", "B", "A", 10)

	r := e.Close()
	logResult(t, "simultaneous", r)

	if fmt.Sprint(r.Defaulters) != "[A B]" {
		t.Fatalf("defaulters = %v, want [A B] via simultaneous detection", r.Defaulters)
	}
	if fmt.Sprint(r.RevokedOIDs) != "[o1 o2]" {
		t.Fatalf("revoked = %v, want [o1 o2]", r.RevokedOIDs)
	}
	net := positionsMap(r)
	if net["A"] != 0 || net["B"] != 0 || net["C"] != 0 || len(r.Instructions) != 0 {
		t.Fatalf("want all-zero result, got net=%v instr=%v", net, r.Instructions)
	}
}

// TestTiePickSmallerID 覆盖付方与收方金额并列时取 id 小者。
func TestTiePickSmallerID(t *testing.T) {
	e := NewEngine()
	mustRegister(t, e, "A", 0)
	mustRegister(t, e, "B", 100)
	mustRegister(t, e, "C", 0)
	mustRegister(t, e, "D", 100)
	mustSubmit(t, e, "1", "B", "A", 30)
	mustSubmit(t, e, "2", "D", "C", 30)

	r := e.Close()
	logResult(t, "tie", r)

	want := []Instruction{{Payer: "B", Payee: "A", Amount: 30}, {Payer: "D", Payee: "C", Amount: 30}}
	if fmt.Sprint(r.Instructions) != fmt.Sprint(want) {
		t.Fatalf("instructions = %v, want %v", r.Instructions, want)
	}
}

// TestSinglePayerSplit 覆盖单个付方被拆成多条指令，付方剩余留在队首不重排。
func TestSinglePayerSplit(t *testing.T) {
	e := NewEngine()
	mustRegister(t, e, "A", 0)
	mustRegister(t, e, "B", 100)
	mustRegister(t, e, "C", 0)
	mustSubmit(t, e, "x", "B", "A", 20)
	mustSubmit(t, e, "y", "B", "C", 30)

	r := e.Close()
	logResult(t, "split", r)

	want := []Instruction{{Payer: "B", Payee: "C", Amount: 30}, {Payer: "B", Payee: "A", Amount: 20}}
	if fmt.Sprint(r.Instructions) != fmt.Sprint(want) {
		t.Fatalf("instructions = %v, want %v", r.Instructions, want)
	}
}

// TestEmptyCycle 覆盖空周期也可 Close。
func TestEmptyCycle(t *testing.T) {
	e := NewEngine()
	mustRegister(t, e, "A", 10)
	r := e.Close()
	logResult(t, "empty", r)
	if r.Cycle != 1 || len(r.Defaulters) != 0 || len(r.RevokedOIDs) != 0 ||
		len(r.Instructions) != 0 || len(r.Positions) != 1 || r.Positions[0].Net != 0 {
		t.Fatalf("unexpected empty-cycle result: %+v", r)
	}
}

// TestCycleIncrementAndOIDReuse 覆盖周期号递增、参与方保留、新周期 oid 可重用。
func TestCycleIncrementAndOIDReuse(t *testing.T) {
	e := NewEngine()
	mustRegister(t, e, "A", 100)
	mustRegister(t, e, "B", 100)
	mustSubmit(t, e, "dup", "A", "B", 10)
	r1 := e.Close()
	if r1.Cycle != 1 {
		t.Fatalf("first cycle = %d, want 1", r1.Cycle)
	}

	if err := e.Register("A", 1); !errors.Is(err, ErrDuplicateParty) {
		t.Fatalf("re-register error = %v, want ErrDuplicateParty", err)
	}
	mustSubmit(t, e, "dup", "B", "A", 5)
	r2 := e.Close()
	logResult(t, "cycle2", r2)
	if r2.Cycle != 2 {
		t.Fatalf("second cycle = %d, want 2", r2.Cycle)
	}
	net := positionsMap(r2)
	if net["A"] != 5 || net["B"] != -5 {
		t.Fatalf("cycle2 positions = %v, want A=5 B=-5", r2.Positions)
	}
	if len(r2.Instructions) != 1 || r2.Instructions[0] != (Instruction{"B", "A", 5}) {
		t.Fatalf("cycle2 instructions = %v, want [B->A 5]", r2.Instructions)
	}
}

// TestRejectionDoesNotMutate 覆盖各类被拒绝操作不改变参与方、义务与周期。
func TestRejectionDoesNotMutate(t *testing.T) {
	e := NewEngine()
	mustRegister(t, e, "A", 50)
	mustRegister(t, e, "B", 50)

	if err := e.Register("", 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty id: %v", err)
	}
	if err := e.Register("A", -1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("negative cap: %v", err)
	}
	if err := e.Register("A", MaxCap+1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("cap too large: %v", err)
	}
	if err := e.Register("A", 50); !errors.Is(err, ErrDuplicateParty) {
		t.Fatalf("duplicate: %v", err)
	}

	if err := e.Submit("", "A", "A", 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty oid: %v", err)
	}
	if err := e.Submit("z", "A", "A", 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("amt zero: %v", err)
	}
	if err := e.Submit("z", "A", "A", MaxAmount+1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("amt too large: %v", err)
	}
	mustSubmit(t, e, "dup", "A", "B", 1)
	if err := e.Submit("dup", "B", "B", 1); !errors.Is(err, ErrDuplicateOID) {
		t.Fatalf("duplicate oid must precede unknown party: %v", err)
	}
	if err := e.Submit("u1", "X", "A", 1); !errors.Is(err, ErrUnknownParty) {
		t.Fatalf("unknown from: %v", err)
	}
	if err := e.Submit("u2", "A", "Y", 1); !errors.Is(err, ErrUnknownParty) {
		t.Fatalf("unknown to: %v", err)
	}
	if err := e.Submit("self", "A", "A", 1); !errors.Is(err, ErrSelfCounterparty) {
		t.Fatalf("self counterparty: %v", err)
	}

	// 被拒绝的义务（含自对手的 self）均不存在，当前周期只有 dup 一条 A->B 义务。
	r := e.Close()
	logResult(t, "reject-state", r)
	if r.Cycle != 1 {
		t.Fatalf("cycle = %d, want 1", r.Cycle)
	}
	if len(r.Defaulters) != 0 || len(r.RevokedOIDs) != 0 {
		t.Fatalf("unexpected default/revoke: %+v", r)
	}
	net := positionsMap(r)
	if net["A"] != -1 || net["B"] != 1 {
		t.Fatalf("positions = %v, want A=-1 B=1", r.Positions)
	}
	if len(r.Instructions) != 1 || r.Instructions[0] != (Instruction{"A", "B", 1}) {
		t.Fatalf("instructions = %v, want [A->B 1]", r.Instructions)
	}
}

// TestCycleFull 覆盖周期义务数达到上限后返回 ErrCycleFull 且不新增义务。
func TestCycleFull(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped in -short mode: fills 100000 obligations")
	}
	e := NewEngine()
	mustRegister(t, e, "A", MaxCap)
	mustRegister(t, e, "B", 0)
	for i := 0; i < MaxObligationsPerCycle; i++ {
		mustSubmit(t, e, fmt.Sprintf("oid-%06d", i), "A", "B", 1)
	}
	if err := e.Submit("overflow", "A", "B", 1); !errors.Is(err, ErrCycleFull) {
		t.Fatalf("overflow error = %v, want ErrCycleFull", err)
	}
	// 溢出义务未被接受：关闭后没有 overflow 被撤销，且总额恰为上限。
	r := e.Close()
	if len(r.RevokedOIDs) != 0 {
		t.Fatalf("unexpected revoked: %v", r.RevokedOIDs)
	}
	if positionsMap(r)["B"] != MaxObligationsPerCycle {
		t.Fatalf("receiver net = %d, want %d", positionsMap(r)["B"], int64(MaxObligationsPerCycle))
	}
}
