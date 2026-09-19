// Command demo exercises the three-valued predicate evaluator and prints
// one OK/FAIL verdict line per scenario, plus a final summary.
package main

import (
	"errors"
	"fmt"
	"math"
	"os"

	"ontology"
)

var passed, failed int

func check(name string, ok bool, detail string) {
	if ok {
		passed++
		fmt.Printf("OK   %s: %s\n", name, detail)
	} else {
		failed++
		fmt.Printf("FAIL %s: %s\n", name, detail)
	}
}

func main() {
	ev := ontology.NewEvaluator(8)
	attrs := map[string]any{"n": int64(1), "f": 1.0, "b": true, "nan": math.NaN()}
	miss := ontology.EqTo("ghost", int64(0)) // Unknown: attribute absent

	// Kleene propagation through And/Not.
	v, _ := ev.Eval(ontology.AndP(ontology.EqTo("n", int64(2)), miss), attrs)
	check("And(False,Unknown)", v == ontology.False, "result="+v.String())
	v, _ = ev.Eval(ontology.AndP(ontology.EqTo("n", int64(1)), miss), attrs)
	check("And(True,Unknown)", v == ontology.Unknown, "result="+v.String())
	v, _ = ev.Eval(ontology.NotP(miss), attrs)
	check("Not(Unknown)", v == ontology.Unknown, "result="+v.String())

	// Missing attribute vs IsNull.
	v, _ = ev.Eval(miss, attrs)
	check("missing attr cmp", v == ontology.Unknown, "result="+v.String())
	v, _ = ev.Eval(ontology.IsNull("ghost"), attrs)
	check("IsNull(missing)", v == ontology.True, "result="+v.String())

	// Short-circuit leaf counting.
	_, n, _ := ev.EvalCount(ontology.AndP(ontology.EqTo("n", int64(2)), ontology.EqTo("f", 1.0)), attrs)
	check("And(False,_) skips right", n == 1, fmt.Sprintf("leaves=%d", n))
	_, n, _ = ev.EvalCount(ontology.AndP(miss, ontology.EqTo("n", int64(2))), attrs)
	check("And(Unknown,False) no skip", n == 2, fmt.Sprintf("leaves=%d", n))

	// Numeric cross-type equality.
	v, _ = ev.Eval(ontology.EqTo("n", 1.0), attrs)
	check("int64(1)==float64(1.0)", v == ontology.True, "result="+v.String())

	// Decidable type errors.
	_, err := ev.Eval(ontology.LtTo("b", false), attrs)
	var te *ontology.TypeError
	check("bool Lt type error", errors.As(err, &te), fmt.Sprintf("err=%v", err))

	// NaN compares as Unknown, never an error.
	v, err = ev.Eval(ontology.EqTo("nan", 1.0), attrs)
	check("NaN Eq", err == nil && v == ontology.Unknown, "result="+v.String())

	// Errors in short-circuited subtrees stay unreported.
	bad := ontology.EqTo("b", int64(1)) // bool vs int64: *TypeError if evaluated
	_, n, err = ev.EvalCount(ontology.AndP(ontology.EqTo("n", int64(2)), bad), attrs)
	check("skipped err unreported", err == nil && n == 1, fmt.Sprintf("leaves=%d err=%v", n, err))

	// Depth limit is a decidable error.
	deep := ontology.NotP(ontology.NotP(ontology.NotP(ontology.NotP(ontology.NotP(miss)))))
	_, err = ontology.NewEvaluator(3).Eval(deep, attrs)
	var de *ontology.DepthError
	check("depth limit error", errors.As(err, &de), fmt.Sprintf("err=%v", err))

	fmt.Printf("TOTAL %d passed, %d failed\n", passed, failed)
	if failed > 0 {
		os.Exit(1)
	}
}
