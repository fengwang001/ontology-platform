// Command demo exercises every semantic of the trace package and
// prints one OK/FAIL verdict per semantic.
package main

import (
	"errors"
	"fmt"
	"slices"
	"sync"

	"ontology/trace"
)

func gen() func() trace.SpanID {
	n := 0
	return func() trace.SpanID {
		n++
		return trace.SpanID(fmt.Sprintf("id%06d", n))
	}
}

func main() {
	ok := func(name string, cond bool) {
		verdict := "OK"
		if !cond {
			verdict = "FAIL"
		}
		fmt.Printf("%-28s %s\n", name, verdict)
	}

	// 1. Derivation: shared trace ID, fresh span ID, parent link.
	root := trace.NewRoot(gen(), true)
	child := root.Child()
	grand := child.Child()
	ok("1 derivation", root.ParentID() == "" &&
		child.TraceID() == root.TraceID() &&
		child.ParentID() == root.SpanID() &&
		child.SpanID() != root.SpanID() &&
		grand.TraceID() == root.TraceID())

	// 2. Sampling: inherited, never recomputed.
	ok("2 sampling inherited", child.Sampled() == root.Sampled() &&
		grand.Sampled() == root.Sampled())

	// 3. Baggage copy-on-write with alternating same-key writes.
	p := trace.NewRoot(gen(), true)
	p.SetBaggage("k", "p1")
	c := p.Child()
	p.SetBaggage("k", "p2")
	c.SetBaggage("k", "c2")
	pv, _ := p.Baggage("k")
	cv, _ := c.Baggage("k")
	ok("3 baggage copy-on-write", pv == "p2" && cv == "c2")

	// 4. Keys sorted, stable, unique on duplicate set.
	s := trace.NewRoot(gen(), false)
	s.SetBaggage("b", "1")
	s.SetBaggage("a", "1")
	s.SetBaggage("a", "2")
	av, _ := s.Baggage("a")
	ok("4 keys sorted+unique", slices.Equal(s.BaggageKeys(), []string{"a", "b"}) && av == "2")

	// 5. Wire round trip preserves everything; Marshal deterministic.
	w := trace.NewRoot(gen(), true)
	w.SetBaggage("user", "alice")
	wc := w.Child()
	wc.SetBaggage("op", "charge")
	back, err := trace.Parse(wc.Marshal())
	rt := err == nil &&
		back.TraceID() == wc.TraceID() && back.SpanID() == wc.SpanID() &&
		back.ParentID() == wc.ParentID() && back.Sampled() == wc.Sampled() &&
		slices.Equal(back.BaggageKeys(), wc.BaggageKeys())
	bv, _ := back.Baggage("user")
	ov, _ := back.Baggage("op")
	wv, _ := wc.Baggage("user")
	ok("5 wire round trip", rt && bv == wv && ov == "charge" &&
		wc.Marshal() == wc.Marshal())

	// 6. Robust parsing: malformed input -> (nil, ErrMalformed).
	bad := []string{"", "t-s-01", "t-s-p-1", "t-s-p-10", "-s-p-01", "t--p-01", "t-s-p-01;k"}
	robust := true
	for _, wire := range bad {
		span, err := trace.Parse(wire)
		if span != nil || !errors.Is(err, trace.ErrMalformed) {
			robust = false
		}
	}
	plain, err := trace.Parse("t-s-p-01")
	ok("6 parse robust", robust && err == nil && len(plain.BaggageKeys()) == 0)

	// 7. Concurrency: mixed ops, unique child IDs (verify with -race).
	cr := trace.NewRoot(gen(), true)
	cr.SetBaggage("base", "v")
	var wg sync.WaitGroup
	ids := make(chan trace.SpanID, 400)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				cr.SetBaggage(fmt.Sprintf("k%d", i), "x")
				cr.Baggage("base")
				cr.Marshal()
				ids <- cr.Child().SpanID()
			}
		}(i)
	}
	wg.Wait()
	close(ids)
	seen := map[trace.SpanID]bool{}
	dup := false
	for id := range ids {
		if seen[id] {
			dup = true
		}
		seen[id] = true
	}
	ok("7 concurrent safe", !dup && len(seen) == 400)

	// 8. No shared mutable state across parse or derivation.
	src := trace.NewRoot(gen(), true)
	src.SetBaggage("k", "orig")
	cp, _ := trace.Parse(src.Marshal())
	cp.SetBaggage("k", "changed")
	kid := src.Child()
	kid.SetBaggage("k", "kid")
	mv, _ := src.Baggage("k")
	ok("8 no shared state", mv == "orig")
}
