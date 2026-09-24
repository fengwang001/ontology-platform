// Package props parses and writes Java .properties content.
package props

import "ontology/logical"

// DecodeError describes a malformed escape, with physical position.
type DecodeError struct {
	Line   int
	Column int
	Msg    string
}

func (e *DecodeError) Error() string { return e.Msg }

// Properties is an insertion-ordered key/value map.
type Properties struct {
	order []string
	vals  map[string]string
}

// New returns an empty Properties.
func New() *Properties {
	return &Properties{vals: map[string]string{}}
}

// Get returns the value for key.
func (p *Properties) Get(key string) (string, bool) {
	v, ok := p.vals[key]
	return v, ok
}

// Set inserts or replaces a value, preserving first-seen order.
func (p *Properties) Set(key, value string) {
	if _, ok := p.vals[key]; !ok {
		p.order = append(p.order, key)
	}
	p.vals[key] = value
}

// Len returns the number of distinct keys.
func (p *Properties) Len() int { return len(p.order) }

// Range iterates keys in first-appearance order.
func (p *Properties) Range(f func(key, value string) bool) {}

// Load parses Java .properties text. Skeleton.
func Load(input []byte) (*Properties, error) {
	_ = logical.NewSplitter()
	return New(), nil
}

// Store writes the properties in .properties format. Skeleton.
func (p *Properties) Store() []byte {
	return nil
}
