package kv

import (
	"errors"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/schema"
)

var errBoom = errors.New("boom")

func ok(t testing.TB, c bool, m string, a ...any) {
	if !c {
		t.Fatalf(m, a...)
	}
}

type harn struct {
	st   *Store
	r    *schema.Registry
	n    []int64
	gate <-chan struct{}
	sig  chan<- struct{}
}

func newH() *harn { r := schema.NewRegistry(); return &harn{st: New(r), r: r, n: make([]int64, 8)} }
func (h *harn) reg(fail int, fs ...int) {
	for _, f := range fs {
		f := f
		h.r.Register(f, func(b []byte) ([]byte, error) {
			atomic.AddInt64(&h.n[f], 1)
			if h.sig != nil && f == 1 {
				h.sig <- struct{}{}
				<-h.gate
			}
			if f == fail {
				return nil, errBoom
			}
			return append(append([]byte(nil), b...), byte('0'+f+1)), nil
		})
	}
}
func (h *harn) c(f int) int { return int(atomic.LoadInt64(&h.n[f])) }
func (h *harn) prep(fail, up int, fs ...int) {
	h.reg(fail, fs...)
	h.st.Write("k", []byte("a"))
	h.st.Upgrade(up)
}
func TestMigrationWriteback(t *testing.T) {
	for _, tc := range []struct {
		up, reads int
		regs      []int
		want      string
	}{
		{4, 2, []int{1, 2, 3}, "a234"}, {3, 1, []int{1, 2}, "a23"},
	} {
		h := newH()
		h.prep(0, tc.up, tc.regs...)
		var g []byte
		for i := 0; i < tc.reads; i++ {
			g, _ = h.st.Read("k")
		}
		ok(t, string(g) == tc.want, "got %q want %q", g, tc.want)
		g[0] = 'z' // 改返回切片不得影响存储
		v, raw, _ := h.st.Stored("k")
		ok(t, v == tc.up && raw[0] == 'a', "sv=%d raw=%q", v, raw)
		for _, f := range tc.regs {
			ok(t, h.c(f) == 1, "step %d ran %d", f, h.c(f))
		}
	}
	h := newH() // v3 写入只跑第3步；旧键二次读不重跑前缀、也不从 v1 重跑。
	h.reg(0, 1, 2)
	h.st.Write("old", []byte("a"))
	h.st.Upgrade(3)
	h.st.Read("old")
	h.st.Write("fresh", []byte("c"))
	h.reg(0, 3)
	h.st.Upgrade(4)
	vf, _ := h.st.Read("fresh")
	ok(t, string(vf) == "c4" && h.c(1) == 1 && h.c(3) == 1, "fresh %q %d,%d", vf, h.c(1), h.c(3))
	vo, _ := h.st.Read("old")
	ok(t, string(vo) == "a234" && h.c(1) == 1, "old %q cnt1=%d", vo, h.c(1))
}
func TestFailureLeavesNoTrace(t *testing.T) {
	h := newH()
	h.prep(2, 3, 1, 2)
	for i := 0; i < 2; i++ {
		_, e := h.st.Read("k")
		ok(t, errors.Is(e, schema.ErrMigrationFailed) && errors.Is(e, errBoom), "read %d %v", i, e)
	}
	v, r, _ := h.st.Stored("k")
	ok(t, v == 1 && string(r) == "a" && h.c(1) == 2 && h.c(2) == 2, "trace (%d,%q) %d,%d", v, r, h.c(1), h.c(2))
	g := newH()
	g.prep(0, 3, 1)
	_, e := g.st.Read("k")
	ok(t, errors.Is(e, schema.ErrMissingMigration) && g.c(1) == 0, "missing %v %d", e, g.c(1))
	v, r, _ = g.st.Stored("k")
	ok(t, v == 1 && string(r) == "a", "changed (%d,%q)", v, r)
	g.r.Register(2, func(b []byte) ([]byte, error) { return b, nil })
	rv, e := g.st.Read("k")
	ok(t, e == nil && string(rv) == "a2", "recover (%q,%v)", rv, e)
}
func TestLazyUpgradeTouched(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		h := newH()
		h.reg(0, 1, 2, 3)
		for i := 0; i < m; i++ {
			h.st.Write("k"+strconv.Itoa(i), []byte("x"))
		}
		h.st.Upgrade(4)
		ok(t, h.st.touchedCount() <= 1, "m=%d upgrade touched %d", m, h.st.touchedCount())
		if _, e := h.st.Read("k" + strconv.Itoa(m/2)); e != nil || h.st.touchedCount() > 1 {
			t.Fatalf("m=%d read touched %d", m, h.st.touchedCount())
		}
	}
}
func TestConcurrentReadDedup(t *testing.T) {
	prevP := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(prevP)
	h := newH()
	rel, sig, bar := make(chan struct{}), make(chan struct{}, 1), make(chan struct{})
	h.gate, h.sig = rel, sig
	h.reg(0, 1, 2, 3)
	h.st.Write("k", []byte("v"))
	h.st.Upgrade(4)
	const N = 32
	var wg sync.WaitGroup
	vals := make([][]byte, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); <-bar; vals[i], _ = h.st.Read("k") }(i)
	}
	go func() { bar <- struct{}{} }()
	<-sig
	close(bar)
	runtime.Gosched()
	close(rel)
	wg.Wait()
	for i := 1; i < N; i++ {
		if string(vals[i]) != string(vals[0]) {
			t.Fatalf("reader %d: %q != %q", i, vals[i], vals[0])
		}
	}
	ok(t, string(vals[0]) == "v234" && h.c(1)*h.c(2)*h.c(3) == 1, "once %q %d,%d,%d", vals[0], h.c(1), h.c(2), h.c(3))
}
