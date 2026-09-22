package ontology

import (
	"bytes"
	"math"
	"math/rand"
	"reflect"
	"testing"
)

// normalize maps -0.0 to 0.0: they compare equal, so they share one
// encoding and decode as 0.0.
func normalize(row []any) []any {
	for i, k := range row {
		if f, ok := k.(float64); ok && f == 0 {
			row[i] = float64(0)
		}
	}
	return row
}

func TestRoundtripRandom(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for iter := 0; iter < 40000; iter++ {
		n := 1 + r.Intn(5)
		desc := make([]bool, n)
		for i := range desc {
			desc[i] = r.Intn(2) == 0
		}
		row := normalize(randRow(r, n, false))
		enc := NewEncoder(desc)
		back, err := Decode(desc, enc.Encode(row))
		if err != nil {
			t.Fatalf("iter %d: decode: %v", iter, err)
		}
		if !reflect.DeepEqual(row, back) {
			t.Fatalf("iter %d: got %#v want %#v", iter, back, row)
		}
	}
}

func TestRoundtripPreservesTypes(t *testing.T) {
	row := []any{int64(3), float64(3), int64(0), float64(0),
		int64(-3), float64(-3), int64(math.MinInt64), float64(math.MaxFloat64)}
	back, err := Decode(nil, NewEncoder(nil).Encode(row))
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range row {
		if reflect.TypeOf(back[i]) != reflect.TypeOf(want) {
			t.Fatalf("key %d: type %T, want %T", i, back[i], want)
		}
		if back[i] != want {
			t.Fatalf("key %d: got %v want %v", i, back[i], want)
		}
	}
}

func TestStringByteContentRoundtrip(t *testing.T) {
	rows := [][]any{
		{"\x00", "\xff", "\x00\xff\x00", ""},
		{"", "x"},
		{"", ""},
		{"a\x00b", "a", "\xff\xff"},
		{"\x00"},
	}
	for _, row := range rows {
		back, err := Decode(nil, NewEncoder(nil).Encode(row))
		if err != nil {
			t.Fatalf("%q: %v", row, err)
		}
		if !reflect.DeepEqual(row, back) {
			t.Fatalf("got %#v want %#v", back, row)
		}
	}
}

func TestEmptyStringThenNonEmptyKey(t *testing.T) {
	enc := NewEncoder(nil)
	row := []any{"", "rest"}
	back, err := Decode(nil, enc.Encode(row))
	if err != nil || !reflect.DeepEqual(row, back) {
		t.Fatalf("got %v %v", back, err)
	}
	if bytes.Compare(enc.Encode([]any{"", "rest"}), enc.Encode([]any{"\x01"})) >= 0 {
		t.Fatal(`"" followed by a key must sort before "\x01"`)
	}
}

func TestEncodeDeterministic(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	for iter := 0; iter < 2000; iter++ {
		row := randRow(r, 1+r.Intn(4), true)
		a := NewEncoder(nil).Encode(row)
		b := NewEncoder(nil).Encode(row)
		if !bytes.Equal(a, b) {
			t.Fatalf("iter %d: encoding not deterministic for %v", iter, row)
		}
	}
}

func TestSmallIntEncodedLen(t *testing.T) {
	enc := NewEncoder(nil)
	for v := int64(-127); v <= 127; v++ {
		if n := enc.EncodedLen([]any{v}); n >= 8 {
			t.Fatalf("int64(%d) encoded to %d bytes, want < 8", v, n)
		}
	}
	if n := enc.EncodedLen([]any{int64(1)}); n > 6 {
		t.Fatalf("int64(1) encoded to %d bytes, want <= 6", n)
	}
}

func TestEncodeDoesNotMutateInput(t *testing.T) {
	row := []any{int64(-5), 2.5, "a\x00b", nil, "\xff"}
	snapshot := make([]any, len(row))
	copy(snapshot, row)
	NewEncoder([]bool{true, false, true, false, true}).Encode(row)
	if !reflect.DeepEqual(row, snapshot) {
		t.Fatalf("input slice modified: %v", row)
	}
}
