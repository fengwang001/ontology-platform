// Command demo runs smoke checks against the union-find packages.
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"

	"ontology/check"
	"ontology/id"
	"ontology/uf"
)

var failed bool

func ok(name string, cond bool) {
	if !cond {
		failed = true
		fmt.Println("FAIL " + name)
		return
	}
	fmt.Println("OK " + name)
}

func main() {
	u := uf.New(5)
	ok("count starts at n", u.Count() == 5)
	merged, _ := u.Union(0, 1)
	again, _ := u.Union(0, 1)
	ok("union reports real merges", merged && !again)
	u.Union(1, 2)
	conn, _ := u.Connected(0, 2)
	ok("transitivity and count", conn && u.Count() == 3)
	_, err := u.Find(-1)
	ok("bad index is ErrBadIndex", errors.Is(err, id.ErrBadIndex))
	empty, one := uf.New(0), uf.New(1)
	self, _ := one.Connected(0, 0)
	ok("edge cases n=0 and n=1", empty.Count() == 0 && self)
	chain := uf.New(1 << 16)
	for i := 0; i < 1<<16-1; i++ {
		chain.Union(i+1, i)
	}
	chain.Find(0)
	ok("chain find hops <= 16", chain.LastFindHops() <= 16)
	rnd, ref := uf.New(50), check.NewNaive(50)
	r := rand.New(rand.NewSource(1))
	agree := true
	for i := 0; i < 200; i++ {
		x, y := r.Intn(50), r.Intn(50)
		rnd.Union(x, y)
		ref.Union(x, y)
		got, _ := rnd.Connected(x, y)
		agree = agree && got == ref.Connected(x, y)
	}
	ok("matches naive BFS reference", agree && rnd.Count() == ref.Count())
	if failed {
		fmt.Println("FAIL total")
		os.Exit(1)
	}
	fmt.Println("OK total 7/7")
}
