package api_test

import (
	"reflect"
	"strings"
	"sync"
	"testing"

	"ontology/api"
	"ontology/enc"
)

func encode(t *testing.T, k int, seq []string) (*api.Engine, []enc.Token) {
	e, err := api.New(k)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range seq {
		if e.Append(v) != nil {
			t.Fatalf("append %q", v)
		}
	}
	return e, e.Tokens()
}

func naive(k int, seq []string) []enc.Token {
	m, out := map[string]int{}, []enc.Token{}
	for _, v := range seq {
		if c, ok := m[v]; ok {
			out = append(out, enc.RefToken(c, 1))
			continue
		}
		if len(m) == k {
			out, m = append(out, enc.ResetToken()), map[string]int{}
		}
		c := len(m)
		m[v] = c
		out = append(out, enc.PutToken(c, v))
	}
	return out
}

var seqCases = []struct {
	k   int
	seq []string
}{
	{4, []string{"a", "a", "a", "b", "c", "d", "e", "a"}},
	{1, []string{"x", "x", "y", "y", "x"}},
	{2, []string{"p", "q", "r", "p", "q", "r", "r"}},
}

var eightWant = []enc.Token{enc.PutToken(0, "a"), enc.RefToken(0, 2), enc.PutToken(1, "b"), enc.PutToken(2, "c"), enc.PutToken(3, "d"), enc.ResetToken(), enc.PutToken(0, "e"), enc.PutToken(1, "a")}

func TestRoundTrip(t *testing.T) {
	for _, tc := range seqCases {
		e, tok := encode(t, tc.k, tc.seq)
		got, err := e.Decode(tok)
		if err != nil || !reflect.DeepEqual(got, tc.seq) {
			t.Fatalf("k=%d: %v %v", tc.k, got, err)
		}
	}
}

func TestCodeBounds(t *testing.T) {
	for _, tc := range seqCases {
		_, tok := encode(t, tc.k, tc.seq)
		for i, x := range tok {
			if x.Code < 0 || x.Code >= tc.k || (x.Kind == enc.KindRef && x.Count < 1) {
				t.Fatalf("k=%d token %d: %+v", tc.k, i, x)
			}
		}
	}
}

func TestRLELossless(t *testing.T) {
	for _, tc := range seqCases {
		_, tok := encode(t, tc.k, tc.seq)
		flat := []enc.Token{}
		for _, x := range tok {
			if x.Kind != enc.KindRef {
				flat = append(flat, x)
				continue
			}
			y := x
			y.Count = 1
			for j := 0; j < x.Count; j++ {
				flat = append(flat, y)
			}
		}
		if !reflect.DeepEqual(flat, naive(tc.k, tc.seq)) {
			t.Fatalf("k=%d expansion differs", tc.k)
		}
		if tc.k == 4 && !reflect.DeepEqual(tok, eightWant) {
			t.Fatalf("stream=%v", tok)
		}
	}
}

func TestRejectedLeavesState(t *testing.T) {
	g, err := api.New(0)
	if err != api.ErrBadConfig || g != nil {
		t.Fatalf("New(0)=%v,%v", g, err)
	}
	e, _ := api.New(2)
	if e.Append("z") != nil {
		t.Fatal(err)
	}
	before := e.Tokens()
	if err := e.Append(""); err != enc.ErrEmptyValue {
		t.Fatal(err)
	}
	for _, b := range [][]enc.Token{{enc.RefToken(1, 1)}, {enc.PutToken(2, "y")}, {enc.PutToken(0, "y"), enc.RefToken(0, 0)}} {
		if _, err := e.Decode(b); err != enc.ErrBadToken {
			t.Fatalf("%v: %v", b, err)
		}
	}
	if !reflect.DeepEqual(e.Tokens(), before) || e.Append("q") != nil {
		t.Fatal("state changed or unusable")
	}
	if out, _ := e.Decode(e.Tokens()); strings.Join(out, "") != "zq" {
		t.Fatalf("decode=%v", out)
	}
	if api.ErrBadConfig == enc.ErrEmptyValue || api.ErrBadConfig == enc.ErrBadToken || enc.ErrEmptyValue == enc.ErrBadToken {
		t.Fatal("sentinels not distinct")
	}
}

func TestConcurrentDecode(t *testing.T) {
	e, tok := encode(t, 4, seqCases[0].seq)
	ref := e.Tokens()
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, derr := e.Decode(tok)
			if derr != nil || strings.Join(out, "") != "aaabcdea" ||
				!reflect.DeepEqual(e.Tokens(), ref) {
				t.Errorf("concurrent decode/snapshot disagreed: %v %v", out, derr)
			}
		}()
	}
	wg.Wait()
}

func TestSelfCheck(t *testing.T) {
	e, err := api.New(1)
	if err != nil || e.SelfCheck() != nil {
		t.Fatalf("SelfCheck: %v %v", err, e)
	}
}
