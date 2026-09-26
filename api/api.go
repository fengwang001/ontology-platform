// Package api is the public entry point: compile, match and self-check.
// It depends on match, never the other way round.
package api

import (
	"errors"
	"fmt"
	"strings"

	"ontology/match"
	"ontology/parse"
)

// Matcher is an immutable compiled pattern. All fields are unexported and
// never mutated after construction, so Match is safe for concurrent use.
type Matcher struct {
	pattern string
}

// Compile fully validates pattern before constructing anything: a rejected
// pattern returns an error and changes no state. The error is one of the
// parse sentinels, decidable via errors.Is.
func Compile(pattern string) (*Matcher, error) {
	if err := parse.Validate(pattern); err != nil {
		return nil, err
	}
	return &Matcher{pattern: pattern}, nil
}

// Pattern reports the compiled pattern. It exists only to aid diagnostics.
func (m *Matcher) Pattern() string { return m.pattern }

// Match reports whether the whole text s matches the compiled pattern. A
// text rejected by the length limit returns the parse.ErrTooLong sentinel.
func (m *Matcher) Match(s string) (bool, error) {
	if err := parse.ValidateText(s); err != nil {
		return false, err
	}
	return match.Match(s, m.pattern), nil
}

// SelfCheck verifies the package invariants on built-in pairs: core
// semantics via match.SelfCheck, three distinct rejection kinds, and that a
// rejection leaves the service usable. It reports only pass/fail.
func SelfCheck() error {
	if err := match.SelfCheck(); err != nil {
		return err
	}
	type reject struct {
		pattern  string
		sentinel error
	}
	rejects := []reject{
		{"a\x01b", parse.ErrUnsupportedChar},
		{strings.Repeat("a", parse.MaxLen+1), parse.ErrTooLong},
		{strings.Repeat("*", parse.MaxWildcards+1), parse.ErrTooManyWildcards},
	}
	for _, r := range rejects {
		if got, err := Compile(r.pattern); err == nil || got != nil || !isSentinel(err, r.sentinel) {
			return fmt.Errorf("api: rejection %q misbehaved: %v", r.pattern, err)
		}
		// Failure leaves no trace: a legal pattern compiles and works right after.
		good, err := Compile("*a*b")
		if err != nil {
			return err
		}
		if v, err := good.Match("adceb"); err != nil || !v {
			return fmt.Errorf("api: service not restored after rejection")
		}
	}
	return nil
}

func isSentinel(err, target error) bool { return errors.Is(err, target) }
