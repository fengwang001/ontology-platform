package api_test

import "errors"
import "fmt"
import "sync"
import "sync/atomic"
import "testing"

import "ontology/api"
import "ontology/rnd"

func keys(p string, n int) []string {
	ks := make([]string, n)
	for i := range ks {
		ks[i] = fmt.Sprintf("%s%04d", p, i)
	}
	return ks
}

func TestAPIOwnerMatchesNaive(t *testing.T) {
	l := api.New()
	ns := make([]string, 8)
	for i := range ns {
		ns[i] = fmt.Sprintf("n%d", i)
		l.AddNode(ns[i])
	}
	ks := keys("rk", 150)
	check := func(stage string) {
		t.Helper()
		for _, k := range ks {
			got, _ := l.Owner(k)
			want, _ := rnd.Pick(k, ns, rnd.Weight)
			if got != want {
				t.Fatalf("%s: %s=%s want %s", stage, k, got, want)
			}
		}
	}
	check("initial")
	l.AddNode("zz-new")
	ns = append(ns, "zz-new")
	check("after add")
	l.RemoveNode(ns[0])
	ns = ns[1:]
	check("after remove")
}

func TestDeterminism(t *testing.T) {
	order := []string{"a", "b", "c", "d", "e"}
	l := api.New()
	for _, id := range order {
		l.AddNode(id)
	}
	first := map[string]string{}
	for _, k := range keys("dk", 80) {
		got, _ := l.Owner(k)
		if again, _ := l.Owner(k); again != got {
			t.Fatalf("%s: %s vs %s", k, again, got)
		}
		first[k] = got
	}
	l2 := api.New()
	for i := len(order) - 1; i >= 0; i-- {
		l2.AddNode(order[i])
	}
	for k, want := range first {
		if got, _ := l2.Owner(k); got != want {
			t.Errorf("order changed %s: %s want %s", k, got, want)
		}
	}
}

func TestSentinelErrors(t *testing.T) {
	l := api.New()
	l.AddNode("x")
	s := []error{api.ErrEmptyNodeID, api.ErrDuplicate, api.ErrNodeMissing, api.ErrEmptyKey}
	if s[0] == s[1] || s[1] == s[2] || s[2] == s[3] || s[0] == s[2] || s[0] == s[3] || s[1] == s[3] {
		t.Fatal("sentinel errors are not pairwise distinct")
	}
	cases := []struct{ err, want error }{
		{l.AddNode(""), api.ErrEmptyNodeID},
		{l.AddNode("x"), api.ErrDuplicate},
		{l.RemoveNode("ghost"), api.ErrNodeMissing},
		{func() error { _, e := l.Owner(""); return e }(), api.ErrEmptyKey},
	}
	for i, c := range cases {
		if !errors.Is(c.err, c.want) {
			t.Errorf("case %d: %v", i, c.err)
		}
	}
}

func TestFailureNoTrace(t *testing.T) {
	l := api.New()
	l.AddNode("x")
	l.Owner("keep")
	for i, op := range []func() error{
		func() error { return l.AddNode("") },
		func() error { return l.AddNode("x") },
		func() error { return l.RemoveNode("ghost") },
	} {
		before, _ := l.Owner("keep")
		if err := op(); err == nil {
			t.Fatalf("case %d succeeded", i)
		}
		after, _ := l.Owner("keep")
		if after != before || l.AddNode(fmt.Sprintf("y%d", i)) != nil {
			t.Fatalf("case %d left a trace or broke usage", i)
		}
	}
}

func TestSelfCheck(t *testing.T) {
	if err := api.New().SelfCheck(); err != nil {
		t.Fatalf("SelfCheck failed: %v", err)
	}
}
func TestConcurrentOwners(t *testing.T) {
	l := api.New()
	for i := 0; i < 8; i++ {
		l.AddNode(fmt.Sprintf("n%d", i))
	}
	ks := keys("ck", 200)
	want := map[string]string{}
	for _, k := range ks {
		want[k], _ = l.Owner(k)
	}
	start, bad := make(chan struct{}), int32(0)
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			for n := 0; n < len(ks); n++ {
				i := (n + g*7) % len(ks)
				if got, _ := l.Owner(ks[i]); got != want[ks[i]] {
					atomic.AddInt32(&bad, 1)
				}
			}
			if l.SelfCheck() != nil {
				atomic.AddInt32(&bad, 1)
			}
		}(g)
	}
	close(start)
	wg.Wait()
	if bad != 0 {
		t.Fatalf("%d concurrency mismatches", bad)
	}
}
