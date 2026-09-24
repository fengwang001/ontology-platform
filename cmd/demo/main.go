// Command demo exercises the consistent hash ring end to end and prints
// one OK/FAIL verdict line per check. It takes no arguments, uses no
// network, and exits 0 when every check passes.
package main

import (
	"fmt"
	"os"

	"ontology/ring"
)

const (
	keyCount = 100000
	nodes    = 10
	vnodes   = 200
)

var passed, total int

func check(detail string, ok bool) {
	total++
	verdict := "OK  "
	if ok {
		passed++
	} else {
		verdict = "FAIL"
	}
	fmt.Printf("%s %s\n", verdict, detail)
}

func nodeID(i int) string { return fmt.Sprintf("node-%02d", i) }

func genKeys() []string {
	keys := make([]string, keyCount)
	for i := range keys {
		keys[i] = fmt.Sprintf("key-%08d", i)
	}
	return keys
}

func build(n, vn int) *ring.Ring {
	r := ring.New()
	for i := 0; i < n; i++ {
		if err := r.Add(nodeID(i), vn); err != nil {
			fmt.Println("FAIL  build ring:", err)
			os.Exit(1)
		}
	}
	return r
}

func owners(r *ring.Ring, keys []string) map[string]int {
	counts := make(map[string]int)
	for _, k := range keys {
		n, err := r.Locate(k)
		if err != nil {
			fmt.Println("FAIL  locate:", err)
			os.Exit(1)
		}
		counts[n]++
	}
	return counts
}

func maxMin(counts map[string]int) (int, int) {
	min, max := -1, 0
	for _, c := range counts {
		if min < 0 || c < min {
			min = c
		}
		if c > max {
			max = c
		}
	}
	return min, max
}

func main() {
	keys := genKeys()

	// 1. Distribution over 10 nodes x 200 vnodes.
	r := build(nodes, vnodes)
	counts := owners(r, keys)
	min, max := maxMin(counts)
	check(fmt.Sprintf("distribution: %d nodes x %d vnodes, %d keys, max/min ratio %.2f",
		nodes, vnodes, keyCount, float64(max)/float64(min)), min > 0)

	// 2. Move ratio when an 11th node joins.
	before := make([]string, len(keys))
	for i, k := range keys {
		before[i], _ = r.Locate(k)
	}
	if err := r.Add(nodeID(nodes), vnodes); err != nil {
		check(fmt.Sprintf("add node: %v", err), false)
		os.Exit(1)
	}
	moved := 0
	for i, k := range keys {
		now, _ := r.Locate(k)
		if now != before[i] {
			moved++
		}
	}
	ratio := float64(moved) / float64(len(keys))
	check(fmt.Sprintf("add node: moved %.2f%% of keys, theory 1/11=%.2f%%, bound [%.2f%%, %.2f%%]",
		ratio*100, 100.0/11, 100.0/22, 300.0/22),
		ratio >= 1.0/22 && ratio <= 3.0/22)

	// 3. Removing a node only moves the keys it owned.
	victim := nodeID(3)
	beforeRemove := make([]string, len(keys))
	for i, k := range keys {
		beforeRemove[i], _ = r.Locate(k)
	}
	if err := r.Remove(victim); err != nil {
		check(fmt.Sprintf("remove node: %v", err), false)
		os.Exit(1)
	}
	unaffected, violated := 0, 0
	for i, k := range keys {
		now, _ := r.Locate(k)
		if beforeRemove[i] == victim {
			continue
		}
		if now == beforeRemove[i] {
			unaffected++
		} else {
			violated++
		}
	}
	check(fmt.Sprintf("remove node: %d keys unaffected, %d unexpected moves",
		unaffected, violated), violated == 0)

	demoWrap()
	demoBalance(keys)
	demoErrors(keys)

	fmt.Printf("TOTAL: %d/%d checks OK\n", passed, total)
	if passed != total {
		os.Exit(1)
	}
}
