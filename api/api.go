// Package api is the public entry point over the evol record engine.
package api

import (
	"errors"
	"fmt"

	"ontology/evol"
	"ontology/sch"
)

// API binds a schema catalog and a record engine.
type API struct{ eng *evol.Engine }

// New builds an API over the standard v1..v3 schema.
func New() *API { return &API{eng: evol.NewEngine(sch.NewStandard())} }

// Write stores one record; a rejected write changes no state.
func (a *API) Write(W int, vs map[string]int) (*evol.Record, error) { return a.eng.Write(W, vs) }

// Read resolves rec under reader version R.
func (a *API) Read(rec *evol.Record, R int) (map[string]int, error) { return a.eng.Read(rec, R) }

// SelfCheck verifies the four invariants on local state (safe concurrently).
func (a *API) SelfCheck() error {
	cat := sch.NewStandard()
	e := evol.NewEngine(cat)
	r1, _ := e.Write(1, map[string]int{"a": 9, "b": 7})
	r2, _ := e.Write(2, map[string]int{"a": 5})
	r3, _ := e.Write(3, map[string]int{"a": 1, "c": 2, "d": 3})
	// Five Reads of the 8-step script, each dual-checked vs the naive reference (invariant 1).
	steps := []struct {
		r    *evol.Record
		R    int
		want map[string]int
	}{
		{r1, 3, map[string]int{"a": 9, "c": 10, "d": 20}},
		{r2, 3, map[string]int{"a": 5, "c": 10, "d": 20}},
		{r3, 2, map[string]int{"a": 1, "b": 2, "c": 2}},
		{r2, 2, map[string]int{"a": 5, "b": 2, "c": 10}},
		{r1, 1, map[string]int{"a": 9, "b": 7}},
	}
	for i, s := range steps {
		got, err := e.Read(s.r, s.R)
		if err != nil || !equal(got, s.want) {
			return fmt.Errorf("step %d: got %v err %v want %v", i+2, got, err, s.want)
		}
		naive := map[string]int{}
		for _, f := range cat.Fields(s.R) {
			if v, ok := s.r.Values()[f]; ok {
				naive[f] = v
			} else {
				naive[f] = cat.Def(f, s.r.W)
			}
		}
		if !equal(got, naive) {
			return fmt.Errorf("step %d diverges from naive reference", i+2)
		}
	}
	if err := backward(cat, e); err != nil { // invariant 2
		return err
	}
	if err := frozen(e); err != nil { // invariant 3
		return err
	}
	return noTrace(e) // invariant 4
}

// backward: fully-written records keep every common field at higher R.
func backward(cat *sch.Catalog, e *evol.Engine) error {
	full := []map[string]int{{"a": 1, "b": 2}, {"a": 1, "b": 2, "c": 10}, {"a": 1, "c": 30, "d": 20}}
	for W := 1; W <= 3; W++ {
		rec, err := e.Write(W, full[W-1])
		if err != nil {
			return err
		}
		for R := W; R <= 3; R++ {
			got, err := e.Read(rec, R)
			if err != nil {
				return err
			}
			for _, f := range cat.Fields(W) {
				if cat.Has(R, f) && got[f] != full[W-1][f] {
					return fmt.Errorf("common field %s changed W=%d R=%d", f, W, R)
				}
			}
		}
	}
	return nil
}

// frozen: a v1 record sees c=10 at every later reader, never the v3 value 30.
func frozen(e *evol.Engine) error {
	rec, err := e.Write(1, map[string]int{"a": 9})
	if err != nil {
		return err
	}
	for R := 2; R <= 3; R++ {
		got, err := e.Read(rec, R)
		if err != nil || got["c"] != 10 {
			return fmt.Errorf("c not frozen at R=%d: %d %v", R, got["c"], err)
		}
	}
	return nil
}

// noTrace: the three distinct sentinels fire and the engine still works after.
func noTrace(e *evol.Engine) error {
	type badW struct {
		W    int
		vs   map[string]int
		want error
	}
	cases := []badW{
		{0, nil, evol.ErrBadWriteVersion}, {4, nil, evol.ErrBadWriteVersion},
		{1, map[string]int{"z": 1}, evol.ErrFieldOutOfScope},
		{1, map[string]int{"": 1}, evol.ErrFieldOutOfScope},
	}
	for _, c := range cases {
		if _, err := e.Write(c.W, c.vs); !errors.Is(err, c.want) {
			return fmt.Errorf("W=%d: got %v want %v", c.W, err, c.want)
		}
	}
	rec, err := e.Write(1, map[string]int{"a": 1})
	if err != nil {
		return err
	}
	for _, R := range []int{0, 9} {
		if _, err := e.Read(rec, R); !errors.Is(err, evol.ErrBadReadVersion) {
			return fmt.Errorf("R=%d: got %v", R, err)
		}
	}
	got, err := e.Read(rec, 3)
	if err != nil || !equal(got, map[string]int{"a": 1, "c": 10, "d": 20}) {
		return fmt.Errorf("use after rejection broken: %v %v", got, err)
	}
	return nil
}

func equal(x, y map[string]int) bool {
	if len(x) != len(y) {
		return false
	}
	for k, v := range x {
		if y[k] != v {
			return false
		}
	}
	return true
}
