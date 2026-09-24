package rrep

import (
	"fmt"
	"sync"
	"testing"
)

// TestCompareCounterScaling 证明 winner 定位是 O(1)：比较过的条目数不随副本数 m 增长。
func TestCompareCounterScaling(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		st := New(m)
		all := make([]int, m)
		for i := range all {
			all[i] = i
		}
		st.Put("g", "lo", 1, all)         // 全部副本持有低版本
		st.Put("g", "hi", 9, []int{3, 4}) // 最高版本只落在少数副本
		w, repaired := st.Read("g")
		if !w.Ok || w.Value != "hi" || repaired != m-2 {
			t.Fatalf("m=%d: got (%q,%d)", m, w.Value, repaired)
		}
		if st.lastCmp > 3 { // 与 m 无关的小常数
			t.Fatalf("m=%d: compared %d entries, grows with m", m, st.lastCmp)
		}
	}
}

// TestCompareCounterWithStaleIndex 堆里有过期条目时，比较数仍只与过期量相关、与 m 无关。
func TestCompareCounterWithStaleIndex(t *testing.T) {
	for _, m := range []int{100, 10000} {
		st := New(m)
		st.Put("g", "a", 5, []int{0})
		st.Put("g", "b", 6, []int{0}) // 使 (a,5) 成为过期堆条目
		w, _ := st.Read("g")
		if w.Value != "b" {
			t.Fatalf("m=%d: got %q", m, w.Value)
		}
		if st.lastCmp > 3 {
			t.Fatalf("m=%d: compared %d entries", m, st.lastCmp)
		}
	}
}

// TestConcurrent 并发读同一已收敛 key 结果逐字段相同；并发写不同 key 互不干扰。
func TestConcurrent(t *testing.T) {
	st := New(4)
	st.Put("hot", "x", 2, []int{0, 1})
	st.Read("hot") // 先收敛
	want, wantR := st.Read("hot")
	const readers, putters = 16, 8
	start := make(chan struct{})
	errs := make(chan string, readers+putters)
	var wg sync.WaitGroup
	for i := 0; i < readers+putters; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			if i < readers {
				if w, r := st.Read("hot"); w != want || r != wantR {
					errs <- fmt.Sprintf("read (%v,%d)", w, r)
				}
				return
			}
			st.Put(fmt.Sprintf("k%d", i-readers), "v", 1, []int{i % 4})
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for msg := range errs {
		t.Fatal(msg)
	}
	for i := 0; i < putters; i++ {
		if w, _ := st.Read(fmt.Sprintf("k%d", i)); !w.Ok || w.Value != "v" {
			t.Fatalf("k%d: %v", i, w)
		}
	}
}
