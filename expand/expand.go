// Package expand resolves ${key} references over a merged key table.
package expand

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

var (
	ErrCycle        = errors.New("reference cycle")
	ErrUnclosedRef  = errors.New("unclosed reference")
	ErrUndefinedRef = errors.New("undefined reference")
)

// Expander expands references with memoization so each key's value is
// scanned at most once; replacements counts performed substitutions.
type Expander struct {
	values       map[string]string
	cache        map[string]string
	replacements int
}

func New(values map[string]string) *Expander {
	return &Expander{values: values, cache: make(map[string]string, len(values))}
}

// Replacements reports how many ${...} substitutions were performed.
func (e *Expander) Replacements() int { return e.replacements }

// Expand resolves the value of one key.
func (e *Expander) Expand(key string) (string, error) {
	return e.resolve(key, nil)
}

// ExpandAll resolves every key; iteration is sorted for deterministic errors.
func (e *Expander) ExpandAll() (map[string]string, error) {
	keys := make([]string, 0, len(e.values))
	for k := range e.values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		v, err := e.resolve(k, nil)
		if err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, nil
}

func (e *Expander) resolve(key string, stack []string) (string, error) {
	if v, ok := e.cache[key]; ok {
		return v, nil
	}
	for i, s := range stack {
		if s == key {
			cycle := append(append([]string{}, stack[i:]...), key)
			return "", fmt.Errorf("%w: %s", ErrCycle, strings.Join(cycle, " -> "))
		}
	}
	raw, ok := e.values[key]
	if !ok {
		return "", fmt.Errorf("%w: %q", ErrUndefinedRef, key)
	}
	out, err := e.expandText(raw, append(stack, key))
	if err != nil {
		return "", err
	}
	e.cache[key] = out
	return out, nil
}

func (e *Expander) expandText(s string, stack []string) (string, error) {
	if !strings.Contains(s, "$") {
		return s, nil
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] != '$' {
			b.WriteByte(s[i])
			i++
			continue
		}
		switch {
		case i+1 < len(s) && s[i+1] == '$':
			b.WriteByte('$')
			i += 2
		case i+1 < len(s) && s[i+1] == '{':
			end := strings.IndexByte(s[i+2:], '}')
			if end < 0 {
				return "", fmt.Errorf("%w: ${ at byte %d", ErrUnclosedRef, i)
			}
			ref := s[i+2 : i+2+end]
			v, err := e.resolve(ref, stack)
			if err != nil {
				return "", err
			}
			e.replacements++
			b.WriteString(v)
			i += 2 + end + 1
		default:
			b.WriteByte('$')
			i++
		}
	}
	return b.String(), nil
}
