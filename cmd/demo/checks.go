package main

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"

	"ontology/expand"
	"ontology/merge"
	"ontology/source"
	"ontology/trace"
	"ontology/validate"
)

func expandDemo() (map[string]string, error) {
	res, err := merged()
	if err != nil {
		return nil, err
	}
	vals := map[string]string{}
	for k, m := range res.Entries {
		vals[k] = m.Value
	}
	return expand.New(vals).ExpandAll()
}

func checkExpand() {
	out, err := expandDemo()
	check("expand greeting", err == nil && out["greeting"] == "hello env",
		`file "hello ${name}" + env name=env -> "hello env"`)
}

func checkCycle() {
	_, err := expand.New(map[string]string{"a": "${b}", "b": "${a}"}).Expand("a")
	ok := errors.Is(err, expand.ErrCycle) && strings.Contains(err.Error(), "a -> b -> a")
	check("cycle closed path", ok, fmt.Sprintf("%v", err))
}

func checkEscape() {
	got, err := expand.New(map[string]string{"a": "x", "v": "$${a}"}).Expand("v")
	check("escape", err == nil && got == "${a}", `$${a} stays literal`)
}

func checkUnclosed() {
	_, err := expand.New(map[string]string{"v": "ab ${a"}).Expand("v")
	ok := errors.Is(err, expand.ErrUnclosedRef) && strings.Contains(err.Error(), "byte 3")
	check("unclosed position", ok, fmt.Sprintf("%v", err))
}

func checkK20() {
	vals := map[string]string{"k0": "x"}
	refs := 0
	for i := 1; i <= 20; i++ {
		vals[fmt.Sprintf("k%d", i)] = fmt.Sprintf("${k%[1]d}${k%[1]d}", i-1)
		refs += 2
	}
	ex := expand.New(vals)
	out, err := ex.ExpandAll()
	ok := err == nil && len(out["k20"]) == 1<<20 && ex.Replacements() <= 4*refs
	check("k20 memoized", ok,
		fmt.Sprintf("replacements %d <= bound %d (naive 2^20)", ex.Replacements(), 4*refs))
}

func reportFor(order []int) (string, error) {
	l, err := layers(order)
	if err != nil {
		return "", err
	}
	res := merge.Merge(l...)
	vals := map[string]string{}
	for k, m := range res.Entries {
		vals[k] = m.Value
	}
	out, err := expand.New(vals).ExpandAll()
	if err != nil {
		return "", err
	}
	return trace.Report(trace.Build(res, out)), nil
}

func checkTraceOrder() {
	rep, err := reportFor([]int{0, 1, 2, 3})
	want := `key=who final=cli:"c0" overridden=[default:"d0",file:"f0",env:"e0"] expanded="c0"`
	check("trace four layers", err == nil && strings.Contains(rep, want),
		"overridden listed low->high: default,file,env")
}

func checkRawAndExpanded() {
	rep, err := reportFor([]int{0, 1, 2, 3})
	ok := err == nil && strings.Contains(rep, `final=file:"hello ${name}"`) &&
		strings.Contains(rep, `expanded="hello env"`)
	check("raw and expanded", ok, `report holds both "hello ${name}" and "hello env"`)
}

func checkDeterminism() {
	want, err := reportFor([]int{0, 1, 2, 3})
	ok := err == nil
	for trial := 0; trial < 20 && ok; trial++ {
		got, err := reportFor(rand.Perm(4))
		ok = err == nil && got == want
	}
	check("determinism x20", ok, "shuffled source construction, byte-identical report")
}

func checkTypeError() {
	res := merge.Merge([]source.Entry{{Key: "port", Value: "abc", Layer: source.Env}})
	err := validate.Check(map[string]validate.Rule{"port": {Type: "int"}}, res)
	var te *validate.TypeError
	ok := errors.Is(err, validate.ErrTypeMismatch) && errors.As(err, &te) &&
		te.Key == "port" && te.Want == "int" && te.Got == "abc" && te.Layer == source.Env
	check("type error fields", ok, fmt.Sprintf("%v", err))
}

func checkRequired() {
	err := validate.Check(map[string]validate.Rule{
		"m1": {Required: true},
		"m2": {Required: true},
	}, merge.Merge(nil))
	var re *validate.RequiredError
	ok := errors.Is(err, validate.ErrRequired) && errors.As(err, &re) &&
		len(re.Keys) == 2 && re.Keys[0] == "m1" && re.Keys[1] == "m2"
	check("required lists all", ok, fmt.Sprintf("%v", err))
}

func checkEnvAmbiguity() {
	_, err := source.FromEnv(source.Env, []string{"A_B_C=1", "A__B_C=2"}, "")
	ok := errors.Is(err, source.ErrAmbiguousEnv) &&
		strings.Contains(err.Error(), "A_B_C") && strings.Contains(err.Error(), "A__B_C")
	check("env ambiguity", ok, fmt.Sprintf("%v", err))
}
