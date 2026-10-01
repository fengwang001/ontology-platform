package ontology

func (idx *Index) Compact(keep int) (int, error) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	if keep < 0 || keep > idx.version {
		return idx.watermark, ErrVersionRange
	}
	if keep < idx.watermark {
		return idx.watermark, ErrWatermarkRollback
	}
	if keep == idx.watermark {
		return idx.watermark, nil
	}

	idx.stats = append([]globalStat(nil), idx.stats[keep-idx.statsBaseVersion:]...)
	idx.statsBaseVersion = keep

	for term, state := range idx.terms {
		state.baselineDF = state.dfAt(keep)
		state.baselineVersion = keep
		state.changes = compactIntChanges(state.changes, keep)

		for docID, posting := range state.postings {
			posting.baselineExists = posting.existsAt(keep)
			posting.baselineVersion = keep
			posting.changes = compactBoolChanges(posting.changes, keep)
			if !posting.baselineExists && !boolChangeHasTrue(posting.changes) {
				delete(state.postings, docID)
			}
		}
		if state.baselineDF == 0 && len(state.postings) == 0 {
			delete(idx.terms, term)
		}
	}

	for docID, doc := range idx.docs {
		snapshot := doc.existsAt(keep)
		doc.baselineVersion = keep
		doc.baselineExists = snapshot.exists
		doc.baselineTerms = append([]string(nil), snapshot.terms...)
		doc.changes = compactDocChanges(doc.changes, keep)
		if !doc.baselineExists && !docChangeHasLive(doc.changes) {
			delete(idx.docs, docID)
		}
	}

	idx.watermark = keep
	return idx.watermark, nil
}

func compactIntChanges(changes []point, keep int) []point {
	var result []point
	for _, change := range changes {
		if change.version >= keep {
			result = append(result, change)
		}
	}
	return result
}

func compactBoolChanges(changes []boolPoint, keep int) []boolPoint {
	var result []boolPoint
	for _, change := range changes {
		if change.version >= keep {
			result = append(result, change)
		}
	}
	return result
}

func compactDocChanges(changes []docRevision, keep int) []docRevision {
	var result []docRevision
	for _, change := range changes {
		if change.version >= keep {
			result = append(result, change)
		}
	}
	return result
}

func boolChangeHasTrue(changes []boolPoint) bool {
	for _, change := range changes {
		if change.value {
			return true
		}
	}
	return false
}

func docChangeHasLive(changes []docRevision) bool {
	for _, change := range changes {
		if change.exists {
			return true
		}
	}
	return false
}
