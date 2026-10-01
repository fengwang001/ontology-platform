package bm25

func (r *Ranker) Add(docID string, terms []string) error {
	if docID == "" || invalidTerms(terms) {
		return ErrInvalidArgument
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.docs[docID]; ok {
		return ErrDuplicateDocument
	}

	newVersion := r.version + 1
	counts := termCounts(terms)
	for term, tf := range counts {
		r.addPosting(term, docID, newVersion, tf)
	}
	r.docs[docID] = &docState{start: newVersion, dl: len(terms)}
	r.currentTerms[docID] = counts
	r.global = append(r.global, globalEvent{
		version: newVersion,
		deltaN:  1,
		deltaL:  len(terms),
	})
	r.version = newVersion
	return nil
}

func (r *Ranker) Update(docID string, terms []string) error {
	if docID == "" || invalidTerms(terms) {
		return ErrInvalidArgument
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	doc, ok := r.docs[docID]
	if !ok {
		return ErrDocumentNotFound
	}

	newVersion := r.version + 1
	oldCounts := r.currentTerms[docID]
	newCounts := termCounts(terms)

	for term, oldTF := range oldCounts {
		newTF := newCounts[term]
		if newTF != oldTF {
			r.removePosting(term, docID, newVersion)
			if newTF > 0 {
				r.addPosting(term, docID, newVersion, newTF)
			}
		}
	}
	for term, newTF := range newCounts {
		if _, ok := oldCounts[term]; !ok {
			r.addPosting(term, docID, newVersion, newTF)
		}
	}

	oldLength := doc.dl
	doc.end = newVersion
	r.docArchive[docID] = append(r.docArchive[docID], doc)
	r.docs[docID] = &docState{start: newVersion, dl: len(terms)}
	r.currentTerms[docID] = newCounts
	r.global = append(r.global, globalEvent{
		version: newVersion,
		deltaL:  len(terms) - oldLength,
	})
	r.version = newVersion
	return nil
}

func (r *Ranker) Delete(docID string) error {
	if docID == "" {
		return ErrInvalidArgument
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	doc, ok := r.docs[docID]
	if !ok {
		return ErrDocumentNotFound
	}

	newVersion := r.version + 1
	for term := range r.currentTerms[docID] {
		r.removePosting(term, docID, newVersion)
	}

	doc.end = newVersion
	r.docArchive[docID] = append(r.docArchive[docID], doc)
	delete(r.docs, docID)
	delete(r.currentTerms, docID)
	r.global = append(r.global, globalEvent{
		version: newVersion,
		deltaN:  -1,
		deltaL:  -doc.dl,
	})
	r.version = newVersion
	return nil
}

func (r *Ranker) getTermData(term string) *termData {
	data := r.terms[term]
	if data == nil {
		data = &termData{
			active:  make(map[string]*interval),
			archive: make(map[string][]*interval),
		}
		r.terms[term] = data
	}
	return data
}

func (r *Ranker) addPosting(term, docID string, version, tf int) {
	data := r.getTermData(term)
	data.active[docID] = &interval{start: version, tf: tf}
	data.dfEvents = append(data.dfEvents, dfEvent{version: version, delta: 1})
}

func (r *Ranker) removePosting(term, docID string, version int) {
	data := r.getTermData(term)
	posting := data.active[docID]
	posting.end = version
	data.archive[docID] = append(data.archive[docID], posting)
	delete(data.active, docID)
	data.dfEvents = append(data.dfEvents, dfEvent{version: version, delta: -1})
}
