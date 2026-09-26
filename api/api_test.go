package api

import (
	"errors"
	"math/rand"
	"strings"
	"sync"
	"testing"
)

func randStr(r *rand.Rand, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + r.Intn(3))
	}
	return string(b)
}

// 不变量 1、3：三类查询与朴素结果一致（表驱动 + 循环生成随机规模）。
func TestQueriesMatchNaive(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	cases := []string{"abcbc", "aaaa", "z", "abcabc"}
	for i := 0; i < 15; i++ {
		cases = append(cases, randStr(r, 1+r.Intn(40)))
	}
	for _, s := range cases {
		m, err := New(s)
		if err != nil {
			t.Fatalf("New(%q): %v", s, err)
		}
		if got := m.DistinctSubstrings(); got != naiveDistinct(s) {
			t.Fatalf("s=%q: Distinct=%d, 朴素=%d", s, got, naiveDistinct(s))
		}
		for i := 0; i < len(s); i++ {
			for j := i + 1; j <= len(s); j++ {
				if got, _ := m.Occurrences(s[i:j]); got != naiveCount(s, s[i:j]) {
					t.Fatalf("s=%q sub=%q: Occ=%d, 朴素=%d", s, s[i:j], got, naiveCount(s, s[i:j]))
				}
			}
		}
		if got, err := m.Occurrences(s + "\x00"); got != 0 || err != nil {
			t.Fatalf("s=%q: 不存在的子串应返回 (0,nil), 得到 (%d,%v)", s, got, err)
		}
		for _, ts := range []string{s, randStr(r, 30), "x" + s} {
			got, err := m.LongestCommonSubstring(ts)
			if err != nil || len(got) != naiveLCS(s, ts) ||
				!strings.Contains(s, got) || !strings.Contains(ts, got) {
				t.Fatalf("s=%q t=%q: LCS=%q 与朴素不一致", s, ts, got)
			}
		}
	}
}

// 三类故障注入：哨兵错误可判定且互不相同。
func TestSentinelErrors(t *testing.T) {
	if ErrEmptyString == ErrTooLong || ErrTooLong == ErrEmptyQuery || ErrEmptyString == ErrEmptyQuery {
		t.Fatal("三类哨兵错误必须互不相同")
	}
	if _, err := New(""); !errors.Is(err, ErrEmptyString) {
		t.Fatalf("空串应报 ErrEmptyString, 得到 %v", err)
	}
	if _, err := New(strings.Repeat("x", maxLen+1)); !errors.Is(err, ErrTooLong) {
		t.Fatalf("超长应报 ErrTooLong, 得到 %v", err)
	}
	m, _ := New("abcbc")
	if _, err := m.Occurrences(""); !errors.Is(err, ErrEmptyQuery) {
		t.Fatalf("空查询应报 ErrEmptyQuery, 得到 %v", err)
	}
	if _, err := m.LongestCommonSubstring(""); !errors.Is(err, ErrEmptyQuery) {
		t.Fatalf("空 LCS 应报 ErrEmptyQuery, 得到 %v", err)
	}
}

// 不变量 4：被拒操作不留痕，之后查询结果不变。
func TestRejectedOpsKeepState(t *testing.T) {
	m, _ := New("abcbc")
	d0 := m.DistinctSubstrings()
	o0, _ := m.Occurrences("b")
	l0, _ := m.LongestCommonSubstring("bcabc")
	_, _ = New("")
	_, _ = New(strings.Repeat("x", maxLen+1))
	_, _ = m.Occurrences("")
	_, _ = m.LongestCommonSubstring("")
	if m.DistinctSubstrings() != d0 {
		t.Fatal("被拒操作后 Distinct 变了")
	}
	if o, _ := m.Occurrences("b"); o != o0 {
		t.Fatal("被拒操作后 Occurrences 变了")
	}
	if l, _ := m.LongestCommonSubstring("bcabc"); l != l0 {
		t.Fatal("被拒操作后 LCS 变了")
	}
}

// 并发只读：N 个 goroutine 并发查询同一实例，结果必须逐项相同。
func TestConcurrentReads(t *testing.T) {
	m, _ := New("abcbcabcbc")
	type res struct {
		d, o int
		l    string
	}
	want := res{d: m.DistinctSubstrings()}
	want.o, _ = m.Occurrences("bc")
	want.l, _ = m.LongestCommonSubstring("bcabc")
	start := make(chan struct{})
	errs := make(chan string, 8)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < 100; i++ {
				got := res{d: m.DistinctSubstrings()}
				got.o, _ = m.Occurrences("bc")
				got.l, _ = m.LongestCommonSubstring("bcabc")
				if got != want {
					errs <- "并发读结果不一致"
					return
				}
			}
			if err := m.SelfCheck(); err != nil {
				errs <- err.Error()
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}

// SelfCheck 必须能被直接调用且通过。
func TestSelfCheck(t *testing.T) {
	m, _ := New("abcbc")
	if err := m.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
