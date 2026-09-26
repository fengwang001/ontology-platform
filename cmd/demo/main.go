package main

import (
	"errors"
	"fmt"

	"ontology/aggregate"
	"ontology/errdef"
	errmap "ontology/map"
	"ontology/wrap"
)

type check struct {
	name string
	ok   bool
}

func main() {
	var results []check

	results = append(results, check{"skeleton runs", true})

	nf := errdef.New(errdef.CodeNotFound, "object missing",
		errdef.WithObject("Person"))
	results = append(results,
		check{"typed error matches its sentinel", errors.Is(nf, errdef.ErrNotFound)},
		check{"concrete error Is-A ErrBase", errors.Is(nf, errdef.ErrBase)},
		check{"different concrete code does not match",
			!errors.Is(nf, errdef.ErrConflict)},
	)

	wrapped := wrap.Wrap(nf, wrap.WithObject("Person"), wrap.WithProperty("age"))
	var typed *errdef.Error
	counter100, counter10000 := -1, -1
	{
		err := error(errdef.New(errdef.CodeNotFound, "not found"))
		for i := 0; i < 100; i++ {
			err = wrap.Wrap(err, wrap.WithObject(fmt.Sprintf("o%d", i)))
		}
		errdef.ResetCompareCount()
		if errors.Is(err, errdef.ErrNotFound) {
			counter100 = errdef.IsCompareCount()
		}
	}
	{
		err := error(errdef.New(errdef.CodeNotFound, "not found"))
		for i := 0; i < 10000; i++ {
			err = wrap.Wrap(err)
		}
		errdef.ResetCompareCount()
		if errors.Is(err, errdef.ErrNotFound) {
			counter10000 = errdef.IsCompareCount()
		}
	}
	results = append(results,
		check{"wrapped Is pierces context to original type",
			errors.Is(wrapped, errdef.ErrNotFound)},
		check{"wrapped Is still matches base class",
			errors.Is(wrapped, errdef.ErrBase)},
		check{"errors.As reaches typed error through wrap",
			errors.As(wrapped, &typed) && typed.Code() == errdef.CodeNotFound},
		check{"Unwrap exposes the cause",
			wrap.Unwrap(wrapped) == error(nf)},
		check{"wrapper carries object/property context",
			wrapped.Error() != "" && typed.Object() == "Person"},
		check{"compare count does not explode (100 layers)", counter100 == 1},
		check{"compare count does not explode (10000 layers)", counter10000 == 1},
	)

	e1 := errdef.New(errdef.CodeNotFound, "missing", errdef.WithObject("Person"))
	e2 := errdef.New(errdef.CodeConflict, "version clash", errdef.WithObject("Order"))
	agg := aggregate.New(e1, e2)
	indexOK := agg.Len() == 2
	orderOK := true
	agg.Range(func(i int, err error) bool {
		want := []error{e1, e2}[i]
		if err != want {
			orderOK = false
		}
		at, ok := agg.At(i)
		if !ok || at != want {
			indexOK = false
		}
		return true
	})
	if _, ok := agg.At(2); ok {
		indexOK = false
	}
	nested := aggregate.New(aggregate.New(
		errdef.New(errdef.CodeInternal, "boom")), e2)
	results = append(results,
		check{"aggregate Is matches any child (conflict)",
			errors.Is(agg, errdef.ErrConflict)},
		check{"aggregate Is matches any child (not found)",
			errors.Is(agg, errdef.ErrNotFound)},
		check{"aggregate Is false when no child matches",
			!errors.Is(agg, errdef.ErrValidation)},
		check{"aggregate is indexable and ordered", indexOK && orderOK},
		check{"nested aggregate Is still matches",
			errors.Is(nested, errdef.ErrInternal)},
		check{"empty aggregate is nil", aggregate.New(nil, nil) == nil},
	)

	httpOK := true
	statusCases := []struct {
		err    error
		status int
	}{
		{errdef.ErrNotFound, 404},
		{errdef.ErrAlreadyExists, 409},
		{errdef.ErrConflict, 409},
		{errdef.ErrValidation, 400},
		{errdef.ErrPermissionDenied, 403},
		{errdef.ErrInternal, 500},
		{errors.New("plain"), 500},
		{agg, 404},
	}
	for _, c := range statusCases {
		if errmap.HTTPStatus(c.err) != c.status {
			httpOK = false
		}
	}

	root := errdef.New(errdef.CodeNotFound, "missing",
		errdef.WithObject("Person"), errdef.WithProperty("age"),
		errdef.WithCause(errdef.New(errdef.CodeInternal, "db down")))
	rich := wrap.Wrap(root, wrap.WithObject("Batch"), wrap.WithProperty("42"))
	data, err := errmap.Marshal(rich)
	if err != nil {
		panic(err)
	}
	back, err := errmap.Unmarshal(data)
	if err != nil {
		panic(err)
	}
	var backTyped *errdef.Error
	rtOK := errors.As(back, &backTyped) &&
		errors.Is(back, errdef.ErrNotFound) &&
		errors.Is(back, errdef.ErrBase) &&
		!errors.Is(back, errdef.ErrConflict) &&
		backTyped.Object() == "Person" &&
		backTyped.Property() == "age" &&
		errdef.CauseDepth(back) == errdef.CauseDepth(rich)

	future := errdef.NewUnknown("future_quota_exceeded", "nope")
	fdata, _ := errmap.Marshal(future)
	fback, ferr := errmap.Unmarshal(fdata)
	unknownOK := ferr == nil && fback != nil &&
		errors.Is(fback, errdef.ErrBase) && !errors.Is(fback, errdef.ErrNotFound)
	var ftyped *errdef.Error
	if errors.As(fback, &ftyped) {
		unknownOK = unknownOK && ftyped.RawCode() == "future_quota_exceeded"
	}

	aggData, _ := errmap.Marshal(agg)
	aggBack, _ := errmap.Unmarshal(aggData)
	rtOK = rtOK && errors.Is(aggBack, errdef.ErrConflict)

	results = append(results,
		check{"HTTP status mapping (all types + aggregate)", httpOK},
		check{"unknown/plain error degrades to 500",
			errmap.HTTPStatus(errors.New("x")) == 500},
		check{"round-trip preserves Is, context and cause depth", rtOK},
		check{"unknown code unmarshals without panic", unknownOK},
	)

	failed := 0
	for _, r := range results {
		mark := "OK"
		if !r.ok {
			mark = "FAIL"
			failed++
		}
		fmt.Printf("%-48s %s\n", r.name, mark)
	}
	if failed > 0 {
		fmt.Printf("%d check(s) failed\n", failed)
	}
}
