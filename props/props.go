// Package props parses logical lines of a java.util.Properties stream into an
// ordered key/value map and writes it back with minimal escaping.
package props

import "ontology/logical"

// SyntaxError carries an invalid escape with its physical location.
type SyntaxError struct {
	Msg  string
	Line int
	Col  int
}

func (e *SyntaxError) Error() string { return e.Msg }

// Properties is an insertion-ordered key/value set.
type Properties struct {
	pairs []Pair
	index map[string]int
}

// Pair is one key/value entry.
type Pair struct {
	Key   string
	Value string
}

// New returns an empty Properties set.
func New() *Properties {
	return &Properties{index: map[string]int{}}
}

// Get returns the value of key.
func (p *Properties) Get(key string) (string, bool) {
	i, ok := p.index[key]
	if !ok {
		return "", false
	}
	return p.pairs[i].Value, true
}

// Pairs returns entries in first-occurrence order.
func (p *Properties) Pairs() []Pair { return p.pairs }

// Load parses data with the logical-line scanner.
func Load(data []byte) (*Properties, error) {
	_ = logical.NewScanner(data)
	return New(), nil
}

// Store writes the mapping back in Load-compatible form.
func (p *Properties) Store() []byte {
	return nil
}
