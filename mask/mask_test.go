package mask

import (
	"bytes"
	"testing"
)

func TestApply(t *testing.T) {
	cases := []struct {
		name string
		in   Value
		lv   Level
		want Value
	}{
		{"plain int", IntVal(42), LevelPlain, IntVal(42)},
		{"plain str", StrVal([]byte("abc")), LevelPlain, StrVal([]byte("abc"))},
		{"partial str 9", StrVal([]byte("123456789")), LevelPartial, StrVal([]byte("*****6789"))},
		{"partial str 5", StrVal([]byte("12345")), LevelPartial, StrVal([]byte("*2345"))},
		{"partial str 4 all star", StrVal([]byte("1234")), LevelPartial, StrVal([]byte("****"))},
		{"partial str 3 all star", StrVal([]byte("987")), LevelPartial, StrVal([]byte("***"))},
		{"partial str 1", StrVal([]byte("x")), LevelPartial, StrVal([]byte("*"))},
		{"partial int pos", IntVal(5250), LevelPartial, IntVal(5200)},
		{"partial int exact", IntVal(5200), LevelPartial, IntVal(5200)},
		{"partial int neg floor", IntVal(-250), LevelPartial, IntVal(-300)},
		{"partial int neg exact", IntVal(-300), LevelPartial, IntVal(-300)},
		{"partial int neg rem", IntVal(-201), LevelPartial, IntVal(-300)},
		{"partial int zero", IntVal(0), LevelPartial, IntVal(0)},
		{"null int stays null", NullVal(TypeInt), LevelPartial, NullVal(TypeInt)},
		{"null str stays null", NullVal(TypeStr), LevelHash, NullVal(TypeStr)},
		{"null at deny stays null", NullVal(TypeInt), LevelDeny, NullVal(TypeInt)},
		{"nullify", IntVal(7), LevelNull, NullVal(TypeInt)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Apply(tc.in, tc.lv)
			if got.Null != tc.want.Null || got.Type != tc.want.Type {
				t.Fatalf("got %+v want %+v", got, tc.want)
			}
			if !got.Null && got.Type == TypeInt && got.Int != tc.want.Int {
				t.Fatalf("int got %d want %d", got.Int, tc.want.Int)
			}
			if !got.Null && got.Type == TypeStr && !bytes.Equal(got.Str, tc.want.Str) {
				t.Fatalf("str got %q want %q", got.Str, tc.want.Str)
			}
		})
	}
}

func TestHashIsFixed16Hex(t *testing.T) {
	for _, in := range []Value{IntVal(0), IntVal(5250), IntVal(-250),
		StrVal([]byte("")), StrVal([]byte("123456789"))} {
		got := Apply(in, LevelHash)
		if got.Type != TypeStr || len(got.Str) != 16 {
			t.Fatalf("input %+v hash = %q, want 16-char str", in, got.Str)
		}
		for _, c := range got.Str {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
				t.Fatalf("non-lowercase-hex char %q in %q", c, got.Str)
			}
		}
	}
	// determinism and int decimal-text hashing
	a := Apply(IntVal(5250), LevelHash)
	b := Apply(StrVal([]byte("5250")), LevelHash)
	if !bytes.Equal(a.Str, b.Str) {
		t.Fatalf("int 5250 must hash like its decimal text: %q vs %q", a.Str, b.Str)
	}
}

func TestApplyDoesNotAliasInput(t *testing.T) {
	src := []byte("123456789")
	got := Apply(StrVal(src), LevelPlain)
	src[0] = 'Z'
	if got.Str[0] == 'Z' {
		t.Fatal("plain result aliases input bytes")
	}
}
