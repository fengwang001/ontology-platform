package api

import (
	"errors"
	"fmt"
	"math/rand"
	"ontology/fk"
	"sort"
	"strings"
	"sync"
	"testing"
)

// canon encodes a view as "p1,p2|c1=p1,c2=p2" with all parts sorted.
func canon(ps []string, cs map[string]string) string {
	ks := make([]string, 0, len(cs))
	for k, v := range cs {
		ks = append(ks, k+"="+v)
	}
	sort.Strings(ks)
	return strings.Join(ps, ",") + "|" + strings.Join(ks, ",")
}
func view(d *DB) string { return canon(d.ViewP(), d.ViewC()) }

// model is an independent naive replay yielding identical verdicts/views.
type model struct {
	p map[string]bool
	c map[string]string
}

func (m *model) apply(s scStep) error {
	switch s.op {
	case kPIns:
		m.p[s.a] = true
	case kPDel:
		if !m.p[s.a] {
			return fk.ErrParentNotFound
		}
		for _, q := range m.c {
			if q == s.a {
				return fk.ErrParentReferenced
			}
		}
		delete(m.p, s.a)
	case kCIns:
		if !m.p[s.b] {
			return fk.ErrOrphanChild
		}
		if _, ok := m.c[s.a]; !ok {
			m.c[s.a] = s.b
		}
	case kCDel:
		if _, ok := m.c[s.a]; !ok {
			return fk.ErrChildNotFound
		}
		delete(m.c, s.a)
	}
	return nil
}
func (m *model) canon() string {
	ps := make([]string, 0, len(m.p))
	for q := range m.p {
		ps = append(ps, q)
	}
	sort.Strings(ps)
	return canon(ps, m.c)
}

// TestThirteenSteps pins every NOTES.md row and the four invariant checks.
func TestThirteenSteps(t *testing.T) {
	want := []string{"|", "p1|", "p1|k1=p1", "p1|k1=p1", "p1|k1=p1", "p1|k1=p1,k3=p1", "p1|k3=p1", "p1|k3=p1", "p1|", "|", "|", "p1|", "p1|k3=p1"}
	d := New()
	for i, s := range thirteen() {
		if err := d.run(s); !errors.Is(err, s.want) || view(d) != want[i] {
			t.Fatalf("step %d: err=%v view=%q want %q", i+1, err, view(d), want[i])
		}
	}
	if err := d.SelfCheck(); err != nil {
		t.Fatal(err)
	}
	if len(map[error]struct{}{fk.ErrOrphanChild: {}, fk.ErrParentReferenced: {}, fk.ErrParentNotFound: {}, fk.ErrChildNotFound: {}}) != 4 {
		t.Fatal("the four sentinel errors must be pairwise distinct")
	}
}

func TestRejectionLeavesNoTrace(t *testing.T) {
	cases := [][]scStep{
		{{kPIns, "p1", "", nil}, {kCIns, "k", "g", nil}},
		{{kPIns, "p1", "", nil}, {kCIns, "k", "p1", nil}, {kPDel, "p1", "", nil}},
		{{kPDel, "g", "", nil}},
		{{kCDel, "g", "", nil}},
	}
	wants := []error{fk.ErrOrphanChild, fk.ErrParentReferenced, fk.ErrParentNotFound, fk.ErrChildNotFound}
	for i, steps := range cases {
		d := New()
		for _, s := range steps[:len(steps)-1] {
			_ = d.run(s)
		}
		before := view(d)
		if !errors.Is(d.run(steps[len(steps)-1]), wants[i]) || view(d) != before {
			t.Fatalf("rejection %v left a trace: %q", wants[i], view(d))
		}
	}
	d := New()
	_ = d.PIns("p1")
	if err := d.CIns("k", "g"); !errors.Is(err, fk.ErrOrphanChild) {
		t.Fatal(err)
	}
	if err := d.CIns("k", "p1"); err != nil || d.ViewC()["k"] != "p1" {
		t.Fatal("re-delivered child after parent arrives must succeed")
	}
}

func TestRandomDifferential(t *testing.T) {
	for seed := int64(1); seed <= 20; seed++ {
		rng := rand.New(rand.NewSource(seed))
		d, m := New(), &model{map[string]bool{}, map[string]string{}}
		for n := 0; n < 150; n++ {
			s := scStep{op: rng.Intn(4), a: "c" + fmt.Sprint(rng.Intn(4)), b: "p" + fmt.Sprint(rng.Intn(5))}
			if got, want := d.run(s), m.apply(s); !errors.Is(got, want) {
				t.Fatalf("seed %d step %d: got %v want %v", seed, n, got, want)
			}
			if view(d) != m.canon() || d.checkInvariants() != nil {
				t.Fatalf("seed %d step %d: invariant drift: %v", seed, n, d.checkInvariants())
			}
		}
	}
}

func TestConcurrentReads(t *testing.T) {
	d := New()
	for i := 0; i < 8; i++ {
		_ = d.PIns(fmt.Sprintf("p%d", i))
		_ = d.CIns(fmt.Sprintf("c%d", i), fmt.Sprintf("p%d", i))
	}
	const N = 16
	start, res := make(chan struct{}), make([]string, N)
	var wg sync.WaitGroup
	for g := range res {
		wg.Add(1)
		go func(g int) { defer wg.Done(); <-start; res[g] = view(d) }(g)
	}
	close(start)
	wg.Wait()
	for g := 1; g < N; g++ {
		if res[0] != res[g] {
			t.Fatalf("goroutine %d saw a different view", g)
		}
	}
}
