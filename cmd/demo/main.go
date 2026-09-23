// Command demo exercises the inverted index end to end: overlapping
// phrase counts, comparison bounds, truncation classification, prefix
// recovery, and delete/merge equivalence. It prints one OK/FAIL line
// per check and a final total.
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"ontology/merge"
	"ontology/phrase"
	"ontology/posting"
	"ontology/segment"
)

var passed, total int

func check(name string, ok bool, detail string) {
	total++
	status := "FAIL"
	if ok {
		status = "OK"
		passed++
	}
	if detail != "" {
		fmt.Printf("%s %s %s\n", status, name, detail)
		return
	}
	fmt.Printf("%s %s\n", status, name)
}

func main() {
	check(`AAA/"AA"=2`, phraseCount(indexOf([]string{"A", "A", "A"}), "A", "A") == 2, "")
	check(`AAAA/"AA"=3`, phraseCount(indexOf([]string{"A", "A", "A", "A"}), "A", "A") == 3, "")
	check(`ABABA/"ABA"=2`, phraseCount(indexOf([]string{"A", "B", "A", "B", "A"}), "A", "B", "A") == 2, "")

	const n = 10000
	tokens := make([]string, 0, 2*n)
	for i := 0; i < n; i++ {
		tokens = append(tokens, "A", "B")
	}
	phrase.ResetCounters()
	hits := phraseCount(indexOf(tokens), "A", "B")
	cmp := phrase.PosComparisons()
	check("phrase-10k", hits == n && cmp <= 4*2*n,
		fmt.Sprintf("hits=%d cmp=%d bound=%d", hits, cmp, 4*2*n))

	phrase.ResetCounters()
	andLists := []posting.List{docList(0, 150), docList(0, 100), docList(0, 120)}
	andN := len(phrase.And(andLists))
	andCmp := phrase.AndComparisons()
	check("and-shortest", andN == 100 && andCmp <= 4*100*3,
		fmt.Sprintf("docs=%d cmp=%d bound=%d", andN, andCmp, 4*100*3))

	dir, err := os.MkdirTemp("", "demo")
	if err != nil {
		check("mktemp", false, err.Error())
		os.Exit(1)
	}
	defer os.RemoveAll(dir)
	truncationChecks(dir)
	deleteMergeChecks(dir)

	fmt.Printf("TOTAL %d/%d OK\n", passed, total)
	if passed != total {
		os.Exit(1)
	}
}

func truncationChecks(dir string) {
	lists := map[string]posting.List{}
	for i := 0; i < 200; i++ {
		term := fmt.Sprintf("t%03d", i)
		var b posting.Builder
		for d := 0; d <= i%5; d++ {
			b.Add(uint32(d), uint32(d))
		}
		lists[term] = b.List()
	}
	full := filepath.Join(dir, "full.seg")
	if err := segment.Write(full, lists); err != nil {
		check("segment-write", false, err.Error())
		return
	}
	data, err := os.ReadFile(full)
	if err != nil {
		check("segment-read", false, err.Error())
		return
	}
	classes := []error{segment.ErrHeaderIncomplete, segment.ErrDictIncomplete,
		segment.ErrPostingsIncomplete, segment.ErrCRCMismatch}
	names := []string{"header", "dict", "postings", "crc"}
	seen := map[int]bool{}
	danglingFree := true
	for cut := 1; cut < len(data); cut++ {
		p := filepath.Join(dir, "trunc.seg")
		if err := os.WriteFile(p, data[:cut], 0o644); err != nil {
			check("trunc-write", false, err.Error())
			return
		}
		_, err := segment.Open(p)
		for i, class := range classes {
			if errors.Is(err, class) {
				seen[i] = true
			}
		}
		rec, rerr := segment.Recover(p)
		if rerr == nil {
			danglingFree = false
			continue
		}
		for _, term := range rec.Terms() {
			l, ok := rec.Postings(term)
			if !ok {
				danglingFree = false
				continue
			}
			if _, derr := posting.Decode(posting.Encode(l)); derr != nil {
				danglingFree = false
			}
		}
	}
	for i, name := range names {
		check("truncate-"+name, seen[i], "")
	}
	check("recover-no-dangling", danglingFree, "")
}

func deleteMergeChecks(dir string) {
	sub := filepath.Join(dir, "set")
	if err := os.Mkdir(sub, 0o755); err != nil {
		check("set-mkdir", false, err.Error())
		return
	}
	set, err := merge.Open(sub)
	if err != nil {
		check("set-open", false, err.Error())
		return
	}
	for d := 0; d < 30; d++ {
		set.AddDocument([]string{"A", "B"})
		if d%10 == 9 {
			if err := set.Flush(); err != nil {
				check("flush", false, err.Error())
				return
			}
		}
	}
	for _, d := range []uint32{3, 17, 29} {
		set.Delete(d)
	}
	immediate := set.Query("A", "B")
	if _, err := set.MergeSegments(); err != nil {
		check("merge", false, err.Error())
		return
	}
	merged := set.Query("A", "B")
	same := len(immediate) == len(merged) && len(immediate) == 27
	for i := range immediate {
		if same && immediate[i].Doc != merged[i].Doc {
			same = false
		}
	}
	check("delete-merge-equal", same, fmt.Sprintf("hits=%d", len(merged)))
}
