package argv

import (
	"reflect"
	"testing"
)

var testSpecs = []Spec{
	{Long: "output", Short: 'o', Kind: String, Default: "out.txt"},
	{Long: "all", Short: 'a', Kind: Bool},
	{Long: "brief", Short: 'b', Kind: Bool},
	{Long: "count", Short: 'c', Kind: Bool},
}

func mustParse(t *testing.T, specs []Spec, args []string) *Result {
	t.Helper()
	r, err := Parse(specs, args)
	if err != nil {
		t.Fatalf("Parse(%v) unexpected error: %v", args, err)
	}
	if r == nil {
		t.Fatalf("Parse(%v) returned nil Result with nil error", args)
	}
	return r
}

// Semantics 1: "--output=x" equals "--output x"; "-o x" equals "-ox".
func TestAssignmentFormsEquivalent(t *testing.T) {
	cases := [][2][]string{
		{{"--output=x"}, {"--output", "x"}},
		{{"-o", "x"}, {"-ox"}},
	}
	for _, c := range cases {
		a := mustParse(t, testSpecs, c[0])
		b := mustParse(t, testSpecs, c[1])
		if a.String("output") != "x" || b.String("output") != "x" {
			t.Fatalf("%v vs %v: got %q and %q", c[0], c[1],
				a.String("output"), b.String("output"))
		}
		if a.WasSet("output") != b.WasSet("output") {
			t.Fatalf("%v vs %v: WasSet differs", c[0], c[1])
		}
	}
}

// Semantics 2: merged bool shorts; a string short in a merged group
// takes the rest of the token (or the next arg) as its value.
func TestShortMerging(t *testing.T) {
	r := mustParse(t, testSpecs, []string{"-abc"})
	if !r.Bool("all") || !r.Bool("brief") || !r.Bool("count") {
		t.Fatalf("-abc: got all=%v brief=%v count=%v",
			r.Bool("all"), r.Bool("brief"), r.Bool("count"))
	}

	r = mustParse(t, testSpecs, []string{"-abo", "x"})
	if !r.Bool("all") || !r.Bool("brief") || r.String("output") != "x" {
		t.Fatalf("-abo x: got all=%v brief=%v output=%q",
			r.Bool("all"), r.Bool("brief"), r.String("output"))
	}

	r = mustParse(t, testSpecs, []string{"-abox"})
	if !r.Bool("all") || !r.Bool("brief") || r.String("output") != "x" {
		t.Fatalf("-abox: got all=%v brief=%v output=%q",
			r.Bool("all"), r.Bool("brief"), r.String("output"))
	}
	if r.Bool("count") {
		t.Fatalf("-abox: count must stay false, nothing parsed after o")
	}
}

// Semantics 3: "--" terminates flag parsing; the rest are operands.
func TestDoubleDashTerminator(t *testing.T) {
	r := mustParse(t, testSpecs, []string{"-a", "--", "--output", "-b", "--"})
	want := []string{"--output", "-b", "--"}
	if !reflect.DeepEqual(r.Operands(), want) {
		t.Fatalf("operands: got %v want %v", r.Operands(), want)
	}
	if !r.Bool("all") || r.Bool("brief") {
		t.Fatalf("flags after -- must not be parsed")
	}
	if r.WasSet("output") {
		t.Fatalf("--output after -- must not be set")
	}
}

// Semantics 4: operands may be interleaved with flags, order kept.
func TestInterleavedOperands(t *testing.T) {
	r := mustParse(t, testSpecs, []string{"a", "--all", "b", "-c", "d"})
	want := []string{"a", "b", "d"}
	if !reflect.DeepEqual(r.Operands(), want) {
		t.Fatalf("operands: got %v want %v", r.Operands(), want)
	}
	if !r.Bool("all") || !r.Bool("count") {
		t.Fatalf("interleaved flags not parsed")
	}
}

// Semantics 7: a bare "-" is an operand; "-5"/"--5" are unknown flags.
func TestDashAndNegatives(t *testing.T) {
	r := mustParse(t, testSpecs, []string{"-", "x"})
	if !reflect.DeepEqual(r.Operands(), []string{"-", "x"}) {
		t.Fatalf("bare - must be an operand, got %v", r.Operands())
	}
	for _, args := range [][]string{{"-5"}, {"--5"}} {
		r, err := Parse(testSpecs, args)
		if err == nil || r != nil {
			t.Fatalf("Parse(%v): want unknown-flag error and nil result", args)
		}
		if !errorsIs(err, ErrUnknownFlag) {
			t.Fatalf("Parse(%v): got %v, want ErrUnknownFlag", args, err)
		}
	}
}
