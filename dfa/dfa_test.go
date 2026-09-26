package dfa

import (
	"sync"
	"testing"

	"ontology/nfa"
)

// 不变量：查重用哈希定位。已含 m 个状态的 DFA 再做一次新集合查重，
// 检查的既有状态个数不随 m 线性增长（白盒读非导出计数器，不经导出接口）。
func TestDedupChecksConstant(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		n := chainNFA(m)
		d, err := Build(n)
		if err != nil || d.NumStates() != m {
			t.Fatalf("m=%d: build err=%v states=%d", m, err, d.NumStates())
		}
		before := d.dedupChecks
		d.intern(map[int]bool{0: true, 1: true}, n) // 链上从未出现的新集合
		if got := d.dedupChecks - before; got > 2 {
			t.Fatalf("m=%d: dedup checks=%d, want <= 2", m, got)
		}
	}
}

// 并发：N 个 goroutine 对同一 DFA 并发 Accepts，结果逐值相同。
func TestConcurrentAccepts(t *testing.T) {
	n := &nfa.NFA{
		NumStates: 3,
		Trans: map[int]map[byte][]int{
			0: {'a': {1}},
			1: {nfa.Epsilon: {2}, 'b': {1}},
			2: {'c': {2}},
		},
		Start:    0,
		Accept:   map[int]bool{2: true},
		Alphabet: []byte{'a', 'b', 'c'},
	}
	d, err := Build(n)
	if err != nil {
		t.Fatal(err)
	}
	strs := []string{"a", "ab", "ac", "abb", "b", "", "z", "abc", "acbb", "cccc"}
	want := make([]bool, len(strs))
	for i, s := range strs {
		want[i] = d.Accepts(s)
	}
	var wg sync.WaitGroup
	mismatch := make(chan struct{}, 1)
	for g := 0; g < 64; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for round := 0; round < 100; round++ {
				for i, s := range strs {
					if d.Accepts(s) != want[i] {
						select {
						case mismatch <- struct{}{}:
						default:
						}
					}
				}
			}
		}()
	}
	wg.Wait()
	select {
	case <-mismatch:
		t.Fatal("concurrent Accepts returned inconsistent results")
	default:
	}
}
