// Package name defines declaration names, kinds and source positions.
package name

// Kind classifies whether a declaration allows forward references.
type Kind int

const (
	// Plain declarations may only be referenced at positions >= Pos.
	Plain Kind = iota
	// Forward declarations may also be referenced before Pos.
	Forward
)

// Decl is a single declaration of a name inside one scope.
type Decl struct {
	Name string
	Kind Kind
	Pos  int
}

// Equal reports whether two names are the same.
func Equal(a, b string) bool { return a == b }
