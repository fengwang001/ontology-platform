package ontology

import (
	"strconv"
	"sync"
	"testing"
)

// buildScaleIndex creates nDocs documents. Exactly `hits` documents carry the
// query terms (each with the same fixed posting footprint); every other
// document contains only unrelated vocabulary, so the summed posting length of
// the distinct query terms is identical across scale tiers.
func buildScaleIndex(b *testing.B, nDocs, hits int) {
	b.Helper()
	ix := NewIndex()
	for d := 0; d < nDocs; d++ {
		docID := strconv.Itoa(d)
		var terms []string
		if d < hits {
			terms = []string{"a", "b", "x", "a", "b"}
		} else {
			terms = []string{"" + "z" + strconv.Itoa(d%7), "y"}
		}
		if err := ix.Add(docID, terms); err != nil {
			b.Fatalf("add %s: %v", docID, err)
		}
	}
	query := []string{"a", "b"}
	distinctPostingSum := hits * 4 // each hit doc: a x2 + b x2
	before := ix.PostingReads()
	got, err := ix.Phrase(query, 0, 100, 10)
	if err != nil {
		b.Fatalf("phrase: %v", err)
	}
	reads := ix.PostingReads() - before
	if reads != distinctPostingSum {
		b.Fatalf("scale n=%d hits=%d: read %d entries, want exactly %d",
			nDocs, hits, reads, distinctPostingSum)
	}
	if len(got) != 10 { // k=10: all hit docs tie at freq 2, minGap 0
		b.Fatalf("scale n=%d: got %d results, want 10 (top-k)", nDocs, len(got))
	}
}

func BenchmarkPostingReads1k(b *testing.B) {
	for i := 0; i < b.N; i++ {
		buildScaleIndex(b, 1000, 50)
	}
}

func BenchmarkPostingReads100k(b *testing.B) {
	for i := 0; i < b.N; i++ {
		buildScaleIndex(b, 100000, 50)
	}
}

// TestPostingReadsScale directly compares the two tiers: the counter must not
// grow with the number of unrelated documents and must never exceed the summed
// posting length of the distinct query terms.
func TestPostingReadsScale(t *testing.T) {
	run := func(nDocs, hits int) int {
		t.Helper()
		ix := NewIndex()
		for d := 0; d < nDocs; d++ {
			docID := strconv.Itoa(d)
			var terms []string
			if d < hits {
				terms = []string{"a", "b", "x", "a", "b"}
			} else {
				terms = []string{"z" + strconv.Itoa(d%7), "y"}
			}
			if err := ix.Add(docID, terms); err != nil {
				t.Fatalf("add %s: %v", docID, err)
			}
		}
		before := ix.PostingReads()
		if _, err := ix.Phrase([]string{"a", "b"}, 0, 100, 10); err != nil {
			t.Fatalf("phrase: %v", err)
		}
		reads := ix.PostingReads() - before
		// Upper bound: summed posting length of distinct query terms over all
		// documents that contain every distinct term.
		bound := hits * 4
		if reads > bound {
			t.Fatalf("n=%d: reads %d exceed bound %d", nDocs, reads, bound)
		}
		return reads
	}
	reads1k := run(1000, 50)
	reads100k := run(100000, 50)
	t.Logf("posting entries read: 1000 docs -> %d ; 100000 docs -> %d (identical)",
		reads1k, reads100k)
	if reads1k != reads100k {
		t.Fatalf("counter grew with unrelated documents: %d vs %d", reads1k, reads100k)
	}

	// Repeated query terms share one posting slice: [a,a,b] distinct terms are
	// a,b, so reads stay at the distinct-term sum, not the query-length sum.
	ix := NewIndex()
	for d := 0; d < 2000; d++ {
		if err := ix.Add(strconv.Itoa(d), []string{"z", "y"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := ix.Add("hit", []string{"a", "a", "b"}); err != nil {
		t.Fatal(err)
	}
	before := ix.PostingReads()
	if _, err := ix.Phrase([]string{"a", "a", "b"}, 0, 100, 10); err != nil {
		t.Fatal(err)
	}
	reads := ix.PostingReads() - before
	if reads != 3 {
		t.Fatalf("repeated-term query read %d entries, want 3 (a x2 + b x1 once each)", reads)
	}
}

// Concurrent Add/Delete/Phrase must be race-free and observe serializable
// snapshots; run with -race.
func TestConcurrentAccess(t *testing.T) {
	ix := NewIndex()
	for d := 0; d < 200; d++ {
		if err := ix.Add(strconv.Itoa(d), []string{"a", "b"}); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				docID := strconv.Itoa((id*300 + i) % 400)
				switch i % 4 {
				case 0:
					_ = ix.Add(docID+"n", []string{"a", "x", "b"})
				case 1:
					_ = ix.Delete(docID)
				case 2:
					got, err := ix.Phrase([]string{"a", "b"}, 1, 100, 10)
					if err != nil {
						t.Errorf("phrase: %v", err)
						return
					}
					for _, r := range got {
						if r.Freq < 1 || r.MinGap < 0 || r.MinGap > 1 {
							t.Errorf("inconsistent snapshot result %+v", r)
							return
						}
					}
				default:
					_ = ix.PostingReads()
				}
			}
		}(w)
	}
	wg.Wait()
}
