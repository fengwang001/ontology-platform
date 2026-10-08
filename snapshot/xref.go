package snapshot

// filterLinks drops every link record in the link section's recoverable
// prefix that references an object outside the object section's recoverable
// prefix. A dropped link is excluded even though it is structurally intact:
// keeping it would leave the recovered ontology referencing objects that do
// not exist.
func filterLinks(links []LinkRecord, objectIDs map[string]struct{}) (kept []LinkRecord, diags []Diagnostic) {
	for i, l := range links {
		var missing []string
		if _, ok := objectIDs[l.From]; !ok {
			missing = append(missing, l.From)
		}
		if _, ok := objectIDs[l.To]; !ok {
			missing = append(missing, l.To)
		}
		if len(missing) > 0 {
			diags = append(diags, Diagnostic{
				Category:    CatCrossRefMissing,
				Section:     SectionLinks,
				RecordIndex: i,
				MissingRefs: missing,
			})
			continue
		}
		kept = append(kept, l)
	}
	return kept, diags
}

// filterActions drops every action record in the action section's
// recoverable prefix whose dependencies are not all retained. An action
// record is all-or-nothing: if any object or link it modifies is missing,
// the whole record is unusable — partial recovery of its modifications is
// not allowed.
//
// Dependencies are checked against the FINAL retained sets (object prefix,
// and links that survived filterLinks), which is strictly stronger than the
// required check against section prefixes: anything absent from a prefix is
// also absent from the final set, and an action is never allowed to
// resurrect a link that the cross-section check already dropped.
func filterActions(actions []ActionRecord, objectIDs, linkIDs map[string]struct{}) (kept []ActionRecord, diags []Diagnostic) {
	for i, a := range actions {
		var missing []string
		for _, id := range a.Objects {
			if _, ok := objectIDs[id]; !ok {
				missing = append(missing, id)
			}
		}
		for _, id := range a.Links {
			if _, ok := linkIDs[id]; !ok {
				missing = append(missing, id)
			}
		}
		if len(missing) > 0 {
			diags = append(diags, Diagnostic{
				Category:    CatActionDepsMissing,
				Section:     SectionActions,
				RecordIndex: i,
				MissingRefs: missing,
			})
			continue
		}
		kept = append(kept, a)
	}
	return kept, diags
}
