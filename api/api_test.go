package api_test

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"ontology/api"
	"ontology/enc"
)

var cases = []struct {
	name string
	k    int
	seq  []string
}{
	{"eight-k4", 4, []string{"a", "a", "a", "b", "c", "d", "e", "a"}},
	{"multi-reset", 2, []string{"a", "b", "c", "d", "e", "a"}},
	{"run-break", 3, []string{"a", "a", "b", "a", "a"}},
}

func encode(t *testing.T, k int, seq []string) *api.Coder {
	t.Helper()
	c, err := api.New(k)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, v := range seq {
		if e := c.Append(v); e != nil {
			t.Fatalf("Append(%q): %v", v, e)
		}
	}
	return c
}
func TestRoundTrip(t *testing.T) {
	for _, c0 := range cases {
		c := encode(t, c0.k, c0.seq)
		if got, e := c.Decode(c.Tokens()); e != nil || !reflect.DeepEqual(got, c0.seq) {
			t.Fatalf("%s: %v %v", c0.name, got, e)
		}
	}
}
func TestCodeBounds(t *testing.T) {
	for _, c0 := range cases {
		for _, tk := range encode(t, c0.k, c0.seq).Tokens() {
			if (tk.Kind != enc.KindReset && (tk.Code < 0 || tk.Code >= c0.k)) ||
				(tk.Kind == enc.KindRef && tk.Count < 1) {
				t.Fatalf("%s: illegal %+v", c0.name, tk)
			}
		}
	}
}
func expand(ts []enc.Token) []enc.Token {
	out := []enc.Token{}
	for _, tk := range ts {
		if tk.Kind != enc.KindRef {
			out = append(out, tk)
			continue
		}
		for i := 0; i < tk.Count; i++ {
			out = append(out, enc.Ref(tk.Code, 1))
		}
	}
	return out
}
func flatEncode(k int, seq []string) []enc.Token {
	m, out := map[string]int{}, []enc.Token{}
	for _, v := range seq {
		code, ok := m[v]
		if !ok {
			if len(m) >= k {
				out, m = append(out, enc.Reset()), map[string]int{}
			}
			code = len(m)
			m[v], out = code, append(out, enc.Put(code, v))
		} else {
			out = append(out, enc.Ref(code, 1))
		}
	}
	return out
}
func TestRLEExpansion(t *testing.T) {
	for _, c0 := range cases {
		c := encode(t, c0.k, c0.seq)
		if g, w := expand(c.Tokens()), flatEncode(c0.k, c0.seq); !reflect.DeepEqual(g, w) {
			t.Fatalf("%s: %v != %v", c0.name, g, w)
		}
	}
}
func TestRejectedOpsNoTrace(t *testing.T) {
	if c, e := api.New(0); !errors.Is(e, api.ErrInvalidConfig) || c != nil {
		t.Fatalf("New(0)=%v,%v", c, e)
	}
	c := encode(t, 4, []string{"a"})
	before := c.Tokens()
	if e := c.Append(""); !errors.Is(e, api.ErrInvalidValue) || !reflect.DeepEqual(c.Tokens(), before) {
		t.Fatalf("empty value: %v or state changed", e)
	}
	bad := [][]enc.Token{{enc.Ref(3, 1)}, {enc.Put(4, "x")}, {enc.Put(0, "x"), enc.Ref(0, 0)}}
	for i, b := range bad {
		if _, e := c.Decode(b); !errors.Is(e, api.ErrBadToken) {
			t.Fatalf("bad[%d] %v", i, e)
		}
	}
	if api.ErrInvalidConfig == api.ErrInvalidValue || api.ErrInvalidValue == api.ErrBadToken ||
		api.ErrInvalidConfig == api.ErrBadToken {
		t.Fatal("sentinels not distinct")
	}
	if e := c.Append("b"); e != nil {
		t.Fatalf("unusable after rejection: %v", e)
	}
	if got, _ := c.Decode(c.Tokens()); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("after rejection got %v", got)
	}
}
func TestConcurrentDecode(t *testing.T) {
	seq := []string{"a", "a", "a", "b", "c", "d", "e", "a"}
	c := encode(t, 4, seq)
	base := c.Tokens()
	const n = 64
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok, start := true, make(chan struct{})
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			<-start
			got, e := c.Decode(base)
			mu.Lock()
			ok = ok && e == nil && reflect.DeepEqual(got, seq) && reflect.DeepEqual(c.Tokens(), base)
			mu.Unlock()
		}()
	}
	close(start)
	wg.Wait()
	if !ok {
		t.Fatal("concurrent decode/snapshot mismatch")
	}
}
func TestSelfCheck(t *testing.T) {
	c, err := api.New(4)
	if err == nil {
		err = c.SelfCheck()
	}
	if err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}
