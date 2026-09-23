// Package merge combines entries from priority layers in a single pass.
package merge

import "ontology/source"

// Override records one value that was shadowed by a higher-priority layer.
type Override struct {
	Layer source.Layer
	Value string
}

// Merged is the winning raw value for a key plus everything it shadowed,
// ordered from lowest to highest priority.
type Merged struct {
	Value      string
	Layer      source.Layer
	Overridden []Override
}

// Result is the merged key table. Values are raw (unexpanded).
type Result struct {
	Entries map[string]Merged
}

// lookups counts map reads; tests assert the single-pass bound.
var lookups int

// Merge folds layers ordered from lowest to highest priority into one table.
// Later entries within a layer shadow earlier ones.
func Merge(layers ...[]source.Entry) *Result {
	lookups = 0
	res := &Result{Entries: make(map[string]Merged)}
	for _, layer := range layers {
		for _, e := range layer {
			lookups++
			m, ok := res.Entries[e.Key]
			if ok {
				m.Overridden = append(m.Overridden, Override{Layer: m.Layer, Value: m.Value})
			}
			m.Value = e.Value
			m.Layer = e.Layer
			res.Entries[e.Key] = m
		}
	}
	return res
}
