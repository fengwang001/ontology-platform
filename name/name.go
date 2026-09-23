// Package name defines source names and declarations.
package name

import "errors"

// ErrEmpty rejects an empty name.
var ErrEmpty = errors.New("name: empty name")

// Name is a source-level identifier. Equality is by exact text.
type Name struct{ text string }

// New validates text and returns the Name.
func New(text string) (Name, error) {
	if text == "" {
		return Name{}, ErrEmpty
	}
	return Name{text: text}, nil
}

// String returns the name text.
func (n Name) String() string { return n.text }

// Equal reports whether two names are the same.
func (n Name) Equal(o Name) bool { return n.text == o.text }

// Kind classifies a declaration's forward-reference rule.
type Kind int

const (
	// NoForward may only be referenced at positions at or after its own.
	NoForward Kind = iota
	// Forward may also be referenced at earlier positions.
	Forward
)

// AllowsForward reports whether references before the declaration bind to it.
func (k Kind) AllowsForward() bool { return k == Forward }

// Decl is a single declaration: name, kind, and source position ordinal.
type Decl struct {
	name Name
	kind Kind
	pos  int
}

// NewDecl builds a declaration at source position pos.
func NewDecl(n Name, k Kind, pos int) Decl { return Decl{name: n, kind: k, pos: pos} }

// Name returns the declared name.
func (d Decl) Name() Name { return d.name }

// Kind returns the declaration kind.
func (d Decl) Kind() Kind { return d.kind }

// Pos returns the source position ordinal.
func (d Decl) Pos() int { return d.pos }
