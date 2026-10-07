package ontology

// DanglingSource explains why a dangling link record exists.
// The two sources must never be conflated, and when a link satisfies
// both conditions Boundary wins (boundary-first precedence).
type DanglingSource int

const (
	// DanglingBoundary: the other endpoint was never part of the
	// requested scope object set.
	DanglingBoundary DanglingSource = iota + 1
	// DanglingPermission: the other endpoint was requested in scope but
	// was removed wholesale because the caller lacks existence permission.
	DanglingPermission
)

func (s DanglingSource) String() string {
	switch s {
	case DanglingBoundary:
		return "boundary"
	case DanglingPermission:
		return "permission"
	default:
		return "unknown"
	}
}

// DanglingLink is one side-channel record of a main-graph link that was
// excluded from the snapshot body because exactly one endpoint survived
// into the effective object set. The link itself is not part of the
// snapshot; only this existence/exclusion fact is queryable.
type DanglingLink struct {
	LinkID   ID
	Type     LinkType
	From     ID
	To       ID
	Included ID        // the endpoint present in the snapshot
	Excluded ID        // the endpoint absent from the snapshot
	Dir      Direction // direction as seen from Included
	Source   DanglingSource
}

// Snapshot is a self-contained point-in-time image of a subgraph.
// Invariant: every link in Links has both endpoints in Objects, so the
// image is internally consistent and never contains a dangling link body.
type Snapshot struct {
	Revision int64

	// Scope is the validated, de-duplicated, sorted requested scope.
	Scope []ID
	// Principal is the caller the snapshot was extracted for.
	Principal string
	// Objects are the included objects, sorted by ID.
	Objects []Object
	// Links are the included links, sorted by ID.
	Links []Link
	// Dangling is the side information on excluded incident links,
	// in a deterministic total order.
	Dangling []DanglingLink

	// candidateLinksChecked is the internal, non-contractual cost measure:
	// it counts only candidate link instances actually inspected during
	// extraction (one count per distinct link incident to a scope object).
	// Its growth depends solely on the scope size and its directly incident
	// link volume, never on the overall main-graph size. Not exposed via
	// any documented caller-facing accessor of the platform contract;
	// InternalCandidateLinksChecked exists for local verification only.
	candidateLinksChecked int
}

// InternalCandidateLinksChecked returns the internal cost metric. It is
// intentionally named Internal* and documented as verification-only.
func (s *Snapshot) InternalCandidateLinksChecked() int {
	return s.candidateLinksChecked
}

// HasDangling reports whether a dangling record with the given link ID exists.
func (s *Snapshot) HasDangling(linkID ID) bool {
	for i := range s.Dangling {
		if s.Dangling[i].LinkID == linkID {
			return true
		}
	}
	return false
}

// DanglingByLink returns the side-channel record for a link ID.
func (s *Snapshot) DanglingByLink(linkID ID) (DanglingLink, bool) {
	for i := range s.Dangling {
		if s.Dangling[i].LinkID == linkID {
			return s.Dangling[i], true
		}
	}
	return DanglingLink{}, false
}
