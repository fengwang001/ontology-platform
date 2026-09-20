// Command demo exercises every semantic guarantee of the trace
// package and prints one OK/FAIL verdict per guarantee.
package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology/trace"
)

var failed bool

func check(name string, ok bool) {
	verdict := "OK"
	if !ok {
		verdict = "FAIL"
		failed = true
	}
	fmt.Printf("%-4s %s\n", verdict, name)
}

func gen() func() trace.SpanID {
	n := 0
	return func() trace.SpanID { n++; return trace.SpanID(fmt.Sprintf("id%d", n)) }
}

func main() {
	// 1. Derivation: shared trace ID, fresh span IDs, parent links.
	root := NewRootSpan()
	child, grand := root.Child(), root.Child().Child()
	check("1 derivation: traceID inherited, parent linked, root parent empty",
		child.TraceID() == root.TraceID() && grand.TraceID() == root.TraceID() &&
			child.ParentID() == root.SpanID() && child.SpanID() != root.SpanID() &&
			root.ParentID() == "")

	// 2. Sampling: inherited, never recomputed.
	s2 := trace.NewRoot(gen(), true).Child().Child()
	u2 := trace.NewRoot(gen(), false).Child().Child()
	check("2 sampling: decision inherited down the tree",
		s2.Sampled() && !u2.Sampled())

	// 3. Baggage copy-on-write with alternating same-key writes.
	p := trace.NewRoot(gen(), true)
	p.SetBaggage("k", "p1")
	c := p.Child()
	c.SetBaggage("k", "c1")
	p.SetBaggage("k", "p2")
	p.SetBaggage("ponly", "x")
	c.SetBaggage("conly", "y")
	cv, _ := c.Baggage("k")
	pv, _ := p.Baggage("k")
	_, cSeesP := c.Baggage("ponly")
	_, pSeesC := p.Baggage("conly")
	pre, _ := c.Baggage("k")
	check("3 baggage: snapshot at derivation, then isolated both ways",
		cv == "c1" && pv == "p2" && pre == "c1" && !cSeesP && !pSeesC)

	// 4. Baggage keys sorted, unique, last write wins.
	s4 := trace.NewRoot(gen(), false)
	s4.SetBaggage("z", "1")
	s4.SetBaggage("a", "1")
	s4.SetBaggage("m", "1")
	s4.SetBaggage("a", "2")
	av, _ := s4.Baggage("a")
	check("4 baggage keys: ascending, unique, last write wins",
		reflect.DeepEqual(s4.BaggageKeys(), []string{"a", "m", "z"}) && av == "2")

	// 5. Wire round-trip is lossless and byte-deterministic.
	s5 := trace.NewRoot(gen(), true)
	s5.SetBaggage("user", "alice")
	s5.SetBaggage("tenant", "acme")
	w1, w2 := s5.Marshal(), s5.Marshal()
	back, err := trace.Parse(w1)
	bv, _ := back.Baggage("user")
	check("5 wire: round-trip lossless, marshal deterministic",
		err == nil && w1 == w2 && back.TraceID() == s5.TraceID() &&
			back.SpanID() == s5.SpanID() && back.ParentID() == s5.ParentID() &&
			back.Sampled() == s5.Sampled() && bv == "alice" && back.Marshal() == w1)

	// 6. Parse rejects malformed input with (nil, ErrMalformed).
	bad := []string{"", "a-b", "a-b-1", "-b-01", "a--01", "a-b-01-nokv", "a-b-00-=v"}
	robust := true
	for _, in := range bad {
		s, err := trace.Parse(in)
		if s != nil || !errors.Is(err, trace.ErrMalformed) {
			robust = false
		}
	}
	empty, err := trace.Parse("a-b-01")
	check("6 parse: malformed -> (nil, ErrMalformed); empty baggage ok",
		robust && err == nil && len(empty.BaggageKeys()) == 0)

	// 7. Concurrency: race-free mixed ops, distinct child IDs.
	s7 := trace.NewRoot(gen(), true)
	s7.SetBaggage("boot", "yes")
	const workers = 16
	var wg sync.WaitGroup
	ids := make(chan trace.SpanID, workers*20)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				s7.SetBaggage(fmt.Sprintf("k%d", w), "v")
				s7.Baggage("boot")
				s7.Marshal()
				ids <- s7.Child().SpanID()
			}
		}(w)
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
	check("7 concurrency: mixed ops race-free, child IDs distinct",
		!dup && len(seen) == workers*20)

	// 8. No shared mutable state between spans.
	o8 := trace.NewRoot(gen(), true)
	o8.SetBaggage("k", "orig")
	p8, _ := trace.Parse(o8.Marshal())
	c8 := o8.Child()
	p8.SetBaggage("k", "parsed")
	c8.SetBaggage("k", "child")
	ov, _ := o8.Baggage("k")
	check("8 isolation: parsed/child spans share no mutable map",
		ov == "orig")

	if failed {
		os.Exit(1)
	}
}

// NewRootSpan builds a root span with a deterministic generator.
func NewRootSpan() *trace.Span { return trace.NewRoot(gen(), true) }
