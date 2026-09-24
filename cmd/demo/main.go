package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"slices"
	"sync"

	"ontology/mono"
	"ontology/slide"
)

var failed bool

func ok(name string, cond bool) {
	mark := "OK   "
	if !cond {
		mark, failed = "FAIL ", true
	}
	fmt.Println(mark + name)
}

func naive(seq []int, w int) []int {
	out := make([]int, 0, len(seq)-w+1)
	for j := 0; j+w <= len(seq); j++ {
		m := seq[j]
		for _, v := range seq[j+1 : j+w] {
			if v > m {
				m = v
			}
		}
		out = append(out, m)
	}
	return out
}

func match(seq []int, w int) bool {
	got, err := slide.Maxes(seq, w)
	return err == nil && slices.Equal(got, naive(seq, w))
}

func main() {
	rng := rand.New(rand.NewSource(1))
	seq := make([]int, 500)
	for i := range seq {
		seq[i] = rng.Intn(101) - 50
	}
	ok("随机序列与朴素实现一致", match(seq, 7))
	ok("[3,3,2] w=2 输出 [3 3]", match([]int{3, 3, 2}, 2))
	eq, inc, dec := make([]int, 1000), make([]int, 1000), make([]int, 1000)
	for i := range eq {
		eq[i], inc[i], dec[i] = 7, i, 1000-i
	}
	ok("全相等/单调递增/单调递减", match(eq, 13) && match(inc, 13) && match(dec, 13))
	one, _ := slide.Maxes(seq, len(seq))
	ok("w 等于序列长度", len(one) == 1 && one[0] == naive(seq, len(seq))[0])
	var q mono.Queue
	_ = q.Push(0, 1)
	rej := q.Push(0, 2)
	m, _ := q.Max()
	_, e1 := slide.New(0)
	_, e2 := slide.Maxes(seq, len(seq)+1)
	s, _ := slide.New(2)
	s.Feed(1)
	s.Feed(2)
	ok("三类可判定错误且被拒后可用", errors.Is(rej, mono.ErrBadIndex) &&
		errors.Is(e1, slide.ErrBadWidth) && errors.Is(e2, slide.ErrWindowTooWide) &&
		rej != e1 && e1 != e2 && rej != e2 && m == 1 && s.Feed(3) == 3 && s.SelfCheck() == nil)
	linear := true
	for _, n := range []int{1000, 100000} {
		s, _ := slide.New(64)
		for i := 0; i < n; i++ {
			s.Feed(i % 5)
		}
		linear = linear && s.SelfCheck() == nil
	}
	ok("入出队总次数不超过 2n（n=1000,100000）", linear)
	gold, _ := slide.Maxes(seq, 7)
	same := make([]bool, 8)
	var wg sync.WaitGroup
	for g := range same {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, _ := slide.Maxes(seq, 7)
			same[g] = slices.Equal(r, gold)
		}()
	}
	wg.Wait()
	ok("并发 Maxes 结果逐元素相同", !slices.Contains(same, false))
	if failed {
		os.Exit(1)
	}
}
