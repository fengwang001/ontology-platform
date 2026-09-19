package main

import (
	"fmt"
	"sync"
	"sync/atomic"

	"ontology"
)

func main() {
	passed := 0
	check := func(name string, ok bool, detail string) {
		if ok {
			passed++
			fmt.Printf("OK %s\n", name)
			return
		}
		fmt.Printf("FAIL %s: %s\n", name, detail)
	}

	target := coercion.Target{Kind: coercion.String}
	props := map[string]any{"null": nil, "zero": ""}
	strict := coercion.New(coercion.Strict)
	missing, missingErr := strict.FromMap(props, "missing", target)
	null, nullErr := strict.FromMap(props, "null", target)
	zero, zeroErr := strict.FromMap(props, "zero", target)
	check("missing/null/zero tri-state",
		missing.IsMissing() && null.IsNull() && zero.IsPresent() &&
			category(missingErr) == coercion.Missing && category(nullErr) == coercion.Null &&
			zeroErr == nil && zero.Value == "",
		fmt.Sprintf("%v %v %v", missing.State, null.State, zero.State))

	_, overflow := strict.Convert("9223372036854775808", coercion.Target{Kind: coercion.Int64Kind})
	_, precision := strict.Convert(2.5, coercion.Target{Kind: coercion.Int64Kind})
	check("numeric overflow and precision errors",
		category(overflow) == coercion.Overflow && category(precision) == coercion.PrecisionLoss,
		fmt.Sprintf("%v | %v", overflow, precision))

	loose := coercion.New(coercion.Lenient)
	looseResult, looseErr := loose.Convert(2.5, coercion.Target{Kind: coercion.Int64Kind})
	check("strict fails and lenient records same category",
		precision != nil && looseErr == nil && looseResult.Value == int64(2) &&
			looseResult.Categories()[0] == coercion.PrecisionLoss,
		fmt.Sprintf("%v %#v", precision, looseResult))

	_, yesErr := strict.Convert("yes", coercion.Target{Kind: coercion.BoolKind})
	_, twoErr := strict.Convert(int64(2), coercion.Target{Kind: coercion.BoolKind})
	check("yes and 2 both fail bool conversion",
		category(yesErr) == coercion.Invalid && category(twoErr) == coercion.Invalid,
		fmt.Sprintf("%v | %v", yesErr, twoErr))

	_, sliceErr := strict.Convert([]any{int64(1), "bad", int64(3)},
		coercion.Target{Kind: coercion.Int64Slice})
	indexOK := false
	if errs, ok := sliceErr.(coercion.Errors); ok && len(errs) == 1 {
		indexOK = errs[0].Index == 1 && errs[0].Category == coercion.Invalid
	}
	check("slice error locates index 1", indexOK, fmt.Sprintf("%v", sliceErr))

	var failed atomic.Bool
	var wait sync.WaitGroup
	for worker := 0; worker < 50; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			result, err := loose.Convert([]any{float64(worker) + 0.25},
				coercion.Target{Kind: coercion.Int64Slice})
			if err != nil || len(result.Degradations) != 1 || result.Degradations[0].Index != 0 ||
				result.Value.([]int64)[0] != int64(worker) {
				failed.Store(true)
			}
		}(worker)
	}
	wait.Wait()
	check("concurrent lenient records stay isolated", !failed.Load(), "records crossed goroutines")

	fmt.Printf("TOTAL %d/%d checks passed\n", passed, 6)
}

func category(err error) coercion.ErrorCategory {
	category, _ := coercion.CategoryOf(err)
	return category
}
