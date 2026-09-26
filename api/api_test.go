package api_test

import (
	"errors"
	"math/rand"
	"slices"
	"strings"
	"sync"
	"testing"

	"ontology/api"
)

// naive 枚举所有起点对逐字符扩展，取最大长度与最小起始下标。
func naive(a, b string) (best, start int) {
	for i := 0; i < len(a); i++ {
		for j := 0; j < len(b); j++ {
			k := 0
			for i+k < len(a) && j+k < len(b) && a[i+k] == b[j+k] {
				k++
			}
			if k > best {
				best, start = k, i
			}
		}
	}
	return best, start
}

func randStr(rng *rand.Rand, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + rng.Intn(4))
	}
	return string(b)
}

func TestKnownPairs(t *testing.T) {
	cases := []struct {
		a, b, text string
		l, s       int
	}{
		{"banana", "ananas", "anana", 5, 1},
		{"abcx", "abc", "abc", 3, 0},
		{"abcde", "abfce", "ab", 2, 0},
	}
	for _, c := range cases {
		k, _ := api.New(c.a)
		l, s, err := k.Query(c.b)
		if err != nil || l != c.l || s != c.s {
			t.Errorf("%q/%q: Query=(%d,%d,%v), want (%d,%d)", c.a, c.b, l, s, err, c.l, c.s)
		}
		text, err := k.Substring(c.b)
		if err != nil || text != c.text {
			t.Errorf("%q/%q: Substring=(%q,%v), want %q", c.a, c.b, text, err, c.text)
		}
	}
}

func TestQueryMatchesNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for _, n := range []int{1, 7, 30, 100} {
		for _, m := range []int{1, 9, 40, 200} {
			a, b := randStr(rng, n), randStr(rng, m)
			k, _ := api.New(a)
			l, s, err := k.Query(b)
			if nl, ns := naive(a, b); err != nil || l != nl || s != ns {
				t.Fatalf("a=%q b=%q: Query=(%d,%d), naive=(%d,%d), err=%v", a, b, l, s, nl, ns, err)
			}
			text, err := k.Substring(b)
			if err != nil || text != a[s:s+l] || !strings.Contains(b, text) {
				t.Errorf("a=%q b=%q: Substring=%q 不是真实公共子串", a, b, text)
			}
		}
	}
}

func TestErrorsAndStateIntact(t *testing.T) {
	if api.ErrEmptyRef == api.ErrEmptyQuery || api.ErrEmptyQuery == api.ErrTooLong ||
		api.ErrEmptyRef == api.ErrTooLong {
		t.Fatal("三个哨兵错误必须互不相同")
	}
	k, _ := api.New("banana")
	long := strings.Repeat("x", 1<<20)
	cases := []struct {
		name string
		call func() error
		want error
	}{
		{"New(空参照)", func() error { _, err := api.New(""); return err }, api.ErrEmptyRef},
		{"New(超长)", func() error { _, err := api.New(long); return err }, api.ErrTooLong},
		{"Query(空)", func() error { _, _, err := k.Query(""); return err }, api.ErrEmptyQuery},
		{"Substring(空)", func() error { _, err := k.Substring(""); return err }, api.ErrEmptyQuery},
		{"Query(超长)", func() error { _, _, err := k.Query(long); return err }, api.ErrTooLong},
		{"Substring(超长)", func() error { _, err := k.Substring(long); return err }, api.ErrTooLong},
	}
	bl, bs, _ := k.Query("ananas")
	bt, _ := k.Substring("ananas")
	for _, c := range cases {
		if err := c.call(); !errors.Is(err, c.want) {
			t.Errorf("%s err=%v, want %v", c.name, err, c.want)
		}
	}
	al, as, _ := k.Query("ananas")
	at, _ := k.Substring("ananas")
	if bl != al || bs != as || bt != at {
		t.Error("被拒操作改变了状态")
	}
}

func TestConcurrentConsistent(t *testing.T) {
	k, _ := api.New("mississippi-abracadabra-banana")
	rng := rand.New(rand.NewSource(3))
	const g = 16
	bs := make([]string, g)
	for i := range bs {
		bs[i] = randStr(rng, 1+rng.Intn(20))
	}
	type res struct {
		l, s int
		text string
	}
	want, got := make([]res, g), make([]res, g)
	for i, b := range bs {
		want[i].l, want[i].s, _ = k.Query(b)
		want[i].text, _ = k.Substring(b)
	}
	var wg sync.WaitGroup
	for i := range bs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got[i].l, got[i].s, _ = k.Query(bs[i])
			got[i].text, _ = k.Substring(bs[i])
			_ = k.SelfCheck()
		}(i)
	}
	wg.Wait()
	if !slices.Equal(want, got) {
		t.Errorf("并发结果与串行不一致: want=%v got=%v", want, got)
	}
}

func TestSelfCheck(t *testing.T) {
	k, _ := api.New("selfcheck-ref")
	if err := k.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
