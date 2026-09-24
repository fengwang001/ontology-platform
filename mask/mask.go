// Package mask validates CDC events and rewrites sensitive columns into
// domain tokens. Traversal order is fixed: event arrival, then Before, then
// After, with column names in byte order inside each image.
package mask

import (
	"errors"
	"sort"

	"ontology/tokn"
)

// Event is one CDC change. A nil *string value is SQL NULL; a nil map means
// the image is absent (as opposed to an empty map).
type Event struct {
	Table  string
	Op     byte
	Before map[string]*string
	After  map[string]*string
}

var (
	// ErrUnknownTable: the event's table has no entry in the domain config.
	ErrUnknownTable = errors.New("mask: unconfigured table")
	// ErrEventShape: Op is invalid or images are missing/contradictory.
	ErrEventShape = errors.New("mask: illegal event shape")
)

// Masker validates and masks events against a fixed domain configuration.
type Masker struct {
	domains map[string]map[string]string // table -> column -> domain
	tabs    *tokn.Tables
}

// NewMasker builds a Masker. Configuration must already be validated.
func NewMasker(domains map[string]map[string]string, tabs *tokn.Tables) *Masker {
	return &Masker{domains: domains, tabs: tabs}
}

// Mask returns a rewritten copy of ev; the input is never modified. A
// rejection (unknown table, bad shape, token limit) changes no state.
func (m *Masker) Mask(ev Event) (Event, error) {
	cols, ok := m.domains[ev.Table]
	if !ok {
		return Event{}, ErrUnknownTable
	}
	if err := validate(ev); err != nil {
		return Event{}, err
	}
	out := Event{Table: ev.Table, Op: ev.Op}
	err := m.tabs.WithTx(func(tx *tokn.Tx) error {
		var e error
		if ev.Before != nil {
			out.Before, e = m.rewrite(tx, cols, ev.Before)
			if e != nil {
				return e
			}
		}
		if ev.After != nil {
			out.After, e = m.rewrite(tx, cols, ev.After)
		}
		return e
	})
	if err != nil {
		return Event{}, err
	}
	return out, nil
}

// validate checks Op and image presence/shape before any allocation happens.
func validate(ev Event) error {
	switch ev.Op {
	case 'I':
		if ev.Before != nil || ev.After == nil {
			return ErrEventShape
		}
	case 'D':
		if ev.After != nil || ev.Before == nil {
			return ErrEventShape
		}
	case 'U':
		if ev.Before == nil || ev.After == nil || !sameKeys(ev.Before, ev.After) {
			return ErrEventShape
		}
	default:
		return ErrEventShape
	}
	return nil
}

func sameKeys(a, b map[string]*string) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}

// rewrite copies one image into a fresh map. Sensitive non-NULL values are
// tokenized in sorted column order; NULL and non-sensitive values pass
// through untouched.
func (m *Masker) rewrite(tx *tokn.Tx, cols map[string]string, in map[string]*string) (map[string]*string, error) {
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make(map[string]*string, len(in))
	for _, k := range keys {
		v := in[k]
		if dom, sensitive := cols[k]; sensitive && v != nil {
			tok, err := tx.Get(dom, *v)
			if err != nil {
				return nil, err
			}
			s := tok
			out[k] = &s
			continue
		}
		out[k] = v // nil (NULL) or non-sensitive pointer: copied, never altered
	}
	return out, nil
}
