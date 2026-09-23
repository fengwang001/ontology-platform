package main

import (
	"ontology/phrase"
	"ontology/posting"
)

func indexOf(docs ...[]string) map[string]posting.List {
	builders := map[string]*posting.Builder{}
	for d, tokens := range docs {
		for p, tok := range tokens {
			b := builders[tok]
			if b == nil {
				b = &posting.Builder{}
				builders[tok] = b
			}
			b.Add(uint32(d), uint32(p))
		}
	}
	out := map[string]posting.List{}
	for t, b := range builders {
		out[t] = b.List()
	}
	return out
}

func phraseCount(index map[string]posting.List, terms ...string) int {
	lists := make([]posting.List, len(terms))
	for i, t := range terms {
		lists[i] = index[t]
	}
	n := 0
	for _, h := range phrase.Phrase(lists) {
		n += len(h.Starts)
	}
	return n
}

func docList(lo, hi uint32) posting.List {
	var l posting.List
	for d := lo; d < hi; d++ {
		l = append(l, posting.Entry{Doc: d, Pos: []uint32{0}})
	}
	return l
}
