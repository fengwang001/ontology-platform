// Command roadnet-demo replays the worked example from the roadnet
// contract and prints every query result, so the service behaviour
// can be verified locally with a single command.
package main

import (
	"fmt"

	"ontology/roadnet"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func query(nt *roadnet.Net, s int, t0 int64, g int, ver *int64) {
	verStr := "current"
	if ver != nil {
		verStr = fmt.Sprintf("v%d", *ver)
	}
	res, err := nt.EarliestArrival(s, t0, g, ver)
	if err != nil {
		fmt.Printf("EarliestArrival(%d,%d,%d,%s) -> %v\n", s, t0, g, verStr, err)
		return
	}
	fmt.Printf("EarliestArrival(%d,%d,%d,%s) -> arrival=%d legs=%v popped=%d\n",
		s, t0, g, verStr, res.Arrival, res.Legs, res.Popped)
}

func main() {
	nt, err := roadnet.NewNet(4, 100)
	must(err)
	add := func(u, v int, p []roadnet.Segment) {
		id, err := nt.AddEdge(u, v, p)
		must(err)
		fmt.Printf("AddEdge(%d,%d,%v) -> id=%d version=%d\n", u, v, p, id, nt.Version())
	}
	add(0, 1, []roadnet.Segment{{Offset: 0, Cost: 5}})
	add(1, 3, []roadnet.Segment{{Offset: 0, Cost: 10}, {Offset: 20, Cost: 2}})
	add(0, 2, []roadnet.Segment{{Offset: 0, Cost: 3}})
	add(2, 3, []roadnet.Segment{{Offset: 0, Cost: 4}, {Offset: 10, Cost: -1}, {Offset: 30, Cost: 1}})

	query(nt, 0, 0, 3, nil) // 7, [(3,0,3),(4,3,7)]
	query(nt, 0, 8, 3, nil) // 22, [(1,8,13),(2,20,22)]

	add(0, 3, []roadnet.Segment{{Offset: 0, Cost: 7}}) // edge 5
	query(nt, 0, 0, 3, nil)                            // 7, [(5,0,7)]

	must(nt.Announce(5, 5, []roadnet.Segment{{Offset: 0, Cost: -1}}))
	fmt.Printf("Announce(5,5,[(0,-1)]) -> version=%d\n", nt.Version())
	query(nt, 0, 6, 3, nil) // 13, [(3,6,9),(4,9,13)]

	must(nt.Advance(10))
	if err := nt.Announce(3, 9, []roadnet.Segment{{Offset: 0, Cost: 1}}); err != nil {
		fmt.Printf("Announce(3,9,...) rejected: %v\n", err)
	}
	must(nt.Announce(3, 10, []roadnet.Segment{{Offset: 0, Cost: 1}}))
	must(nt.Announce(3, 10, []roadnet.Segment{{Offset: 0, Cost: 2}}))
	fmt.Printf("Announce(3,10,...) append+replace -> version=%d\n", nt.Version())
	query(nt, 0, 12, 3, nil) // 22, [(1,12,17),(2,20,22)]

	ver := func(v int64) *int64 { return &v }
	query(nt, 0, 10, 2, nil)    // 12, [(3,10,12)]
	query(nt, 0, 10, 2, ver(7)) // 11, [(3,10,11)]
	query(nt, 0, 10, 2, ver(6)) // 13, [(3,10,13)]
	query(nt, 0, 10, 2, ver(9)) // version not yet produced
}
