package argv

import (
	"errors"
	"reflect"
	"testing"
)

// Semantics 5: errors are classified and never return a half Result.
func TestErrorClassification(t *testing.T) {
	cases := []struct {
		name  string
		specs []Spec
		args  []string
		want  error
	}{
		{"unknown long", baseSpecs(), []string{"--nope"}, ErrUnknownFlag},
		{"unknown short", baseSpecs(), []string{"-z"}, ErrUnknownFlag},
		{"unknown in bundle", baseSpecs(), []string{"-abz"}, ErrUnknownFlag},
		{"missing long value", baseSpecs(), []string{"--output"}, ErrMissingValue},
		{"missing short value", baseSpecs(), []string{"-o"}, ErrMissingValue},
		{"duplicate bool", baseSpecs(), []string{"--all", "-a"}, ErrDuplicate},
		{"duplicate string", baseSpecs(), []string{"-ox", "--output=y"}, ErrDuplicate},
		{"required missing", []Spec{{Long: "name", Kind: String, Required: true}},
			[]string{}, ErrRequired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := Parse(tc.specs, tc.args)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if r != nil {
				t.Fatalf("Result = %+v, want nil on error", r)
			}
		})
	}
}

// Semantics 6: defaults and explicit empty values are distinguishable.
func TestDefaultVsExplicitEmpty(t *testing.T) {
	r := mustParse(t, baseSpecs(), nil)
	if r.String("output") != "stdout" || r.WasSet("output") {
		t.Fatalf("default: String=%q WasSet=%v", r.String("output"), r.WasSet("output"))
	}

	r = mustParse(t, baseSpecs(), []string{"--output="})
	if r.String("output") != "" || !r.WasSet("output") {
		t.Fatalf("explicit empty: String=%q WasSet=%v", r.String("output"), r.WasSet("output"))
	}
}

// Semantics 7: lone "-" is an operand; "-5"/"--5" are unknown flags.
func TestDashAndNegativeNumbers(t *testing.T) {
	r := mustParse(t, baseSpecs(), []string{"-", "x"})
	if want := []string{"-", "x"}; !reflect.DeepEqual(r.Operands(), want) {
		t.Fatalf("operands = %v, want %v", r.Operands(), want)
	}
	for _, args := range [][]string{{"-5"}, {"--5"}} {
		r, err := Parse(baseSpecs(), args)
		if !errors.Is(err, ErrUnknownFlag) {
			t.Fatalf("Parse(%v) err = %v, want ErrUnknownFlag", args, err)
		}
		if r != nil {
			t.Fatalf("Parse(%v) Result = %+v, want nil", args, r)
		}
	}
}

// Semantics 8: Parse mutates nothing and calls are independent.
func TestNoMutationAndRepeatable(t *testing.T) {
	specs := baseSpecs()
	specsSnapshot := append([]Spec(nil), specs...)
	args := []string{"--output=x", "-a", "pos"}
	argsSnapshot := append([]string(nil), args...)

	r1 := mustParse(t, specs, args)
	if r1.String("output") != "x" || !r1.Bool("all") {
		t.Fatalf("first parse wrong: output=%q all=%v", r1.String("output"), r1.Bool("all"))
	}
	if !reflect.DeepEqual(specs, specsSnapshot) {
		t.Fatalf("specs mutated: %v", specs)
	}
	if !reflect.DeepEqual(args, argsSnapshot) {
		t.Fatalf("args mutated: %v", args)
	}

	r2 := mustParse(t, specs, []string{"--verbose"})
	if r2.String("output") != "stdout" || r2.WasSet("output") {
		t.Fatalf("default polluted by previous parse: %q", r2.String("output"))
	}
	if r2.Bool("all") || !r2.Bool("verbose") {
		t.Fatalf("bool state leaked: all=%v verbose=%v", r2.Bool("all"), r2.Bool("verbose"))
	}
	if len(r2.Operands()) != 0 {
		t.Fatalf("operands leaked: %v", r2.Operands())
	}
}
