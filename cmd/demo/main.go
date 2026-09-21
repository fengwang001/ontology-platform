// Command demo exercises every documented semantic of package mux and
// prints one OK/FAIL verdict per check.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"ontology/mux"
)

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

var failed bool

func check(name string, ok bool) {
	verdict := "OK  "
	if !ok {
		verdict = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s\n", verdict, name)
}

func main() {
	clk := &clock{now: time.Unix(1000, 0)}
	m := mux.New(clk.Now)

	// 1. One-to-one dispatch with payload isolation.
	ch, _ := m.Register("a")
	p := []byte("hello")
	m.Deliver("a", p)
	p[0] = 'X'
	got := <-ch
	s := m.Stats()
	check("1 one-to-one dispatch, payload isolated", string(got) == "hello" && s.Delivered == 1 && s.Pending == 0)

	// 2. Duplicate id rejected, original waiter intact, re-register works.
	chB, _ := m.Register("b")
	_, dupErr := m.Register("b")
	m.Deliver("b", []byte("still-alive"))
	alive := string(<-chB) == "still-alive"
	chB2, reErr := m.Register("b")
	m.Deliver("b", []byte("again"))
	reused := reErr == nil && string(<-chB2) == "again"
	check("2 duplicate id rejected, slot reusable", errors.Is(dupErr, mux.ErrDuplicateID) && alive && reused)

	// 3. Orphan response dropped without creating a slot.
	m.Deliver("ghost", []byte("old"))
	chG, _ := m.Register("ghost")
	m.Deliver("ghost", []byte("new"))
	s = m.Stats()
	check("3 orphan dropped, no phantom slot", s.Orphans == 1 && string(<-chG) == "new")

	// 4. Late response counted separately from orphans.
	clk.advance(time.Minute)
	m.Tick() // nothing timed yet; no-op
	waitRes := make(chan error, 1)
	go func() { _, err := m.Wait("w", clk.Now().Add(time.Second)); waitRes <- err }()
	for m.Stats().Pending != 1 {
		time.Sleep(time.Millisecond)
	}
	clk.advance(2 * time.Second)
	m.Tick()
	timedOut := errors.Is(<-waitRes, mux.ErrTimedOut)
	m.Deliver("w", []byte("late"))
	s = m.Stats()
	check("4 late vs orphan distinguished", timedOut && s.Late == 1 && s.Orphans == 1)

	// 5. Timeout accounting: Pending down, Delivered/Orphans untouched.
	check("5 timeout accounting clean", s.Pending == 0 && s.Delivered == 4 && s.Orphans == 1)

	// 6. Close drains waiters, rejects new ones, stays safe.
	chC, _ := m.Register("c")
	m.Close()
	_, open := <-chC
	_, regErr := m.Register("z")
	m.Deliver("c", []byte("x"))
	m.Deliver("nobody", []byte("x"))
	m.Close() // idempotent
	s = m.Stats()
	check("6 close drains, rejects, idempotent", !open && errors.Is(regErr, mux.ErrClosed) && s.Late == 2 && s.Orphans == 2 && s.Pending == 0)

	// 7. Conservation: delivered + timedOut == completed, pending consistent.
	m2 := mux.New(clk.Now)
	for i := 0; i < 3; i++ {
		c, _ := m2.Register(fmt.Sprint("k", i))
		m2.Deliver(fmt.Sprint("k", i), []byte("v"))
		<-c
	}
	done := make(chan error, 2)
	go func() { _, err := m2.Wait("t1", clk.Now().Add(time.Second)); done <- err }()
	go func() { _, err := m2.Wait("t2", clk.Now().Add(time.Second)); done <- err }()
	for m2.Stats().Pending != 2 {
		time.Sleep(time.Millisecond)
	}
	clk.advance(time.Second)
	m2.Tick()
	t1, t2 := <-done, <-done
	s2 := m2.Stats()
	completed := s2.Delivered + 2 // 2 timed out
	check("7 counters conserve registrations", errors.Is(t1, mux.ErrTimedOut) && errors.Is(t2, mux.ErrTimedOut) && completed == 5 && s2.Pending == 0)

	// 8. Concurrent smoke: parallel waiters all get their own response.
	m3 := mux.New(nil)
	var wg sync.WaitGroup
	allOK := true
	for i := 0; i < 32; i++ {
		id := fmt.Sprint("c", i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := m3.Wait(id, time.Now().Add(time.Minute))
			if err != nil || string(got) != id {
				allOK = false
			}
		}()
	}
	for m3.Stats().Pending != 32 {
		time.Sleep(time.Millisecond)
	}
	for i := 0; i < 32; i++ {
		id := fmt.Sprint("c", i)
		go m3.Deliver(id, []byte(id))
	}
	wg.Wait()
	check("8 concurrent dispatch correct", allOK && m3.Stats().Delivered == 32)

	if failed {
		os.Exit(1)
	}
}
