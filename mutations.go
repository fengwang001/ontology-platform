package ontology

func (idx *Index) Add(docID string, terms []string) (int, error) {
	if docID == "" || !validTerms(terms) {
		return 0, ErrInvalidArgument
	}
	nextTerms := append([]string(nil), terms...)

	idx.mu.Lock()
	defer idx.mu.Unlock()
	if idx.docExistsLocked(docID) {
		return 0, ErrDuplicateDoc
	}
	idx.applyLocked(docID, nil, nextTerms)
	return idx.version, nil
}

func (idx *Index) Update(docID string, terms []string) (int, error) {
	if docID == "" || !validTerms(terms) {
		return 0, ErrInvalidArgument
	}
	nextTerms := append([]string(nil), terms...)

	idx.mu.Lock()
	defer idx.mu.Unlock()
	doc := idx.docs[docID]
	if doc == nil || !doc.existsAt(idx.version).exists {
		return 0, ErrDocNotFound
	}
	idx.applyLocked(docID, doc.contentAt(idx.version), nextTerms)
	return idx.version, nil
}

func (idx *Index) Delete(docID string) (int, error) {
	if docID == "" {
		return 0, ErrInvalidArgument
	}

	idx.mu.Lock()
	defer idx.mu.Unlock()
	doc := idx.docs[docID]
	if doc == nil || !doc.existsAt(idx.version).exists {
		return 0, ErrDocNotFound
	}
	idx.applyLocked(docID, doc.contentAt(idx.version), nil)
	return idx.version, nil
}

func (idx *Index) applyLocked(docID string, oldTerms, newTerms []string) {
	idx.version++
	oldFrequency := termFrequencies(oldTerms)
	newFrequency := termFrequencies(newTerms)

	previous := idx.stats[idx.version-1-idx.statsBaseVersion]
	next := globalStat{n: previous.n, l: previous.l}
	switch {
	case oldTerms == nil:
		next.n++
		next.l += len(newTerms)
	case newTerms == nil:
		next.n--
		next.l -= len(oldTerms)
	default:
		next.l += len(newTerms) - len(oldTerms)
	}
	idx.stats = append(idx.stats, next)

	doc := idx.docs[docID]
	if doc == nil {
		doc = &docState{}
		idx.docs[docID] = doc
	}
	doc.changes = append(doc.changes, docRevision{
		version: idx.version,
		exists:  newTerms != nil,
		terms:   append([]string(nil), newTerms...),
	})

	for term := range oldFrequency {
		if newFrequency[term] == 0 {
			idx.setPostingLocked(term, docID, false)
		}
	}
	for term := range newFrequency {
		if oldFrequency[term] == 0 {
			idx.setPostingLocked(term, docID, true)
		}
	}
}

func (idx *Index) setPostingLocked(term, docID string, exists bool) {
	state := idx.terms[term]
	if state == nil {
		state = &termState{postings: make(map[string]*postingState)}
		idx.terms[term] = state
	}
	posting := state.postings[docID]
	if posting == nil {
		posting = &postingState{}
		state.postings[docID] = posting
	}

	nextDF := state.dfAt(idx.version - 1)
	if exists {
		nextDF++
	} else {
		nextDF--
	}
	state.changes = append(state.changes, point{version: idx.version, value: nextDF})
	posting.changes = append(posting.changes, boolPoint{version: idx.version, value: exists})
}

func (idx *Index) docExistsLocked(docID string) bool {
	doc := idx.docs[docID]
	return doc != nil && doc.existsAt(idx.version).exists
}
