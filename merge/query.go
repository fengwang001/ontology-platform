package merge

import (
	"ontology/phrase"
	"ontology/posting"
	"ontology/segment"
)

// Query runs a phrase query over one consistent snapshot plus the
// in-memory buffer, filtering deleted docs.
func (s *Set) Query(terms ...string) []phrase.Hit {
	snap := s.snap.Load()
	mem := s.memSnapshot()
	var hits []phrase.Hit
	for i, seg := range snap.segs {
		lists, ok := segLists(seg, terms)
		if !ok {
			continue
		}
		for _, h := range phrase.Phrase(lists) {
			if !snap.deleted[i][h.Doc] {
				hits = append(hits, h)
			}
		}
	}
	if len(mem) > 0 {
		hits = append(hits, phrase.Phrase(listsOf(mem, terms))...)
	}
	return hits
}

// AndDocs runs a boolean AND over one consistent snapshot.
func (s *Set) AndDocs(terms ...string) []uint32 {
	snap := s.snap.Load()
	mem := s.memSnapshot()
	var out []uint32
	for i, seg := range snap.segs {
		lists, ok := segLists(seg, terms)
		if !ok {
			continue
		}
		for _, d := range phrase.And(lists) {
			if !snap.deleted[i][d] {
				out = append(out, d)
			}
		}
	}
	if len(mem) > 0 {
		out = append(out, phrase.And(listsOf(mem, terms))...)
	}
	return out
}

func segLists(seg *segment.Segment, terms []string) ([]posting.List, bool) {
	lists := make([]posting.List, len(terms))
	for i, t := range terms {
		l, ok := seg.Postings(t)
		if !ok {
			return nil, false
		}
		lists[i] = l
	}
	return lists, true
}

func listsOf(index map[string]posting.List, terms []string) []posting.List {
	lists := make([]posting.List, len(terms))
	for i, t := range terms {
		lists[i] = index[t]
	}
	return lists
}

func (s *Set) memSnapshot() map[string]posting.List {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.mem) == 0 {
		return nil
	}
	out := map[string]posting.List{}
	for t, b := range s.mem {
		src := b.List()
		cp := make(posting.List, len(src))
		for i, e := range src {
			cp[i] = posting.Entry{Doc: e.Doc, Pos: append([]uint32{}, e.Pos...)}
		}
		out[t] = cp
	}
	return out
}
