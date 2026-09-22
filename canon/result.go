package canon

import "ontology/query"

// Rewrites is a bitmask recording which kinds of rewrites were applied
// while canonicalizing.
type Rewrites uint32

const (
	RwNone        Rewrites = 0
	RwCase        Rewrites = 1 << iota // scheme/host case folded
	RwTrailingDot                      // host trailing dot removed
	RwDefaultPort                      // default port removed
	RwPortSyntax                       // empty port / leading zeros fixed
	RwIPv6                             // IPv6 literal recompressed
	RwEscape                           // escapes uppercased / redundant ones folded
	RwDotSegments                      // "." or ".." segments resolved
	RwQueryOrder                       // query items reordered (sorted mode)
	RwEmptyQuery                       // empty "?" dropped
)

func (r Rewrites) Has(f Rewrites) bool { return r&f != 0 }

// Result is the read-only answer of one canonicalization. It is a fresh
// value per call; inspecting it never advances any state.
type Result struct {
	Canonical    string       // the unique canonical form
	Scheme       string       // lowercased scheme
	Host         string       // canonical host (IPv6 keeps brackets)
	Port         string       // canonical port, "" if absent/default
	Path         string       // canonical path
	PathSegments []string     // resolved segment list
	Query        []query.Item // normalized query items (mode order)
	Rewritten    bool         // Canonical != raw input
	RewriteKinds Rewrites     // which kinds of rewrites happened
}
