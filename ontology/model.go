package ontology

// ContributionPolicy declares how an instance contributes when it is linked to
// several different grouping instances simultaneously through links of the
// view's link type. A view definition MUST select one explicitly; there is no
// implicit default that silently keeps only one group.
type ContributionPolicy int

const (
	// PolicyFullEach: the instance contributes its full value to every group it
	// currently belongs to.
	PolicyFullEach ContributionPolicy = iota + 1
	// PolicyDenyMulti: simultaneous membership in more than one group is
	// forbidden. Adding a second membership is rejected with
	// ClassPolicyRejected and changes nothing; the one existing membership
	// contributes fully.
	PolicyDenyMulti
)

func (p ContributionPolicy) valid() bool {
	return p == PolicyFullEach || p == PolicyDenyMulti
}

// ViewDef declares an aggregation view.
type ViewDef struct {
	Name          string
	GroupType     string
	AggType       string
	LinkType      string
	ValueProperty string
	Contribution  ContributionPolicy
}

// Optional represents a property value that may be absent. An absent value is
// distinct from zero: it contributes zero to the sum and is not counted in the
// participating-instance count.
type Optional struct {
	Present bool
	Value   float64
}

// Aggregate is an incrementally maintained per-group result.
type Aggregate struct {
	Sum   float64
	Count int
}

// membership is an instance's current memberships in one view and the cached
// per-membership contribution.
type membership struct {
	groups  []string
	contrib map[string]float64
	present map[string]bool
}

// groupAgg is a view's maintained result for one group.
type groupAgg struct {
	sum   float64
	count int
}
