package ontology

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestRandomizedComparisonWithNaive(t *testing.T) {
	random := rand.New(rand.NewSource(20261002))
	alphabet := []rune("ab界")

	for iteration := 0; iteration < 2000; iteration++ {
		wordCount := random.Intn(18)
		words := randomWords(random, alphabet, wordCount, 8)
		var query string
		if random.Intn(3) == 0 {
			query = string(alphabet[random.Intn(len(alphabet))])
		} else {
			query = randomString(random, alphabet, 1+random.Intn(7))
		}
		theta := 1 + random.Intn(100)
		k := 1 + random.Intn(8)

		finder := NewFinder()
		for _, word := range words {
			if err := finder.Add(word); err != nil {
				t.Fatalf("iteration %d Add(%q): %v", iteration, word, err)
			}
		}

		got, err := finder.Similar(query, theta, k)
		if err != nil {
			t.Fatalf("iteration %d Similar(%q,%d,%d): %v", iteration, query, theta, k, err)
		}
		want := naiveSimilar(words, query, theta, k)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("iteration %d mismatch\ninput words=%v\nquery=%q theta=%d k=%d\ngot=%v\nwant=%v", iteration, words, query, theta, k, got, want)
		}
		t.Logf("case=%d input=%s output=%s basis=naive multiset intersection; effectiveThreshold=%d ordering=Dice desc,byte asc; folding=retained-only", iteration+1,
			fmt.Sprintf("{words:%v q:%q theta:%d k:%d}", words, query, theta, k),
			formatResults(got), effectiveThresholdForTest(query, theta))
	}
}

func TestInvertedReadCountDoesNotGrowWithUnrelatedWords(t *testing.T) {
	read1000, sum1000 := buildAndMeasureReads(t, 1000)
	read100000, sum100000 := buildAndMeasureReads(t, 100000)
	if read1000 > sum1000 {
		t.Fatalf("1000-word read count %d exceeds distinct posting-list length sum %d", read1000, sum1000)
	}
	if read100000 > sum100000 {
		t.Fatalf("100000-word read count %d exceeds distinct posting-list length sum %d", read100000, sum100000)
	}
	if read1000 != read100000 {
		t.Fatalf("read counts grew with unrelated words: 1000=%d, 100000=%d", read1000, read100000)
	}
	if sum1000 != sum100000 {
		t.Fatalf("matched posting-list sums unexpectedly differ: 1000=%d, 100000=%d", sum1000, sum100000)
	}
	t.Logf("input q=abc theta=30 matched_words=3 at dictionary_sizes=1000,100000 output invertedReads=%d bound=sum(distinct query trigram posting lengths)=%d basis=unrelated words share no query trigram", read1000, sum1000)
}

func buildAndMeasureReads(t *testing.T, total int) (int, int) {
	t.Helper()
	finder := NewFinder()
	matched := []string{"abcde", "abcd", "abcx"}
	for _, word := range matched {
		if err := finder.Add(word); err != nil {
			t.Fatal(err)
		}
	}
	for i := len(matched); i < total; i++ {
		word := fmt.Sprintf("zzz%06d", i)
		if err := finder.Add(word); err != nil {
			t.Fatal(err)
		}
	}
	_, reads, err := finder.similar("abc", 30, 10)
	if err != nil {
		t.Fatal(err)
	}
	queryTrigrams, _ := trigramMultiset("abc")
	finder.mu.RLock()
	sum := 0
	for trigram := range queryTrigrams {
		sum += len(finder.postings[trigram])
	}
	finder.mu.RUnlock()
	return reads, sum
}

func naiveSimilar(words []string, query string, theta, k int) []Result {
	queryTrigrams, querySize := trigramMultiset(query)
	effective := effectiveThresholdForTest(query, theta)
	type naiveHit struct {
		word        string
		numerator   int
		denominator int
	}
	var hits []naiveHit
	for _, word := range words {
		wordTrigrams, wordSize := trigramMultiset(word)
		intersection := multisetIntersection(queryTrigrams, wordTrigrams)
		denominator := querySize + wordSize
		if 200*intersection < effective*denominator {
			continue
		}
		numerator := 2 * intersection
		divisor := greatestCommonDivisor(numerator, denominator)
		hits = append(hits, naiveHit{word: word, numerator: numerator / divisor, denominator: denominator / divisor})
	}
	sort.Slice(hits, func(i, j int) bool {
		left := int64(hits[i].numerator) * int64(hits[j].denominator)
		right := int64(hits[j].numerator) * int64(hits[i].denominator)
		return left > right || (left == right && hits[i].word < hits[j].word)
	})

	type retainedHit struct {
		naiveHit
		folded int
	}
	var retained []retainedHit
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
		} else {
			retained = append(retained, retainedHit{naiveHit: candidate})
		}
	}
	if len(retained) > k {
		retained = retained[:k]
	}
	results := make([]Result, len(retained))
	for index, item := range retained {
		results[index] = Result{Word: item.word, Dice: strconv.Itoa(item.numerator) + "/" + strconv.Itoa(item.denominator), Folded: item.folded}
	}
	return results
}

func effectiveThresholdForTest(query string, theta int) int {
	threshold := theta
	if runeLen(query) <= 2 {
		threshold += 20
		if threshold > 100 {
			threshold = 100
		}
	}
	return threshold
}

func randomWords(random *rand.Rand, alphabet []rune, count, maxLen int) []string {
	seen := make(map[string]struct{}, count)
	words := make([]string, 0, count)
	for len(words) < count {
		word := randomString(random, alphabet, 1+random.Intn(maxLen))
		if _, ok := seen[word]; ok {
			continue
		}
		seen[word] = struct{}{}
		words = append(words, word)
	}
	return words
}

func randomString(random *rand.Rand, alphabet []rune, length int) string {
	var builder strings.Builder
	for i := 0; i < length; i++ {
		builder.WriteRune(alphabet[random.Intn(len(alphabet))])
	}
	return builder.String()
}

func formatResults(results []Result) string {
	parts := make([]string, len(results))
	for index, result := range results {
		parts[index] = fmt.Sprintf("{%s %s folded=%d}", result.Word, result.Dice, result.Folded)
	}
	return "[" + strings.Join(parts, ",") + "]"
}
