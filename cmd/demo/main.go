package main

import (
	"errors"
	"fmt"
	"math"
	"os"

	"ontology"
)

var failures int

func check(name string, ok bool, detail string) {
	status := "OK  "
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Printf("%s %-28s %s\n", status, name, detail)
}

func main() {
	ev := ontology.NewEvaluator(8)
	attrs := map[string]any{"a": int64(1), "flag": true, "x": math.NaN(), "name": "abc"}
	trueLeaf := ontology.Comparison{Op: ontology.Eq, Attr: "a", Value: int64(1)}
	falseLeaf := ontology.Comparison{Op: ontology.Eq, Attr: "a", Value: int64(2)}
	unknownLeaf := ontology.Comparison{Op: ontology.Eq, Attr: "missing", Value: int64(1)}

	eval := func(p ontology.Predicate) ontology.Result {
		r, err := ev.Evaluate(p, attrs)
		if err != nil {
			return ontology.Result{Value: ontology.Unknown, Leaves: -1}
		}
		return r
	}

	r := eval(ontology.AndPred{Left: falseLeaf, Right: unknownLeaf})
	check("And(False,Unknown)=False", r.Value == ontology.False, r.Value.String())
	r = eval(ontology.AndPred{Left: trueLeaf, Right: unknownLeaf})
	check("And(True,Unknown)=Unknown", r.Value == ontology.Unknown, r.Value.String())
	r = eval(ontology.NotPred{Child: unknownLeaf})
	check("Not(Unknown)=Unknown", r.Value == ontology.Unknown, r.Value.String())

	r = eval(ontology.Comparison{Op: ontology.Gt, Attr: "missing", Value: int64(0)})
	check("missing attr -> Unknown", r.Value == ontology.Unknown, r.Value.String())
	r = eval(ontology.IsNullPred{Attr: "missing"})
	check("IsNull(missing) -> True", r.Value == ontology.True, r.Value.String())

	r = eval(ontology.AndPred{Left: falseLeaf, Right: ontology.AndPred{Left: trueLeaf, Right: trueLeaf}})
	check("short-circuit leaves=1", r.Leaves == 1, fmt.Sprintf("leaves=%d", r.Leaves))
	r = eval(ontology.AndPred{Left: unknownLeaf, Right: falseLeaf})
	check("Unknown no short-circuit", r.Value == ontology.False && r.Leaves == 2,
		fmt.Sprintf("value=%s leaves=%d", r.Value, r.Leaves))

	r = eval(ontology.Comparison{Op: ontology.Eq, Attr: "a", Value: float64(1.0)})
	check("int64(1)==float64(1.0)", r.Value == ontology.True, r.Value.String())

	_, err := ev.Evaluate(ontology.Comparison{Op: ontology.Lt, Attr: "flag", Value: false}, attrs)
	var terr *ontology.TypeError
	check("bool Lt -> TypeError", errors.As(err, &terr), fmt.Sprint(err))

	r = eval(ontology.Comparison{Op: ontology.Eq, Attr: "x", Value: 1.0})
	check("NaN Eq -> Unknown", r.Value == ontology.Unknown, r.Value.String())

	bad := ontology.Comparison{Op: ontology.Eq, Attr: "name", Value: int64(1)}
	r2, err := ev.Evaluate(ontology.AndPred{Left: falseLeaf, Right: bad}, attrs)
	check("skipped error not reported", err == nil && r2.Value == ontology.False && r2.Leaves == 1,
		fmt.Sprintf("err=%v leaves=%d", err, r2.Leaves))

	deep := ontology.Predicate(ontology.NotPred{Child: ontology.NotPred{Child: trueLeaf}})
	_, err = ontology.NewEvaluator(2).Evaluate(deep, attrs)
	var derr *ontology.DepthError
	check("depth limit -> DepthError", errors.As(err, &derr), fmt.Sprint(err))

	fmt.Printf("TOTAL: %d checks, %d failed\n", 13, failures)
	if failures > 0 {
		os.Exit(1)
	}
}
