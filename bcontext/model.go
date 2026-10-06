// Package bcontext is a browsing-context cross-origin isolation and
// permissions-policy evaluation kernel.
//
// The package is organized into four files:
//
//   - model.go: error categories, public input types, parsed internal
//     documents/edges/contexts and configuration helpers.
//   - kernel_all.go: mutation operations (LoadTop/LoadFrame/Navigate/
//     SetFrameAllow/OpenPopup), read-only evaluation (Isolated/
//     FeatureAllowed/OpenerReference) and the operation logger.
//   - scenarios_test.go: the fixed scenario matrix required by the spec.
//   - differential_all_test.go: an independently written naive model plus
//     randomized differential testing, concurrency tests and complexity
//     verification.
//
// All operations are serialized internally by one RWMutex, so concurrent
// calls are equivalent to some serial order. Isolation is a flag fixed at
// document load; Isolated is O(1). FeatureAllowed walks only the queried
// document's ancestor chain, so it is O(depth) and independent of the total
// number of contexts.
package bcontext

import "errors"

// Distinguishable error categories. Every validation failure maps to exactly
// one of these sentinel errors (wrapped), in the rejection order mandated by
// the specification:
// invalid argument < no context < no document < embed policy mismatch
// < opener group broken < unknown feature.
var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrNoContext       = errors.New("context does not exist")
	ErrNoDocument      = errors.New("document does not exist")
	ErrEmbedPolicy     = errors.New("embed policy mismatch")
	ErrOpenerBroken    = errors.New("opener group broken")
	ErrUnknownFeature  = errors.New("unknown feature")
)

// IsError reports whether err (or any wrapped error) is one of the kernel
// error categories and returns that sentinel.
func IsError(err error) (error, bool) {
	for _, target := range []error{
		ErrInvalidArgument,
		ErrNoContext,
		ErrNoDocument,
		ErrEmbedPolicy,
		ErrOpenerBroken,
		ErrUnknownFeature,
	} {
		if errors.Is(err, target) {
			return target, true
		}
	}
	return nil, false
}

// OpenerPolicy is the Cross-Origin-Opener-Policy declared by a document.
type OpenerPolicy string

const (
	OpenerNone                  OpenerPolicy = "none"
	OpenerSameOrigin            OpenerPolicy = "same-origin"
	OpenerSameOriginAllowPopups OpenerPolicy = "same-origin-allow-popups"
)

// EmbedderPolicy is the Cross-Origin-Embedder-Policy declared by a document.
type EmbedderPolicy string

const (
	EmbedderUnsafeNone     EmbedderPolicy = "unsafe-none"
	EmbedderRequireCorp    EmbedderPolicy = "require-corp"
	EmbedderCredentialless EmbedderPolicy = "credentialless"
)

// Header is a document's response-header input.
//
// Allow is the permissions-policy declaration: feature name -> explicit list
// of origins the feature is allowed for. A missing feature key means the
// document does not declare that feature, so the feature default applies.
// "*" inside a list matches every origin.
type Header struct {
	Origin   string
	Opener   OpenerPolicy
	Embedder EmbedderPolicy
	Allow    map[string][]string
}

// FrameAllow is the allow attribute of an embedding frame element:
// feature name -> origins the embedded document may use the feature for.
// A missing feature key means the feature's default list applies.
type FrameAllow struct {
	Allow map[string][]string
}

// Config describes the per-feature defaults and isolation requirements.
//
// DefaultAllowAll maps a feature to true when its default allow list is
// "all origins"; absent/false means "self origin only".
// RequiresIsolation lists features that are unavailable in any document that
// is not cross-origin isolated.
type Config struct {
	DefaultAllowAll   map[string]bool
	RequiresIsolation map[string]bool
}

// Logger receives one human-readable line per operation, containing its
// inputs, output and the decision basis.
type Logger interface {
	Log(line string)
}

// docDecl is a parsed document declaration for one feature.
type docDecl struct {
	origins  map[string]bool // explicit origins
	wildcard bool            // "*" present
}

// doc is a parsed and loaded document. isolated is fixed at load time.
type doc struct {
	origin   string
	opener   OpenerPolicy
	embedder EmbedderPolicy
	// declared maps feature -> declaration; a nil pointer means undeclared.
	declared map[string]*docDecl
	isolated bool
}

// edgeAllow is a parsed frame allow attribute for one feature.
type edgeAllow struct {
	origins  map[string]bool
	wildcard bool
}

// edge is the immutable-per-frame embedding relation. Its allow list is fixed
// when the frame is created and only changes via SetFrameAllow, affecting
// documents loaded afterwards.
type edge struct {
	allow map[string]*edgeAllow // nil entry means undeclared
}

// context is one browsing context. doc == nil means the context has been
// replaced by an ancestor navigation (tombstone): the context id still exists
// and is distinguishable from an unknown id, but its document is gone.
type context struct {
	id     int64
	doc    *doc
	parent *context
	edge   *edge
	// pendingEdge, when non-nil, replaces edge on the next document loaded
	// into this context; the currently loaded document keeps evaluating
	// against the old edge.
	pendingEdge *edge

	// popupOf is the opener context id; 0 when this context is not a popup.
	popupOf int64
	// openerBroken, once true, permanently forbids this openee from
	// referencing its opener; navigation never restores it.
	openerBroken bool
}

type nopLogger struct{}

func (nopLogger) Log(string) {}

func (c Config) featureKnown(f string) bool {
	for k := range c.DefaultAllowAll {
		if k == f {
			return true
		}
	}
	for k := range c.RequiresIsolation {
		if k == f {
			return true
		}
	}
	return false
}

func (c Config) defaultAllowsAll(f string) bool { return c.DefaultAllowAll[f] }

func (c Config) requiresIsolation(f string) bool { return c.RequiresIsolation[f] }

func (d *doc) allowsOwnOrigin(f string, cfg Config) bool {
	decl := d.declared[f]
	if decl == nil {
		// Undeclared: default always includes the document's own origin.
		return true
	}
	return decl.wildcard || decl.origins[d.origin]
}

// allowsOrigin evaluates a document declaration against an arbitrary origin
// (used for the top document and for parent-side declarations).
func (d *doc) allowsOrigin(f string, origin string, cfg Config) bool {
	decl := d.declared[f]
	if decl == nil {
		if cfg.defaultAllowsAll(f) {
			return true
		}
		return d.origin == origin
	}
	return decl.wildcard || decl.origins[origin]
}

func (e *edge) allows(f, origin string, parentOrigin string, cfg Config) bool {
	a := e.allow[f]
	if a == nil {
		if cfg.defaultAllowsAll(f) {
			return true
		}
		return parentOrigin == origin
	}
	return a.wildcard || a.origins[origin]
}

func isolationCapable(p EmbedderPolicy) bool {
	return p == EmbedderRequireCorp || p == EmbedderCredentialless
}
