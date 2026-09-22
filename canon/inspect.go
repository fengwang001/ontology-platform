package canon

import "ontology/query"

// Rewrites is a bitmask of normalization categories that fired.
type Rewrites uint32

const (
	KindScheme     Rewrites = 1 << iota // scheme case folded
	KindHost                            // host case, trailing dot, IPv6 form
	KindPort                            // default port dropped, port re-formed
	KindEscape                          // percent-escape case/redundancy
	KindDotSegment                      // "." or ".." segments resolved
	KindEmptyPath                       // empty path became "/"
	KindQuery                           // query escapes or empty items
	KindQueryOrder                      // query parameters reordered
	KindFragment                        // fragment dropped
)

// Report describes the canonical parts of a URL and what was rewritten.
type Report struct {
	Canonical string
	Scheme    string
	Host      string
	Port      string
	Path      string
	Segments  []string
	Query     []query.Item
	HasQuery  bool
	Rewritten bool
	Kinds     Rewrites
}

// Inspect returns the canonical parts of raw without advancing any
// shared state: two calls at the same instant return identical reports.
func (n *Normalizer) Inspect(raw string) (Report, error) {
	_, rep, err := n.normalize(raw, false)
	return rep, err
}
