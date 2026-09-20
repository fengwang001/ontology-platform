package argv

import (
	"reflect"
	"testing"
)

// baseSpecs covers bool, string-with-default and long-only flags.
func baseSpecs() []Spec {
	return []Spec{
		{Long: "all", Short: 'a', Kind: Bool},
		{Long: "brief", Short: 'b', Kind: Bool},
		{Long: "color", Short: 'c', Kind: Bool},
		{Long: "output", Short: 'o', Kind: String, Default: "stdout"},
		{Long: "verbose", Kind: Bool},
	}
}

func mustParse(t *testing.T, specs []Spec, args []string) *Result {
	t.Helper()
	r, err := Parse(specs, args)
	if err != nil {
		t.Fatalf("Parse(%v) returned error: %v", args, err)
	}
	if r == nil {
		t.Fatalf("Parse(%v) returned nil Result with nil error", args)
	}
	return r
}

// Semantics 1: "--output=x" equals "--output x"; "-o x" equals "-ox".
func TestEqualsAndSpaceFormsEquivalent(t *testing.T) {
	long1 := mustParse(t, baseSpecs(), []string{"--output=x"})
	long2 := mustParse(t, baseSpecs(), []string{"--output", "x"})
	if long1.String("output") != long2.String("output") ||
		long1.WasSet("output") != long2.WasSet("output") {
		t.Fatalf("long forms differ: %q vs %q",
			long1.String("output"), long2.String("output"))
	}

	short1 := mustParse(t, baseSpecs(), []string{"-o", "x"})
	short2 := mustParse(t, baseSpecs(), []string{"-ox"})
	if short1.String("output") != "x" || short2.String("output") != "x" {
		t.Fatalf("short forms differ: %q vs %q",
			short1.String("output"), short2.String("output"))
	}
	if long1.String("output") != short1.String("output") {
		t.Fatalf("long and short forms differ: %q vs %q",
			long1.String("output"), short1.String("output"))
	}
}

// Semantics 2: bundled bool shorts; a string short consumes the rest.
func TestShortBundling(t *testing.T) {
	r := mustParse(t, baseSpecs(), []string{"-abc"})
	for _, name := range []string{"all", "brief", "color"} {
		if !r.Bool(name) {
			t.Fatalf("-abc: %s not set", name)
		}
	}

	r = mustParse(t, baseSpecs(), []string{"-abo", "x"})
	if !r.Bool("all") || !r.Bool("brief") || r.String("output") != "x" {
		t.Fatalf("-abo x: all=%v brief=%v output=%q",
			r.Bool("all"), r.Bool("brief"), r.String("output"))
	}

	r = mustParse(t, baseSpecs(), []string{"-abox"})
	if !r.Bool("all") || !r.Bool("brief") || r.String("output") != "x" {
		t.Fatalf("-abox: all=%v brief=%v output=%q",
			r.Bool("all"), r.Bool("brief"), r.String("output"))
	}
}

// Semantics 3: "--" terminates flag parsing and is not an operand.
func TestDoubleDashTerminator(t *testing.T) {
	r := mustParse(t, baseSpecs(), []string{"--", "--output", "-a", "--"})
	want := []string{"--output", "-a", "--"}
	if !reflect.DeepEqual(r.Operands(), want) {
		t.Fatalf("operands = %v, want %v", r.Operands(), want)
	}
	if r.WasSet("output") || r.Bool("all") {
		t.Fatal("flags after -- must not be parsed")
	}
}

// Semantics 4: operands may be interleaved with flags, order kept.
func TestInterleavedOperands(t *testing.T) {
	r := mustParse(t, baseSpecs(), []string{"a", "--all", "b", "-o", "v", "c"})
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(r.Operands(), want) {
		t.Fatalf("operands = %v, want %v", r.Operands(), want)
	}
	if !r.Bool("all") || r.String("output") != "v" {
		t.Fatalf("flags broken: all=%v output=%q", r.Bool("all"), r.String("output"))
	}
}
