// Command demo exercises the three-valued predicate evaluator and
// prints one OK/FAIL verdict per scenario, then a total line.
package main

import (
	"fmt"
	"math"

	"ontology"
)

var passed, total int

func check(label string, ok bool, detail string) {
	total++
	verdict := "OK  "
	if ok {
		passed++
	} else {
		verdict = "FAIL"
	}
	fmt.Printf("%s %s: %s\n", verdict, label, detail)
}

func main() {
	ev := ontology.NewEvaluator(3)
	deep := ontology.NewEvaluator(2)
	run := func(e *ontology.Evaluator, p *ontology.Predicate, props map[string]any) (ontology.Trilean, error, int) {
		r := e.NewResult()
		v, err := e.Eval(p, props, r)
		return v, err, r.LeafCount()
	}
	props := map[string]any{"n": int64(1), "f": float64(1.0), "flag": true, "name": "alice"}

	v, _, _ := run(ev, ontology.AndP(ontology.Eq("n", int64(2)), ontology.Eq("gone", 1)), props)
	check("And(False, Unknown) is False", v == ontology.False, v.String())

	v, _, _ = run(ev, ontology.AndP(ontology.Eq("n", int64(1)), ontology.Eq("gone", 1)), props)
	check("And(True, Unknown) is Unknown", v == ontology.Unknown, v.String())

	v, _, _ = run(ev, ontology.NotP(ontology.Eq("gone", 1)), props)
	check("Not(Unknown) is Unknown", v == ontology.Unknown, v.String())

	v, _, _ = run(ev, ontology.Eq("gone", 1), props)
	w, _, _ := run(ev, ontology.IsNull("gone"), props)
	check("missing attr: compare Unknown, IsNull True",
		v == ontology.Unknown && w == ontology.True, v.String()+"/"+w.String())

	_, _, n := run(ev, ontology.AndP(ontology.Eq("n", int64(2)), ontology.Eq("name", "x")), props)
	check("short-circuit skips right subtree", n == 1, fmt.Sprintf("leaves=%d", n))

	_, _, n = run(ev, ontology.AndP(ontology.Eq("gone", 1), ontology.Eq("n", int64(2))), props)
	check("Unknown does not short-circuit", n == 2, fmt.Sprintf("leaves=%d", n))

	v, _, _ = run(ev, ontology.Eq("n", float64(1.0)), props)
	w, _, _ = run(ev, ontology.Eq("f", int64(1)), props)
	check("int64(1) equals float64(1.0)", v == ontology.True && w == ontology.True, v.String())

	_, err, _ := run(ev, ontology.Lt("flag", true), props)
	check("bool Lt is a decidable type error", ontology.IsTypeError(err), fmt.Sprint(err))

	v, err, _ = run(ev, ontology.Eq("f", math.NaN()), props)
	check("NaN compare is Unknown, not error", v == ontology.Unknown && err == nil, v.String())

	_, err, _ = run(ev, ontology.AndP(ontology.Eq("n", int64(2)), ontology.Eq("name", int64(7))), props)
	check("type error in skipped subtree not reported", err == nil, "no error")

	_, err, _ = run(ev, ontology.AndP(ontology.Eq("n", int64(1)), ontology.Eq("name", int64(7))), props)
	check("type error in evaluated subtree reported", ontology.IsTypeError(err), fmt.Sprint(err))

	deepTree := ontology.NotP(ontology.NotP(ontology.Eq("n", int64(1))))
	_, err, _ = run(deep, deepTree, props)
	check("depth limit exceeded is decidable", ontology.IsDepthError(err), fmt.Sprint(err))

	fmt.Printf("TOTAL %d/%d passed\n", passed, total)
}
