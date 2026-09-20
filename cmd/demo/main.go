// Command demo checks each documented semantic of package argv and
// prints one OK/FAIL line per semantic. It always exits with code 0.
package main

import (
	"errors"
	"fmt"
	"reflect"

	"ontology/argv"
)

var specs = []argv.Spec{
	{Long: "output", Short: 'o', Kind: argv.String, Default: "dflt"},
	{Long: "all", Short: 'a', Kind: argv.Bool},
	{Long: "brief", Short: 'b', Kind: argv.Bool},
}

var failed int

func check(name string, ok bool) {
	mark := "OK  "
	if !ok {
		mark = "FAIL"
		failed++
	}
	fmt.Printf("%s %s\n", mark, name)
}

func parse(args ...string) (*argv.Result, error) {
	return argv.Parse(specs, args)
}

func main() {
	eq := func(a, b []string) bool {
		ra, _ := parse(a...)
		rb, _ := parse(b...)
		return ra.String("output") == rb.String("output") &&
			ra.WasSet("output") == rb.WasSet("output")
	}
	check("1 assign forms: --o=x==--o x, -o x==-ox",
		eq([]string{"--output=x"}, []string{"--output", "x"}) &&
			eq([]string{"-o", "x"}, []string{"-ox"}))

	r1, _ := parse("-abo", "x")
	r2, _ := parse("-abox")
	check("2 short merge: -abc bools, string suffix is value",
		r1.Bool("all") && r1.Bool("brief") && r1.String("output") == "x" &&
			r2.String("output") == "x")

	r3, _ := parse("--", "--output")
	check("3 -- terminator: rest are operands",
		reflect.DeepEqual(r3.Operands(), []string{"--output"}) && !r3.WasSet("output"))

	r4, _ := parse("a", "--all", "b", "c")
	check("4 interleaved operands keep order",
		reflect.DeepEqual(r4.Operands(), []string{"a", "b", "c"}) && r4.Bool("all"))

	_, e1 := parse("--nope")
	_, e2 := parse("--output")
	_, e3 := parse("-a", "--all")
	_, e4 := argv.Parse([]argv.Spec{{Long: "need", Kind: argv.String, Required: true}}, nil)
	nilOnErr := func(err error) bool {
		r, e := parse("--nope")
		return err != nil && r == nil && e != nil
	}
	check("5 errors: unknown/missing/dup/required, nil result",
		errors.Is(e1, argv.ErrUnknownFlag) && errors.Is(e2, argv.ErrMissingValue) &&
			errors.Is(e3, argv.ErrDuplicate) && errors.Is(e4, argv.ErrRequired) &&
			nilOnErr(e1))

	r6a, _ := parse()
	r6b, _ := parse("--output=")
	check("6 default vs explicit empty",
		r6a.String("output") == "dflt" && !r6a.WasSet("output") &&
			r6b.String("output") == "" && r6b.WasSet("output"))

	r7, _ := parse("-")
	_, e7a := parse("-5")
	_, e7b := parse("--5")
	check("7 bare - is operand; -5/--5 unknown",
		reflect.DeepEqual(r7.Operands(), []string{"-"}) &&
			errors.Is(e7a, argv.ErrUnknownFlag) && errors.Is(e7b, argv.ErrUnknownFlag))

	snap := append([]argv.Spec(nil), specs...)
	r8a, _ := parse("--output=x")
	r8b, _ := parse()
	check("8 no mutation, reusable specs",
		reflect.DeepEqual(specs, snap) && r8a.String("output") == "x" &&
			r8b.String("output") == "dflt" && !r8b.WasSet("output"))

	fmt.Printf("summary: %d failed\n", failed)
}
