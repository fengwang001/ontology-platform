package check_test

import (
	"errors"
	"math/rand"
	"reflect"
	"sync"
	"testing"

	"ontology/check"
	"ontology/hash"
	"ontology/match"
)

func roll(text string, m int, base, mod uint64, visit func(i int, v uint64)) {
	r := hash.New(base, mod, m)
	for i := 0; i < m; i++ {
		r.Append(text[i])
	}
	for i := 0; i+m <= len(text); i++ {
		visit(i, r.Value())
		if i+m < len(text) {
			r.Remove(text[i])
			r.Append(text[i+m])
		}
	}
}

func fresh(s string, base, mod uint64) uint64 {
	r := hash.New(base, mod, len(s))
	for i := 0; i < len(s); i++ {
		r.Append(s[i])
	}
	return r.Value()
}
func TestFindAllAgainstNaive(t *testing.T) {
	cases := []struct{ text, pattern string }{{"aaaa", "aa"}, {"abcabcabc", "abc"}, {"abc", "abc"},
		{"abc", "d"}, {"ab", "abc"}, {"", "a"}, {"a", "a"}, {"mississippi", "issi"}, {"汉字匹配汉字", "匹配"}}
	for _, tc := range cases {
		if got, err := match.FindAll(tc.text, tc.pattern); err != nil || !reflect.DeepEqual(got, check.Naive(tc.text, tc.pattern)) {
			t.Errorf("FindAll(%q,%q)=%v,%v", tc.text, tc.pattern, got, err)
		}
	}
	if _, err := match.FindAll("abc", ""); !errors.Is(err, match.ErrEmptyPattern) {
		t.Fatalf("err=%v", err)
	}
}
func TestRollingHash(t *testing.T) {
	pattern, text := "\x00\x03", "xx\x0a\x00yy"   // base=10,mod=97 下两者哈希均为 3
	roll(text, 2, 10, 97, func(i int, v uint64) { // 钉住减法取模：滚动值须等于逐窗口重算值
		if f := fresh(text[i:i+2], 10, 97); v != f {
			t.Fatalf("i=%d rolling=%d fresh=%d", i, v, f)
		}
	})
	run := func(verify bool) (out []int) {
		target := fresh(pattern, 10, 97)
		roll(text, 2, 10, 97, func(i int, v uint64) {
			if v == target && (!verify || text[i:i+2] == pattern) {
				out = append(out, i)
			}
		})
		return out
	}
	if got := run(true); len(got) != 0 {
		t.Fatalf("带验证仍假阳性: %v", got)
	}
	if got := run(false); !reflect.DeepEqual(got, []int{2}) {
		t.Fatalf("哈希命中即判成功应产生假阳性[2], 实际 %v", got)
	}
}
func TestVerifyCountBoundAndConcurrent(t *testing.T) {
	b := make([]byte, 100000)
	rand.New(rand.NewSource(1)).Read(b)
	text, pattern := string(b), string(b[5000:5008])
	match.ResetVerifyCount()
	got, _ := match.FindAll(text, pattern)
	if !reflect.DeepEqual(got, check.Naive(text, pattern)) {
		t.Fatalf("got=%v", got)
	}
	target, hits := fresh(pattern, match.Base, match.Mod), 0
	roll(text, len(pattern), match.Base, match.Mod, func(_ int, v uint64) {
		if v == target {
			hits++
		}
	})
	if vc := match.VerifyCount(); vc > int64(len(pattern)*hits) || vc >= int64(len(text)) {
		t.Fatalf("verify=%d 上界 m*(出现+碰撞)=%d", vc, len(pattern)*hits)
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got2, err := match.FindAll(text, pattern); err != nil || !reflect.DeepEqual(got2, got) {
				t.Errorf("并发结果不一致: %v %v", got2, err)
			}
		}()
	}
	wg.Wait()
}
