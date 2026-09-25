package argv

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// Characterization tests: these pin down the CURRENT behavior of
// edge cases not covered by parse_test.go / errors_test.go. They
// assert what the implementation does today, not what it should do.

// Edge 1: a Bool long flag given "=value" silently ignores the
// value, sets the flag to true, and reports no error.
func TestBoolLongFlagWithEqualsValue(t *testing.T) {
	values := []string{"false", "true", "0", "no", "", "anything"}
	for _, v := range values {
		arg := "--verbose=" + v
		t.Run(fmt.Sprintf("arg=%q", arg), func(t *testing.T) {
			r := mustParse(t, baseSpecs(), []string{arg})
			if !r.Bool("verbose") {
				t.Fatalf("%s: Bool(verbose) = false, want true (value ignored)", arg)
			}
			if !r.WasSet("verbose") {
				t.Fatalf("%s: WasSet(verbose) = false, want true", arg)
			}
			if len(r.Operands()) != 0 {
				t.Fatalf("%s: operands = %v, want none", arg, r.Operands())
			}
		})
	}
}

// Edge 2: a Bool flag does NOT consume the following token as its
// value; the token becomes a positional operand instead. This is
// asymmetric with String flags, which do consume the next token.
func TestBoolFlagDoesNotConsumeNextToken(t *testing.T) {
	cases := []struct {
		name string
		args []string
		flag string
	}{
		{"long bool", []string{"--verbose", "true"}, "verbose"},
		{"short bool", []string{"-a", "true"}, "all"},
		{"bundled bool tail", []string{"-ab", "true"}, "brief"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := mustParse(t, baseSpecs(), tc.args)
			if !r.Bool(tc.flag) {
				t.Fatalf("Parse(%v): Bool(%s) = false, want true", tc.args, tc.flag)
			}
			want := []string{"true"}
			if !reflect.DeepEqual(r.Operands(), want) {
				t.Fatalf("Parse(%v): operands = %v, want %v "+
					"(bool flag left the next token as an operand)",
					tc.args, r.Operands(), want)
			}
		})
	}
}

// Edge 3: a String flag missing its value swallows a following "--"
// terminator as its value. The "--" then no longer terminates flag
// parsing, so later tokens are still parsed as flags.
func TestStringFlagSwallowsDoubleDashTerminator(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"long flag", []string{"--output", "--", "--verbose"}},
		{"short flag", []string{"-o", "--", "--verbose"}},
		{"long flag trailing", []string{"--output", "--"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := mustParse(t, baseSpecs(), tc.args)
			if r.String("output") != "--" {
				t.Fatalf("Parse(%v): output = %q, want %q "+
					"(terminator consumed as value)", tc.args, r.String("output"), "--")
			}
			if len(r.Operands()) != 0 {
				t.Fatalf("Parse(%v): operands = %v, want none", tc.args, r.Operands())
			}
			trailing := len(tc.args) > 2
			if r.Bool("verbose") != trailing {
				t.Fatalf("Parse(%v): Bool(verbose) = %v, want %v "+
					"(flag after swallowed -- is still parsed as a flag)",
					tc.args, r.Bool("verbose"), trailing)
			}
		})
	}
}

// Edge 4: repeating a Bool flag is always ErrDuplicate; there is no
// idempotent-repeat semantics, even for the exact same flag form.
func TestDuplicateBoolFlagRejected(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"same long twice", []string{"--verbose", "--verbose"}},
		{"same short twice", []string{"-a", "-a"}},
		{"bundled repeat", []string{"-aa"}},
		{"bool with equals then plain", []string{"--verbose=x", "--verbose"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := Parse(baseSpecs(), tc.args)
			if !errors.Is(err, ErrDuplicate) {
				t.Fatalf("Parse(%v): err = %v, want ErrDuplicate", tc.args, err)
			}
			if r != nil {
				t.Fatalf("Parse(%v): Result = %+v, want nil on error", tc.args, r)
			}
		})
	}
}

// Edge 5: when several Required flags are missing at once, the
// returned ErrRequired names whichever flag the map iteration hits
// first, so the reported flag is not deterministic across runs. The
// only stable guarantees are: the error matches ErrRequired, the
// Result is nil, and the named flag is one of the missing ones.
func TestMultipleMissingRequiredIsNondeterministic(t *testing.T) {
	specs := []Spec{
		{Long: "alpha", Kind: String, Required: true},
		{Long: "beta", Kind: String, Required: true},
		{Long: "gamma", Kind: Bool, Required: true},
	}
	missing := map[string]bool{"alpha": true, "beta": true, "gamma": true}
	seen := make(map[string]int)
	for i := 0; i < 200; i++ {
		r, err := Parse(specs, nil)
		if !errors.Is(err, ErrRequired) {
			t.Fatalf("iteration %d: err = %v, want ErrRequired", i, err)
		}
		if r != nil {
			t.Fatalf("iteration %d: Result = %+v, want nil on error", i, r)
		}
		name := strings.TrimPrefix(err.Error(), ErrRequired.Error()+": --")
		if !missing[name] {
			t.Fatalf("iteration %d: error names %q, want one of %v", i, name, missing)
		}
		seen[name]++
	}
	t.Logf("ErrRequired flag distribution over 200 runs: %v", seen)
}
