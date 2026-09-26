package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"

	"ontology/api"
)

var okAll = true

func check(name string, cond bool) {
	status := "OK  "
	if !cond {
		status = "FAIL"
		okAll = false
	}
	fmt.Printf("%s %s\n", status, name)
}

func hashRNG(i int) int { return int((uint(i)*2654435761)>>16)%i + 1 }

func elems(n int) []string {
	es := make([]string, n)
	for i := range es {
		es[i] = fmt.Sprintf("e%d", i+1)
	}
	return es
}

func naiveReplay(es []string, k int, rng func(int) int) []string {
	var slots []string
	for idx, e := range es {
		if i := idx + 1; i <= k {
			slots = append(slots, e)
		} else if j := rng(i); j <= k {
			slots[j-1] = e
		}
	}
	return slots
}

func main() {
	// 1. 第三节六步轨迹：k=3，j 依次为 2、4、1。
	js := map[int]int{4: 2, 5: 4, 6: 1}
	s1, err := api.New(3, func(i int) int { return js[i] })
	want := [][]string{{"A"}, {"A", "B"}, {"A", "B", "C"}, {"A", "D", "C"}, {"A", "D", "C"}, {"F", "D", "C"}}
	good := err == nil
	for i, e := range []string{"A", "B", "C", "D", "E", "F"} {
		good = good && s1.Feed([]string{e}) == nil && slices.Equal(s1.Sample(), want[i])
	}
	check("六步轨迹 A..F -> F|D|C", good)

	// 2. 样本大小恒为 min(k,N)。
	good = true
	for _, m := range []int{0, 2, 3, 5, 100} {
		s, e := api.New(3, hashRNG)
		good = good && e == nil && s.Feed(elems(m)) == nil && len(s.Sample()) == min(3, m) && s.Size() == m
	}
	check("样本大小恒为 min(k,N)", good)

	// 3. N<=k 时全部保留且保序。
	s3, _ := api.New(5, hashRNG)
	_ = s3.Feed([]string{"a", "b", "c"})
	check("N<=k 全部保留且保序", slices.Equal(s3.Sample(), []string{"a", "b", "c"}))

	// 4. 与朴素重放一致。
	s4, _ := api.New(4, hashRNG)
	es4 := elems(50)
	_ = s4.Feed(es4)
	check("与朴素重放一致", slices.Equal(s4.Sample(), naiveReplay(es4, 4, hashRNG)))

	// 5. 四类可判定错误，互不相同。
	_, e1 := api.New(0, hashRNG)
	_, e2 := api.New(1, nil)
	sb, _ := api.New(1, func(i int) int { return 0 })
	e3 := sb.Feed([]string{"a", "b"})
	se, _ := api.New(2, hashRNG)
	e4 := se.Feed([]string{""})
	good = errors.Is(e1, api.ErrBadCapacity) && errors.Is(e2, api.ErrNilRNG) &&
		errors.Is(e3, api.ErrBadDraw) && errors.Is(e4, api.ErrEmptyElement) &&
		!errors.Is(e1, api.ErrNilRNG) && !errors.Is(e2, api.ErrBadDraw) &&
		!errors.Is(e3, api.ErrEmptyElement) && !errors.Is(e4, api.ErrBadCapacity)
	check("四类可判定错误互不相同", good)

	// 6. 被拒后状态不变，且可继续使用。
	s6, _ := api.New(3, hashRNG)
	_ = s6.Feed([]string{"a", "b", "c", "d"})
	before, n6 := s6.Sample(), s6.Size()
	_ = s6.Feed([]string{"x", "", "y"})
	good = slices.Equal(s6.Sample(), before) && s6.Size() == n6 &&
		s6.Feed([]string{"z"}) == nil && s6.Size() == n6+1
	check("被拒后状态不变且可继续", good)

	// 7. 大 m 下样本恒为 k 个（只返回已存的 k 个，不重扫历史）。
	s7, _ := api.New(10, hashRNG)
	_ = s7.Feed(elems(10000))
	check("大 m 下样本恒为 k 个", len(s7.Sample()) == 10)

	// 8. 并发只读结果一致。
	s8, _ := api.New(5, hashRNG)
	_ = s8.Feed(elems(1000))
	want8 := s8.Sample()
	start := make(chan struct{})
	res := make(chan bool, 64)
	var wg sync.WaitGroup
	for g := 0; g < 64; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			res <- slices.Equal(s8.Sample(), want8) && s8.Size() == 1000
		}()
	}
	close(start)
	wg.Wait()
	close(res)
	good = true
	for r := range res {
		good = good && r
	}
	check("并发只读结果一致", good)

	// 9. SelfCheck 四条不变量。
	sc, _ := api.New(3, hashRNG)
	check("SelfCheck 四条不变量", sc.SelfCheck() == nil)

	if !okAll {
		os.Exit(1)
	}
}
