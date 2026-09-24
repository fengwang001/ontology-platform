package rrep

import (
	"fmt"
	"sync"
	"testing"
)

// 证明按版本维护的 winner 索引 O(1) 定位：Read 判定 winner 时逐副本
// 比较过的条目数不随副本数 m 线性增长（不超过与 m 无关的小常数）。
func TestCompareCountIndependentOfM(t *testing.T) {
	for _, m := range []int{100, 500, 2000, 10000} {
		s := NewStore(m)
		// 低版本只铺两个副本，最高版本只落在少数几个副本，其余全空。
		s.Put("k", "low", 1, []int{0, 1})
		s.Put("k", "high", 9, []int{m / 2, m - 1})
		v, repaired, ok := s.Read("k")
		if !ok || v != "high" {
			t.Fatalf("m=%d winner=%q ok=%v，应为 high", m, v, ok)
		}
		if repaired != m-2 {
			t.Fatalf("m=%d repaired=%d，应为 %d", m, repaired, m-2)
		}
		if s.lastCmp > 1 {
			t.Fatalf("m=%d 逐副本比较数=%d，随 m 增长", m, s.lastCmp)
		}
	}
}

// 同一个 key 上 N 个 goroutine 并发 Read 结果逐字段相同；
// 另有 goroutine 并发 Put 到不同 key，结束后各 key 读回正确。
func TestConcurrentReadWrite(t *testing.T) {
	s := NewStore(8)
	s.Put("hot", "a", 5, []int{0, 3})
	s.Put("hot", "b", 9, []int{2})
	s.Put("hot", "c", 9, []int{5})
	want, _, _ := s.Read("hot") // 先收敛，之后并发读应一致
	const R, W = 32, 16
	vals, reps := make([]string, R), make([]int, R)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < R+W; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			if i < R {
				v, r, _ := s.Read("hot")
				vals[i], reps[i] = v, r
			} else {
				s.Put(fmt.Sprintf("w%d", i), "z", 7, []int{i % 8})
			}
		}(i)
	}
	close(start)
	wg.Wait()
	for i := 0; i < R; i++ {
		if vals[i] != want || reps[i] != 0 {
			t.Fatalf("读%d: v=%q r=%d，应为 %q/0", i, vals[i], reps[i], want)
		}
	}
	for i := R; i < R+W; i++ {
		if v, _, ok := s.Read(fmt.Sprintf("w%d", i)); !ok || v != "z" {
			t.Fatalf("key w%d 读回 v=%q ok=%v", i, v, ok)
		}
	}
	for i, e := range s.Snapshot("hot") {
		if e.Empty || e.Value != want {
			t.Fatalf("副本%d=%+v 未收敛到 %q", i, e, want)
		}
	}
}
