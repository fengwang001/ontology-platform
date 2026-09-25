package check

import (
	"errors"
	"math/rand/v2"
	"reflect"
	"sync"
	"testing"

	"ontology/match"
)

func TestFindAll(t *testing.T) {
	cases := []struct {
		name    string
		text    string
		pattern string
		want    []int
	}{
		{"single", "hello", "ll", []int{2}},
		{"overlap", "aaaaa", "aa", []int{0, 1, 2, 3}},
		{"multiple", "ababa", "aba", []int{0, 2}},
		{"equal", "abc", "abc", []int{0}},
		{"none", "abcdef", "xyz", nil},
		{"prefix suffix", "xabcxabc", "abc", []int{1, 5}},
		{"utf8 bytes", "你好你好", "你好", []int{0, 6}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := match.FindAll(tc.text, tc.pattern)
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			ref, err := NaiveFindAll(tc.text, tc.pattern)
			if err != nil {
				t.Fatalf("naive err: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
			if !reflect.DeepEqual(got, ref) {
				t.Fatalf("got %v naive %v", got, ref)
			}
		})
	}
}

func TestEmptyPattern(t *testing.T) {
	cases := []struct {
		text    string
		pattern string
	}{
		{"abc", ""},
		{"", ""},
	}
	for _, tc := range cases {
		if _, err := match.FindAll(tc.text, tc.pattern); !errors.Is(err, match.ErrEmptyPattern) {
			t.Fatalf("text=%q pattern=%q err=%v", tc.text, tc.pattern, err)
		}
		if _, err := NaiveFindAll(tc.text, tc.pattern); !errors.Is(err, ErrEmptyPattern) {
			t.Fatalf("naive text=%q err=%v", tc.text, err)
		}
	}
}

func TestPatternLongerThanText(t *testing.T) {
	got, err := match.FindAll("ab", "abc")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v want empty", got)
	}
}

func TestDeterminism(t *testing.T) {
	cases := []struct{ text, pattern string }{
		{"mississippi", "issi"},
		{"01234567890123456789", "7"},
	}
	for _, tc := range cases {
		first, _ := match.FindAll(tc.text, tc.pattern)
		for i := 0; i < 5; i++ {
			got, _ := match.FindAll(tc.text, tc.pattern)
			if !reflect.DeepEqual(got, first) {
				t.Fatalf("nondeterministic: %v vs %v", got, first)
			}
		}
	}
}

// TestCollisionNoFalsePositive：窗口哈希为多项式 h = Σ ci*base^(m-1-i)。
// 固定 base=911371、mod=1000003 时，3 字符子串 "   " 与 "\"9o"
// 的窗口哈希同为 615248（构造出的确定性碰撞）。
func TestCollisionNoFalsePositive(t *testing.T) {
	text, pattern := "   \"9oxx", "\"9o"
	got, err := match.FindAll(text, pattern)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !reflect.DeepEqual(got, []int{3}) {
		t.Fatalf("correct impl got %v want [3]", got)
	}
	buggy, err := BuggyFindAll(text, pattern)
	if err != nil {
		t.Fatalf("buggy err: %v", err)
	}
	if !reflect.DeepEqual(buggy, []int{0, 3}) {
		t.Fatalf("buggy impl should false-match at 0 (space-sp vs \"9o collision), got %v", buggy)
	}
}

// TestVerifyBound：逐字符验证总次数 ≤ m*(出现次数+碰撞次数)。
func TestVerifyBound(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	alpha := []byte("abcd")
	text := make([]byte, 100_000)
	for i := range text {
		text[i] = alpha[rng.IntN(len(alpha))]
	}
	pattern := "abcd"
	m := len(pattern)
	n := len(text)

	match.ResetVerifyCount()
	got, err := match.FindAll(string(text), pattern)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	used := match.VerifyCount()

	buggy, _ := BuggyFindAll(string(text), pattern)
	collisions := len(buggy) - len(got)
	bound := int64(m * (len(got) + collisions))
	if used > bound {
		t.Fatalf("verifications %d > bound %d (hits=%d hashHits=%d)",
			used, bound, len(got), len(buggy))
	}
	if used >= int64(n) {
		t.Fatalf("verifications %d >= n %d, degenerated toward O(n*m)", used, n)
	}
}

func TestConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				got, err := match.FindAll("ababaababa", "aba")
				if err != nil || !reflect.DeepEqual(got, []int{0, 2, 5, 7}) {
					t.Errorf("got %v err %v", got, err)
				}
			}
		}()
	}
	wg.Wait()
}
