// Command demo is the no-argument, offline acceptance demo for the 2-3 tree.
package main

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"slices"
	"sync"

	"ontology/internal/api"
	"ontology/internal/btree"
)

var failed bool

func judge(name string, ok bool, detail ...any) {
	if ok {
		fmt.Printf("OK %s %v\n", name, detail)
	} else {
		failed = true
		fmt.Printf("FAIL %s %v\n", name, detail)
	}
}
func main() {
	// 1-2. Seven ascending inserts: per-step cost/root/inorder; step-7 root [4].
	t, _ := btree.New(1000)
	wantRoots := [][]int{{1}, {1, 2}, {2}, {2}, {2, 4}, {2, 4}, {4}}
	costs, roots, inorders, stepOK := []int{}, [][]int{}, [][]int{}, true
	for k := 1; k <= 7; k++ {
		c, err := t.Insert(k)
		r, _ := t.Snapshot()
		prefix := make([]int, k)
		for v := range k {
			prefix[v] = v + 1
		}
		costs = append(costs, c)
		roots = append(roots, r)
		inorders = append(inorders, t.OrderedKeys())
		stepOK = stepOK && err == nil && c == []int{0, 0, 1, 0, 1, 0, 2}[k-1] &&
			slices.Equal(r, wantRoots[k-1]) && slices.Equal(inorders[k-1], prefix)
	}
	r7, _ := t.Snapshot()
	judge("seven steps cost/root/inorder", stepOK, costs, roots, inorders)
	judge("step7 root split cascaded to [4]", slices.Equal(r7, []int{4}))

	// 3. Delete(3): two merges, root [4,6], leaves [1,2] [5] [7].
	merges, derr := t.Delete(3)
	root, leaves := t.Snapshot()
	judge("Delete(3) shape and merge count", derr == nil && merges == 2 &&
		slices.Equal(root, []int{4, 6}) && len(leaves) == 3, merges, root, leaves)

	// 4-5. Search equals binary search; OrderedKeys strictly ascending/unique.
	ks := t.OrderedKeys()
	searchOK := true
	for _, k := range append([]int{0, 3, 8}, ks...) {
		_, want := slices.BinarySearch(ks, k)
		searchOK = searchOK && t.Search(k) == want
	}
	judge("Search == binary search", searchOK)
	judge("OrderedKeys strictly ascending/unique", slices.IsSorted(ks) && len(ks) == 6)

	// 6-7. Three distinct decidable errors via the public api; no trace left.
	full, _ := api.New(1)
	full.Insert(1)
	_, eDup := full.Insert(1)
	_, eMiss := full.Delete(9)
	_, eCap := full.Insert(2)
	distinct := errors.Is(eDup, api.ErrDuplicateKey) && errors.Is(eMiss, api.ErrKeyNotFound) &&
		errors.Is(eCap, api.ErrTreeFull) && eDup != eMiss && eDup != eCap && eMiss != eCap
	judge("three distinct sentinel errors", distinct, eDup, eMiss, eCap)
	judge("rejected ops leave no trace", full.Height() == 1 &&
		slices.Equal(full.OrderedKeys(), []int{1}), full.OrderedKeys())

	// 8. Logarithmic shape: height stays <=40 while m grows 100 -> 10000.
	logOK, hs := true, []int{}
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		big, _ := api.New(m)
		for k := range m { // (k*7919)%m is a permutation, so every insert wins
			big.Insert((k * 7919) % m)
		}
		h := big.Height()
		hs, logOK = append(hs, h), logOK && big.Search(m/2) && h <= 40
	}
	judge("height bounded by ~log3(m) (<=40)", logOK, hs)

	// 9. Concurrent disjoint inserts with concurrent searches stay consistent.
	const G, P = 16, 64
	ct, _ := api.New(G * P)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for g := range G {
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			for j := range P {
				ct.Insert(g*P + j)
			}
		}()
		go func() {
			defer wg.Done()
			<-start
			for j := range P {
				for !ct.Search(g*P + j) {
					runtime.Gosched()
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	final := ct.OrderedKeys()
	judge("concurrent inserts/searches consistent",
		len(final) == G*P && slices.IsSorted(final) && final[0] == 0 && final[len(final)-1] == G*P-1, len(final))

	// 10. The built-in self-check passes (empty tree and a populated one).
	empty, _ := api.New(10)
	pop, _ := api.New(1000)
	for k := 1; k <= 7; k++ {
		pop.Insert(k)
	}
	judge("SelfCheck passes", empty.SelfCheck() == nil && pop.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
