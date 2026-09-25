package check

import (
	"errors"
	"math/rand/v2"
	"sync"
	"testing"

	"ontology/hash"
	"ontology/match"
)

func win(m int, s string) *hash.Roller {
	r, _ := hash.New(hash.Base, hash.Mod)
	r.SetLength(m)
	for i := 0; i < m; i++ {
		r.Append(s[i])
	}
	return r
}

func randText(seed, n int) string {
	rng := rand.New(rand.NewPCG(uint64(seed), uint64(seed*7+1)))
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + rng.IntN(26))
	}
	return string(b)
}

// buggyHitOnly 内联“哈希命中即成功”的错误实现，碰撞会产生假阳性。
func buggyHitOnly(text, pattern string) []int {
	m, out := len(pattern), []int{}
	w, ph := win(m, text[:m]), win(m, pattern).Value()
	for i := 0; i+m <= len(text); i++ {
		if w.Value() == ph {
			out = append(out, i)
		}
		if i+m < len(text) {
			w.Shift(text[i], text[i+m])
		}
	}
	return out
}

func TestFindAll(t *testing.T) {
	type tc struct {
		name, text, pattern string
		want                []int
		emptyPattern, buggy bool
	}
	cases := []tc{
		{"same", "abc", "abc", []int{0}, false, false},
		{"overlap", "aaaa", "aa", []int{0, 1, 2}, false, false},
		{"multi", "ababab", "ab", []int{0, 2, 4}, false, false},
		{"none", "abcdef", "xyz", []int{}, false, false},
		{"collision", "aaabeaaaaaad", "be", []int{3}, false, true},
		{"longer", "ab", "abcdef", []int{}, false, false},
		{"empty", "abc", "", nil, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			want, err := Naive(c.text, c.pattern)
			if c.emptyPattern {
				if !errors.Is(err, match.ErrEmptyPattern) {
					t.Fatalf("want ErrEmptyPattern, got %v", err)
				}
				return
			}
			if err := Compare(c.text, c.pattern); err != nil {
				t.Fatal(err)
			}
			got := match.FindAll(c.text, c.pattern)
			if err := Diff(want, got); err != nil || Diff(c.want, got) != nil {
				t.Fatalf("positions: got %v want %v", got, c.want)
			}
			if err := Diff(got, match.FindAll(c.text, c.pattern)); err != nil {
				t.Fatalf("determinism: %v", err)
			}
			if c.buggy && !hasPos(buggyHitOnly(c.text, c.pattern), 0) {
				t.Fatal("buggy impl must false-positive at 0")
			}
		})
	}
}

func hasPos(xs []int, target int) bool {
	for _, x := range xs {
		if x == target {
			return true
		}
	}
	return false
}

func TestRandomEquivalenceAndBound(t *testing.T) {
	cases := []struct{ seed, n, m int }{{1, 1000, 5}, {7, 100000, 5}, {8, 100000, 8}}
	for _, c := range cases {
		t.Run("", func(t *testing.T) {
			text, pattern := randText(c.seed, c.n), randText(c.seed+123, c.m)
			want, _ := Naive(text, pattern)
			if err := Diff(want, match.FindAll(text, pattern)); err != nil {
				t.Fatal(err)
			}
			if c.n < 100000 {
				return
			}
			w, ph, hits := win(c.m, text[:c.m]), win(c.m, pattern).Value(), 0
			for i := 0; i+c.m <= c.n; i++ {
				if w.Value() == ph {
					hits++
				}
				if i+c.m < c.n {
					w.Shift(text[i], text[i+c.m])
				}
			}
			match.ResetVerifications()
			match.FindAll(text, pattern)
			if v, bound := match.Verifications(), int64(c.m*hits); v > bound {
				t.Fatalf("verifications %d > bound %d (hits=%d)", v, bound, hits)
			}
		})
	}
}

func TestConcurrent(t *testing.T) {
	cases := []struct{ text, pattern string }{{"aaabeaaaaaad", "be"}, {randText(5, 5000), randText(50, 3)}}
	for _, c := range cases {
		t.Run("", func(t *testing.T) {
			var wg sync.WaitGroup
			for g := 0; g < 20; g++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for k := 0; k < 50; k++ {
						if err := Compare(c.text, c.pattern); err != nil {
							t.Error(err)
						}
					}
				}()
			}
			wg.Wait()
		})
	}
}

func TestSentinels(t *testing.T) {
	if !errors.Is(match.ErrEmptyPattern, match.ErrEmptyPattern) || errors.Is(ErrMismatch, match.ErrEmptyPattern) || errors.Is(hash.ErrInvalidParam, ErrMismatch) {
		t.Fatal("sentinel errors.Is not distinguishable")
	}
}
