package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/esc"
	"ontology/frame"
)

func main() {
	ok := true
	check := func(name string, cond bool) {
		if !cond {
			ok = false
		}
		s := "OK  "
		if !cond {
			s = "FAIL"
		}
		fmt.Printf("%s %s\n", s, name)
	}

	// Section 3: the 9-byte stream in two feeds + flush.
	f := api.New()
	g1, e1 := f.Feed([]byte{0x7E, 0x41, 0x7E, 0x7E, 0x7D})
	g2, e2 := f.Feed([]byte{0x5E, 0x7D, 0x5D, 0x7E})
	fe := f.Flush()
	check("feed1 yields frame [41]", e1 == nil && len(g1) == 1 && bytes.Equal(g1[0], []byte{0x41}))
	check("feed2 yields frame [7E 7D], flush clean",
		e2 == nil && len(g2) == 1 && bytes.Equal(g2[0], []byte{0x7E, 0x7D}) && fe == nil)

	// Round-trip of a payload containing FLAG/ESC.
	p := []byte{0x00, esc.Flag, esc.Esc, 0xFF}
	r := api.New()
	rg, _ := r.Feed(api.Frame(p))
	check("roundtrip payload with FLAG/ESC",
		len(rg) == 1 && bytes.Equal(rg[0], p) && r.Flush() == nil)

	// Any split point equals one-shot feeding.
	stream := append(api.Frame([]byte{0x41}), api.Frame([]byte{esc.Flag, esc.Esc})...)
	split := true
	for i := 1; i < len(stream); i++ {
		s := api.New()
		s.Feed(stream[:i])
		s.Feed(stream[i:])
		g := s.Frames()
		if s.Flush() != nil || len(g) != 2 || !bytes.Equal(g[0], []byte{0x41}) ||
			!bytes.Equal(g[1], []byte{esc.Flag, esc.Esc}) {
			split = false
		}
	}
	check("any split point == one-shot", split)

	// Three distinguishable faults.
	bad := api.New()
	bad.Feed([]byte{esc.Flag})
	_, errBad := bad.Feed([]byte{esc.Esc, esc.Flag})
	te, tf := api.New(), api.New()
	te.Feed([]byte{esc.Flag, esc.Esc})
	tf.Feed([]byte{esc.Flag, 0x41})
	errTE, errTF := te.Flush(), tf.Flush()
	check("3 distinct faults", errors.Is(errBad, api.ErrBadEscape) &&
		errors.Is(errTE, api.ErrTruncatedEscape) && errors.Is(errTF, api.ErrTruncatedFrame) &&
		!errors.Is(errBad, api.ErrTruncatedEscape) && !errors.Is(errTE, api.ErrTruncatedFrame))

	// Rejection leaves no trace; instance stays usable. bad is still IN
	// with an empty payload, so the correct continuation completes it.
	pre := len(bad.Frames())
	g, err := bad.Feed([]byte{0x77, esc.Flag})
	check("state intact after rejection",
		err == nil && len(bad.Frames()) == pre+1 && len(g) == 1 && bytes.Equal(g[0], []byte{0x77}))

	// Single-pass: byte-by-byte feed of m bytes inspects exactly m bytes.
	check("byte-by-byte feed inspects exactly m", frame.SinglePass(10000))

	// Concurrent read-only Frames agree byte-for-byte.
	c := api.New()
	c.Feed(stream)
	want := c.Frames()
	var wg sync.WaitGroup
	var mu sync.Mutex
	same := true
	start := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for k := 0; k < 50; k++ {
				got := c.Frames()
				if len(got) != len(want) || !bytes.Equal(got[0], want[0]) || !bytes.Equal(got[1], want[1]) {
					mu.Lock()
					same = false
					mu.Unlock()
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	check("concurrent Frames identical", same)

	check("SelfCheck", api.New().SelfCheck() == nil)

	if !ok {
		os.Exit(1)
	}
}
