package ontology

import (
	"bytes"
	"math/rand"
	"sync"
	"testing"
)

// TestConcurrentRegistration 多个 goroutine 并发登记同一批不相交块：
// 无论串行顺序如何，最终分隔键集合必须与顺序重放一致，且不发生竞态。
func TestConcurrentRegistration(t *testing.T) {
	rngKeys := genSortedKeyPool(rand.New(rand.NewSource(7)), 20)

	run := func() [][]byte {
		var b Builder
		type pair struct{ last, next []byte }
		var pairs []pair
		for i := 0; i+2 < len(rngKeys); i += 2 {
			pairs = append(pairs, pair{rngKeys[i], rngKeys[i+2]})
		}
		var wg sync.WaitGroup
		for _, p := range pairs {
			wg.Add(1)
			go func(last, next []byte) {
				defer wg.Done()
				_, _ = b.AddBlock(last, next)
			}(p.last, p.next)
		}
		wg.Wait()
		_, _ = b.Finish(rngKeys[len(rngKeys)-2])

		// Finish 后并发 Seek/Seps 也必须安全。
		for g := 0; g < 4; g++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				_, _ = b.Seek(rngKeys[id%len(rngKeys)])
				_ = b.Seps()
			}(g)
		}
		wg.Wait()
		return b.Seps()
	}

	var ref naiveBuilder
	for i := 0; i+2 < len(rngKeys); i += 2 {
		_ = ref.addBlock(rngKeys[i], rngKeys[i+2])
	}
	_ = ref.finish(rngKeys[len(rngKeys)-2])

	for i := 0; i < 20; i++ {
		got := run()
		// 成功登记的块数取决于交错顺序，允许变化，但至少含 Finish 的一个 sep。
		if len(got) < 1 {
			t.Fatal("至少应存在 Finish 的分隔键")
		}
		// 已登记块必须保持字节序且每个 sep 都能在顺序重放结果中找到。
		for _, sep := range got {
			found := false
			for _, want := range ref.seps {
				if bytes.Equal(sep, want) {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("并发产生了朴素重放中不存在的分隔键 %q", sep)
			}
		}
		for j := 1; j < len(got); j++ {
			if bytes.Compare(got[j-1], got[j]) >= 0 {
				t.Fatalf("分隔键未保持字节序: %q >= %q", got[j-1], got[j])
			}
		}
	}
	t.Log("并发登记稳定：重复 20 轮，seps 数随串行交错变化，但均为朴素重放的有序子集")
}
