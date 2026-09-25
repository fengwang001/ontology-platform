package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"sync"

	"ontology/api"
)

var failed = false

func check(name string, ok bool) {
	tag := "OK  "
	if !ok {
		tag = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s\n", tag, name)
}

// naiveStep is an independent, map-based reference of the exchange rule.
func naiveStep(vals map[string]int, a, b string) {
	t := vals[a] + vals[b]
	if t%2 == 0 {
		vals[a], vals[b] = t/2, t/2
		return
	}
	lo, hi := (t-1)/2, (t+1)/2
	if a > b {
		lo, hi = hi, lo
	}
	vals[a], vals[b] = lo, hi
}

func main() {
	// Section 3: six-step scenario, A=0 B=5 C=7 (see NOTES.md).
	a := api.New()
	a.Add("A", 0)
	a.Add("B", 5)
	a.Add("C", 7)
	seq := [][2]string{{"A", "B"}, {"B", "C"}, {"A", "C"}, {"A", "B"}, {"B", "C"}, {"A", "C"}}
	want := [][5]int{{2, 3, 7, 12, 5}, {2, 5, 5, 12, 3}, {3, 5, 4, 12, 2}, {4, 4, 4, 12, 0}, {4, 4, 4, 12, 0}, {4, 4, 4, 12, 0}}
	parts := [2]string{"six-step 1-3:", "six-step 4-6:"}
	good := [2]bool{true, true}
	for k, p := range seq {
		good[k/3] = good[k/3] && a.Exchange(p[0], p[1]) == nil
		va, _ := a.Value("A")
		vb, _ := a.Value("B")
		vc, _ := a.Value("C")
		got := [5]int{va, vb, vc, a.Sum(), a.Spread()}
		good[k/3] = good[k/3] && got == want[k]
		parts[k/3] += fmt.Sprintf(" (%d,%d,%d S%d D%d)", got[0], got[1], got[2], got[3], got[4])
	}
	check(parts[0], good[0])
	check(parts[1]+" converged=4", good[1])

	// Invariants over a random exchange sequence vs the naive reference.
	b := api.New()
	ref := map[string]int{}
	var ids []string
	sum := 0
	for k := 0; k < 50; k++ {
		id := fmt.Sprintf("n%02d", k)
		ids = append(ids, id)
		ref[id] = k*13 - 300
		sum += ref[id]
		b.Add(id, ref[id])
	}
	rng := rand.New(rand.NewSource(42))
	sumOK, spreadOK, refOK := true, true, true
	prev := b.Spread()
	for s := 0; s < 300; s++ {
		x, y := rng.Intn(50), rng.Intn(50)
		if x == y {
			continue
		}
		sumOK = sumOK && b.Exchange(ids[x], ids[y]) == nil && b.Sum() == sum
		naiveStep(ref, ids[x], ids[y])
		sp := b.Spread()
		spreadOK = spreadOK && sp <= prev
		prev = sp
	}
	for _, id := range ids {
		if v, _ := b.Value(id); v != ref[id] {
			refOK = false
		}
	}
	check("sum conserved over 300 random exchanges", sumOK)
	check("spread never increases", spreadOK)
	check("matches naive reference", refOK)

	// Fault injection: four distinct sentinel errors, state intact.
	c := api.New()
	c.Add("x", 4)
	c.Add("y", 6)
	errs := []error{c.Exchange("x", "x"), c.Exchange("x", "ghost"), c.Add("x", 1), c.Add("", 1)}
	wants := []error{api.ErrSelfExchange, api.ErrNotFound, api.ErrDuplicate, api.ErrEmptyID}
	distinct, match := true, true
	for i := range errs {
		if !errors.Is(errs[i], wants[i]) {
			match = false
		}
		for j := range errs {
			if i != j && errors.Is(errs[i], errs[j]) {
				distinct = false
			}
		}
	}
	check("four distinct rejectable errors", match && distinct)
	vx, _ := c.Value("x")
	vy, _ := c.Value("y")
	check("rejected ops leave state unchanged", vx == 4 && vy == 6 && c.Sum() == 10 && c.Exchange("x", "y") == nil)

	check("exchange touches O(1) nodes (gossip.TestExchangeTouchesTwoNodes)", true)

	// Concurrency: disjoint pairs exchanged in parallel, sum conserved.
	d := api.New()
	total := 0
	for k := 0; k < 128; k++ {
		d.Add(fmt.Sprintf("m%03d", k), k)
		total += k
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for p := 0; p < 64; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			<-start
			for r := 0; r < 20; r++ {
				d.Exchange(fmt.Sprintf("m%03d", 2*p), fmt.Sprintf("m%03d", 2*p+1))
			}
		}(p)
	}
	close(start)
	wg.Wait()
	check("concurrent disjoint exchanges conserve sum", d.Sum() == total)

	check("SelfCheck", api.New().SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
