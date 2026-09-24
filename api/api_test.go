package api_test

import (
	"ontology/api"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func ai(x string) int { n, _ := strconv.Atoi(x); return n }
func ok(t *testing.T, c bool, f string, a ...any) {
	if !c {
		t.Fatalf(f, a...)
	}
}
func run(s *api.System, line string) (ap, sk int, err error) {
	for _, op := range strings.Split(line, ";") {
		f := strings.Fields(op)
		switch f[0] {
		case "D":
			err = s.Down(ai(f[1]))
		case "U":
			ap, sk, err = s.Up(ai(f[1]))
		case "W":
			err = s.Write(f[1], f[3], int64(ai(f[2])))
		}
		if err != nil {
			return
		}
	}
	return
}
func st(s *api.System, k string, hr int) string {
	b := []string{}
	for _, e := range s.Get(k) {
		v := "-"
		if e.Ver != 0 {
			v = e.Value + "@" + strconv.FormatInt(e.Ver, 10)
		}
		b = append(b, v)
	}
	for _, e := range s.Hints(hr) {
		b = append(b, "h"+strconv.FormatInt(e.Ver, 10))
	}
	return strings.Join(b, ",")
}
func TestEightStepScenario(t *testing.T) {
	s, _ := api.New(3, 3)
	_ = s.Down(1)
	ops := strings.Split("W k 5 a|W k 7 b|W k 6 c|W k 8 d|U 1|D 1; W k 9 e|W k 9 f|U 1", "|")
	wants := strings.Split("a@5,-,a@5,h5|b@7,-,b@7,h5,h7|b@7,-,b@7,h5,h7,h6|b@7,-,b@7,h5,h7,h6|b@7,b@7,b@7|e@9,b@7,e@9,h9|e@9,b@7,e@9,h9,h9|e@9,e@9,e@9", "|")
	ap := [...]int{0, 0, 0, 0, 2, 0, 0, 1}
	sk := [...]int{0, 0, 0, 0, 1, 0, 0, 1}
	for i := range ops {
		a, k, e := run(s, ops[i])
		got := st(s, "k", 1)
		ok(t, (e == api.ErrHintOverflow) == (i == 3) && (i == 3 || e == nil) && a == ap[i] && k == sk[i] && got == wants[i],
			"step %d: counts %d,%d err %v state %s want %s", i+1, a, k, e, got, wants[i])
	}
}
func TestNaiveReference(t *testing.T) {
	mh := []int{3, 5, 10}
	scr := strings.Split("D 1; W k 5 a; W k 7 b; W k 6 c; W k 8 d; U 1|D 0; W k 9 e; W k 9 f; U 0|D 1; W x 1 a; W y 2 b; W x 3 c; U 1; D 2; W x 4 d; W y 1 z; U 2", "|")
	win := []map[string]string{{"k": "b@7"}, {"k": "e@9"}, {"x": "d@4", "y": "b@2"}}
	for ci := range mh {
		s, _ := api.New(3, mh[ci])
		for _, op := range strings.Split(scr[ci], ";") {
			_, _, _ = run(s, op)
		}
		g := s.Get
		for r := 0; r < 3; r++ {
			_, _, _ = s.Up(r)
			for k, w := range win[ci] {
				got := g(k)[r].Value + "@" + strconv.FormatInt(g(k)[r].Ver, 10)
				ok(t, got == w, "case %d %s R%d = %s want %s", ci, k, r, got, w)
			}
		}
	}
}
func TestReplayIdempotent(t *testing.T) {
	s, _ := api.New(2, 5)
	_, _, e := run(s, "D 0; W k 1 a; W q 2 b")
	ok(t, e == nil, "%v", e)
	a, sk, _ := s.Up(0)
	a2, sk2, _ := s.Up(0)
	ok(t, a == 2 && sk == 0 && a2 == 0 && sk2 == 0 && st(s, "k", 0) == "a@1,a@1" && st(s, "q", 0) == "b@2,b@2" && len(s.Hints(0)) == 0,
		"Up %d,%d then %d,%d k=%s q=%s", a, sk, a2, sk2, st(s, "k", 0), st(s, "q", 0))
}
func TestNoDowngrade(t *testing.T) {
	s, _ := api.New(1, 5)
	_, _, e := run(s, "W k 10 hi; D 0; W k 5 lo; W k 10 tie; W k 11 new")
	ok(t, e == nil, "%v", e)
	a, sk, _ := s.Up(0)
	ok(t, a == 1 && sk == 2 && st(s, "k", 0) == "new@11", "Up %d,%d state %s", a, sk, st(s, "k", 0))
}
func TestRejectedLeavesNoTrace(t *testing.T) {
	ok(t, api.ErrBadConfig != api.ErrBadVersion && api.ErrBadConfig != api.ErrEmptyKey && api.ErrBadConfig != api.ErrHintOverflow && api.ErrBadVersion != api.ErrEmptyKey && api.ErrBadVersion != api.ErrHintOverflow && api.ErrEmptyKey != api.ErrHintOverflow, "sentinels not distinct")
	s, _ := api.New(3, 2)
	_, c1 := api.New(0, 1)
	_, c2 := api.New(1, 0)
	_, _, uo := s.Up(9)
	for _, b := range [][2]error{{c1, api.ErrBadConfig}, {c2, api.ErrBadConfig}, {s.Write("k", "v", 0), api.ErrBadVersion}, {s.Write("", "v", 1), api.ErrEmptyKey}, {uo, api.ErrBadConfig}, {s.Down(-1), api.ErrBadConfig}} {
		ok(t, b[0] == b[1], "got %v want %v", b[0], b[1])
	}
	_, _, e := run(s, "D 0; W k 1 a; W k 2 b")
	ok(t, e == nil, "%v", e)
	ok(t, s.Write("k", "c", 3) == api.ErrHintOverflow, "want overflow")
	ok(t, st(s, "k", 0) == "-,b@2,b@2,h1,h2", "overflow left a trace: %s", st(s, "k", 0))
	a, _, _ := s.Up(0)
	ok(t, a == 2 && st(s, "k", 0) == "b@2,b@2,b@2", "recovery %d %s", a, st(s, "k", 0))
	ok(t, s.Write("q", "z", 1) == nil, "system unusable after reject")
}
func hammer(t *testing.T, s *api.System, g, W int, wg *sync.WaitGroup) {
	defer wg.Done()
	k := "key" + strconv.Itoa(g)
	var last int64
	for j := 1; j <= W; j++ {
		if v := s.Get(k)[0].Ver; v < last {
			t.Errorf("regress key%d %d->%d", g, last, v)
			return
		} else {
			last = s.Get(k)[0].Ver
		}
		if e := s.Write(k, "v"+strconv.Itoa(j), int64(g*1000+j)); e != nil {
			t.Error(e)
			return
		}
	}
}
func TestConcurrentConvergence(t *testing.T) {
	const G, W = 32, 20
	s, _ := api.New(3, G*W+1)
	_ = s.Down(1)
	var wg sync.WaitGroup
	for g := 0; g < G; g++ {
		wg.Add(1)
		go hammer(t, s, g, W, &wg)
	}
	wg.Wait()
	h := len(s.Hints(1))
	a, sk, e := s.Up(1)
	ok(t, h == G*W && e == nil && a == G*W && sk == 0, "hints %d Up %d,%d,%v", h, a, sk, e)
	for g := 0; g < G; g++ {
		w := "v" + strconv.Itoa(W) + "@" + strconv.Itoa(g*1000+W)
		ok(t, st(s, "key"+strconv.Itoa(g), 1) == w+","+w+","+w, "key%d = %s", g, st(s, "key"+strconv.Itoa(g), 1))
	}
}
