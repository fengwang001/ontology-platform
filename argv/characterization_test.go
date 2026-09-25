package argv

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// edgeSpecs adds a short name for verbose so bundled repeats are
// expressible, plus two required flags for the required-check tests.
func edgeSpecs() []Spec {
	return []Spec{
		{Long: "all", Short: 'a', Kind: Bool},
		{Long: "output", Short: 'o', Kind: String, Default: "stdout"},
		{Long: "verbose", Short: 'v', Kind: Bool},
	}
}

// Characterization 1: a Bool long flag given "=value" silently
// ignores the value, still reports true, and does not error.
func TestBoolLongFlagWithEqualsValue(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"equals false", []string{"--verbose=false"}},
		{"equals true", []string{"--verbose=true"}},
		{"equals empty", []string{"--verbose="}},
		{"equals junk", []string{"--verbose=banana"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := Parse(edgeSpecs(), tc.args)
			if err != nil {
				t.Fatalf("Parse(%v) err = %v, want nil (value silently ignored)", tc.args, err)
			}
			if !r.Bool("verbose") {
				t.Fatalf("Parse(%v): verbose = false, want true", tc.args)
			}
			if !r.WasSet("verbose") {
				t.Fatalf("Parse(%v): WasSet(verbose) = false, want true", tc.args)
			}
			if got := r.Operands(); len(got) != 0 {
				t.Fatalf("Parse(%v): operands = %v, want none", tc.args, got)
			}
		})
	}
}

// Characterization 2: a Bool flag does NOT consume the following
// space-separated token; the token becomes a positional operand.
func TestBoolFlagDoesNotConsumeNextToken(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"long form", []string{"--verbose", "true"}},
		{"short form", []string{"-v", "false"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := Parse(edgeSpecs(), tc.args)
			if err != nil {
				t.Fatalf("Parse(%v) err = %v, want nil", tc.args, err)
			}
			if !r.Bool("verbose") {
				t.Fatalf("Parse(%v): verbose = false, want true", tc.args)
			}
			want := []string{tc.args[1]}
			if !reflect.DeepEqual(r.Operands(), want) {
				t.Fatalf("Parse(%v): operands = %v, want %v (token not consumed as value)",
					tc.args, r.Operands(), want)
			}
		})
	}
}

// Characterization 3: a String flag missing its value swallows a
// following "--" as its value, so "--" no longer terminates flag
// parsing and later flags are still parsed as flags.
func TestStringFlagSwallowsDoubleDash(t *testing.T) {
	cases := []struct {
		name         string
		args         []string
		wantOutput   string
		wantVerbose  bool
		wantOperands []string
	}{
		{"long flag eats terminator", []string{"--output", "--", "--verbose"},
			"--", true, []string{}},
		{"short flag eats terminator", []string{"-o", "--", "--verbose"},
			"--", true, []string{}},
		{"operand after eaten terminator", []string{"--output", "--", "pos"},
			"--", false, []string{"pos"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := Parse(edgeSpecs(), tc.args)
			if err != nil {
				t.Fatalf("Parse(%v) err = %v, want nil", tc.args, err)
			}
			if r.String("output") != tc.wantOutput {
				t.Fatalf("Parse(%v): output = %q, want %q (\"--\" swallowed as value)",
					tc.args, r.String("output"), tc.wantOutput)
			}
			if r.Bool("verbose") != tc.wantVerbose {
				t.Fatalf("Parse(%v): verbose = %v, want %v (terminator lost, flag still parsed)",
					tc.args, r.Bool("verbose"), tc.wantVerbose)
			}
			if !reflect.DeepEqual(r.Operands(), tc.wantOperands) {
				t.Fatalf("Parse(%v): operands = %v, want %v",
					tc.args, r.Operands(), tc.wantOperands)
			}
		})
	}
}

// Characterization 4: repeating any flag, even an idempotent Bool,
// is always ErrDuplicate; there is no idempotent-repeat semantics.
func TestRepeatedBoolFlagIsDuplicate(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"long repeated", []string{"--verbose", "--verbose"}},
		{"short bundled repeat", []string{"-vv"}},
		{"short repeated across tokens", []string{"-v", "-v"}},
		{"mixed long and short", []string{"--verbose", "-v"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := Parse(edgeSpecs(), tc.args)
			if !errors.Is(err, ErrDuplicate) {
				t.Fatalf("Parse(%v) err = %v, want ErrDuplicate", tc.args, err)
			}
			if r != nil {
				t.Fatalf("Parse(%v) Result = %+v, want nil on error", tc.args, r)
			}
		})
	}
}

// Characterization 5: when several Required flags are missing, the
// reported flag depends on map iteration order and may vary between
// calls. Pin the stable parts (ErrRequired, nil Result, message
// names one of the missing flags) and record how unstable the rest
// actually is.
func TestMultipleMissingRequiredIsNondeterministic(t *testing.T) {
	specs := []Spec{
		{Long: "alpha", Kind: String, Required: true},
		{Long: "beta", Kind: String, Required: true},
		{Long: "gamma", Kind: Bool, Required: true},
		{Long: "delta", Kind: String, Required: true},
	}
	names := []string{"alpha", "beta", "gamma", "delta"}

	seen := map[string]int{}
	const runs = 500
	for n := 0; n < runs; n++ {
		r, err := Parse(specs, nil)
		if !errors.Is(err, ErrRequired) {
			t.Fatalf("run %d: err = %v, want ErrRequired", n, err)
		}
		if r != nil {
			t.Fatalf("run %d: Result = %+v, want nil on error", n, r)
		}
		matched := ""
		for _, name := range names {
			if strings.Contains(err.Error(), "--"+name) {
				matched = name
				break
			}
		}
		if matched == "" {
			t.Fatalf("run %d: err %q names none of the missing required flags", n, err)
		}
		seen[matched]++
	}
	if len(seen) == 0 || len(seen) > len(names) {
		t.Fatalf("impossible distribution: %v", seen)
	}
	t.Logf("required-error flag distribution over %d runs: %v (distinct=%d)",
		runs, seen, len(seen))
}
