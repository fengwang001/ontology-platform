package bm25

func (r *Ranker) Compact(keep int) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if keep < 0 || keep > r.version {
		return ErrVersionOutOfRange
	}
	if keep < r.watermark {
		return ErrWatermarkRollback
	}
	if keep == r.watermark {
		return nil
	}

	baseStats := r.statsAt(keep)
	keptGlobal := make([]globalEvent, 0, len(r.global)+1)
	if keep > 0 {
		keptGlobal = append(keptGlobal, globalEvent{version: keep, deltaN: baseStats.n, deltaL: baseStats.l})
	}
	for _, event := range r.global {
		if event.version > keep {
			keptGlobal = append(keptGlobal, event)
		}
	}
	r.global = keptGlobal

	for _, doc := range r.docs {
		if doc.start < keep {
			doc.start = keep
		}
	}
	compactDocSpans(r.docArchive, keep)

	for term, data := range r.terms {
		baseDF := dfAt(data.dfEvents, keep, r.watermark)
		keptDFEvents := make([]dfEvent, 0, len(data.dfEvents)+1)
		if keep > 0 {
			keptDFEvents = append(keptDFEvents, dfEvent{version: keep, delta: baseDF})
		}
		for _, event := range data.dfEvents {
			if event.version > keep {
				keptDFEvents = append(keptDFEvents, event)
			}
		}
		data.dfEvents = keptDFEvents

		for _, posting := range data.active {
			if posting.start < keep {
				posting.start = keep
			}
		}
		compactIntervals(data.archive, keep)

		if baseDF == 0 && len(data.active) == 0 && len(data.archive) == 0 {
			delete(r.terms, term)
		}
	}

	r.watermark = keep
	return nil
}

func clampStart(start *int, keep int) {
	if *start < keep {
		*start = keep
	}
}

func compactDocSpans(archive map[string][]*docState, keep int) {
	for docID, spans := range archive {
		kept := make([]*docState, 0, len(spans))
		for _, span := range spans {
			if span.end > keep {
				clampStart(&span.start, keep)
				kept = append(kept, span)
			}
		}
		if len(kept) == 0 {
			delete(archive, docID)
		} else {
			archive[docID] = kept
		}
	}
}

func compactIntervals(archive map[string][]*interval, keep int) {
	for docID, postings := range archive {
		kept := make([]*interval, 0, len(postings))
		for _, posting := range postings {
			if posting.end > keep {
				clampStart(&posting.start, keep)
				kept = append(kept, posting)
			}
		}
		if len(kept) == 0 {
			delete(archive, docID)
		} else {
			archive[docID] = kept
		}
	}
}
