package cluster

import (
	"errors"
	"fmt"
	"maps"
	"math/rand"
	"sync"
	"testing"

	"ontology/rnd"
)

func genKeys(n int) []string {
	r := rand.New(rand.NewSource(42))
	ks := make([]string, n)
	for i := range ks {
		ks[i] = fmt.Sprintf("key-%d-%d", r.Int63(), i)
	}
	return ks
}
func best(k string, ns []string) string { b, _, _ := rnd.Best(k, ns); return b }

func addNodes(c *Cluster, ns ...string) {
	for _, n := range ns {
		c.AddNode(n)
	}
}
func register(c *Cluster, ks []string) map[string]string {
	m := map[string]string{}
	for _, k := range ks {
		m[k], _ = c.Owner(k)
	}
	return m
}
func ok(t *testing.T, cond bool, f string, a ...any) {
	t.Helper()
	if !cond {
		t.Fatalf(f, a...)
	}
}
func TestOwnerMatchesNaiveScan(t *testing.T) { // invariant 1
	c := New()
	addNodes(c, "n0", "n1", "n2", "n3")
	ks := genKeys(50)
	register(c, ks)
	c.RemoveNode("n2")
	c.AddNode("n4")
	for _, k := range ks {
		got, _ := c.Owner(k)
		ok(t, got == best(k, []string{"n0", "n1", "n3", "n4"}), "Owner(%s)=%s", k, got)
	}
	ok(t, c.SelfCheck() == nil, "SelfCheck failed")
}
func TestMinimalMigration(t *testing.T) { // invariant 2
	c := New()
	addNodes(c, "n1", "n2", "n3")
	ks := genKeys(80)
	prev := register(c, ks)
	c.AddNode("n4")
	for _, k := range ks { // add: move iff new weight strictly greater
		got, _ := c.Owner(k)
		move := rnd.Weight(k, "n4") > rnd.Weight(k, prev[k])
		ok(t, (got == "n4") == move, "add: %s wrongly migrated", k)
		prev[k] = got
	}
	c.RemoveNode("n2")
	for _, k := range ks { // remove: only n2-owned keys may move
		got, _ := c.Owner(k)
		want := best(k, []string{"n1", "n3", "n4"})
		ok(t, got == want && (prev[k] == "n2" || got == prev[k]), "remove %s: %s", k, got)
	}
}
func TestDeterministic(t *testing.T) { // invariant 3
	ks := genKeys(40)
	var base map[string]string
	for oi, order := range [][]string{{"n1", "n2", "n3", "n4"}, {"n4", "n2", "n1", "n3"}, {"n3", "n1", "n4", "n2"}} {
		c := New()
		addNodes(c, order...)
		got := register(c, ks)
		ok(t, maps.Equal(got, register(c, ks)), "Owner unstable across calls")
		if oi == 0 {
			base = got
		} else {
			ok(t, maps.Equal(got, base), "ownership depends on add order")
		}
	}
}
func TestRejectedOpsLeaveNoTrace(t *testing.T) { // invariant 4
	c := New()
	c.AddNode("n1")
	c.Owner("k1")
	shot := func() string { c.mu.RLock(); defer c.mu.RUnlock(); return fmt.Sprint(c.nodes, c.owner, c.owned) }
	check := func(do func() error, want error) {
		before := shot()
		err := do()
		ok(t, errors.Is(err, want) && shot() == before, "rejected op err=%v want=%v", err, want)
	}
	check(func() error { return c.AddNode("") }, ErrEmptyNodeID)
	check(func() error { return c.AddNode("n1") }, ErrDuplicateNode)
	check(func() error { return c.RemoveNode("zz") }, ErrNodeNotFound)
	check(func() error { _, e := c.Owner(""); return e }, ErrEmptyKey)
	ok(t, ErrEmptyNodeID != ErrDuplicateNode && ErrDuplicateNode != ErrNodeNotFound, "sentinels not distinct")
	ok(t, c.AddNode("n2") == nil, "unusable after rejection")
}
func TestRemoveInspectsOnlyOwnedKeys(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		c := New()
		addNodes(c, "A", "B")
		var ak, bk []string
		for i := 0; len(ak) < 5 || len(bk) < m-5; i++ {
			k := fmt.Sprintf("bulk-%d", i)
			b := best(k, []string{"A", "B"})
			if b == "A" && len(ak) < 5 {
				ak = append(ak, k)
			}
			if b == "B" && len(bk) < m-5 {
				bk = append(bk, k)
			}
		}
		register(c, append(ak, bk...))
		ok(t, c.RemoveNode("A") == nil, "remove failed")
		ok(t, c.lastRemoveScanned == 5, "m=%d scanned %d want 5", m, c.lastRemoveScanned) // A's count, not m
		for _, k := range append(ak, bk...) {
			o, _ := c.Owner(k)
			ok(t, o == "B", "key %s -> %s want B", k, o)
		}
	}
}
func TestConcurrentOwner(t *testing.T) {
	c := New()
	addNodes(c, "n1", "n2", "n3", "n4")
	ks := genKeys(200)
	serial := register(c, ks)
	const N = 16
	var wg sync.WaitGroup
	start := make(chan struct{})
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ok(t, maps.Equal(register(c, ks), serial), "concurrent Owner differs from serial")
		}()
	}
	close(start)
	wg.Wait()
}
