package ontology

import (
	"sync"
	"sync/atomic"
)

type Match struct {
	Word   string
	Score  string
	Folded int
}

type Finder struct {
	mu           sync.RWMutex
	words        map[string]*indexEntry
	postings     map[string]map[*indexEntry]struct{}
	postingReads atomic.Uint64
}

type indexEntry struct {
	word  string
	runes []rune
	size  int
	grams trigramSet
}

func NewFinder() *Finder {
	return &Finder{
		words:    make(map[string]*indexEntry),
		postings: make(map[string]map[*indexEntry]struct{}),
	}
}

func (f *Finder) Add(word string) error {
	if !validWord(word) {
		return ErrInvalidWord
	}

	runes := []rune(word)
	grams := trigrams(word)
	entry := &indexEntry{
		word:  word,
		runes: runes,
		size:  len(runes) + 2,
		grams: grams,
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if _, exists := f.words[word]; exists {
		return ErrWordExists
	}

	f.words[word] = entry
	for gram := range grams {
		posting := f.postings[gram]
		if posting == nil {
			posting = make(map[*indexEntry]struct{})
			f.postings[gram] = posting
		}
		posting[entry] = struct{}{}
	}
	return nil
}

func (f *Finder) Remove(word string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	entry, exists := f.words[word]
	if !exists {
		return ErrWordMissing
	}

	for gram := range entry.grams {
		delete(f.postings[gram], entry)
		if len(f.postings[gram]) == 0 {
			delete(f.postings, gram)
		}
	}
	delete(f.words, word)
	return nil
}
