// Command demo checks each documented semantic of package argv and
// prints one OK/FAIL line per rule. It always exits with code 0.
package main

import (
	"errors"
	"fmt"
	"reflect"

	"ontology/argv"
)

var specs = []argv.Spec{
	{Long: "all", Short: 'a', Kind: argv.Bool},
	{Long: "brief", Short: 'b', Kind: argv.Bool},
	{Long: "color", Short: 'c', Kind: argv.Bool},
	{Long: "output", Short: 'o', Kind: argv.String, Default: "stdout"},
	{Long: "name", Short: 'n', Kind: argv.String, Required: true},
}

func parse(args ...string) (*argv.Result, error) {
	return argv.Parse(specs, args)
}

func main() {
	checks := []struct {
		name string
		ok   func() bool
	}{
		{"1 equals/space forms equivalent", checkEquivalentForms},
		{"2 short bundling", checkBundling},
		{"3 double-dash terminator", checkTerminator},
		{"4 interleaved operands", checkInterleaved},
		{"5 classified errors, nil result", checkErrors},
		{"6 default vs explicit empty", checkDefault},
		{"7 lone dash / negative numbers", checkDash},
		{"8 no mutation, repeatable", checkRepeatable},
	}
	for _, c := range checks {
		verdict := "OK"
		if !c.ok() {
			verdict = "FAIL"
		}
		fmt.Printf("%-36s %s\n", c.name, verdict)
	}
}

func checkEquivalentForms() bool {
	r1, e1 := parse("--output=x", "-n", "n1")
	r2, e2 := parse("--output", "x", "-nn1")
	return e1 == nil && e2 == nil &&
		r1.String("output") == "x" && r2.String("output") == "x" &&
		r1.String("name") == "n1" && r2.String("name") == "n1"
}

func checkBundling() bool {
	r1, e1 := parse("-abc", "-n", "n1")
	r2, e2 := parse("-abo", "x", "-n", "n1")
	r3, e3 := parse("-abox", "-n", "n1")
	return e1 == nil && e2 == nil && e3 == nil &&
		r1.Bool("all") && r1.Bool("brief") && r1.Bool("color") &&
		r2.Bool("all") && r2.Bool("brief") && r2.String("output") == "x" &&
		r3.String("output") == "x"
}

func checkTerminator() bool {
	r, err := parse("-n", "n1", "--", "--output", "-a")
	return err == nil && !r.WasSet("output") && !r.Bool("all") &&
		reflect.DeepEqual(r.Operands(), []string{"--output", "-a"})
}

func checkInterleaved() bool {
	r, err := parse("a", "--all", "b", "-n", "n1", "c")
	return err == nil && r.Bool("all") &&
		reflect.DeepEqual(r.Operands(), []string{"a", "b", "c"})
}

func checkErrors() bool {
	cases := []struct {
		args []string
		want error
	}{
		{[]string{"--nope"}, argv.ErrUnknownFlag},
		{[]string{"--output"}, argv.ErrMissingValue},
		{[]string{"-a", "--all"}, argv.ErrDuplicate},
		{[]string{}, argv.ErrRequired},
	}
	for _, tc := range cases {
		r, err := parse(tc.args...)
		if !errors.Is(err, tc.want) || r != nil {
			return false
		}
	}
	return true
}

func checkDefault() bool {
	r1, e1 := parse("-n", "n1")
	r2, e2 := parse("-n", "n1", "--output=")
	return e1 == nil && e2 == nil &&
		r1.String("output") == "stdout" && !r1.WasSet("output") &&
		r2.String("output") == "" && r2.WasSet("output")
}

func checkDash() bool {
	r, err := parse("-n", "n1", "-")
	if err != nil || !reflect.DeepEqual(r.Operands(), []string{"-"}) {
		return false
	}
	for _, args := range [][]string{{"-5"}, {"--5"}} {
		if r, err := parse(args...); !errors.Is(err, argv.ErrUnknownFlag) || r != nil {
			return false
		}
	}
	return true
}

func checkRepeatable() bool {
	snapshot := append([]argv.Spec(nil), specs...)
	args := []string{"-n", "n1", "--output=x"}
	if _, err := parse(args...); err != nil {
		return false
	}
	if !reflect.DeepEqual(specs, snapshot) ||
		!reflect.DeepEqual(args, []string{"-n", "n1", "--output=x"}) {
		return false
	}
	r, err := parse("-n", "n2")
	return err == nil && r.String("output") == "stdout" && !r.WasSet("output")
}
