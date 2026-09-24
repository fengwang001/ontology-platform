// Package props parses Java .properties logical lines into ordered
// key/value pairs and writes them back with minimal escaping.
package props

import (
	"errors"
	"io"

	"ontology/logical"
)

// ErrUnicode is the sentinel error for a malformed \uXXXX escape.
var ErrUnicode = errors.New("props: malformed \\uXXXX escape")

// UnicodeError carries the physical position of a malformed \u escape.
type UnicodeError struct {
	Line int
	Col  int
}

func (e *UnicodeError) Error() string { return ErrUnicode.Error() }
func (e *UnicodeError) Unwrap() error { return ErrUnicode }

// Properties is an insertion-ordered key/value map.
type Properties struct {
	keys []string
	m    map[string]string
}

// New returns empty Properties.
func New() *Properties { return &Properties{m: map[string]string{}} }

// Set inserts or updates a key, preserving first-seen order.
func (p *Properties) Set(key, value string) {}

// Len returns the number of keys.
func (p *Properties) Len() int { return len(p.keys) }

// At returns the key/value at order index i.
func (p *Properties) At(i int) (string, string) { return "", "" }

// Get returns the value for key.
func (p *Properties) Get(key string) (string, bool) { return "", false }

// Parse consumes logical lines from r and loads them.
func (p *Properties) Parse(r *logical.Reader) error { return nil }

// Load parses a .properties document.
func Load(data []byte) (*Properties, error) { return New(), nil }

// Store writes the properties in .properties text form.
func (p *Properties) Store(w io.Writer) error { return nil }
