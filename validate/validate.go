// Package validate checks merged values against a typed schema.
package validate

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"ontology/merge"
	"ontology/source"
)

var (
	ErrTypeMismatch = errors.New("type mismatch")
	ErrRequired     = errors.New("required keys missing")
)

// Rule declares the expected type and presence of one key.
type Rule struct {
	Type     string // "string", "int" or "bool"
	Required bool
}

// TypeError reports a value that does not match its declared type.
type TypeError struct {
	Key   string
	Want  string
	Got   string
	Layer source.Layer
}

func (e *TypeError) Error() string {
	return fmt.Sprintf("key %q: expected %s, got %q (from %s)",
		e.Key, e.Want, e.Got, e.Layer)
}

func (e *TypeError) Unwrap() error { return ErrTypeMismatch }

// RequiredError lists every missing required key, sorted.
type RequiredError struct{ Keys []string }

func (e *RequiredError) Error() string {
	return fmt.Sprintf("required keys missing: %s", strings.Join(e.Keys, ", "))
}

func (e *RequiredError) Unwrap() error { return ErrRequired }

// Check validates the merged table against the schema. All failures are
// aggregated with errors.Join so errors.Is matches each sentinel.
func Check(schema map[string]Rule, res *merge.Result) error {
	keys := make([]string, 0, len(schema))
	for k := range schema {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var errs []error
	var missing []string
	for _, k := range keys {
		rule := schema[k]
		m, ok := res.Entries[k]
		if !ok {
			if rule.Required {
				missing = append(missing, k)
			}
			continue
		}
		if !typeOK(rule.Type, m.Value) {
			errs = append(errs, &TypeError{Key: k, Want: rule.Type, Got: m.Value, Layer: m.Layer})
		}
	}
	if len(missing) > 0 {
		errs = append(errs, &RequiredError{Keys: missing})
	}
	return errors.Join(errs...)
}

func typeOK(typ, value string) bool {
	switch typ {
	case "int":
		_, err := strconv.Atoi(value)
		return err == nil
	case "bool":
		_, err := strconv.ParseBool(value)
		return err == nil
	default:
		return true
	}
}
