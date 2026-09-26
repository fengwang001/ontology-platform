package api

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestCyclicEqual(t *testing.T) {
	cases := []struct {
		s, other string
		want     bool
	}{
		{"aabaa", "aaaba", true},
		{"aabaa", "baaab", false}, // 字符多重集不同
		{"baabaa", "aabaab", true},
		{"baabaa", "ababaa", false},
		{"abc", "abcd", false}, // 不等长：false，不报错
		{"abc", "", false},     // 不等长：false
		{"aaaa", "aaaa", true},
		{"bca", "abc", true},
	}
	for _, c := range cases {
		x, err := New(c.s)
		if err != nil {
			t.Fatalf("New(%q): %v", c.s, err)
		}
		if got := x.CyclicEqual(c.other); got != c.want {
			t.Errorf("CyclicEqual(%q,%q)=%v, want %v", c.s, c.other, got, c.want)
		}
	}
}

func TestSentinelErrorsDistinct(t *testing.T) {
	if _, err := New(""); !errors.Is(err, ErrEmpty) {
		t.Errorf("New(\"\") err=%v, want ErrEmpty", err)
	}
	if _, err := New(strings.Repeat("a", maxLen+1)); !errors.Is(err, ErrTooLong) {
		t.Errorf("New(too long) err=%v, want ErrTooLong", err)
	}
	x, _ := New("baabaa")
	for _, k := range []int{-1, 6, 100} {
		if _, err := x.Rotate(k); !errors.Is(err, ErrIndex) {
			t.Errorf("Rotate(%d) err=%v, want ErrIndex", k, err)
		}
	}
	if ErrEmpty == ErrTooLong || ErrTooLong == ErrIndex || ErrEmpty == ErrIndex {
		t.Error("sentinel errors must be mutually distinct")
	}
}

func TestRejectionLeavesStateIntact(t *testing.T) {
	x, _ := New("baabaa")
	k0, _ := x.MinRotation()
	r0, _ := x.Rotate(k0)
	// 一系列被拒操作
	_, _ = New("")
	_, _ = New(strings.Repeat("a", maxLen+1))
	_, _ = x.Rotate(-1)
	_, _ = x.Rotate(6)
	k1, _ := x.MinRotation()
	r1, _ := x.Rotate(k1)
	if k0 != k1 || r0 != r1 || k0 != 1 || r0 != "aabaab" {
		t.Errorf("state changed after rejections: k=%d->%d r=%q->%q", k0, k1, r0, r1)
	}
}

func TestConcurrentReads(t *testing.T) {
	x, _ := New("baabaa")
	wantK, _ := x.MinRotation()
	wantR, _ := x.Rotate(wantK)
	const N = 64
	ks := make([]int, N)
	rs := make([]string, N)
	eq := make([]bool, N)
	var wg sync.WaitGroup
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			ks[g], _ = x.MinRotation()
			rs[g], _ = x.Rotate(ks[g])
			eq[g] = x.CyclicEqual("aabaab")
			_ = x.SelfCheck()
		}(g)
	}
	wg.Wait()
	for g := 0; g < N; g++ {
		if ks[g] != wantK || rs[g] != wantR || !eq[g] {
			t.Errorf("goroutine %d: k=%d r=%q eq=%v, want %d %q true", g, ks[g], rs[g], eq[g], wantK, wantR)
		}
	}
}

func TestSelfCheck(t *testing.T) {
	x, err := New("baabaa")
	if err != nil {
		t.Fatal(err)
	}
	if !x.SelfCheck() {
		t.Error("SelfCheck failed on built-in samples")
	}
}
