package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"slices"
	"sync"
	"sync/atomic"

	"ontology/deque"
	"ontology/mono"
	"ontology/slide"
)

var failed bool

func check(name string, ok bool) {
	s := "OK"
	if !ok {
		s, failed = "FAIL", true
	}
	fmt.Println(s, name)
}

func naive(seq []int, w int) []int {
	out := make([]int, 0, len(seq)-w+1)
	for j := 0; j+w <= len(seq); j++ {
		m := seq[j]
		for _, v := range seq[j : j+w] {
			m = max(m, v)
		}
		out = append(out, m)
	}
	return out
}

func main() {
	dq, mq := deque.New[int](), mono.New()
	dq.PushBack(3)
	for i, x := range []int{3, 3, 2} {
		_ = mq.Push(i, x)
		mq.Expire(max(0, i-1))
	}
	_, mv, _ := mq.Max()
	check("deque/mono 基础不变量", dq.Len() == 1 && mv == 3 && mq.Healthy(1, 2))
	rng := rand.New(rand.NewSource(1))
	seq := make([]int, 500)
	for i := range seq {
		seq[i] = rng.Intn(7) - 3
	}
	got, err := slide.Maxes(seq, 13)
	check("随机序列与朴素实现一致", err == nil && slices.Equal(got, naive(seq, 13)))
	got332, err := slide.Maxes([]int{3, 3, 2}, 2)
	check("[3,3,2] w=2 输出 [3 3]", err == nil && slices.Equal(got332, []int{3, 3}))
	shapeOK := true
	for _, sh := range [][]int{{5, 5, 5, 5}, {1, 2, 3, 4, 5}, {5, 4, 3, 2, 1}} {
		g, e := slide.Maxes(sh, 3)
		shapeOK = shapeOK && e == nil && slices.Equal(g, naive(sh, 3))
	}
	check("全相等/递增/递减形态", shapeOK)
	gotFull, err := slide.Maxes(seq, len(seq))
	check("w 等于序列长度", err == nil && slices.Equal(gotFull, naive(seq, len(seq))))
	_, e1 := slide.New(0)
	_, e2 := slide.Maxes(seq, -2)
	_, e3 := slide.Maxes(seq, len(seq)+1)
	bad := mono.New()
	_ = bad.Push(0, 1)
	e4, alive := bad.Push(0, 9), bad.Push(1, 3) == nil
	errsOK := errors.Is(e1, slide.ErrNonPositiveWidth) && errors.Is(e2, slide.ErrNonPositiveWidth) &&
		errors.Is(e3, slide.ErrWidthExceedsLength) && errors.Is(e4, mono.ErrIndexNotIncreasing) && alive
	check("三类可判定错误且拒绝后可用", errsOK)
	opsOK := true
	for _, n := range []int{1000, 100000} {
		s, _ := slide.New(64)
		for i := 0; i < n; i++ {
			s.Feed(i % 9)
		}
		opsOK = opsOK && s.SelfCheck() == nil
	}
	check("两档长度入出队次数不超过 2n", opsOK)
	var wg sync.WaitGroup
	var mismatch atomic.Int32
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := slide.Maxes(seq, 13)
			if e != nil || !slices.Equal(r, got) {
				mismatch.Add(1)
			}
		}()
	}
	wg.Wait()
	check("并发结果逐元素相同", mismatch.Load() == 0)
	if failed {
		os.Exit(1)
	}
}
