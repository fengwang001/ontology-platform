package ontology

import (
	"fmt"
	"testing"
)

func TestPostingReadsDoNotGrowWithUnrelatedWords(t *testing.T) {
	query := "zxywvuts"
	queryGrams := trigrams(query)

	measure := func(t *testing.T, total int) uint64 {
		t.Helper()
		f := buildScaleFinder(t, total)

		before := f.postingReads.Load()
		got, err := f.Similar(query, 30, 20)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 20 {
			t.Logf("actual hits: %#v", got)
			t.Fatalf("total=%d hits=%d, want 20", total, len(got))
		}
		reads := f.postingReads.Load() - before

		var sumPostingLengths uint64
		f.mu.RLock()
		for gram := range queryGrams {
			sumPostingLengths += uint64(len(f.postings[gram]))
		}
		f.mu.RUnlock()

		if reads > sumPostingLengths {
			t.Fatalf("reads=%d exceeds sum of query posting lengths=%d", reads, sumPostingLengths)
		}
		t.Logf("输入: 词典 %d 词、Similar(%q,30,20)\n输出: %#v\n判定依据: 读取倒排项 %d，q 的 %d 个不同三元组倒排表长度之和为 %d", total, query, got, reads, len(queryGrams), sumPostingLengths)
		return reads
	}

	readsAt1000 := measure(t, 1000)
	readsAt100000 := measure(t, 100000)
	if readsAt1000 != readsAt100000 {
		t.Fatalf("posting reads grew with unrelated words: %d vs %d", readsAt1000, readsAt100000)
	}
	want := uint64(20 * (len(query) + 2))
	if got := uint64(len(queryGrams)); got != want/20 {
		t.Fatalf("distinct query trigrams=%d, want %d", got, want/20)
	}
	if readsAt1000 != want {
		t.Fatalf("reads=%d, want %d", readsAt1000, want)
	}
}

func buildScaleFinder(t *testing.T, unrelated int) *Finder {
	t.Helper()
	f := NewFinder()

	for i := 0; i < unrelated; i++ {
		word := fmt.Sprintf("%011d", baseThree(i, 11))
		if err := f.Add(word); err != nil {
			t.Fatalf("Add(%q): %v", word, err)
		}
	}

	for i := 0; i < 20; i++ {
		word := fmt.Sprintf("zxywvuts-%d", 20+i)
		if err := f.Add(word); err != nil {
			t.Fatalf("Add(%q): %v", word, err)
		}
	}
	return f
}

func baseThree(value int, width int) int {
	result := 0
	place := 1
	for i := 0; i < width; i++ {
		result += (value % 3) * place
		value /= 3
		place *= 10
	}
	return result
}
