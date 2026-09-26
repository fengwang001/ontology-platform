// Package api is the public entry point. It depends only on parse (which in
// turn depends on lex). It is stateless, so every method is safe for
// concurrent use.
package api

import (
	"errors"
	"fmt"
	"reflect"

	"ontology/parse"
)

// API is the stateless public handle; create one with New and share it.
type API struct{}

// New returns a ready-to-use, stateless API.
func New() *API { return &API{} }

// ParseText parses one strict JSON document.
func (a *API) ParseText(s string) (parse.Value, error) {
	return parse.Parse([]byte(s))
}

// Stringify serializes a value and guarantees the result re-parses.
func (a *API) Stringify(v parse.Value) (string, error) {
	if v.Kind < parse.KindNull || v.Kind > parse.KindObject {
		return "", errors.New("api: unknown value kind")
	}
	s := v.String()
	if _, err := parse.Parse([]byte(s)); err != nil {
		return "", fmt.Errorf("api: stringify produced invalid JSON: %w", err)
	}
	return s, nil
}

var validTexts = []string{
	`null`, `true`, `false`, `0`, `-0.5`, `1e3`, `12345.678`,
	`"a\nb"`, `"emoji😀end"`, `"tab\there/quote\"end"`,
	`[]`, `[1,2,[3,false],null]`, `{}`, `{"a":1,"b":[true,null],"c":"x"}`,
	`  {"k" : [ {"n": -2.5e2} , {} ] }  `,
}

var invalidTexts = []string{
	`01`, `1.`, `1e`, `1e+`, `-`, `{"a":1,"a":2}`,
	`"\q"`, `"\u12"`, `"\uD800"`, `"a\uDC00b"`, `[1,2`, `tru`,
}

// sentinelReps picks one text per required sentinel; their errors must be
// pairwise distinct: the four number-grammar sentinels, bad escape, lone
// \u, lone surrogate and duplicate key.
var sentinelReps = []string{
	`01`, `1.`, `1e`, `-`, `"\q"`, `"\u12"`, `"\uD800"`, `{"a":1,"a":2}`,
}

var numVectors = map[string]float64{
	"0": 0, "-0.5": -0.5, "1e3": 1000, "1.25": 1.25,
	"100": 100, "-1.5E2": -150, "0.0": 0,
}

// SelfCheck runs the four invariants against built-in texts and returns the
// first failure, or nil when all hold.
func (a *API) SelfCheck() error {
	// 1. Round trip: parse -> stringify -> re-parse must be deeply equal.
	for _, t := range validTexts {
		v, err := a.ParseText(t)
		if err != nil {
			return fmt.Errorf("roundtrip parse %q: %w", t, err)
		}
		s, err := a.Stringify(v)
		if err != nil {
			return fmt.Errorf("roundtrip stringify %q: %w", t, err)
		}
		v2, err := a.ParseText(s)
		if err != nil {
			return fmt.Errorf("roundtrip reparse %q: %w", s, err)
		}
		if !reflect.DeepEqual(v, v2) {
			return fmt.Errorf("roundtrip mismatch %q vs %q", t, s)
		}
	}
	// 2. Strict rejection: every invalid text fails; the eight required
	// sentinels (one representative each) are pairwise distinct.
	for _, t := range invalidTexts {
		if _, err := a.ParseText(t); err == nil {
			return fmt.Errorf("expected rejection for %q", t)
		}
	}
	errs := map[string]error{}
	for _, t := range sentinelReps {
		_, err := a.ParseText(t)
		if err == nil {
			return fmt.Errorf("expected rejection for %q", t)
		}
		errs[t] = err
	}
	for i, t1 := range sentinelReps {
		for _, t2 := range sentinelReps[i+1:] {
			if errors.Is(errs[t1], errs[t2]) || errors.Is(errs[t2], errs[t1]) {
				return fmt.Errorf("sentinels of %q and %q are the same", t1, t2)
			}
		}
	}
	// 3. Number values agree with the textbook reference results.
	for t, want := range numVectors {
		v, err := a.ParseText(t)
		if err != nil || v.Kind != parse.KindNumber || v.Num != want {
			return fmt.Errorf("number vector %q = %v (want %v), err=%v", t, v.Num, want, err)
		}
	}
	// 4. Failure leaves no trace: zero value on error, API stays usable.
	z, err := a.ParseText(`{"a":1,"a":2}`)
	if err == nil {
		return errors.New("duplicate key should fail")
	}
	if !reflect.DeepEqual(z, parse.Value{}) {
		return errors.New("failed parse returned a partial value")
	}
	if _, err := a.ParseText(`{"ok":true}`); err != nil {
		return fmt.Errorf("API unusable after failure: %w", err)
	}
	return nil
}
