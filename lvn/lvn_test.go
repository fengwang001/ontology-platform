package lvn

import (
	"errors"
	"reflect"
	"testing"
)

// run appends every instruction, logging input, output and reason,
// and fails on unexpected rejection.
func run(t *testing.T, b *Block, seq []Instr) []Result {
	t.Helper()
	out := make([]Result, 0, len(seq))
	for _, ins := range seq {
		r, err := b.Append(ins)
		if err != nil {
			t.Fatalf("Append(%s) rejected: %v", ins, err)
		}
		t.Logf("in=%-14s out=%s", ins, r)
		out = append(out, r)
	}
	return out
}

func TestCommutativeReuse(t *testing.T) {
	b := NewBlock()
	got := run(t, b, []Instr{
		Const(10), // v1
		Const(20), // v2
		Add(1, 2), // v3
		Add(2, 1), // reuse v3 (commutative)
		Mul(2, 1), // v4
		Mul(1, 2), // reuse v4 (commutative)
		Sub(1, 2), // v5
		Sub(2, 1), // v6: NOT commutative
		Add(1, 2), // reuse v3 across the SUBs
	})
	want := []Result{
		{Produces: true, Value: 1},
		{Produces: true, Value: 2},
		{Produces: true, Value: 3},
		{Produces: true, Value: 3, Reused: true},
		{Produces: true, Value: 4},
		{Produces: true, Value: 4, Reused: true},
		{Produces: true, Value: 5},
		{Produces: true, Value: 6},
		{Produces: true, Value: 3, Reused: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if n := b.ValueCount(); n != 6 {
		t.Fatalf("ValueCount = %d, want 6", n)
	}
}

func TestStoreLoadForwarding(t *testing.T) {
	b := NewBlock()
	got := run(t, b, []Instr{
		Const(7),    // v1
		Store(0, 1), // s0 := v1, version 1
		Load(0),     // forwarded v1
		Load(1),     // v2 (no known value, slot 1 ver 0 epoch 0)
		Load(1),     // reuse v2 via table
		Store(1, 1), // s1 := v1, version 1
		Load(1),     // forwarded v1
	})
	want := []Result{
		{Produces: true, Value: 1},
		{},
		{Produces: true, Value: 1, Reused: true},
		{Produces: true, Value: 2},
		{Produces: true, Value: 2, Reused: true},
		{},
		{Produces: true, Value: 1, Reused: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestRedundantStore(t *testing.T) {
	b := NewBlock()
	got := run(t, b, []Instr{
		Const(3),    // v1
		Store(0, 1), // s0 := v1, version 1
		Store(0, 1), // redundant: known value already v1, version unchanged
		Const(4),    // v2
		Store(0, 2), // s0 := v2, version 2
		Store(0, 2), // redundant again
	})
	want := []Result{
		{Produces: true, Value: 1},
		{},
		{Redundant: true},
		{Produces: true, Value: 2},
		{},
		{Redundant: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if v := b.SlotVersion(0); v != 2 {
		t.Fatalf("SlotVersion(0) = %d, want 2 (redundant stores must not bump)", v)
	}
}

func TestCallBarrier(t *testing.T) {
	b := NewBlock()
	got := run(t, b, []Instr{
		Const(1),    // v1
		Store(0, 1), // s0 := v1
		Load(0),     // forwarded v1
		Call(),      // v2, epoch 1, known values cleared
		Load(0),     // v3: known value gone, re-read with (s0, ver 1, epoch 1)
		Load(0),     // reuse v3
		Const(1),    // reuse v1: CONST key has no epoch
		Add(1, 1),   // v4
		Call(),      // v5, epoch 2
		Add(1, 1),   // reuse v4: arithmetic keys have no epoch
	})
	want := []Result{
		{Produces: true, Value: 1},
		{},
		{Produces: true, Value: 1, Reused: true},
		{Produces: true, Value: 2},
		{Produces: true, Value: 3},
		{Produces: true, Value: 3, Reused: true},
		{Produces: true, Value: 1, Reused: true},
		{Produces: true, Value: 4},
		{Produces: true, Value: 5},
		{Produces: true, Value: 4, Reused: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestCallsNeverMerge(t *testing.T) {
	b := NewBlock()
	got := run(t, b, []Instr{Call(), Call(), Call()})
	want := []Result{
		{Produces: true, Value: 1},
		{Produces: true, Value: 2},
		{Produces: true, Value: 3},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if e := b.CallEpoch(); e != 3 {
		t.Fatalf("CallEpoch = %d, want 3", e)
	}
}

func TestStoreVersionBumpsInvalidateLoadKey(t *testing.T) {
	b := NewBlock()
	got := run(t, b, []Instr{
		Const(1),    // v1
		Const(2),    // v2
		Store(0, 1), // s0 ver 1
		Call(),      // v3, epoch 1, clears known
		Load(0),     // v4: key (s0, ver 1, epoch 1)
		Store(0, 2), // s0 ver 2
		Call(),      // v5, epoch 2
		Load(0),     // v6: key (s0, ver 2, epoch 2) differs from v4's key
	})
	want := []Result{
		{Produces: true, Value: 1},
		{Produces: true, Value: 2},
		{},
		{Produces: true, Value: 3},
		{Produces: true, Value: 4},
		{},
		{Produces: true, Value: 5},
		{Produces: true, Value: 6},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestRejections(t *testing.T) {
	b := NewBlock()
	if _, err := b.Append(Const(1)); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Append(Const(2)); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		ins  Instr
		want error
	}{
		{"operand too large", Add(1, 99), ErrBadOperand},
		{"operand zero", Sub(0, 1), ErrBadOperand},
		{"operand negative", Mul(-1, 1), ErrBadOperand},
		{"store operand missing", Store(0, 42), ErrBadOperand},
		{"load negative slot", Load(-1), ErrBadSlot},
		{"store negative slot", Store(-2, 1), ErrBadSlot},
		{"operand checked before slot", Store(-1, 99), ErrBadOperand},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := b.ValueCount()
			_, err := b.Append(tc.ins)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Append(%s) err = %v, want %v", tc.ins, err, tc.want)
			}
			t.Logf("in=%s rejected: %v", tc.ins, err)
			if after := b.ValueCount(); after != before {
				t.Fatalf("rejected instruction consumed a value number: %d -> %d", before, after)
			}
		})
	}

	// State must be untouched: next numbers continue seamlessly.
	r, err := b.Append(Const(3))
	if err != nil || r.Value != 3 || r.Reused {
		t.Fatalf("after rejections Append(CONST 3) = %+v, %v; want v3 new", r, err)
	}
}

func TestSeal(t *testing.T) {
	b := NewBlock()
	if _, err := b.Append(Const(1)); err != nil {
		t.Fatal(err)
	}
	if err := b.Seal(); err != nil {
		t.Fatalf("first Seal: %v", err)
	}
	if err := b.Seal(); !errors.Is(err, ErrSealed) {
		t.Fatalf("second Seal err = %v, want ErrSealed", err)
	}
	// Sealed beats operand and slot errors.
	for _, ins := range []Instr{Const(2), Add(1, 99), Load(-1), Store(-1, 99), Call()} {
		if _, err := b.Append(ins); !errors.Is(err, ErrSealed) {
			t.Fatalf("Append(%s) after seal err = %v, want ErrSealed", ins, err)
		}
	}
	if n := b.ValueCount(); n != 1 {
		t.Fatalf("ValueCount = %d, want 1 (post-seal appends must not allocate)", n)
	}
}

func TestReplayDeterminism(t *testing.T) {
	seq := []Instr{
		Const(5), Const(9), Add(1, 2), Mul(2, 1), Store(0, 3),
		Load(0), Call(), Load(0), Sub(1, 2), Sub(2, 1), Store(0, 4),
		Store(0, 4), Load(1), Call(), Load(1), Const(5),
	}
	first := run(t, NewBlock(), seq)
	second := run(t, NewBlock(), seq)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("replay mismatch:\nfirst  %+v\nsecond %+v", first, second)
	}
}
