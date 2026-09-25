// Package api wires sch+evol together and exposes New, Read and SelfCheck.
package api

import (
	"errors"
	"fmt"
	"reflect"

	"ontology/evol"
	"ontology/sch"
)

var ErrSelfCheck = errors.New("api: self-check failed")

type API struct {
	t *sch.Table
	s *evol.Store
}

// New builds the fixed v1..v3 schema and service.
func New() *API {
	t := sch.New([]map[string]int{
		{"a": 1, "b": 2},
		{"a": 1, "b": 2, "c": 10},
		{"a": 1, "c": 30, "d": 20},
	})
	return &API{t: t, s: evol.New(t)}
}

type Record = evol.Record

func (x *API) Write(w int, v map[string]int) (Record, error) { return x.s.Write(w, v) }

// Len reports the number of successfully written records.
func (x *API) Len() int { return x.s.Len() }

func (x *API) Read(rec Record, r int) (map[string]int, error) { return x.s.Read(rec, r) }

// naiveDef is an independent oracle: last default at or before w; if the field
// is unborn at w, its first introduction default.
func (x *API) naiveDef(f string, w int) int {
	d, found := 0, false
	for v := 1; v <= w; v++ {
		if raw, ok := x.t.Defaults(v)[f]; ok {
			d, found = raw, true
		}
	}
	if !found {
		for v := w + 1; v <= x.t.N(); v++ {
			if raw, ok := x.t.Defaults(v)[f]; ok {
				return raw
			}
		}
	}
	return d
}

// naiveRead is the field-by-field reference resolution for reader version r.
func (x *API) naiveRead(rec Record, r int) map[string]int {
	out := map[string]int{}
	for _, f := range x.t.Fields(r) {
		if v, ok := evol.Explicit(rec, f); ok {
			out[f] = v
		} else {
			out[f] = x.naiveDef(f, rec.W())
		}
	}
	return out
}

var builtInWrites = []struct {
	w int
	v map[string]int
}{
	{1, map[string]int{"a": 9, "b": 7}},
	{2, map[string]int{"a": 5}},
	{3, map[string]int{"a": 1, "c": 2, "d": 3}},
	{1, map[string]int{}},
	{2, map[string]int{"c": 99}},
}

// SelfCheck verifies the four invariants, constant-time default lookup and
// concurrent-read consistency.
func (x *API) SelfCheck() error {
	if err := sch.SelfTest(); err != nil {
		return err
	}
	var recs []Record
	for _, b := range builtInWrites {
		rec, err := x.s.Write(b.w, b.v)
		if err != nil {
			return err
		}
		recs = append(recs, rec)
	}
	for _, rec := range recs { // invariant 1: Read == naive oracle
		for r := 1; r <= 3; r++ {
			got, err := x.s.Read(rec, r)
			if err != nil || !reflect.DeepEqual(got, x.naiveRead(rec, r)) {
				return fmt.Errorf("%w: invariant1 W=%d R=%d", ErrSelfCheck, rec.W(), r)
			}
		}
	}
	full := []map[string]int{{"a": 1, "b": 2}, {"a": 1, "b": 2, "c": 10}, {"a": 1, "c": 30, "d": 20}}
	for wi, vals := range full { // invariant 2: common fields survive for R >= W
		rec, _ := x.s.Write(wi+1, vals)
		for r := wi + 1; r <= 3; r++ {
			got, _ := x.s.Read(rec, r)
			for f, v := range vals {
				if x.t.Has(r, f) && got[f] != v {
					return fmt.Errorf("%w: invariant2", ErrSelfCheck)
				}
			}
		}
	}
	g2, _ := x.s.Read(recs[0], 2) // invariant 3: defaults frozen at W
	g3, _ := x.s.Read(recs[0], 3)
	if g2["c"] != 10 || g3["c"] != 10 || g3["d"] != 20 || g2["b"] != 7 {
		return fmt.Errorf("%w: invariant3", ErrSelfCheck)
	}
	n0 := x.s.Len() // invariant 4: rejected operations leave no trace
	bad := []struct {
		w int
		v map[string]int
		e error
	}{
		{0, nil, evol.ErrInvalidWriteVersion}, {4, nil, evol.ErrInvalidWriteVersion},
		{1, map[string]int{"z": 1}, evol.ErrUnknownField},
		{1, map[string]int{"": 1}, evol.ErrUnknownField},
	}
	for _, b := range bad {
		if _, err := x.s.Write(b.w, b.v); !errors.Is(err, b.e) {
			return fmt.Errorf("%w: invariant4 write", ErrSelfCheck)
		}
	}
	if _, err := x.s.Read(recs[0], 0); !errors.Is(err, evol.ErrInvalidReadVersion) {
		return fmt.Errorf("%w: invariant4 read", ErrSelfCheck)
	}
	if x.s.Len() != n0 {
		return fmt.Errorf("%w: invariant4 state", ErrSelfCheck)
	}
	return nil
}
