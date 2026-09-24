// Package props parses and writes Java .properties text.
package props

import (
	"errors"
	"io"

	"ontology/logical"
)

// ErrBadUnicode is wrapped by every malformed \uXXXX escape error.
var ErrBadUnicode = errors.New("malformed \\uXXXX escape")

// ParseError carries the physical line and column (1-based) of a bad escape.
type ParseError struct {
	Line   int
	Column int
	Err    error
}

func (e *ParseError) Error() string { return "" }
func (e *ParseError) Unwrap() error { return e.Err }

// Properties stores key/value pairs in first-occurrence order.
type Properties struct {
	order []string
	m     map[string]string
}

// New returns an empty Properties.
func New() *Properties {
	return &Properties{m: map[string]string{}}
}

// Load reads .properties text and merges it into p.
func (p *Properties) Load(r io.Reader) error {
	lr := logical.NewReader(r)
	_ = lr
	return nil
}

// Get returns the value of key.
func (p *Properties) Get(key string) string { return p.m[key] }

// Set inserts or overwrites a key.
func (p *Properties) Set(key, value string) {}

// Pairs returns keys in first-occurrence order.
func (p *Properties) Keys() []string { return append([]string(nil), p.order...) }

// Store writes p in .properties text.
func (p *Properties) Store(w io.Writer) error { return nil }
