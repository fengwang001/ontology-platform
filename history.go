package ontology

func (doc *docState) existsAt(version int) docRevision {
	current := docRevision{exists: doc.baselineExists, terms: doc.baselineTerms}
	if version <= doc.baselineVersion {
		return current
	}
	for _, revision := range doc.changes {
		if revision.version > version {
			break
		}
		current = revision
	}
	return current
}

func (doc *docState) contentAt(version int) []string {
	return doc.existsAt(version).terms
}

func (state *termState) dfAt(version int) int {
	value := state.baselineDF
	if version <= state.baselineVersion {
		return value
	}
	for _, change := range state.changes {
		if change.version > version {
			break
		}
		value = change.value
	}
	return value
}

func (posting *postingState) existsAt(version int) bool {
	exists := posting.baselineExists
	if version <= posting.baselineVersion {
		return exists
	}
	for _, change := range posting.changes {
		if change.version > version {
			break
		}
		exists = change.value
	}
	return exists
}
