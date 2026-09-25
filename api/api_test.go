package api_test

import (
	"errors"
	"sync"
	"testing"
	"unicode/utf8"

	"ontology/api"
	"ontology/grapheme"
)

// 钉住不变量 2：每条规则单独触发的切/不切，用簇数判定。
func TestBoundaryRules(t *testing.T) {
	g := api.New()
	cases := []struct {
		name string
		s    string
		want int
	}{
		{"rule1 CRLF glued", "\r\n", 1},
		{"rule1 CR alone breaks", "\ra", 2},
		{"rule2 ZWJ glues next", "a‍b", 1},
		{"rule3 combining glues", "é", 1},
		{"rule3 emoji modifier glues", "👍🏻", 1},
		{"rule3 variation selector glues", "a️", 1},
		{"rule4 RI pair glues", "\U0001F1E6\U0001F1E7", 1},
		{"rule4 RI triple splits 2+1", "\U0001F1E6\U0001F1E7\U0001F1E8", 2},
		{"rule4 RI quad splits 2+2", "\U0001F1E6\U0001F1E7\U0001F1E8\U0001F1E9", 2},
		{"plain pair breaks", "ab", 2},
		{"LF then CR breaks", "\n\r", 2},
	}
	for _, c := range cases {
		if n, err := g.Count(c.s); err != nil || n != c.want {
			t.Fatalf("%s: Count(%q) = %d, %v; want %d", c.name, c.s, n, err, c.want)
		}
	}
}

// 钉住不变量 3：首簇 Start==0、首尾相接、末簇 End==len(s)、字节数自洽。
func TestOffsetChain(t *testing.T) {
	g := api.New()
	for _, s := range []string{
		"éa‍b\r\n\U0001F1E6\U0001F1E7c👍🏻",
		"plain", "́́́x", "\U0001F1E6\U0001F1E7\U0001F1E8",
	} {
		cs, err := g.Segments(s)
		if err != nil {
			t.Fatalf("%q: %v", s, err)
		}
		if cs[0].Start != 0 || cs[len(cs)-1].End != len(s) {
			t.Fatalf("%q: not anchored: %+v", s, cs)
		}
		for i, c := range cs {
			if i > 0 && cs[i-1].End != c.Start {
				t.Fatalf("%q: gap at cluster %d", s, i)
			}
			want := 0
			for _, r := range s[c.Start:c.End] {
				want += utf8.RuneLen(r)
			}
			if c.End-c.Start != want {
				t.Fatalf("%q: cluster %d bytes %d, want %d", s, i, c.End-c.Start, want)
			}
		}
	}
}

// 钉住不变量 4：三类故障可判定、互不相同、不留痕，拒绝后正常使用。
func TestFailures(t *testing.T) {
	g := api.New()
	if _, err := g.Segments(""); !errors.Is(err, api.ErrEmpty) {
		t.Fatalf("empty: %v", err)
	}
	if _, err := g.Segments("a\xff"); !errors.Is(err, grapheme.ErrInvalidUTF8) {
		t.Fatalf("invalid utf-8: %v", err)
	}
	for _, i := range []int{-1, 6, 100} {
		if _, err := g.At("éa‍b\r\n\U0001F1E6\U0001F1E7c👍🏻", i); !errors.Is(err, api.ErrOutOfRange) {
			t.Fatalf("At(%d): %v", i, err)
		}
	}
	if api.ErrEmpty == api.ErrOutOfRange ||
		errors.Is(api.ErrEmpty, grapheme.ErrInvalidUTF8) ||
		errors.Is(api.ErrOutOfRange, grapheme.ErrInvalidUTF8) {
		t.Fatal("sentinel errors not distinct")
	}
	if cs, err := g.Segments("a\xff"); cs != nil || err == nil {
		t.Fatalf("partial result leaked: %v", cs)
	}
	if n, err := g.Count("éa‍b\r\n\U0001F1E6\U0001F1E7c👍🏻"); err != nil || n != 6 {
		t.Fatalf("state broken after rejections: %d, %v", n, err)
	}
	if err := g.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

// 并发：N 个 goroutine 对同一字符串各自 Segments，结果逐字段相同。
func TestConcurrentIdentical(t *testing.T) {
	g := api.New()
	const s = "éa‍b\r\n\U0001F1E6\U0001F1E7c👍🏻 世界👩‍💻"
	want, err := g.Segments(s)
	if err != nil {
		t.Fatal(err)
	}
	const N = 32
	res := make([][]api.Cluster, N)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for p := 0; p < N; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			<-start
			res[p], _ = g.Segments(s)
		}(p)
	}
	close(start)
	wg.Wait()
	for p := 0; p < N; p++ {
		if len(res[p]) != len(want) {
			t.Fatalf("goroutine %d: %d clusters, want %d", p, len(res[p]), len(want))
		}
		for i := range want {
			if res[p][i] != want[i] {
				t.Fatalf("goroutine %d cluster %d: %+v, want %+v", p, i, res[p][i], want[i])
			}
		}
	}
}
