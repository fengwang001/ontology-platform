package canon

// Equivalent reports whether a and b canonicalize to the identical byte
// string. Equivalence is exactly "same canonical form", which makes it
// reflexive, symmetric and transitive by construction. If either input
// fails to canonicalize, the error is returned and the bool is false.
func (n *Normalizer) Equivalent(a, b string) (bool, error) {
	ra, err := n.Normalize(a)
	if err != nil {
		return false, err
	}
	rb, err := n.Normalize(b)
	if err != nil {
		return false, err
	}
	return ra.Canonical == rb.Canonical, nil
}

var defaultOrdered = New(ModeOrdered, Limits{})

// Equivalent is the package-level judge using a default ordered-mode
// normalizer (the strictest reading of query order).
func Equivalent(a, b string) (bool, error) {
	return defaultOrdered.Equivalent(a, b)
}
