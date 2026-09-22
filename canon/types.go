// Package canon canonicalizes URLs into a unique byte form and decides URL
// equivalence from canonical-form equality. It composes pct, host, segpath
// and query and adds limits, rewrite accounting, scan accounting and a
// concurrency-safe memoizing normalizer.
package canon

import "ontology/query"

// RewriteKind identifies one class of normalization applied to a URL.
type RewriteKind string

const (
	RewriteSchemeCase RewriteKind = "scheme-case"
	RewriteHostCase   RewriteKind = "host-case"
	RewriteTrailingDot RewriteKind = "host-trailing-dot"
	RewriteDefaultPort RewriteKind = "default-port-removed"
	RewritePortNorm   RewriteKind = "port-normalized"
	RewriteIPv6       RewriteKind = "ipv6-compressed"
	RewriteEscapeCase RewriteKind = "escape-hex-upper"
	RewriteRedundant  RewriteKind = "redundant-escape-decoded"
	RewriteDotSegment RewriteKind = "dot-segment-resolved"
	RewriteQuerySort  RewriteKind = "query-sorted"
	RewriteFragment   RewriteKind = "fragment-dropped"
)

// Limits bound the resources a single normalization may consume. Zero or
// negative values disable that limit.
type Limits struct {
	MaxURLLen     int
	MaxSegments   int
	MaxQueryItems int
}

// Config configures a Normalizer.
type Config struct {
	QueryMode query.Mode
	Limits    Limits
}

// Result is an immutable normalization outcome.
type Result struct {
	url      string
	scheme   string
	host     string
	port     string
	path     []string
	query    []query.Item
	changed  bool
	rewrites []RewriteKind
	scanned  int64
}

// URL returns the canonical URL.
func (r *Result) URL() string { return r.url }

// Scheme returns the lowercase scheme.
func (r *Result) Scheme() string { return r.scheme }

// Host returns the normalized host without a port.
func (r *Result) Host() string { return r.host }

// Port returns the normalized port ("" when absent).
func (r *Result) Port() string { return r.port }

// PathSegments returns the normalized path segments.
func (r *Result) PathSegments() []string { return append([]string(nil), r.path...) }

// QueryItems returns the normalized query items in canonical order.
func (r *Result) QueryItems() []query.Item { return append([]query.Item(nil), r.query...) }

// Changed reports whether any rewrite was applied.
func (r *Result) Changed() bool { return r.changed }

// Rewrites returns the classes of rewrites that were applied.
func (r *Result) Rewrites() []RewriteKind { return append([]RewriteKind(nil), r.rewrites...) }

// ScannedBytes returns the total bytes counted during normalization.
func (r *Result) ScannedBytes() int64 { return r.scanned }
