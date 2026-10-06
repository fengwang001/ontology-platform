package alarm

type chatterWindow struct {
	times []int64
}

func (w *chatterWindow) activate(at int64, windowSize int64, threshold int) bool {
	cutoff := at - windowSize
	kept := w.times[:0]
	for _, recorded := range w.times {
		if recorded > cutoff {
			kept = append(kept, recorded)
		}
	}
	kept = append(kept, at)
	w.times = kept
	return len(w.times) >= threshold
}

func (w *chatterWindow) prune(at int64, windowSize int64) {
	cutoff := at - windowSize
	kept := w.times[:0]
	for _, recorded := range w.times {
		if recorded > cutoff {
			kept = append(kept, recorded)
		}
	}
	w.times = kept
}
