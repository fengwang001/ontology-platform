package ontology

// NaiveAdjudicator is an independently maintained reference implementation.
// It recomputes every decision by linearly scanning ALL registered policies
// and ALL instances, with no indexes. Tests assert that the production
// Adjudicator and NaiveAdjudicator return identical verdicts on the same
// state across randomized policies and operation sequences.
type NaiveAdjudicator struct {
	a *Adjudicator
}

// NewNaiveAdjudicator wraps an adjudicator's registry/config but ignores its
// indexes when deciding.
func NewNaiveAdjudicator(a *Adjudicator) *NaiveAdjudicator {
	return &NaiveAdjudicator{a: a}
}

// Read mirrors Adjudicator.Read using a full policy scan.
func (n *NaiveAdjudicator) Read(subject, typeName, id string) (*InstanceView, *DecisionError) {
	return n.a.read(subject, typeName, id, false)
}

// Write mirrors Adjudicator.Write using a full policy scan.
func (n *NaiveAdjudicator) Write(subject, typeName, id string, values map[string]Value) (*WriteResult, *DecisionError) {
	return n.a.write(subject, typeName, id, values, false)
}

// naiveCandidates is the deliberately O(total policies) candidate selection:
// every registered policy of the type is inspected. It exists so tests can
// compare its touched count against the index-based adjudicator.
func naiveCandidates(catalog *PolicyCatalog, typeName, subject string) ([]RowPolicy, []PropertyPolicy) {
	var rows []RowPolicy
	for _, p := range catalog.scanRowPolicies(typeName) {
		if p.selects(subject) {
			rows = append(rows, p)
		}
	}
	var props []PropertyPolicy
	for _, p := range catalog.scanPropertyPolicies(typeName) {
		if p.selects(subject) {
			props = append(props, p)
		}
	}
	return rows, props
}
