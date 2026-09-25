package cluster

import "fmt"
import "testing"

import "ontology/rnd"

var tab = map[string]map[string]uint64{
	"k1": {"A": 10, "B": 7, "C": 3, "D": 11},
	"k2": {"A": 4, "B": 9, "C": 5, "D": 3},
	"k3": {"A": 6, "B": 6, "C": 2, "D": 6},
	"k4": {"A": 8, "B": 2, "C": 9, "D": 7},
}

func mkk(p string, n int) []string {
	ks := make([]string, n)
	for i := range ks {
		ks[i] = fmt.Sprintf("%s-%04d", p, i)
	}
	return ks
}

func TestSixStepsTable(t *testing.T) {
	c := New(func(k, n string) uint64 { return tab[k][n] })
	for _, id := range []string{"A", "B", "C"} {
		c.AddNode(id)
	}
	want := [...]map[string]string{
		{"k1": "A"},
		{"k1": "A", "k2": "B"},
		{"k1": "A", "k2": "B", "k3": "A"},
		{"k1": "A", "k2": "B", "k3": "A", "k4": "C"},
		{"k1": "D", "k2": "B", "k3": "A", "k4": "C"},
		{"k1": "D", "k2": "B", "k3": "B", "k4": "C"},
	}
	check := func(step int) {
		t.Helper()
		for k, w := range want[step] {
			if got, _ := c.Owner(k); got != w {
				t.Fatalf("step %d: %s=%s want %s", step+1, k, got, w)
			}
		}
	}
	for i, k := range []string{"k1", "k2", "k3", "k4"} {
		c.Owner(k)
		check(i)
	}
	c.AddNode("D")
	check(4)
	c.RemoveNode("A")
	check(5)
}

func TestNaiveConsistencyAfterOps(t *testing.T) {
	c := New()
	check := func() {
		t.Helper()
		for _, k := range c.Keys() {
			want, _ := rnd.Pick(k, c.Nodes(), rnd.Weight)
			if got, _ := c.Owner(k); got != want {
				t.Errorf("%s=%s naive=%s", k, got, want)
			}
		}
	}
	for _, id := range []string{"n0", "n1", "n2"} {
		c.AddNode(id)
	}
	for _, k := range mkk("a", 200) {
		c.Owner(k)
	}
	for _, op := range []func(){
		func() { c.AddNode("n3") },
		func() {
			for _, k := range mkk("b", 100) {
				c.Owner(k)
			}
		},
		func() { c.AddNode("n4") },
		func() { c.RemoveNode("n1") },
		func() {
			c.RemoveNode("n3")
			c.AddNode("n6")
		},
		func() { c.AddNode("n5") },
		func() { c.RemoveNode("n0") },
	} {
		op()
		check()
	}
}

func TestMinimalRelocation(t *testing.T) {
	c := New()
	for _, id := range []string{"n1", "n2", "n3", "n4"} {
		c.AddNode(id)
	}
	ks := mkk("k", 300)
	before := map[string]string{}
	for _, k := range ks {
		before[k], _ = c.Owner(k)
	}
	c.AddNode("new")
	for k, old := range before {
		if got, _ := c.Owner(k); got != old && got != "new" {
			t.Errorf("add moved %s %s->%s", k, old, got)
		}
	}
	after := map[string]string{}
	for _, k := range ks {
		after[k], _ = c.Owner(k)
	}
	c.RemoveNode("n2")
	for k, old := range after {
		if got, _ := c.Owner(k); got != old && old != "n2" {
			t.Errorf("remove moved %s although %s stayed", k, old)
		}
	}
}

func TestRemoveRelocationCount(t *testing.T) {
	const fixed = 3
	for _, m := range []int{100, 1000, 10000} {
		sp := map[string]bool{"s0": true, "s1": true, "s2": true}
		base := map[string]uint64{"A": 1, "B": 50, "C": 2}
		c := New(func(k, n string) uint64 {
			if n == "A" && sp[k] {
				return 100
			}
			return base[n]
		})
		c.AddNode("A")
		c.AddNode("B")
		c.AddNode("C")
		ks := append(mkk("k", m-fixed), "s0", "s1", "s2")
		for _, k := range ks {
			c.Owner(k)
		}
		if n := len(c.owned["A"]); n != fixed {
			t.Fatalf("setup: A owns %d keys, want %d", n, fixed)
		}
		if err := c.RemoveNode("A"); err != nil {
			t.Fatal(err)
		}
		if c.lastRemoveScanned != fixed {
			t.Errorf("m=%d: scanned %d, want %d (must not grow with m)",
				m, c.lastRemoveScanned, fixed)
		}
	}
}
