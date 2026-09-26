package main

import (
	"errors"
	"fmt"
	"reflect"
	"sync"

	"ontology/sampler"

	"ontology/api"
)

func ok(name string, pass bool) {
	if pass {
		fmt.Printf("OK   %s\n", name)
	} else {
		fmt.Printf("FAIL %s\n", name)
	}
}

// naive：先收齐全部元素，再按同一 rng 重放第 k+1..N 步的朴素参照。
func naive(k int, rng func(int) int, es []string) []string {
	slots := make([]string, 0, k)
	for i, e := range es {
		step := i + 1
		if step <= k {
			slots = append(slots, e)
			continue
		}
		if j := rng(step); j <= k {
			slots[j-1] = e
		}
	}
	return slots
}

func main() {
	js := map[int]int{4: 2, 5: 4, 6: 1}
	rng := func(i int) int { return js[i] }
	es := []string{"A", "B", "C", "D", "E", "F"}
	s, _ := sampler.New(3, rng)
	states := [][]string{}
	for _, e := range es { // 六步逐步喂入，记录每步之后样本
		_ = s.Feed(e)
		states = append(states, s.Sample())
	}
	ok("six steps A|AB|ABC|ADC|ADC|FDC",
		fmt.Sprint(states) == fmt.Sprint([][]string{{"A"}, {"A", "B"}, {"A", "B", "C"},
			{"A", "D", "C"}, {"A", "D", "C"}, {"F", "D", "C"}}))

	s2, _ := sampler.New(3, func(int) int { return 1 })
	_ = s2.FeedMany([]string{"a", "b"})
	ok("size==min(k,N), slots filled", s2.Size() == 2 && len(s2.Sample()) == 2)

	s3, _ := sampler.New(5, func(int) int { return 1 })
	first5 := []string{"v0", "v1", "v2", "v3", "v4"}
	_ = s3.FeedMany(first5)
	ok("N<=k: all retained in order", reflect.DeepEqual(s3.Sample(), first5))

	s4, _ := sampler.New(3, func(i int) int { return js[i] })
	_ = s4.FeedMany(es)
	ok("matches naive replay", reflect.DeepEqual(s4.Sample(), naive(3, func(i int) int { return js[i] }, es)))

	errs := []error{}
	_, e1 := sampler.New(0, func(int) int { return 1 })
	_, e2 := sampler.New(3, nil)
	s5, _ := sampler.New(3, func(int) int { return 1 })
	_ = s5.FeedMany([]string{"a", "b", "c"})
	e3 := s5.FeedMany([]string{"x"}) // 第4步 rng 返回 1，合法；改用越界源
	s5b, _ := sampler.New(3, func(int) int { return 9 })
	_ = s5b.FeedMany([]string{"a", "b", "c"})
	e3 = s5b.FeedMany([]string{"x"}) // j=9 > i=4
	e4 := s5.FeedMany([]string{"ok", ""})
	errs = append(errs, e1, e2, e3, e4)
	distinct := true
	for i := range errs {
		for j := i + 1; j < len(errs); j++ {
			if errors.Is(errs[i], errs[j]) {
				distinct = false
			}
		}
	}
	ok("four distinct decidable errors", func() bool {
		want := []error{sampler.ErrBadCapacity, sampler.ErrNilRNG, sampler.ErrJOutOfRange, sampler.ErrEmptyElement}
		for i := range want {
			if !errors.Is(errs[i], want[i]) {
				return false
			}
		}
		return distinct
	}())

	before := s5.Sample()
	sizeBefore := s5.Size()
	_ = s5.FeedMany([]string{"z", ""})
	ok("rejected batch leaves no trace", reflect.DeepEqual(s5.Sample(), before) && s5.Size() == sizeBefore)

	big, _ := sampler.New(5, func(i int) int { return (i*3+1)%i + 1 })
	many := make([]string, 10000)
	for i := range many {
		many[i] = fmt.Sprintf("e%d", i)
	}
	_ = big.FeedMany(many)
	ok("m=10000: Sample yields exactly k", big.Size() == 10000 && len(big.Sample()) == 5)

	ref := big.Sample()
	const g = 16
	var wg sync.WaitGroup
	res := make([][]string, g)
	start := make(chan struct{})
	for i := 0; i < g; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			res[idx] = big.Sample()
		}(i)
	}
	close(start)
	wg.Wait()
	allSame := true
	for _, r := range res {
		if !reflect.DeepEqual(r, ref) {
			allSame = false
		}
	}
	ok("concurrent readers: slot-identical", allSame)

	face, err := api.New(3, func(i int) int { return js[i] })
	ok("api facade + SelfCheck", err == nil && face.Size() == 0 && face.SelfCheck() == nil)
}
