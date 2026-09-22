package canon

import (
	"sync/atomic"

	"ontology/host"
	"ontology/query"
	"ontology/segpath"
)

// Mode selects how query items are ordered in the canonical form.
type Mode int

const (
	// ModeOrdered keeps the original query item order.
	ModeOrdered Mode = iota
	// ModeSorted sorts query items by key, then value.
	ModeSorted
)

// Limits caps input resources. Zero means "use the default".
type Limits struct {
	MaxURLLength    int
	MaxPathSegments int
	MaxQueryParams  int
}

// DefaultLimits returns the built-in resource caps.
func DefaultLimits() Limits {
	return Limits{MaxURLLength: 1 << 20, MaxPathSegments: 4096, MaxQueryParams: 4096}
}

func (l Limits) withDefaults() Limits {
	d := DefaultLimits()
	if l.MaxURLLength <= 0 {
		l.MaxURLLength = d.MaxURLLength
	}
	if l.MaxPathSegments <= 0 {
		l.MaxPathSegments = d.MaxPathSegments
	}
	if l.MaxQueryParams <= 0 {
		l.MaxQueryParams = d.MaxQueryParams
	}
	return l
}

// Normalizer canonicalizes URLs. Its configuration is immutable after
// construction; the only mutable field is the observational scan counter,
// guarded atomically, so a Normalizer is safe for concurrent use.
type Normalizer struct {
	mode   Mode
	limits Limits
	scans  atomic.Uint64 // non-exported byte-scan counter
}

// New builds a Normalizer. Zero Limits fields fall back to defaults.
func New(mode Mode, limits Limits) *Normalizer {
	return &Normalizer{mode: mode, limits: limits.withDefaults()}
}

// ScanCount returns how many input bytes have been scanned so far.
func (n *Normalizer) ScanCount() uint64 { return n.scans.Load() }

// Normalize canonicalizes raw in a single pass per component. On any
// error it returns no partial result and stores nothing.
func (n *Normalizer) Normalize(raw string) (Result, error) {
	if len(raw) > n.limits.MaxURLLength {
		return Result{}, &LimitError{Kind: LimitLength, Got: len(raw), Limit: n.limits.MaxURLLength}
	}
	scheme, rest, schemeCase, err := splitScheme(raw)
	if err != nil {
		return Result{}, err
	}
	authority, path, rawQuery, hasQuery, err := splitRest(rest)
	if err != nil {
		return Result{}, err
	}
	n.scans.Add(uint64(len(raw))) // the split pass above

	h, port, hch, err := host.Normalize(authority, scheme)
	if err != nil {
		return Result{}, err
	}
	n.scans.Add(uint64(len(authority)))

	cpath, segs, escChanged, dotsResolved, err := segpath.Normalize(path)
	if err != nil {
		return Result{}, err
	}
	n.scans.Add(uint64(2 * len(path))) // split pass + pct pass
	if len(segs) > n.limits.MaxPathSegments {
		return Result{}, &LimitError{Kind: LimitSegments, Got: len(segs), Limit: n.limits.MaxPathSegments}
	}

	items, err := query.Parse(rawQuery)
	if err != nil {
		return Result{}, err
	}
	n.scans.Add(uint64(2 * len(rawQuery)))
	if len(items) > n.limits.MaxQueryParams {
		return Result{}, &LimitError{Kind: LimitParams, Got: len(items), Limit: n.limits.MaxQueryParams}
	}

	var rw Rewrites
	if query.Render(items) != rawQuery {
		rw |= RwEscape // ordered render differs: escapes were rewritten
	}
	if n.mode == ModeSorted {
		sorted := query.Sorted(items)
		if query.Render(sorted) != query.Render(items) {
			rw |= RwQueryOrder
		}
		items = sorted
	}
	cq := query.Render(items)
	return n.assemble(raw, scheme, schemeCase, h, port, cpath, segs, cq, items, hasQuery, rw, hch, escChanged, dotsResolved), nil
}
