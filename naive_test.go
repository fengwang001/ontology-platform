package ontology

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
	"unicode/utf8"
)

func TestRandomNaiveComparison(t *testing.T) {
	rng := rand.New(rand.NewSource(20261002))
	alphabet := []rune("ababc中文字é")

	for iteration := 0; iteration < 2000; iteration++ {
		wordCount := 1 + rng.Intn(18)
		words := make([]string, 0, wordCount)
		seen := make(map[string]struct{}, wordCount)
		for len(words) < wordCount {
			word := randomWord(rng, alphabet, 1+rng.Intn(8))
			if _, exists := seen[word]; exists {
				continue
			}
			seen[word] = struct{}{}
			words = append(words, word)
		}

		f := NewFinder()
		for _, word := range words {
			if err := f.Add(word); err != nil {
				t.Fatalf("iteration %d Add(%q): %v", iteration, word, err)
			}
		}

		queryPool := append(append([]string{}, words...), "abc", "aaaa", "ab", "a", "中文", "中字")
		query := queryPool[rng.Intn(len(queryPool))]
		theta := 1 + rng.Intn(100)
		k := 1 + rng.Intn(12)

		got, err := f.Similar(query, theta, k)
		if err != nil {
			t.Fatalf("iteration %d Similar: %v", iteration, err)
		}
		want := naiveSimilar(words, query, theta, k)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("iteration %d mismatch\n输入: words=%#v query=%q theta=%d k=%d\ngot  %#v\nwant %#v\n判定依据: 朴素全量扫描、整数交叉相乘、字节序排序、只对已保留项折叠", iteration, words, query, theta, k, got, want)
		}
		if iteration < 20 || len(got) > 0 {
			t.Logf("用例 %d\n输入: words=%#v query=%q theta=%d k=%d\n输出: %#v\n判定依据: 与全量多重集合交朴素实现一致", iteration, words, query, theta, k, got)
		}

		for _, word := range words {
			if rng.Intn(2) == 0 {
				if err := f.Remove(word); err != nil {
					t.Fatalf("iteration %d Remove(%q): %v", iteration, word, err)
				}
			}
		}
	}
}

func randomWord(rng *rand.Rand, alphabet []rune, length int) string {
	runes := make([]rune, length)
	for i := range runes {
		runes[i] = alphabet[rng.Intn(len(alphabet))]
	}
	return string(runes)
}

type naiveHit struct {
	word         string
	intersection int
	denominator  int
}

func naiveSimilar(words []string, query string, theta int, k int) []Match {
	effectiveThreshold := theta
	if utf8.RuneCountInString(query) <= 2 {
		effectiveThreshold += 20
		if effectiveThreshold > 100 {
			effectiveThreshold = 100
		}
	}

	queryGrams := trigrams(query)
	querySize := trigramSize(query)
	hits := make([]naiveHit, 0)
	for _, word := range words {
		wordGrams := trigrams(word)
		intersection := intersectionCount(queryGrams, wordGrams)
		denominator := querySize + trigramSize(word)
		if 200*intersection >= effectiveThreshold*denominator {
			hits = append(hits, naiveHit{word: word, intersection: intersection, denominator: denominator})
		}
	}

	sort.Slice(hits, func(i, j int) bool {
		left := hits[i].intersection * hits[j].denominator
		right := hits[j].intersection * hits[i].denominator
		if left != right {
			return left > right
		}
		return hits[i].word < hits[j].word
	})

	retained := make([]Match, 0)
	for _, hit := range hits {
		owner := -1
		for i := range retained {
			if properRunePrefix(retained[i].Word, hit.word) || properRunePrefix(hit.word, retained[i].Word) {
				owner = i
				break
			}
		}
		if owner >= 0 {
			retained[owner].Folded++
			continue
		}
		retained = append(retained, Match{
			Word:   hit.word,
			Score:  reducedScore(2*hit.intersection, hit.denominator),
			Folded: 0,
		})
	}

	if len(retained) > k {
		retained = retained[:k]
	}
	if len(retained) == 0 {
		return []Match{}
	}
	return retained
}

func TestRandomReplayProducesIdenticalOutput(t *testing.T) {
	rng := rand.New(rand.NewSource(77))
	first := replayRandomOperations(t, rng)
	rng = rand.New(rand.NewSource(77))
	second := replayRandomOperations(t, rng)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("replay mismatch:\nfirst=%#v\nsecond=%#v\n判定依据: %s", first, second, fmt.Sprint("相同种子、操作序列与当前词项集合应产生完全相同输出"))
	}
}

func replayRandomOperations(t *testing.T, rng *rand.Rand) []Match {
	t.Helper()
	f := NewFinder()
	var result []Match
	words := []string{"a", "aa", "ab", "abc", "abcd", "abcde", "abcx", "aaaa", "中文", "中文字"}
	for i := 0; i < 100; i++ {
		word := words[rng.Intn(len(words))]
		if rng.Intn(2) == 0 {
			_ = f.Add(word)
		} else {
			_ = f.Remove(word)
		}
		if i%5 == 0 {
			got, err := f.Similar("abcde", 1+rng.Intn(100), 1+rng.Intn(8))
			if err != nil {
				t.Fatal(err)
			}
			result = append(result, got...)
		}
	}
	return result
}
