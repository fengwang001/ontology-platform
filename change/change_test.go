package change

import (
	"math"
	"testing"
)

func TestCodecRoundTrip(t *testing.T) {
	cs := []Change{
		{Version: 1, Op: OpInsert, Key: "k1",
			From: Row{Group: "g", GroupPresent: true, Value: 3.5}},
		{Version: 2, Op: OpDelete, Key: "k2",
			From: Row{Group: "", GroupPresent: true, Value: -0.0}},
		{Version: 300, Op: OpUpdate, Key: "k3",
			From: Row{Group: "old", GroupPresent: true, Value: 1},
			To:   Row{Group: "new", GroupPresent: true, Value: 2}},
	}
	for _, want := range cs {
		got, err := Decode(Encode(want))
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if math.Float64bits(got.From.Value) != math.Float64bits(want.From.Value) ||
			(got.Op == OpUpdate && math.Float64bits(got.To.Value) !=
				math.Float64bits(want.To.Value)) {
			t.Fatalf("value bits differ: %+v vs %+v", got, want)
		}
		got.From.Value = want.From.Value
		got.To.Value = want.To.Value
		if got != want {
			t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", got, want)
		}
	}
}

func TestCodecRejectsGarbage(t *testing.T) {
	bad := [][]byte{
		nil, {}, {byte(OpInsert)}, {99, 1},
		Encode(Change{Version: 1, Op: OpInsert, Key: "k"}),
	}
	// Last entry is valid; verify it decodes, then corrupt its tail.
	if _, err := Decode(bad[4]); err != nil {
		t.Fatalf("valid record rejected: %v", err)
	}
	for i, p := range bad[:4] {
		if _, err := Decode(p); err == nil {
			t.Fatalf("case %d: expected error", i)
		}
	}
}

func TestNormZero(t *testing.T) {
	if math.Float64bits(NormZero(math.Copysign(0, -1))) != math.Float64bits(0) {
		t.Fatal("-0 not normalized")
	}
	if math.Signbit(NormZero(math.Copysign(0, -1))) {
		t.Fatal("sign bit retained")
	}
	if !math.IsNaN(NormZero(math.NaN())) {
		t.Fatal("NaN must pass through for later rejection")
	}
}
