package ontology

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
)

type naiveState struct {
	docs  map[string]map[string]struct{}
	picks []pickRecord
	t     int
}

type operation struct {
	name   string
	docID  string
	terms  []string
	term   string
	query  string
	maxExp int
}

func newNaiveState() *naiveState {
	return &naiveState{docs: make(map[string]map[string]struct{})}
}

func validTerm(term string) bool {
	return term != "" && !strings.ContainsRune(term, ' ')
}

func (n *naiveState) index(docID string, terms []string) error {
	if docID == "" || len(terms) == 0 {
		return ErrInvalidArgument
	}
	uniqueTerms := make(map[string]struct{})
	for _, term := range terms {
		if !validTerm(term) {
			return ErrInvalidArgument
		}
		uniqueTerms[term] = struct{}{}
	}
	if _, ok := n.docs[docID]; ok {
		return ErrDuplicateDocID
	}
	n.docs[docID] = uniqueTerms
	return nil
}

func (n *naiveState) remove(docID string) error {
	if _, ok := n.docs[docID]; !ok {
		return ErrDocNotFound
	}
	delete(n.docs, docID)
	return nil
}

func (n *naiveState) termExists(term string) bool {
	for _, doc := range n.docs {
		if _, ok := doc[term]; ok {
			return true
		}
	}
	return false
}

func (n *naiveState) pick(term string) error {
	if !validTerm(term) {
		return ErrInvalidArgument
	}
	if !n.termExists(term) {
		return ErrTermNotFound
	}
	for _, record := range n.picks {
		if record.term == term && record.t == n.t {
			return nil
		}
	}
	n.picks = append(n.picks, pickRecord{term: term, t: n.t})
	return nil
}

func (n *naiveState) expand(query string, maxExp int) (ExpandResult, error) {
	words := make([]string, 0)
	for _, word := range strings.Split(query, " ") {
		if word != "" {
			words = append(words, word)
		}
	}
	if len(words) == 0 {
		return ExpandResult{}, ErrEmptyQuery
	}
	if len(words) > 8 {
		return ExpandResult{}, ErrQueryTooLong
	}
	if maxExp < 1 || maxExp > 1000 {
		return ExpandResult{}, ErrInvalidMaxExp
	}

	n.t++
	completeWords := words
	prefix := ""
	if len(query) > 0 && query[len(query)-1] != ' ' {
		completeWords = words[:len(words)-1]
		prefix = words[len(words)-1]
	}

	conditionalDocIDs := make([]string, 0)
	for docID := range n.docs {
		matches := true
		for _, word := range completeWords {
			if _, ok := n.docs[docID][word]; !ok {
				matches = false
				break
			}
		}
		if matches {
			conditionalDocIDs = append(conditionalDocIDs, docID)
		}
	}
	sort.Strings(conditionalDocIDs)

	if prefix == "" {
		return ExpandResult{
			Candidates: []Candidate{},
			Hits:       append([]string{}, conditionalDocIDs...),
		}, nil
	}

	termSet := make(map[string]struct{})
	for _, docID := range conditionalDocIDs {
		for term := range n.docs[docID] {
			if strings.HasPrefix(term, prefix) {
				termSet[term] = struct{}{}
			}
		}
	}
	candidates := make([]Candidate, 0, len(termSet))
	for term := range termSet {
		conditionalDF := 0
		for _, docID := range conditionalDocIDs {
			if _, ok := n.docs[docID][term]; ok {
				conditionalDF++
			}
		}
		effectivePicks := 0
		for _, record := range n.picks {
			if record.term == term && n.t-record.t <= 3 {
				effectivePicks++
			}
		}
		candidates = append(candidates, Candidate{
			Term:          term,
			ConditionalDF: conditionalDF,
			Score:         conditionalDF + 2*effectivePicks,
		})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Score != candidates[j].Score {
			return candidates[i].Score > candidates[j].Score
		}
		return candidates[i].Term < candidates[j].Term
	})

	total := len(candidates)
	truncated := total > maxExp
	if truncated {
		candidates = candidates[:maxExp]
	}

	retained := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		retained[candidate.Term] = struct{}{}
	}
	hits := make([]string, 0)
	for _, docID := range conditionalDocIDs {
		for term := range retained {
			if _, ok := n.docs[docID][term]; ok {
				hits = append(hits, docID)
				break
			}
		}
	}
	sort.Strings(hits)

	return ExpandResult{Candidates: candidates, Truncated: truncated, Hits: hits}, nil
}

func TestRandomDifferentialAgainstNaiveScanner(t *testing.T) {
	rng := rand.New(rand.NewSource(20261002))
	terms := []string{"", "a", "ap", "ape", "apple", "apply", "apex", "b", "bad term", "z", "anchor", "pa", "pb", "pea", "pear"}
	docIDs := []string{"", "d1", "d2", "d3", "d4", "d5"}
	queryParts := []string{"a", "ap", "ape", "apple", "anchor", "missing", "p", "", " "}

	for sequence := 0; sequence < 2000; sequence++ {
		t.Run(fmt.Sprintf("sequence_%04d", sequence), func(t *testing.T) {
			actual := NewQueryExpander()
			reference := newNaiveState()
			length := 8 + rng.Intn(25)

			for step := 0; step < length; step++ {
				op := operation{name: []string{"index", "remove", "pick", "expand"}[rng.Intn(4)]}

				switch op.name {
				case "index":
					op.docID = docIDs[rng.Intn(len(docIDs))]
					termCount := 1 + rng.Intn(4)
					for i := 0; i < termCount; i++ {
						op.terms = append(op.terms, terms[rng.Intn(len(terms))])
					}
					actualErr := actual.Index(op.docID, append([]string{}, op.terms...))
					referenceErr := reference.index(op.docID, append([]string{}, op.terms...))
					t.Logf("input=%+v actualErr=%v referenceErr=%v basis=Index validates docID/terms and rejects existing docID before state changes", op, actualErr, referenceErr)
					if !sameSentinel(actualErr, referenceErr) {
						t.Fatalf("Index error mismatch")
					}
				case "remove":
					op.docID = docIDs[1+rng.Intn(len(docIDs)-1)]
					actualErr := actual.Remove(op.docID)
					referenceErr := reference.remove(op.docID)
					t.Logf("input=%+v actualErr=%v referenceErr=%v basis=Remove rejects missing docID", op, actualErr, referenceErr)
					if !sameSentinel(actualErr, referenceErr) {
						t.Fatalf("Remove error mismatch")
					}
				case "pick":
					op.term = terms[rng.Intn(len(terms))]
					actualErr := actual.Pick(op.term)
					referenceErr := reference.pick(op.term)
					t.Logf("input=%+v actualErr=%v referenceErr=%v basis=Pick validates term/current existence and deduplicates by current T", op, actualErr, referenceErr)
					if !sameSentinel(actualErr, referenceErr) {
						t.Fatalf("Pick error mismatch")
					}
				case "expand":
					partCount := rng.Intn(10)
					parts := make([]string, partCount)
					for i := range parts {
						parts[i] = queryParts[rng.Intn(len(queryParts))]
					}
					op.query = strings.Join(parts, strings.Repeat(" ", 1+rng.Intn(3)))
					if rng.Intn(2) == 0 {
						op.query += " "
					}
					if rng.Intn(8) == 0 {
						op.maxExp = rng.Intn(1003)
					} else {
						op.maxExp = 1 + rng.Intn(5)
					}

					actualResult, actualErr := actual.Expand(op.query, op.maxExp)
					referenceResult, referenceErr := reference.expand(op.query, op.maxExp)
					t.Logf("input=%+v actual=(%+v,%v) reference=(%+v,%v) basis=Expand parses ASCII spaces, filters conditional docs, scores df+2picks in t-c<=3, sorts and truncates", op, actualResult, actualErr, referenceResult, referenceErr)
					if !sameSentinel(actualErr, referenceErr) {
						t.Fatalf("Expand error mismatch")
					}
					if actualErr == nil && !reflect.DeepEqual(actualResult, referenceResult) {
						t.Fatalf("Expand result mismatch")
					}
					if actualErr == nil {
						mutateResult(actualResult)
					}
				}
			}
		})
	}
}

func sameSentinel(actual error, reference error) bool {
	if actual == nil || reference == nil {
		return actual == reference
	}
	return actual == reference
}

func mutateResult(result ExpandResult) {
	for i := range result.Candidates {
		result.Candidates[i].Term = "mutated"
		result.Candidates[i].ConditionalDF = -1
		result.Candidates[i].Score = -1
	}
	for i := range result.Hits {
		result.Hits[i] = "mutated"
	}
}
