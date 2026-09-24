package api_test

import "reflect"
import "strconv"
import "sync"
import "testing"
import "ontology/api"

type C = api.Change
type O = api.Out

func ins(id, k string, t int64) C { return C{Op: '+', ID: id, Key: k, T: t} }
func del(id string) C             { return C{Op: '-', ID: id} }
func step(t *testing.T, v *api.API, l map[string]O, c C) []O {
	o, err := v.Apply([]C{c})
	if err != nil {
		t.Fatal(err)
	}
	delete(l, c.ID)
	if c.Op == '+' {
		l[c.ID] = O{Op: '+', Key: c.Key, ID: c.ID, T: c.T}
	}
	return o
}
func naive(l map[string]O) map[string]O {
	m := map[string]O{}
	for _, r := range l {
		if c, ok := m[r.Key]; !ok || r.T < c.T || r.T == c.T && r.ID < c.ID {
			m[r.Key] = r
		}
	}
	return m
}
func sign(os []O) (s string) {
	for _, e := range os {
		s += string(e.Op) + e.ID
	}
	return
}
func gen(n int, sd uint64) []C {
	x, nid := sd, 0
	a, d, cs := []string{}, []string{}, []C{}
	for len(cs) < n {
		x = x*6364136223846793005 + 1442695040888963407
		r, id := x%5, ""
		switch {
		case r >= 3 && len(a) > 0:
			id, a = a[0], append(a[:0:0], a[1:]...)
			d, cs = append(d, id), append(cs, del(id))
			continue
		case r == 0 && len(d) > 0:
			i := x % uint64(len(d))
			id, d = d[i], append(d[:i:i], d[i+1:]...)
		default:
			id, nid = "id"+strconv.Itoa(nid), nid+1
		}
		cs = append(cs, ins(id, "k"+strconv.Itoa(int(x%5)), int64(x%201)-100))
		a = append(a, id)
	}
	return cs
}
func randInv(t *testing.T) {
	for sd := uint64(1); sd <= 4; sd++ {
		v := api.New(0)
		l, cur := map[string]O{}, map[string]O{}
		for i, c := range gen(160, sd*7919+1) {
			before := naive(l)
			o := step(t, v, l, c)
			after := naive(l)
			minBad := len(o) > 2 || (len(o) == 0) != reflect.DeepEqual(before, after)
			for _, e := range o {
				g, ok := cur[e.Key]
				if e.Op == '-' && (!ok || g.ID != e.ID || g.T != e.T) || e.Op == '+' && ok {
					t.Fatalf("seed %d prefix broken at %d", sd, i)
				}
				if e.Op == '-' {
					delete(cur, e.Key)
				} else {
					cur[e.Key] = e
				}
			}
			if minBad || !reflect.DeepEqual(v.View(), after) || !reflect.DeepEqual(cur, after) {
				t.Fatalf("seed %d invariant broken at %d", sd, i)
			}
		}
	}
}
func TestRandomViewMatchesBatch(t *testing.T) { randInv(t) }
func TestOutputMinimal(t *testing.T)          { randInv(t) }
func TestLogPrefixes(t *testing.T)            { randInv(t) }
func TestNineStepGolden(t *testing.T) {
	s := []C{ins("a", "k", 5), ins("b", "k", 8), ins("p", "k", 3), ins("m", "k", 3),
		del("m"), ins("e", "k", 1), del("b"), del("e"), del("p")}
	w := []string{"+a", "", "-a+p", "-p+m", "-m+p", "-p+e", "", "-e+p", "-p+a"}
	f := []string{"a", "a", "p", "m", "p", "e", "e", "p", "a"}
	v := api.New(0)
	for i, c := range s {
		o, err := v.Apply([]C{c})
		if err != nil || sign(o) != w[i] || v.View()["k"].ID != f[i] {
			t.Fatalf("step %d %s want %s/%s", i+1, sign(o), w[i], f[i])
		}
	}
}
func TestRejectedBatchAtomicity(t *testing.T) {
	A := []C{ins("a", "k", 1)}
	cases := []struct {
		batch []C
		max   int
		want  error
	}{
		{[]C{ins("", "k", 1)}, 0, api.ErrInvalidChange},
		{[]C{ins("b", "q", 1), ins("a", "k", 9)}, 0, api.ErrIDExists},
		{[]C{del("ghost")}, 0, api.ErrIDMissing},
		{[]C{del("a"), ins("b", "q", 1), ins("c", "r", 1)}, 1, api.ErrRowLimit},
	}
	for ti, tc := range cases {
		v := api.New(tc.max)
		_, _ = v.Apply(A) // 每例都先种入同一合法行 A
		snap := v.View()
		_, err := v.Apply(tc.batch)
		if err != tc.want || !reflect.DeepEqual(snap, v.View()) {
			t.Fatalf("case %d not atomic: want=%v got=%v", ti, tc.want, err)
		}
	}
}
func TestConcurrentView(t *testing.T) {
	v, l := api.New(0), map[string]O{}
	for _, c := range gen(120, 99) {
		step(t, v, l, c)
	}
	ref := v.View()
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if !reflect.DeepEqual(v.View(), ref) || v.SelfCheck() != nil {
					t.Errorf("concurrent read diverged")
				}
			}
		}()
	}
	wg.Wait()
}
func TestSelfCheckBuiltins(t *testing.T) {
	if err := api.New(0).SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
