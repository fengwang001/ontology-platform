package ontology

// RuleVersion is one immutable version of the cascade-cleanup rules.
//
// Required links are modeled as INCOMING typed edges: an object of a
// given object type must retain at least one active incoming edge of
// each required link type; when every required type has zero active
// incoming edges, the object becomes a cascade candidate.
type RuleVersion struct {
	// ID is the immutable version identifier.
	ID string
	// EffectiveFrom is the commit time at which this version becomes
	// current. Versions never backdate.
	EffectiveFrom int64
	// EffectiveFromSeq breaks ties between versions at the same time.
	EffectiveFromSeq uint64
	// Requirement maps object type -> set of required incoming link types.
	// Object types absent from the map are never orphaned.
	Requirement map[string][]string
	// Retroactive reports whether revocations older than EffectiveFrom
	// are evaluated under this version.
	Retroactive bool
}

// Requires reports whether an object of the given type needs the given
// incoming link type under this version.
func (r RuleVersion) Requires(objectType, linkType string) bool {
	for _, required := range r.Requirement[objectType] {
		if required == linkType {
			return true
		}
	}
	return false
}

// RequiredTypes returns the required incoming link types for an object
// type (nil means no requirements).
func (r RuleVersion) RequiredTypes(objectType string) []string {
	return r.Requirement[objectType]
}

// Basis pins the rule version an adjudication is declared against.
type Basis struct {
	// VersionID empty means HEAD (the current version at finalization).
	VersionID string
	// HeadSeq is non-zero for a HEAD basis and records the rule
	// version's EffectiveFromSeq it must still match at finalization.
	HeadSeq uint64
}

// IsHead reports whether this basis follows the mutable version pointer.
func (b Basis) IsHead() bool { return b.VersionID == "" }
