package main

import (
	"errors"
	"fmt"

	"ontology/aggregate"
	"ontology/errdef"
	"ontology/maperr"
	"ontology/wrap"
)

type check struct {
	name string
	ok   bool
}

func main() {
	failed := 0
	checks := []check{
		{"typed error Is its concrete type", errors.Is(
			errdef.NotFound("missing", errdef.WithObject("u1")), errdef.ErrNotFound)},
		{"typed error Is wide base ErrBase", errors.Is(errdef.Conflict("x"), errdef.ErrBase)},
		{"Is traverses cause chain", func() bool {
			deep := errdef.NotFound("root", errdef.WithObject("o"))
			for range 9 {
				deep = errdef.Internal("layer", errdef.WithCause(deep))
			}
			return errors.Is(deep, errdef.ErrNotFound)
		}()},
		{"Is comparison count constant at depth 10000", func() bool {
			var e error = errdef.NotFound("deep")
			for range 9999 {
				e = errdef.NotFound("layer", errdef.WithCause(e))
			}
			errdef.ResetSteps()
			ok := errors.Is(e, errdef.ErrNotFound)
			return ok && errdef.Steps() == 1
		}()},
		{"Wrap keeps type: Is pierces context layers", errors.Is(
			wrap.Wrap(wrap.Wrap(errdef.NotFound("x"), wrap.Ctx{Object: "o"}),
				wrap.Ctx{Property: "p"}), errdef.ErrNotFound)},
		{"Wrap steps constant at 10000 context layers", func() bool {
			var e error = errdef.NotFound("deep")
			for range 10000 {
				e = wrap.Wrap(e, wrap.Ctx{Object: "o"})
			}
			wrap.ResetSteps()
			ok := errors.Is(e, errdef.ErrNotFound)
			return ok && wrap.Steps() == 1
		}()},
		{"aggregate Is matches any child", func() bool {
			agg := aggregate.New(
				errdef.NotFound("a"), errdef.Conflict("b"))
			return errors.Is(agg, errdef.ErrConflict) &&
				errors.Is(agg, errdef.ErrNotFound) &&
				!errors.Is(agg, errdef.ErrValidation)
		}()},
		{"aggregate indexable, ordered, loses no child", func() bool {
			in := []error{
				errdef.NotFound("first", errdef.WithObject("o1")),
				errdef.Conflict("second", errdef.WithObject("o2")),
				errdef.Validation("third"),
			}
			a := aggregate.New(in...).(aggregate.Aggregate)
			if a.Len() != len(in) {
				return false
			}
			for i := range in {
				if a.At(i).Error() != in[i].Error() {
					return false
				}
			}
			return true
		}()},
		{"HTTP status mapping by type", func() bool {
			return maperr.HTTPStatus(errdef.NotFound("x")) == 404 &&
				maperr.HTTPStatus(errdef.Validation("x")) == 400 &&
				maperr.HTTPStatus(errdef.PermissionDenied("x")) == 403 &&
				maperr.HTTPStatus(errdef.Conflict("x")) == 409
		}()},
		{"unknown type degrades to 500", func() bool {
			return maperr.HTTPStatus(errdef.New(errdef.Code("mystery"), "x")) == 500
		}()},
		{"round-trip preserves Is type fidelity", func() bool {
			e := wrap.Wrap(
				errdef.NotFound("x", errdef.WithObject("o"), errdef.WithProperty("p")),
				wrap.Ctx{Object: "outer"})
			data, err := maperr.Marshal(e)
			if err != nil {
				return false
			}
			got, err := maperr.Unmarshal(data)
			if err != nil {
				return false
			}
			return errors.Is(got, errdef.ErrNotFound) &&
				errors.Is(got, errdef.ErrBase) &&
				!errors.Is(got, errdef.ErrConflict) &&
				got.(errdef.ContextProvider).Object() == "outer"
		}()},
		{"unknown code unmarshal does not panic", func() (ok bool) {
			defer func() {
				if r := recover(); r != nil {
					ok = false
				}
			}()
			got, err := maperr.Unmarshal(
				[]byte(`{"kind":"typed","code":"future_thing","message":"x"}`))
			return err == nil && got != nil &&
				got.(errdef.CodeProvider).ErrCode() == errdef.Code("future_thing") &&
				maperr.HTTPStatus(got) == 500
		}()},
	}
	for _, c := range checks {
		verdict := "OK"
		if !c.ok {
			verdict = "FAIL"
			failed++
		}
		fmt.Printf("%s  %s\n", verdict, c.name)
	}
	if failed != 0 {
		fmt.Printf("%d check(s) failed\n", failed)
	}
}
