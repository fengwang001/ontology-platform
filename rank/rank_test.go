package rank

import (
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"sort"
	"sync"
	"testing"

	"ontology/trie"
)

// naive 朴素参照：全排序 (频率降, 字典序升) 后取前 k。
func naive(c []trie.Candidate, k int) []string {
	s := append([]trie.Candidate(nil), c...)
	sort.Slice(s, func(i, j int) bool {
		if s[i].Freq != s[j].Freq {
			return s[i].Freq > s[j].Freq
		}
		return s[i].Word < s[j].Word
	})
	var out []string
	for i := 0; i < len(s) && i < k; i++ {
		out = append(out, s[i].Word)
	}
	return out
}

func TestTopKMatchesNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	for _, n := range []int{0, 1, 5, 50, 500} {
		c := make([]trie.Candidate, n)
		for i := range c {
			c[i] = trie.Candidate{Word: fmt.Sprintf("w%04d", i), Freq: rng.Intn(10)}
		}
		for _, k := range []int{1, 3, 10, 1000} {
			got, err := TopK(c, k)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, naive(c, k)) {
				t.Fatalf("n=%d k=%d: got %v want %v", n, k, got, naive(c, k))
			}
		}
	}
}

func TestTopKOrdering(t *testing.T) {
	rng := rand.New(rand.NewSource(4))
	c := make([]trie.Candidate, 200)
	freq := map[string]int{}
	for i := range c {
		c[i] = trie.Candidate{Word: fmt.Sprintf("w%03d", i), Freq: rng.Intn(5)}
		freq[c[i].Word] = c[i].Freq
	}
	got, err := TopK(c, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) > 20 {
		t.Fatalf("returned %d > k=20", len(got))
	}
	for i := 1; i < len(got); i++ {
		a, b := freq[got[i-1]], freq[got[i]]
		if a < b || (a == b && got[i-1] > got[i]) {
			t.Fatalf("order violated at %d: %v", i, got)
		}
	}
}

func TestTopKRejectsBadK(t *testing.T) {
	for _, k := range []int{0, -1, -100} {
		before := collected.Load()
		_, err := TopK([]trie.Candidate{{Word: "a", Freq: 1}}, k)
		if !errors.Is(err, ErrInvalidK) {
			t.Errorf("k=%d: got %v, want ErrInvalidK", k, err)
		}
		if collected.Load() != before {
			t.Errorf("k=%d: rejected call changed counter", k)
		}
	}
}

// 复杂度：候选收集数 == 前缀匹配数 d，不随库规模 m 增长。
func TestCompleteCandidatesIndependentOfM(t *testing.T) {
	const d = 7
	for _, m := range []int{100, 1000, 10000} {
		tr := trie.New()
		for i := 0; i < d; i++ {
			_ = tr.Insert(fmt.Sprintf("pre-%04d", i), i+1)
		}
		for i := d; i < m; i++ {
			_ = tr.Insert(fmt.Sprintf("zz-%06d", i), 1)
		}
		cands, err := tr.Collect("pre-")
		if err != nil {
			t.Fatal(err)
		}
		before := collected.Load()
		if _, err := TopK(cands, 3); err != nil {
			t.Fatal(err)
		}
		if got := collected.Load() - before; got != d {
			t.Errorf("m=%d: collected %d candidates, want %d", m, got, d)
		}
	}
}

// 并发：N 个 goroutine 各插互不相交的串，结束后结果 == 朴素合并、Count == 总数。
func TestConcurrentInsert(t *testing.T) {
	tr := trie.New()
	const G, per = 8, 250
	var wg sync.WaitGroup
	for g := 0; g < G; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < per; i++ {
				_ = tr.Insert(fmt.Sprintf("g%02d-%04d", g, i), (g*per+i)%97+1)
			}
		}(g)
	}
	wg.Wait()
	if tr.Count() != G*per {
		t.Fatalf("count = %d, want %d", tr.Count(), G*per)
	}
	cands, err := tr.Collect("g")
	if err != nil {
		t.Fatal(err)
	}
	got, err := TopK(cands, G*per)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, naive(cands, G*per)) {
		t.Fatal("concurrent insert: merged result != naive merge")
	}
}
