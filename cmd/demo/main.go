// Command demo exercises the ttlcache scenarios with an injected
// logical clock and prints one OK/FAIL line per check.
package main

import (
	"errors"
	"fmt"

	"ontology/internal/ttlcache"
)

var t int64 // logical clock, advanced manually

func now() int64 { return t }

func check(name string, cond bool) {
	if cond {
		fmt.Println("OK  ", name)
	} else {
		fmt.Println("FAIL", name)
	}
}

func must(c *ttlcache.Cache, err error) *ttlcache.Cache {
	if err != nil {
		panic(err)
	}
	return c
}

func scenarioEvictExpired() {
	t = 0
	c := must(ttlcache.New(2, now))
	_ = c.Put("a", "1", 10)
	_ = c.Put("b", "2", 100)
	t = 15
	_ = c.Put("c", "3", 100)
	_, aOK := c.Get("a")
	_, bOK := c.Get("b")
	check("expired a evicted, b kept", !aOK && bOK && c.Len() == 2)
}

func scenarioEvictLRU() {
	t = 0
	c := must(ttlcache.New(2, now))
	_ = c.Put("a", "1", 100)
	_ = c.Put("b", "2", 100)
	c.Get("a")
	_ = c.Put("c", "3", 100)
	_, bOK := c.Get("b")
	check("nothing expired: LRU b evicted", !bOK)
}

func scenarioEvictEarliestWritten() {
	t = 0
	c := must(ttlcache.New(2, now))
	_ = c.Put("a", "1", 10) // written t=0
	t = 1
	_ = c.Put("b", "2", 30) // written t=1
	t = 9
	c.Get("b") // b becomes MRU
	t = 31     // both expired
	_ = c.Put("c", "3", 100)
	check("both expired: earliest-written a evicted", !c.Delete("a") && c.Delete("b"))
}

func scenarioNoTTLRefresh() {
	t = 0
	c := must(ttlcache.New(2, now))
	_ = c.Put("a", "old", 10)
	t = 5
	_ = c.Put("a", "new", 100) // TTL not refreshed
	t = 9
	v, ok9 := c.Get("a")
	t = 10
	_, ok10 := c.Get("a")
	check("re-put keeps old TTL", ok9 && v == "new" && !ok10)
}

func scenarioErrors() {
	_, errCap := ttlcache.New(0, now)
	c := must(ttlcache.New(1, now))
	errTTL := c.Put("k", "v", 0)
	check("sentinel errors", errors.Is(errCap, ttlcache.ErrInvalidCapacity) &&
		errors.Is(errTTL, ttlcache.ErrInvalidTTL))
}

func main() {
	scenarioEvictExpired()
	scenarioEvictLRU()
	scenarioEvictEarliestWritten()
	scenarioNoTTLRefresh()
	scenarioErrors()
	fmt.Println("demo done")
}
