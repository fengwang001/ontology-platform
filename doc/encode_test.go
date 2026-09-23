package doc

import (
	"bytes"
	"testing"
)

func TestValueEqualAndTypes(t *testing.T) {
	cases := []struct {
		name string
		a, b Value
		want bool
	}{
		{"string equal", String("x"), String("x"), true},
		{"string diff", String("x"), String("y"), false},
		{"number equal", Number(1), Number(1), true},
		{"number diff", Number(1), Number(2), false},
		{"type mismatch", String("1"), Number(1), false},
		{"empty string vs missing sentinel", String(""), String(""), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.a.Equal(c.b); got != c.want {
				t.Fatalf("Equal=%v want %v", got, c.want)
			}
		})
	}
	if String("1").TypeName() == Number(1).TypeName() {
		t.Fatal("type names must differ")
	}
}

func TestEncodeRoundTripAndOrder(t *testing.T) {
	a := Set{
		"k2": {"z": String("w"), "": String("")},
		"":   {"b": Number(1.5), "a": String("x")},
		"k1": {"f": String("v\nq")},
	}
	b := Set{
		"":   {"a": String("x"), "b": Number(1.5)},
		"k2": {"": String(""), "z": String("w")},
		"k1": {"f": String("v\nq")},
	}
	ea, eb := EncodeSet(a), EncodeSet(b)
	if !bytes.Equal(ea, eb) {
		t.Fatalf("encoding order-dependent:\n%x\n%x", ea, eb)
	}
	got, err := DecodeSet(ea)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !bytes.Equal(EncodeSet(got), ea) {
		t.Fatal("round trip not stable")
	}
	if v := got["k2"][""]; v.IsNumber() || v.AsString() != "" {
		t.Fatalf("empty-string field not preserved: %+v", v)
	}
}

func TestEncodeTruncation(t *testing.T) {
	full := EncodeSet(Set{"k": {"a": String("abc"), "n": Number(2)}})
	for n := 1; n < len(full); n++ {
		if _, err := DecodeSet(full[:n]); err == nil {
			t.Fatalf("truncation at %d accepted", n)
		}
	}
}
