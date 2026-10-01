package ontology

import (
	"math/rand"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// naiveMatch exhaustively backtracks every strictly increasing position
// sequence for one document and records, per distinct first position, whether
// any valid match exists and the minimum total gap.
func naiveMatch(terms, query []string, slop, maxGap int) (int, int) {
	if len(query) == 1 {
		cnt := 0
		for _, term := range terms {
			if term == query[0] {
				cnt++
			}
		}
		return cnt, 0
	}
	posByTerm := make(map[string][]int)
	var bounds []int
	for pos, term := range terms {
		if term == Boundary {
			bounds = append(bounds, pos)
			continue
		}
		posByTerm[term] = append(posByTerm[term], pos)
	}
	gapByStart := make(map[int]int)
	var dfs func(j int, picked []int)
	dfs = func(j int, picked []int) {
		if j == len(query) {
			first, last := picked[0], picked[len(picked)-1]
			if spansBoundary(bounds, first, last) {
				return
			}
			gap := last - first - (len(query) - 1)
			if gap > slop {
				return
			}
			if prev, ok := gapByStart[first]; !ok || gap < prev {
				gapByStart[first] = gap
			}
			return
		}
		prevPos := -1
		if j > 0 {
			prevPos = picked[j-1]
		}
		for _, pos := range posByTerm[query[j]] {
			if pos <= prevPos {
				continue
			}
			if j > 0 && pos-prevPos-1 > maxGap {
				break // positions ascending: every later option is worse
			}
			if j > 0 {
				// Partial total gap can never decrease later.
				partial := pos - picked[0] - j
				if partial > slop {
					break
				}
			}
			dfs(j+1, append(picked, pos))
		}
	}
	dfs(0, nil)
	if len(gapByStart) == 0 {
		return 0, 0
	}
	best := -1
	for _, gap := range gapByStart {
		if best < 0 || gap < best {
			best = gap
		}
	}
	return len(gapByStart), best
}

func spansBoundary(bounds []int, first, last int) bool {
	i := sort.SearchInts(bounds, first)
	return i < len(bounds) && bounds[i] <= last
}

// naivePhrase runs the reference implementation over all given documents and
// reproduces the ranking and top-k rule.
func naivePhrase(docs map[string][]string, query []string, slop, maxGap, k int) []Result {
	var out []Result
	for docID, terms := range docs {
		freq, minGap := naiveMatch(terms, query, slop, maxGap)
		if freq > 0 {
			out = append(out, Result{DocID: docID, Freq: freq, MinGap: minGap})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Freq != out[j].Freq {
			return out[i].Freq > out[j].Freq
		}
		if out[i].MinGap != out[j].MinGap {
			return out[i].MinGap < out[j].MinGap
		}
		return out[i].DocID < out[j].DocID
	})
	if len(out) > k {
		out = out[:k]
	}
	return out
}

func formatTerms(terms []string) string {
	parts := make([]string, len(terms))
	for i, term := range terms {
		if term == Boundary {
			parts[i] = Boundary
		} else {
			parts[i] = term
		}
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func formatResults(rs []Result) string {
	if len(rs) == 0 {
		return "[]"
	}
	parts := make([]string, len(rs))
	for i, r := range rs {
		parts[i] = "(" + r.DocID + ",f=" + strconv.Itoa(r.Freq) +
			",g=" + strconv.Itoa(r.MinGap) + ")"
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// TestNaiveFuzz replays 2000 random op sequences and compares every Phrase
// result against per-first-position backtracking. Inputs, outputs and the
// decision basis are written to testdata/fuzz.log.
func TestNaiveFuzz(t *testing.T) {
	const rounds = 2000
	logFile, err := os.Create("testdata/fuzz.log")
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	defer logFile.Close()

	alphabet := []string{"a", "b", "c", Boundary}
	rng := rand.New(rand.NewSource(20261001))
	for round := 0; round < rounds; round++ {
		ix := NewIndex()
		docs := make(map[string][]string)
		var log strings.Builder

		numDocs := 1 + rng.Intn(5)
		for d := 0; d < numDocs; d++ {
			docID := "d" + strconv.Itoa(d)
			length := 1 + rng.Intn(10)
			terms := make([]string, length)
			for i := range terms {
				terms[i] = alphabet[rng.Intn(len(alphabet))]
			}
			if rng.Intn(3) == 0 {
				terms = append(terms, Boundary)
			}
			err := ix.Add(docID, terms)
			if err == nil {
				docs[docID] = terms
			}
		}
		if rng.Intn(2) == 0 && len(docs) > 0 {
			var victim string
			for id := range docs {
				victim = id
				break
			}
			if err := ix.Delete(victim); err == nil {
				delete(docs, victim)
			}
		}

		qlen := 1 + rng.Intn(4)
		query := make([]string, qlen)
		for i := range query {
			query[i] = alphabet[rng.Intn(3)]
		}
		slop := rng.Intn(5)
		maxGap := rng.Intn(5)
		k := 1 + rng.Intn(4)

		got, gerr := ix.Phrase(query, slop, maxGap, k)
		want := naivePhrase(docs, query, slop, maxGap, k)

		log.WriteString("round=" + strconv.Itoa(round))
		ids := make([]string, 0, len(docs))
		for id := range docs {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			log.WriteString(" doc:" + id + "=" + formatTerms(docs[id]))
		}
		log.WriteString(" query=" + formatTerms(query))
		log.WriteString(" slop=" + strconv.Itoa(slop))
		log.WriteString(" maxGap=" + strconv.Itoa(maxGap))
		log.WriteString(" k=" + strconv.Itoa(k))
		log.WriteString(" got=" + formatResults(got))
		log.WriteString(" want=" + formatResults(want))
		basis := "basis: freq=distinct p1 with >=1 strictly-increasing chain; " +
			"per-step gap<=maxGap; sum gap=pn-p1-(n-1)<=slop; no <S> in [p1,pn]"
		log.WriteString(" " + basis + "\n")
		if _, err := logFile.WriteString(log.String()); err != nil {
			t.Fatalf("write log: %v", err)
		}

		if gerr != nil {
			t.Fatalf("round %d: unexpected error %v", round, gerr)
		}
		if !eqResults(got, want) {
			t.Fatalf("round %d mismatch:\n%s\n got %v\n want %v", round, log.String(), got, want)
		}
	}
	t.Logf("fuzz: %d rounds passed; details in testdata/fuzz.log", rounds)
}
