package ontology

import "sort"

// DefaultMaxScopeObjects is the preset upper bound on the scope set size.
const DefaultMaxScopeObjects = 10_000

// Parameter-level rejection errors. Parameter validation precedes all
// permission removal and dangling classification.
var (
	ErrEmptyScope    = IllegalScopeError{"scope object set must not be empty"}
	ErrInvalidID     = IllegalScopeError{"scope contains a malformed object identifier"}
	ErrScopeTooLarge = IllegalScopeError{"scope object set exceeds the configured limit"}
)

// IllegalScopeError marks rejection of the request parameters.
type IllegalScopeError struct{ msg string }

func (e IllegalScopeError) Error() string { return e.msg }

// Extractor extracts self-contained subgraph snapshots from a main graph.
type Extractor struct {
	g     *Graph
	maxSc int
}

// NewExtractor creates an Extractor bound to a main graph.
func NewExtractor(g *Graph) *Extractor {
	return &Extractor{g: g, maxSc: DefaultMaxScopeObjects}
}

// WithMaxScope overrides the preset scope size limit for local testing;
// non-positive values are ignored.
func (e *Extractor) WithMaxScope(n int) *Extractor {
	if n > 0 {
		e.maxSc = n
	}
	return e
}

// validateScope performs parameter validation only, in the fixed order
// empty > malformed ID > oversize, then normalizes to sorted unique IDs.
func (e *Extractor) validateScope(scope []ID) ([]ID, error) {
	if len(scope) == 0 {
		return nil, ErrEmptyScope
	}
	for _, id := range scope {
		if !ValidID(id) {
			return nil, ErrInvalidID
		}
	}
	seen := make(map[ID]struct{}, len(scope))
	uniq := make([]ID, 0, len(scope))
	for _, id := range scope {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		uniq = append(uniq, id)
	}
	if len(uniq) > e.maxSc {
		return nil, ErrScopeTooLarge
	}
	sort.Strings(uniq)
	return uniq, nil
}

// Extract returns a self-contained snapshot of the main graph at exactly
// one deterministic revision. Processing order:
//
//  1. Parameter validation (empty / malformed / oversize).
//  2. Permission removal (before dangling classification): every requested
//     object the principal cannot prove the existence of is removed
//     wholesale, together with all of its links. Nonexistent scope IDs are
//     treated identically (existence cannot be proven to the caller).
//  3. Empty effective set: an empty snapshot is returned, not an error.
//  4. Inclusion: a main-graph link enters the snapshot body iff both
//     endpoints survive. Exactly one surviving endpoint yields a
//     side-channel dangling record; self links are never dangling.
//
// The method dereferences a single immutable state pointer captured up
// front, so objects, links and permissions are all read from the same
// revision; concurrent commits cannot mix points in time.
func (e *Extractor) Extract(principal string, scope []ID) (*Snapshot, error) {
	validated, err := e.validateScope(scope)
	if err != nil {
		return nil, err
	}

	s := e.g.snapshotState()
	return extractState(s, validated, principal), nil
}

// extractState computes a snapshot from one immutable state. Validation
// has already happened. The function is pure with respect to s and is the
// single place implementing removal, inclusion and dangling classification.
func extractState(s *graphState, validated []ID, principal string) *Snapshot {
	snap := &Snapshot{
		Revision:  s.revision,
		Scope:     validated,
		Principal: principal,
	}

	grants := s.grants[principal]
	canSee := func(id ID) bool {
		if _, exists := s.objects[id]; !exists {
			return false
		}
		_, ok := grants[id]
		return ok
	}

	removed := make(map[ID]struct{})
	effective := make([]ID, 0, len(validated))
	for _, id := range validated {
		if canSee(id) {
			effective = append(effective, id)
		} else {
			removed[id] = struct{}{}
		}
	}

	if len(effective) == 0 {
		snap.Objects = []Object{}
		snap.Links = []Link{}
		snap.Dangling = []DanglingLink{}
		return snap
	}

	in := make(map[ID]struct{}, len(effective))
	for _, id := range effective {
		in[id] = struct{}{}
	}

	objects := make([]Object, 0, len(effective))
	for _, id := range effective {
		objects = append(objects, s.objects[id])
	}
	sort.Slice(objects, func(i, j int) bool { return objects[i].ID < objects[j].ID })
	snap.Objects = objects

	// Gather candidates solely through the endpoint index of effective
	// objects. Each candidate link is resolved and inspected exactly once.
	candidates := make(map[ID]Link)
	for _, id := range effective {
		for linkID := range s.endpointLinks[id] {
			candidates[linkID] = s.links[linkID]
		}
	}
	snap.candidateLinksChecked = len(candidates)

	included := make([]Link, 0)
	dangling := make([]DanglingLink, 0)
	for _, lk := range candidates {
		_, fromIn := in[lk.From]
		_, toIn := in[lk.To]
		switch {
		case fromIn && toIn:
			included = append(included, lk)
		case fromIn != toIn:
			includedEP, excludedEP := lk.From, lk.To
			if !fromIn {
				includedEP, excludedEP = lk.To, lk.From
			}
			// Boundary-first precedence: only an endpoint that was
			// requested but permission-removed gets the permission label.
			source := DanglingBoundary
			if _, wasRequested := removed[excludedEP]; wasRequested {
				source = DanglingPermission
			}
			dangling = append(dangling, DanglingLink{
				LinkID:   lk.ID,
				Type:     lk.Type,
				From:     lk.From,
				To:       lk.To,
				Included: includedEP,
				Excluded: excludedEP,
				Dir:      lk.DirectionFrom(includedEP),
				Source:   source,
			})
		}
	}

	sort.Slice(included, func(i, j int) bool { return included[i].ID < included[j].ID })
	snap.Links = included
	sortDangling(dangling)
	snap.Dangling = dangling
	return snap
}

// sortDangling imposes a deterministic total order: included endpoint,
// excluded endpoint, then link ID. Repeated extractions of an unchanged
// graph therefore yield byte-identical side information, including order.
func sortDangling(d []DanglingLink) {
	sort.Slice(d, func(i, j int) bool {
		if d[i].Included != d[j].Included {
			return d[i].Included < d[j].Included
		}
		if d[i].Excluded != d[j].Excluded {
			return d[i].Excluded < d[j].Excluded
		}
		return d[i].LinkID < d[j].LinkID
	})
}
