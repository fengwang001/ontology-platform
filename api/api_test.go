package api

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"

	"ontology/pfx"
)

// brutePi 暴力计算 π[i]：逐个长度试「真前缀==后缀」。
func brutePi(p []byte, i int) int {
	best := 0
	for l := 1; l <= i; l++ {
		ok := true
		for k := 0; k < l && ok; k++ {
			ok = p[k] == p[i-l+1+k]
		}
		if ok {
			best = l
		}
	}
	return best
}

// TestPrefixFunction 钉住不变量 2：π[i] 等于最长真前缀==后缀长度，且 0≤π[i]≤i。
func TestPrefixFunction(t *testing.T) {
	for _, c := range []string{"abaaba", "aa", "a", "ababab", "abcabd", "aaaaaa"} {
		pi := pfx.Compute([]byte(c))
		for i := range pi {
			// brutePi 返回值本身落在 [0,i]，相等即同时钉住值与界
			if pi[i] != brutePi([]byte(c), i) {
				t.Errorf("pi(%q)[%d]=%d, want %d", c, i, pi[i], brutePi([]byte(c), i))
			}
		}
	}
	if got := pfx.Compute([]byte("abaaba")); !reflect.DeepEqual(got, []int{0, 0, 1, 1, 2, 3}) {
		t.Errorf("pi(abaaba)=%v, want [0 0 1 1 2 3]（第三节六行表）", got)
	}
}

// TestNaiveConsistency 钉住不变量 1：固定语料 + 随机语料、多种切块，
// Matches 与朴素结果逐位置相同（含重叠）。
func TestNaiveConsistency(t *testing.T) {
	cases := [][2]string{
		{"abaaba", "aabaabaab"}, {"aa", "aaa"}, {"aa", "aaaa"},
		{"ab", "ababab"}, {"a", "a"}, {"abc", "xyz"}, {"aba", "ababa"},
	}
	rng := rand.New(rand.NewSource(7))
	for _, sz := range []int{100, 1000, 5000} { // 多档规模，循环生成
		pat, text := make([]byte, 3), make([]byte, sz)
		for i := range pat {
			pat[i] = byte('a' + rng.Intn(2))
		}
		for i := range text {
			text[i] = byte('a' + rng.Intn(2))
		}
		cases = append(cases, [2]string{string(pat), string(text)})
	}
	for _, c := range cases {
		want := naiveEnds([]byte(c[0]), []byte(c[1]))
		for _, step := range []int{1, 2, 7, len(c[1]) + 1} {
			m, err := New([]byte(c[0]))
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < len(c[1]); i += step {
				if _, err := m.Feed([]byte(c[1][i:min(i+step, len(c[1]))])); err != nil {
					t.Fatalf("%q@%q step=%d: %v", c[0], c[1], step, err)
				}
			}
			if got := m.Matches(); !reflect.DeepEqual(got, want) {
				t.Errorf("%q@%q step=%d: got %v, want %v", c[0], c[1], step, got, want)
			}
		}
	}
}

// TestFaultInjection 钉住不变量 4 与第五节：三类哨兵错误可判定、互不相同，
// 被拒后状态不变且可继续正常使用。
func TestFaultInjection(t *testing.T) {
	if _, err := New(nil); !errors.Is(err, ErrEmptyPattern) {
		t.Errorf("空模式: %v", err)
	}
	if _, err := New(make([]byte, maxPatLen+1)); !errors.Is(err, ErrPatternTooLong) {
		t.Errorf("超长模式: %v", err)
	}
	for _, p := range [][2]error{{ErrEmptyPattern, ErrPatternTooLong},
		{ErrPatternTooLong, ErrTooManyMatches}, {ErrEmptyPattern, ErrTooManyMatches}} {
		if errors.Is(p[0], p[1]) {
			t.Errorf("哨兵错误不互异: %v vs %v", p[0], p[1])
		}
	}
	m, _ := newMatcher([]byte("aa"), maxPatLen, 1)
	if _, err := m.Feed([]byte("x")); err != nil {
		t.Fatal(err)
	}
	before := m.Matches()
	if _, err := m.Feed([]byte("aaa")); !errors.Is(err, ErrTooManyMatches) {
		t.Fatalf("超限: %v", err)
	}
	if got := m.Matches(); !reflect.DeepEqual(got, before) {
		t.Errorf("被拒后状态改变: %v -> %v", before, got)
	}
	if _, err := m.Feed([]byte("aa")); err != nil { // 被拒后仍可继续
		t.Errorf("复用失败: %v", err)
	}
	if got := m.Matches(); !reflect.DeepEqual(got, []int{2}) {
		t.Errorf("复用后 Matches=%v, want [2]", got)
	}
}

// TestConcurrentReads 钉住第六节：N 个 goroutine 并发只读同一已喂满实例，
// 各自拿到的匹配集合逐元素相同；不用 sleep 制造时序。顺带跑 SelfCheck。
func TestConcurrentReads(t *testing.T) {
	m, _ := New([]byte("aba"))
	if _, err := m.Feed([]byte("ababaababa")); err != nil {
		t.Fatal(err)
	}
	want, start := m.Matches(), make(chan struct{}) // SelfCheck 在下方 goroutine 内并发核验
	errs := make(chan error, 16)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-start // 统一放行制造并发
			for k := 0; k < 50; k++ {
				if !reflect.DeepEqual(m.Matches(), want) {
					errs <- fmt.Errorf("g%d: 匹配集合不一致", id)
					return
				}
				if err := m.SelfCheck(); err != nil {
					errs <- err
					return
				}
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
