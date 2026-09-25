// Command demo runs the schema-evolution checks and prints one OK/FAIL per line.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/evol"
)

func main() {
	x := api.New()
	code := 0
	ok := func(name string, cond bool, detail string) {
		if cond {
			fmt.Printf("OK %s %s\n", name, detail)
		} else {
			fmt.Printf("FAIL %s %s\n", name, detail)
			code = 1
		}
	}
	r1, _ := x.Write(1, map[string]int{"a": 9, "b": 7})
	r2, _ := x.Write(2, map[string]int{"a": 5})
	r3, _ := x.Write(3, map[string]int{"a": 1, "c": 2, "d": 3})
	m2, _ := x.Read(r1, 3)
	m4, _ := x.Read(r2, 3)
	m6, _ := x.Read(r3, 2)
	m7, _ := x.Read(r2, 2)
	m8, _ := x.Read(r1, 1)
	ok("step2 Read(R1,3)", fmt.Sprint(m2) == "map[a:9 c:10 d:20]", fmt.Sprint(m2)+" (b absent)")
	ok("step4 Read(R2,3)", m4["c"] == 10, fmt.Sprint(m4)+" c=10(not 30)")
	ok("step6 Read(R3,2)", m6["b"] == 2, fmt.Sprint(m6)+" b=2(not 0)")
	ok("step7 Read(R2,2)", fmt.Sprint(m7) == "map[a:5 b:2 c:10]", fmt.Sprint(m7))
	ok("step8 Read(R1,1)", fmt.Sprint(m8) == "map[a:9 b:7]", fmt.Sprint(m8))
	ok("backward-compatible", m8["a"] == 9 && m8["b"] == 7, "common fields survive")
	ok("frozen-defaults", m2["c"] == 10 && m2["d"] == 20, "c stays 10 even under R=3")
	distinct := errors.Is(err1(x.Write(0, nil)), evol.ErrInvalidWriteVersion) &&
		errors.Is(err1(x.Write(1, map[string]int{"z": 1})), evol.ErrUnknownField) &&
		errors.Is(err2(x.Read(r1, 9)), evol.ErrInvalidReadVersion)
	ok("three-distinct-errors", distinct, "bad W / bad field / bad R")
	n0 := x.Len()
	_, _ = x.Write(9, nil)
	_, _ = x.Read(r1, -1)
	ok("rejection-leaves-no-trace", x.Len() == n0, "Len unchanged after rejects")
	ok("constant-time-defaults+concurrent-reads", x.SelfCheck() == nil && concurrent(x, r1), "m=100..10000 O(1); goroutines agree")
	os.Exit(code)
}

func err1(_ api.Record, err error) error     { return err }
func err2(_ map[string]int, err error) error { return err }

// concurrent hammers one record while interleaving writes of independent
// records; equal R must yield identical maps. No sleep is used.
func concurrent(x *api.API, rec api.Record) bool {
	var wg sync.WaitGroup
	var mu sync.Mutex
	good := true
	refs := map[int]map[string]int{}
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for k := 0; k < 100; k++ {
				if _, err := x.Write(3, map[string]int{"a": i}); err != nil {
					good = false
				}
				r := 1 + k%3
				got, err := x.Read(rec, r)
				mu.Lock()
				if ref, seen := refs[r]; !seen {
					refs[r] = got
				} else if err != nil || fmt.Sprint(got) != fmt.Sprint(ref) {
					good = false
				}
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	return good
}
