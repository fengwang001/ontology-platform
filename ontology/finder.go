package ontology

import (
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

var (
	ErrInvalidWord      = errors.New("invalid word")
	ErrDuplicateWord    = errors.New("duplicate word")
	ErrWordNotFound     = errors.New("word not found")
	ErrInvalidThreshold = errors.New("invalid threshold")
	ErrInvalidLimit     = errors.New("invalid limit")
)

const (
	startPadding rune = '\x02'
	endPadding   rune = '\x03'
)

type Finder struct {
	mu       sync.RWMutex
	entries  map[string]*entry
	postings map[string]map[*entry]struct{}
}

type entry struct {
	word     string
	trigrams map[string]int
	size     int
}

type Result struct {
	Word   string `json:"word"`
	Dice   string `json:"dice"`
	Folded int    `json:"folded"`
}

type hit struct {
	word        string
	numerator   int
	denominator int
	folded      int
}

func NewFinder() *Finder {
	return &Finder{
		entries:  make(map[string]*entry),
		postings: make(map[string]map[*entry]struct{}),
	}
}

func (f *Finder) Add(word string) error {
	if !validWord(word) {
		return ErrInvalidWord
	}

	trigrams, size := trigramMultiset(word)

	f.mu.Lock()
	defer f.mu.Unlock()

	if _, ok := f.entries[word]; ok {
		return ErrDuplicateWord
	}

	item := &entry{word: word, trigrams: trigrams, size: size}
	f.entries[word] = item
	for trigram := range trigrams {
		posting := f.postings[trigram]
		if posting == nil {
			posting = make(map[*entry]struct{})
			f.postings[trigram] = posting
		}
		posting[item] = struct{}{}
	}
	return nil
}

func (f *Finder) Remove(word string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	item, ok := f.entries[word]
	if !ok {
		return ErrWordNotFound
	}

	for trigram := range item.trigrams {
		delete(f.postings[trigram], item)
		if len(f.postings[trigram]) == 0 {
			delete(f.postings, trigram)
		}
	}
	delete(f.entries, word)
	return nil
}

func (f *Finder) Similar(query string, theta, k int) ([]Result, error) {
	results, _, err := f.similar(query, theta, k)
	return results, err
}

func (f *Finder) similar(query string, theta, k int) ([]Result, int, error) {
	if !validWord(query) {
		return nil, 0, ErrInvalidWord
	}
	if theta < 1 || theta > 100 {
		return nil, 0, ErrInvalidThreshold
	}
	if k < 1 {
		return nil, 0, ErrInvalidLimit
	}

	queryTrigrams, querySize := trigramMultiset(query)
	effectiveThreshold := theta
	if runeLen(query) <= 2 {
		effectiveThreshold += 20
		if effectiveThreshold > 100 {
			effectiveThreshold = 100
		}
	}

	f.mu.RLock()
	defer f.mu.RUnlock()

	candidates := make(map[*entry]struct{})
	invertedReads := 0
	for trigram := range queryTrigrams {
		for item := range f.postings[trigram] {
			invertedReads++
			candidates[item] = struct{}{}
		}
	}

	hits := make([]hit, 0, len(candidates))
	for item := range candidates {
		intersection := multisetIntersection(queryTrigrams, item.trigrams)
		denominator := querySize + item.size
		if 200*intersection < effectiveThreshold*denominator {
			continue
		}
		numerator := 2 * intersection
		divisor := greatestCommonDivisor(numerator, denominator)
		hits = append(hits, hit{
			word:        item.word,
			numerator:   numerator / divisor,
			denominator: denominator / divisor,
		})
	}

	sort.Slice(hits, func(i, j int) bool {
		left := int64(hits[i].numerator) * int64(hits[j].denominator)
		right := int64(hits[j].numerator) * int64(hits[i].denominator)
		if left != right {
			return left > right
		}
		return hits[i].word < hits[j].word
	})

	retained := foldHits(hits)

	if len(retained) > k {
		retained = retained[:k]
	}
	results := make([]Result, len(retained))
	for index, item := range retained {
		results[index] = Result{
			Word:   item.word,
			Dice:   formatFraction(item.numerator, item.denominator),
			Folded: item.folded,
		}
	}
	return results, invertedReads, nil
}

func foldHits(hits []hit) []hit {
	retained := make([]hit, 0, len(hits))
	for _, candidate := range hits {
		owner := -1
		for index := range retained {
			if oneIsTruePrefix(retained[index].word, candidate.word) {
				owner = index
				break
			}
		}
		if owner >= 0 {
			retained[owner].folded++
			continue
		}
		retained = append(retained, candidate)
	}
	return retained
}

func validWord(word string) bool {
	return word != "" &&
		utf8.ValidString(word) &&
		!strings.ContainsRune(word, startPadding) &&
		!strings.ContainsRune(word, endPadding)
}

func trigramMultiset(word string) (map[string]int, int) {
	runes := make([]rune, 0, len(word)+4)
	runes = append(runes, startPadding, startPadding)
	for _, r := range word {
		runes = append(runes, r)
	}
	runes = append(runes, endPadding, endPadding)

	trigrams := make(map[string]int, len(runes)-2)
	for index := 0; index+2 < len(runes); index++ {
		trigrams[string(runes[index:index+3])]++
	}
	return trigrams, len(runes) - 2
}

func multisetIntersection(left, right map[string]int) int {
	if len(left) > len(right) {
		left, right = right, left
	}
	intersection := 0
	for trigram, leftCount := range left {
		rightCount := right[trigram]
		if rightCount < leftCount {
			intersection += rightCount
		} else {
			intersection += leftCount
		}
	}
	return intersection
}

func runeLen(word string) int {
	return utf8.RuneCountInString(word)
}

func oneIsTruePrefix(left, right string) bool {
	if left == right {
		return false
	}
	return strings.HasPrefix(left, right) || strings.HasPrefix(right, left)
}

func formatFraction(numerator, denominator int) string {
	return strconv.Itoa(numerator) + "/" + strconv.Itoa(denominator)
}

func greatestCommonDivisor(left, right int) int {
	for right != 0 {
		left, right = right, left%right
	}
	return left
}
