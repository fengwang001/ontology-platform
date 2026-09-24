// Command demo 逐条打印单调栈「下一个更大元素」的各项判定结果。
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"ontology/nge"
	"ontology/stack"
	"slices"
	"sync"
)

func naive(a []int) []int {
	ans := make([]int, len(a))
	for i := range a {
		ans[i] = nge.None
		for j := i + 1; j < len(a); j++ {
			if a[j] > a[i] {
				ans[i] = j
				break
			}
		}
	}
	return ans
}

// countOps 镜像 mono 的单调栈算法统计压弹次数；权威断言见 mono 包内 TestOpsLinearBound。
func countOps(a []int) (ops int) {
	var st stack.Stack
	for i, v := range a {
		for top, ok := st.Top(); ok && a[top] < v; top, ok = st.Top() {
			_, _ = st.Pop()
			ops++
		}
		st.Push(i)
		ops++
	}
	return ops
}

func main() {
	failed := false
	report := func(name string, ok bool) {
		if !ok {
			failed = true
		}
		fmt.Printf("%-34s %s\n", name, map[bool]string{true: "OK", false: "FAIL"}[ok])
	}
	s, err := nge.New(1 << 20)
	rnd, a := rand.New(rand.NewSource(1)), make([]int, 500)
	for i := range a {
		a[i] = rnd.Intn(50)
	}
	ans, err1 := s.NextGreater(a)
	report("random matches naive+selfcheck", err == nil && err1 == nil &&
		slices.Equal(ans, naive(a)) && s.SelfCheck(a, ans) == nil)
	got223, _ := s.NextGreater([]int{2, 2, 3})
	report("[2,2,3] strict -> [2 2 -1]", slices.Equal(got223, []int{2, 2, nge.None}))
	shapes := true
	for _, sh := range [][]int{{7, 7, 7, 7}, {1, 2, 3, 4, 5}, {5, 4, 3, 2, 1}} {
		got, _ := s.NextGreater(sh)
		shapes = shapes && slices.Equal(got, naive(sh)) && s.SelfCheck(sh, got) == nil
	}
	report("shapes equal/inc/dec vs naive", shapes)
	report("none(-1) distinct from index 0", nge.None == -1 && ans[len(ans)-1] == nge.None)
	in := []int{3, 1, 2}
	small, _ := nge.New(2)
	_, e1 := s.NextGreater(nil)
	_, e2 := small.NextGreater(in)
	_, e3 := nge.New(0)
	report("3 distinct sentinel errors", errors.Is(e1, nge.ErrNilInput) &&
		errors.Is(e2, nge.ErrTooLong) && errors.Is(e3, nge.ErrBadLimit) && e1 != e2 && e2 != e3)
	after, errA := small.NextGreater([]int{1, 2})
	report("rejected input unmodified+reusable", slices.Equal(in, []int{3, 1, 2}) &&
		errA == nil && slices.Equal(after, []int{1, nge.None}))
	inc := make([]int, 100000)
	for i := range inc {
		inc[i] = i
	}
	report("push+pop <= 2n (n=1e3,1e5)", countOps(inc[:1000]) <= 2000 && countOps(inc) <= 200000)
	same := make([]bool, 8)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for k := range same {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			<-start
			got, _ := s.NextGreater(a)
			same[k] = slices.Equal(got, ans)
		}(k)
	}
	close(start)
	wg.Wait()
	report("concurrent results identical", !slices.Contains(same, false))
	if failed {
		panic("demo failed")
	}
}
