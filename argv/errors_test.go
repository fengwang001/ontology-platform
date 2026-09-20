package argv

import (
	"errors"
	"reflect"
	"testing"
)

func errorsIs(err, target error) bool { return errors.Is(err, target) }

// Semantics 5: errors are classified and no partial Result escapes.
func TestErrorClassification(t *testing.T) {
	specs := []Spec{
		{Long: "output", Short: 'o', Kind: String},
		{Long: "all", Short: 'a', Kind: Bool},
		{Long: "name", Kind: String, Required: true},
	}
	cases := []struct {
		name string
		args []string
		want error
	}{
		{"unknown long", []string{"--nope"}, ErrUnknownFlag},
		{"unknown short", []string{"-z"}, ErrUnknownFlag},
		{"missing long value", []string{"--output"}, ErrMissingValue},
		{"missing short value", []string{"-o"}, ErrMissingValue},
		{"duplicate string", []string{"--output=a", "--output=b"}, ErrDuplicate},
		{"duplicate bool", []string{"-a", "--all"}, ErrDuplicate},
		{"duplicate in merge", []string{"-aa"}, ErrDuplicate},
		{"required missing", []string{"--output=x"}, ErrRequired},
	}
	for _, c := range cases {
		r, err := Parse(specs, c.args)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: got err=%v, want %v", c.name, err, c.want)
		}
		if r != nil {
			t.Errorf("%s: got non-nil Result on error", c.name)
		}
	}
}

// Semantics 6: defaults are distinguishable from explicit values.
func TestDefaultVsExplicit(t *testing.T) {
	specs := []Spec{{Long: "output", Short: 'o', Kind: String, Default: "dflt"}}
	r := mustParse(t, specs, nil)
	if r.String("output") != "dflt" || r.WasSet("output") {
		t.Fatalf("unset: got %q WasSet=%v, want %q WasSet=false",
			r.String("output"), r.WasSet("output"), "dflt")
	}
	r = mustParse(t, specs, []string{"--output="})
	if r.String("output") != "" || !r.WasSet("output") {
		t.Fatalf("explicit empty: got %q WasSet=%v, want %q WasSet=true",
			r.String("output"), r.WasSet("output"), "")
	}
}

// Semantics 8: Parse mutates neither specs nor args, and repeated
// parses with the same specs are independent.
func TestNoMutationAndReuse(t *testing.T) {
	specs := []Spec{
		{Long: "output", Short: 'o', Kind: String, Default: "dflt"},
		{Long: "all", Short: 'a', Kind: Bool},
	}
	specsSnapshot := append([]Spec(nil), specs...)
	args := []string{"--output", "x", "-a", "pos"}
	argsSnapshot := append([]string(nil), args...)
	r1 := mustParse(t, specs, args)
	if r1.String("output") != "x" || !r1.Bool("all") {
		t.Fatalf("first parse wrong: %q all=%v", r1.String("output"), r1.Bool("all"))
	}
	r2 := mustParse(t, specs, []string{"other"})
	if r2.String("output") != "dflt" || r2.WasSet("output") || r2.Bool("all") {
		t.Fatalf("second parse polluted: %q WasSet=%v all=%v",
			r2.String("output"), r2.WasSet("output"), r2.Bool("all"))
	}
	if r1.String("output") != "x" {
		t.Fatalf("first result changed after second parse")
	}
	if !reflect.DeepEqual(specs, specsSnapshot) {
		t.Fatalf("specs mutated: %v", specs)
	}
	if !reflect.DeepEqual(args, argsSnapshot) {
		t.Fatalf("args mutated: %v", args)
	}
}
