// Package props parses and writes Java .properties content.
package props

import (
	"io"

	"ontology/logical"
)

// EscapeError describes an invalid backslash escape with physical position.
type EscapeError struct {
	Line   int    // 1-based physical line
	Column int    // 1-based byte column within that physical line
	Detail string
}

func (e *EscapeError) Error() string { return "" }

// Props is an ordered key/value map; duplicate keys keep first-seen order.
type Props struct {
	keys []string
	m    map[string]string
}

// New returns an empty Props.
func New() *Props { return &Props{m: map[string]string{}} }

// Load parses all logical lines from r.
func (p *Props) Load(r io.Reader) error { return nil }

// Get returns the value and presence for key.
func (p *Props) Get(key string) (string, bool) {
	v, ok := p.m[key]
	return v, ok
}

// All returns key/value pairs in first-seen order.
func (p *Props) All() []struct{ K, V string } { return nil }

// Set inserts or overwrites a key.
func (p *Props) Set(k, v string) {}

// Store writes all entries in first-seen order.
func (p *Props) Store(w io.Writer) error { return nil }

var _ = logical.NewReader
