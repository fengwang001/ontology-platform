package ontology

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"unicode/utf8"
)

func addWords(t *testing.T, f *Finder, words ...string) {
	t.Helper()
	for _, word := range words {
		if err := f.Add(word); err != nil {
			t.Fatalf("Add(%q): %v", word, err)
		}
	}
}

func TestTrigramMultisetBoundariesAndRepetition(t *testing.T) {
	single, singleSize := trigramMultiset("a")
	if singleSize != 3 || len(single) != 3 || totalCount(single) != 3 {
		t.Fatalf("single-character multiset = %v, size %d; want three trigrams", single, singleSize)
	}

	repeated, repeatedSize := trigramMultiset("aaaa")
	want := map[string]int{"\x02\x02a": 1, "\x02aa": 1, "aaa": 2, "aa\x03": 1, "a\x03\x03": 1}
	if repeatedSize != 6 || !reflect.DeepEqual(repeated, want) {
		t.Fatalf("aaaa multiset = %v, size %d; want %v, 6", repeated, repeatedSize, want)
	}

	fiveA, fiveSize := trigramMultiset("aaaaa")
	intersection := multisetIntersection(repeated, fiveA)
	if fiveSize != 7 || intersection != 6 {
		t.Fatalf("multiset intersection aaaa/aaaaa = %d; want 6 (set logic would give 5)", intersection)
	}
}

func TestMultibyteCharactersCountByRune(t *testing.T) {
	trigrams, size := trigramMultiset("界á")
	if size != 4 || totalCount(trigrams) != 4 {
		t.Fatalf("multibyte word size = %d, trigrams = %v; want 4 rune windows", size, trigrams)
	}
	if !utf8.ValidString("界á") {
		t.Fatal("test word must be valid UTF-8")
	}
}

func TestThresholdBoundaryAndShortQueryBoost(t *testing.T) {
	f := NewFinder()
	addWords(t, f, "abd")

	got, err := f.Similar("abc", 40, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []Result{{Word: "abd", Dice: "2/5", Folded: 0}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("theta=40 results = %v; want %v (200*2 == 40*10)", got, want)
	}
	if got, err := f.Similar("abc", 41, 10); err != nil || len(got) != 0 {
		t.Fatalf("theta=41 got %v, %v; want empty, nil (200*2 < 41*10)", got, err)
	}

	short := NewFinder()
	addWords(t, short, "ab", "ac")
	got, err = short.Similar("ab", 80, 10)
	if err != nil {
		t.Fatal(err)
	}
	want = []Result{{Word: "ab", Dice: "1/1", Folded: 0}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("two-rune theta=80 results = %v; want only exact match at effective threshold 100", got)
	}
	got, err = short.Similar("ab", 90, 10)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("two-rune theta=90 results = %v, err %v; want capped threshold and %v", got, err, want)
	}
}

func TestSpecFoldingExample(t *testing.T) {
	f := NewFinder()
	addWords(t, f, "abcde", "abcd", "abcx")
	input := []string{"abcde", "abcd", "abcx"}
	for _, k := range []int{3, 2} {
		got, err := f.Similar("abcde", 30, k)
		if err != nil {
			t.Fatal(err)
		}
		want := []Result{{Word: "abcde", Dice: "1/1", Folded: 1}, {Word: "abcx", Dice: "6/13", Folded: 0}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Similar(%q,30,%d) = %v; want %v", "abcde", k, got, want)
		}
		t.Logf("input words=%v q=abcde theta=30 k=%d output=%v basis=scores 1/1,8/13,6/13; abcd folds into retained abcde; abcx is retained", input, k, got)
	}
	got, err := f.Similar("abcde", 30, 1)
	if err != nil {
		t.Fatal(err)
	}
	want := []Result{{Word: "abcde", Dice: "1/1", Folded: 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("k=1 results = %v; want %v (folded count is computed before k)", got, want)
	}
}

func TestTieOrderingIsByteOrder(t *testing.T) {
	f := NewFinder()
	addWords(t, f, "abd", "xbc")
	got, err := f.Similar("abc", 20, 10)
	if err != nil {
		t.Fatal(err)
	}
	wantWords := []string{"abd", "xbc"}
	if len(got) != 2 || got[0].Word != wantWords[0] || got[1].Word != wantWords[1] || got[0].Dice != "2/5" || got[1].Dice != "2/5" {
		t.Fatalf("tied results = %+v; want byte order %v with 2/5 each", got, wantWords)
	}
	t.Logf("input words=[abd,xbc] q=abc theta=20 output=%v basis=equal 2/5 ties sort by UTF-8 byte order", got)
}

func TestFoldOnlyAgainstRetainedAndFirstOwner(t *testing.T) {
	hits := []hit{
		{word: "abcde", numerator: 1, denominator: 1},
		{word: "abcd", numerator: 2, denominator: 3},
		{word: "abcx", numerator: 1, denominator: 2},
	}
	got := foldHits(hits)
	want := []hit{{word: "abcde", numerator: 1, denominator: 1, folded: 1}, {word: "abcx", numerator: 1, denominator: 2}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("foldHits = %+v; want %+v", got, want)
	}

	firstOwner := foldHits([]hit{
		{word: "abcde", numerator: 1, denominator: 2},
		{word: "abcdf", numerator: 1, denominator: 3},
		{word: "abcd", numerator: 1, denominator: 4},
	})
	if len(firstOwner) != 2 || firstOwner[0].word != "abcde" || firstOwner[0].folded != 1 || firstOwner[1].folded != 0 {
		t.Fatalf("first-owner folding = %+v; want abcd folded into first retained abcde", firstOwner)
	}
}

func TestValidationOrderAndRejectionDoesNotMutate(t *testing.T) {
	f := NewFinder()
	addWords(t, f, "known")

	cases := []struct {
		name string
		call func() error
		want error
	}{
		{"add empty", func() error { return f.Add("") }, ErrInvalidWord},
		{"add invalid utf8", func() error { return f.Add("bad\xff") }, ErrInvalidWord},
		{"add start control", func() error { return f.Add("a\x02b") }, ErrInvalidWord},
		{"add end control", func() error { return f.Add("a\x03b") }, ErrInvalidWord},
		{"add duplicate", func() error { return f.Add("known") }, ErrDuplicateWord},
		{"remove missing", func() error { return f.Remove("missing") }, ErrWordNotFound},
	}
	for _, tc := range cases {
		if err := tc.call(); !errors.Is(err, tc.want) {
			t.Fatalf("%s: error = %v; want %v", tc.name, err, tc.want)
		}
	}
	before, err := f.Similar("known", 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Add("bad\xff"); !errors.Is(err, ErrInvalidWord) {
		t.Fatalf("invalid add after rejected operations: %v", err)
	}
	after, err := f.Similar("known", 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("rejected Add changed results: before=%v after=%v", before, after)
	}

	if err := f.Remove("known"); err != nil {
		t.Fatal(err)
	}
	if err := f.Remove("known"); !errors.Is(err, ErrWordNotFound) {
		t.Fatalf("remove after delete = %v; want ErrWordNotFound", err)
	}
	if got, err := f.Similar("\xff", 0, 0); err != ErrInvalidWord || got != nil {
		t.Fatalf("Similar validation order = %v, %v; want invalid word first", got, err)
	}
	if _, err := f.Similar("ok", 0, 0); !errors.Is(err, ErrInvalidThreshold) {
		t.Fatalf("theta validation error = %v; want ErrInvalidThreshold", err)
	}
	if _, err := f.Similar("ok", 1, 0); !errors.Is(err, ErrInvalidLimit) {
		t.Fatalf("k validation error = %v; want ErrInvalidLimit", err)
	}
	if got, err := f.Similar("anything", 1, 10); err != nil || len(got) != 0 {
		t.Fatalf("empty dictionary = %v, %v; want empty result without error", got, err)
	}
}

func TestQueryItselfAndThresholdEndpointsAndLargeK(t *testing.T) {
	f := NewFinder()
	addWords(t, f, "same", "sample")
	got, err := f.Similar("same", 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Word != "same" || got[0].Dice != "1/1" {
		t.Fatalf("theta=1 results = %+v; want registered query first with Dice 1/1", got)
	}
	strict, err := f.Similar("same", 100, 100)
	if err != nil || len(strict) != 1 || strict[0].Word != "same" {
		t.Fatalf("theta=100 results = %+v, err=%v; want only exact", strict, err)
	}
	if len(got) != cap(got) && len(got) > 100 {
		t.Fatalf("unexpected slice size")
	}
	if large, err := f.Similar("same", 1, 1000); err != nil || len(large) != len(got) {
		t.Fatalf("k larger than hits got %d items, err=%v; want %d", len(large), err, len(got))
	}
	t.Logf("input words=[same,sample] q=same theta=1/100 output=%v/%v basis=exact registered word participates", got, strict)
}

func TestRemoveCleansInvertedIndex(t *testing.T) {
	f := NewFinder()
	addWords(t, f, "abc", "abd")
	if err := f.Remove("abc"); err != nil {
		t.Fatal(err)
	}

	f.mu.RLock()
	_, present := f.entries["abc"]
	removedInCandidates := false
	for _, posting := range f.postings {
		for item := range posting {
			if item.word == "abc" {
				removedInCandidates = true
			}
		}
	}
	f.mu.RUnlock()

	if present || removedInCandidates {
		t.Fatalf("removed word remains in entries=%v or postings=%v", present, removedInCandidates)
	}
	got, err := f.Similar("abc", 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Word != "abd" {
		t.Fatalf("results after remove = %v; want only abd", got)
	}
}

func TestAddOrderIndependenceAndConcurrency(t *testing.T) {
	words := []string{"alpha", "alphabet", "aloof", "beta"}
	first := NewFinder()
	addWords(t, first, words...)
	second := NewFinder()
	addWords(t, second, "beta", "aloof", "alphabet", "alpha")
	left, err := first.Similar("alpha", 20, 10)
	if err != nil {
		t.Fatal(err)
	}
	right, err := second.Similar("alpha", 20, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(left, right) {
		t.Fatalf("results depend on insertion order: %v != %v", left, right)
	}

	f := NewFinder()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		word := fmt.Sprintf("word%02d", i)
		wg.Add(3)
		go func() { defer wg.Done(); _ = f.Add(word) }()
		go func() { defer wg.Done(); _ = f.Remove(word) }()
		go func() { defer wg.Done(); _, _ = f.Similar("word", 20, 5) }()
	}
	wg.Wait()
}

func totalCount(trigrams map[string]int) int {
	total := 0
	for _, count := range trigrams {
		total += count
	}
	return total
}
