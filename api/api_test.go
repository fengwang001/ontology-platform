package api_test

import (
	"errors"
	"math/rand"
	"sync"
	"testing"

	"ontology/api"
)

// naive 朴素参照：枚举每个中心（奇偶两类）向两侧逐字符扩展，严格大于才更新。
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

func longestOf(t *testing.T, s string) (int, int) {
	t.Helper()
	var c api.Checker
	err := c.New(s)
	st, ln, err2 := c.Longest()
	if err != nil || err2 != nil {
		t.Fatalf("New/Longest(%q): %v %v", s, err, err2)
	}
	return st, ln
}

// 不变量 1：与朴素参照逐字段相同。
func TestLongestMatchesNaive(t *testing.T) {
	strs := []string{"abbaxyzabba", "a", "aa", "ab", "aba", "abba", "abcba", "aabbaa", "上海自来水来自海上", "abaxabaxabb"}
	r := rand.New(rand.NewSource(7))
	for _, n := range []int{1, 2, 5, 50, 200} {
		b := make([]rune, n)
		for i := range b {
			b[i] = rune('a' + r.Intn(3))
		}
		strs = append(strs, string(b))
	}
	for _, s := range strs {
		st, ln := longestOf(t, s)
		if ns, nl := naive([]rune(s)); st != ns || ln != nl {
			t.Errorf("s=%q got (%d,%d) want (%d,%d)", s, st, ln, ns, nl)
		}
	}
}

// 不变量 3：最长且等长取最左。
func TestLeftmostOnTie(t *testing.T) {
	for _, tc := range []struct {
		s      string
		st, ln int
	}{
		{"abbaxyzabba", 0, 4}, // 两个 "abba"（起始 0 与 7），取 0
		{"xabbay", 1, 4},
		{"zabcbaq", 1, 5},
	} {
		if st, ln := longestOf(t, tc.s); st != tc.st || ln != tc.ln {
			t.Errorf("s=%q got (%d,%d) want (%d,%d)", tc.s, st, ln, tc.st, tc.ln)
		}
	}
}

// 故障注入：三类错误可判定且互不相同，非法 UTF-8 可取出字节偏移。
func TestRejectionsDistinct(t *testing.T) {
	var c api.Checker
	_, _, e0 := c.Longest()
	e1, e2 := c.New(""), c.New(string([]byte{'x', 0xff, 'y'}))
	errs := []error{e0, e1, e2}
	sents := []error{api.ErrNotReady, api.ErrEmpty, api.ErrInvalidUTF8}
	for i, e := range errs { // 每个错误可判定为且仅为自己的哨兵（互不相同）
		for j, s := range sents {
			if (i == j) != errors.Is(e, s) {
				t.Errorf("errors.Is(errs[%d], sents[%d]) = %v", i, j, i != j)
			}
		}
	}
	var iu *api.InvalidUTF8Error
	if !errors.As(e2, &iu) || iu.Offset != 1 {
		t.Errorf("非法 UTF-8 偏移取出失败: %v", e2)
	}
}

// 不变量 4：被拒后状态不变，之后仍可正常使用。
func TestRejectedKeepsState(t *testing.T) {
	var c api.Checker
	if err := c.New("abba"); err != nil {
		t.Fatal(err)
	}
	_ = c.New("")
	_ = c.New(string([]byte{0xff}))
	if st, ln, err := c.Longest(); err != nil || st != 0 || ln != 4 {
		t.Errorf("被拒后状态改变: (%d,%d,%v)", st, ln, err)
	}
	var z api.Checker // 零值被拒后仍可正常构建
	_ = z.New("")
	if err := z.New("aba"); err != nil {
		t.Fatal(err)
	}
	if st, ln, _ := z.Longest(); st != 0 || ln != 3 {
		t.Errorf("got (%d,%d) want (0,3)", st, ln)
	}
	if err := c.SelfCheck(); err != nil { // 内置字符串自检四条不变量
		t.Fatal(err)
	}
}

// 并发：N 个 goroutine 并发 Longest，结果逐字段相同；不用 sleep。
func TestConcurrentLongest(t *testing.T) {
	var c api.Checker
	if err := c.New("abbaxyzabba"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	ch := make(chan [2]int, 64)
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st, ln, _ := c.Longest() // 出错则得 (0,0)，下方断言必失败
			ch <- [2]int{st, ln}
		}()
	}
	wg.Wait()
	close(ch)
	for got := range ch {
		if got != [2]int{0, 4} {
			t.Errorf("并发结果不一致: %v", got)
		}
	}
}
