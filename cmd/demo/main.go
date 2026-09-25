package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/longest"
	"ontology/manacher"
)

var failed bool

func report(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s\n", status, name)
}

// naive 朴素参照：枚举每个中心向两侧逐字符扩展，严格大于才更新（最左）。
func naive(s []rune) (int, int) {
	st, ln := 0, -1
	for i := range s {
		k := 0
		for i-k >= 0 && i+k < len(s) && s[i-k] == s[i+k] {
			k++
		}
		if 2*k-1 > ln {
			st, ln = i-k+1, 2*k-1
		}
	}
	for i := 1; i <= len(s); i++ {
		k := 0
		for i-k-1 >= 0 && i+k < len(s) && s[i-k-1] == s[i+k] {
			k++
		}
		if 2*k > ln {
			st, ln = i-k, 2*k
		}
	}
	return st, ln
}
func matchesNaive() bool {
	for _, s := range []string{"abbaxyzabba", "aabbaa", "abcba", "上海自来水来自海上", "abaxabaxabb"} {
		var c api.Checker
		if c.New(s) != nil {
			return false
		}
		st, ln, err := c.Longest()
		ns, nl := naive([]rune(s))
		if err != nil || st != ns || ln != nl {
			return false
		}
	}
	return true
}

func errorsOK() bool {
	var c api.Checker
	_, _, e0 := c.Longest()
	e1, e2 := c.New(""), c.New(string([]byte{'x', 0xff, 'y'}))
	var iu *api.InvalidUTF8Error
	return errors.Is(e0, api.ErrNotReady) && errors.Is(e1, api.ErrEmpty) &&
		errors.Is(e2, api.ErrInvalidUTF8) && errors.As(e2, &iu) && iu.Offset == 1 &&
		!errors.Is(e0, api.ErrEmpty) && !errors.Is(e1, api.ErrNotReady) && !errors.Is(e2, api.ErrNotReady)
}

func stateOK() bool {
	var c api.Checker
	if c.New("abba") != nil || !errors.Is(c.New(""), api.ErrEmpty) ||
		!errors.Is(c.New(string([]byte{0xff})), api.ErrInvalidUTF8) {
		return false
	}
	st, ln, err := c.Longest() // 两次被拒后状态不变
	return err == nil && st == 0 && ln == 4
}

// linearOK 大 n 最坏形态下半径与闭式解一致（≤2n 硬断言在 TestLinearComparisons）。
func linearOK() bool {
	const n = 10000
	for _, gen := range []func(i int) rune{func(int) rune { return 'a' }, func(i int) rune { return rune('a' + i%2) }} {
		s := make([]rune, n)
		for i := range s {
			s[i] = gen(i)
		}
		d1, _ := manacher.Compute(s)
		for i := 0; i < n; i++ {
			if d1[i] != min(i, n-1-i)+1 {
				return false
			}
		}
	}
	return true
}

func concurrentOK() bool {
	var c api.Checker
	if c.New("abbaxyzabba") != nil {
		return false
	}
	var wg sync.WaitGroup
	ch := make(chan [2]int, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st, ln, err := c.Longest()
			if err != nil {
				st, ln = -1, -1
			}
			ch <- [2]int{st, ln}
		}()
	}
	wg.Wait()
	close(ch)
	for got := range ch {
		if got != [2]int{0, 4} {
			return false
		}
	}
	return true
}

func main() {
	var c api.Checker
	ok := c.New("abbaxyzabba") == nil
	st, ln, err := c.Longest()
	report("abbaxyzabba Start=0 Length=4", ok && err == nil && st == 0 && ln == 4)
	report("与朴素参照一致", matchesNaive())
	report("半径自洽(SelfCheck)", new(api.Checker).SelfCheck() == nil)
	d1, d2 := manacher.Compute([]rune("abbaxyzabba")) // 不变量 3：两个 "abba" 等长取最左
	lst, lln := longest.Find(d1, d2)
	report("等长取最左", lst == 0 && lln == 4)
	report("三类可判定错误", errorsOK())
	report("被拒后状态不变", stateOK())
	report("大n比较次数<=2n(硬断言见TestLinearComparisons)", linearOK())
	report("并发查询一致", concurrentOK())
	if failed {
		os.Exit(1)
	}
}
