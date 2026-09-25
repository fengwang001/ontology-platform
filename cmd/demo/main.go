// Demo for the writer-preference readers-writer lock. No args, no
// network; exit code 0 iff every check passes.
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"ontology/api"
)

var failed bool

func ok(b bool) string {
	if !b {
		failed = true
		return "FAIL"
	}
	return "OK"
}

func do(a *api.API, op, id string) bool {
	switch op {
	case "AcquireRead":
		g, _ := a.AcquireRead(id)
		return g
	case "AcquireWrite":
		g, _ := a.AcquireWrite(id)
		return g
	case "ReleaseRead":
		a.ReleaseRead(id)
	case "ReleaseWrite":
		a.ReleaseWrite(id)
	}
	return false
}

func main() {
	// Section 3: the eight-step trace, one line per step.
	type step struct{ op, id, readers, writer, queue, result string }
	steps := []step{
		{"AcquireRead", "R1", "R1", "-", "[]", "进入"},
		{"AcquireRead", "R2", "R1,R2", "-", "[]", "进入"},
		{"AcquireWrite", "W1", "R1,R2", "-", "[W1]", "等待"},
		{"AcquireRead", "R3", "R1,R2", "-", "[W1,R3]", "等待"},
		{"ReleaseRead", "R1", "R2", "-", "[W1,R3]", "释放"},
		{"ReleaseRead", "R2", "", "W1", "[R3]", "放行W1"},
		{"AcquireRead", "R4", "", "W1", "[R3,R4]", "等待"},
		{"ReleaseWrite", "W1", "R3,R4", "-", "[]", "放行R3,R4"},
	}
	a := api.New()
	for i, s := range steps {
		granted := do(a, s.op, s.id)
		readers, writer := strings.Join(a.Readers(), ","), a.Writer()
		if writer == "" {
			writer = "-"
		}
		good := readers == s.readers && writer == s.writer
		if strings.HasPrefix(s.op, "Acquire") {
			good = good && granted == (s.result == "进入")
		}
		fmt.Printf("%d %s(%s) readers={%s} writer=%s queue=%s %s %s\n",
			i+1, s.op, s.id, readers, writer, s.queue, s.result, ok(good))
	}
	wp := func() bool { // writer preference: waiting writer blocks readers
		b := api.New()
		b.AcquireRead("R1")
		g1, _ := b.AcquireWrite("W1")
		g2, _ := b.AcquireRead("R2")
		return !g1 && !g2
	}()
	me := func() bool { // mutual exclusion: held writer blocks all
		c := api.New()
		g1, _ := c.AcquireWrite("W1")
		g2, _ := c.AcquireRead("R1")
		g3, _ := c.AcquireWrite("W2")
		return g1 && !g2 && !g3
	}()
	faults, notrace := func() (bool, bool) {
		d := api.New()
		d.AcquireRead("R1")
		before := fmt.Sprint(d.Readers(), d.Writer())
		_, e1 := d.AcquireRead("")
		e2 := d.ReleaseRead("R9")
		e3 := d.ReleaseWrite("W9")
		f := errors.Is(e1, api.ErrEmptyID) && errors.Is(e2, api.ErrNotHeld) &&
			errors.Is(e3, api.ErrNotHeld) && api.ErrEmptyID != api.ErrNotHeld
		nt := fmt.Sprint(d.Readers(), d.Writer()) == before
		d.ReleaseRead("R1")
		e4 := d.ReleaseRead("R1")
		g, _ := d.AcquireRead("R2") // still usable after rejections
		return f && errors.Is(e4, api.ErrDoubleRelease) && api.ErrNotHeld != api.ErrDoubleRelease, nt && g
	}()
	ref := api.New().SelfCheck() == nil // naive-reference consistency
	lm := func() bool {                 // grant decision stays O(1) under m waiting writers
		e := api.New()
		e.AcquireRead("R0")
		for i := 0; i < 10000; i++ {
			if g, _ := e.AcquireWrite(fmt.Sprintf("W%d", i)); g {
				return false
			}
		}
		if g, _ := e.AcquireRead("R1"); g {
			return false
		}
		return e.ReleaseRead("R0") == nil && e.Writer() == "W0"
	}()
	cc := func() bool { // concurrent mutual exclusion
		f := api.New()
		var wg sync.WaitGroup
		var bad atomic.Int32
		for g := 0; g < 16; g++ {
			wg.Add(1)
			go func(g int) {
				defer wg.Done()
				rid, wid := fmt.Sprintf("R%d", g), fmt.Sprintf("W%d", g)
				for i := 0; i < 50; i++ {
					if g, _ := f.AcquireRead(rid); g {
						if f.Writer() != "" {
							bad.Add(1)
						}
						f.ReleaseRead(rid)
					}
					if g, _ := f.AcquireWrite(wid); g {
						if len(f.Readers()) > 0 {
							bad.Add(1)
						}
						f.ReleaseWrite(wid)
					}
				}
			}(g)
		}
		wg.Wait()
		return bad.Load() == 0
	}()
	fmt.Printf("writer-preference %s | mutual-exclusion %s | faults(3) %s | no-trace %s\n",
		ok(wp), ok(me), ok(faults), ok(notrace))
	fmt.Printf("naive-reference %s | large-m(10000) %s | concurrency %s\n", ok(ref), ok(lm), ok(cc))
	if failed {
		os.Exit(1)
	}
}
