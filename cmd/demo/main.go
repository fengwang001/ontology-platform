// Command demo exercises the passive-side handshake state machine through its
// public API and prints one OK/FAIL line per guarantee. Exit code is 0 only
// when every check passes. No args, no network.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/hs"
)

var failures int

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK  " + name)
	} else {
		fmt.Println("FAIL " + name)
		failures++
	}
}

func main() {
	a := api.New()
	// Eight canonical operations: SYN returns (serverISN,ack); a successful
	// ACK returns (clientISN,serverISN); the duplicate-ACK no-op returns zeros.
	type st struct {
		k         byte
		src       string
		v, r1, r2 int64
		e         error
	}
	steps := []st{
		{'s', "A", 1000, 0, 1001, nil},
		{'s', "A", 1000, 0, 1001, nil}, // duplicate SYN
		{'s', "B", 2000, 1, 2001, nil},
		{'a', "A", 1, 1000, 0, nil},
		{'a', "A", 1, 0, 0, nil}, // duplicate ACK no-op
		{'s', "C", 3000, 2, 3001, nil},
		{'s', "C", 9999, 3, 10000, nil},
		{'a', "D", 999, 0, 0, api.ErrHalfOpen},
	}
	eightOK, dupSyn, dupAck, orphan := true, true, true, true
	for i, s := range steps {
		var x, y int64
		var err error
		if s.k == 's' {
			x, y, err = a.RecvSYN(s.src, s.v)
		} else {
			x, y, err = a.RecvACK(s.src, s.v)
		}
		if !errors.Is(err, s.e) || x != s.r1 || y != s.r2 {
			eightOK = false
		}
		switch i {
		case 1:
			dupSyn = x == 0 && y == 1001 && err == nil
		case 4:
			dupAck = err == nil && x == 0 && y == 0 && a.Established("A")
		case 7:
			orphan = errors.Is(err, api.ErrHalfOpen)
		}
	}
	probe, _, _ := a.RecvSYN("probe", 0) // reveals nextISN without disturbing A/B/C/D
	check("eight steps: replies and nextISN per step", eightOK && probe == 4)
	check("step2 duplicate SYN identical / step5 duplicate ACK no-op / step8 orphan ErrHalfOpen", dupSyn && dupAck && orphan)

	// Sequence negotiation: SYN-ACK.ack == clientISN+1, completion at serverISN+1.
	b := api.New()
	isn, synAck, e1 := b.RecvSYN("x", 5000)
	cl, sv, e2 := b.RecvACK("x", isn+1)
	check("ack==serverISN+1 completes; SYN-ACK.ack==clientISN+1", e1 == nil && e2 == nil && synAck == 5001 && cl == 5000 && sv == isn)

	// The three rejections are distinct.
	_, _, eb := b.RecvSYN("n", -3)
	_, _, eh := b.RecvACK("ghost", 1)
	c0, _, _ := b.RecvSYN("c", 10)
	_, _, ebad := b.RecvACK("c", c0+5)
	distinct := errors.Is(eb, api.ErrBadSeq) && errors.Is(eh, api.ErrHalfOpen) && errors.Is(ebad, api.ErrBadAck) &&
		!errors.Is(eb, ebad) && !errors.Is(eh, eb)
	check("ErrBadSeq / ErrHalfOpen / ErrBadAck distinct and decidable", distinct)

	// Failures leave no trace: nextISN unchanged and server stays usable.
	p, _, _ := b.RecvSYN("d", 20)
	traceOK := p == c0+1
	if cc, ss, err := b.RecvACK("c", c0+1); err != nil || cc != 10 || ss != c0 {
		traceOK = false
	}
	check("rejected ops leave no trace; still usable afterwards", traceOK)

	check("half-open probe is O(1) at h=100..10000", hs.VerifyProbeConstant() == nil)

	// Concurrent handshakes: no crossing, pairwise distinct serverISNs.
	const N = 64
	c := api.New()
	isns := make([]int64, N)
	var wg sync.WaitGroup
	concOK := true
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			src := fmt.Sprintf("g%02d", g)
			is, _, err := c.RecvSYN(src, int64(1000+g))
			if err != nil {
				concOK = false
				return
			}
			isns[g] = is
			if cl2, sv2, err := c.RecvACK(src, is+1); err != nil || cl2 != int64(1000+g) || sv2 != is {
				concOK = false
			}
		}(g)
	}
	wg.Wait()
	seen := map[int64]bool{}
	for g := 0; g < N; g++ {
		if !c.Established(fmt.Sprintf("g%02d", g)) || seen[isns[g]] {
			concOK = false
		}
		seen[isns[g]] = true
	}
	check("concurrent handshakes: all established, pairs match, ISNs unique", concOK)
	check("api.SelfCheck passes", api.SelfCheck() == nil)

	if failures > 0 {
		os.Exit(1)
	}
}
