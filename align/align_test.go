package align

import (
	"reflect"
	"testing"

	"ontology/dist"
)

func TestScriptApply(t *testing.T) {
	cases := []struct{ a, b string }{
		{"CA", "AC"},
		{"CA", "ABC"},
		{"ab", "ba"},
		{"abc", "ca"},
		{"", "abc"},
		{"abc", ""},
		{"", ""},
		{"日本", "本日"},
		{"\xff", "\xfe"},
		{"kitten", "sitting"},
		{"abcdef", "azced"},
		{"a cat", "an act"},
	}
	for _, c := range cases {
		s, err := Script(c.a, c.b)
		if err != nil {
			t.Fatalf("Script(%q, %q): %v", c.a, c.b, err)
		}
		d, _ := dist.Distance(c.a, c.b)
		if len(s) != d {
			t.Errorf("Script(%q, %q) has %d ops, distance is %d", c.a, c.b, len(s), d)
		}
		got, err := Apply(c.a, s)
		if err != nil || got != c.b {
			t.Errorf("Apply(%q, script) = %q, %v; want %q", c.a, got, err, c.b)
		}
	}
}

func TestScriptDeterministic(t *testing.T) {
	base, err := Script("CA", "ABC")
	if err != nil {
		t.Fatal(err)
	}
	for k := 0; k < 100; k++ {
		s, err := Script("CA", "ABC")
		if err != nil || !reflect.DeepEqual(base, s) {
			t.Fatalf("run %d differs: %v vs %v", k, s, base)
		}
	}
}

func TestTransposeOp(t *testing.T) {
	s, err := Script("abc", "ca")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, op := range s {
		if op.Kind == Trans {
			found = true
		}
	}
	if !found {
		t.Errorf("Script(abc, ca) = %v, want a Trans op", s)
	}
}

func TestScriptLimitError(t *testing.T) {
	old := dist.SetMaxProduct(3)
	defer dist.SetMaxProduct(old)
	if _, err := Script("abcd", "wxyz"); err == nil {
		t.Error("want an error when the rune-count product exceeds the limit")
	}
}
