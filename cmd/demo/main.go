// Demo exercises every guarantee of the idempotent write gateway and
// prints one OK/FAIL verdict line per guarantee, plus a final summary.
// It takes no arguments, uses no network, and always exits 0.
package main

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"ontology/gateway"
)

const ttl = time.Minute

// clock is a manually advanced injected clock; the demo never reads the
// wall clock.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

var failures int

func check(name string, ok bool) {
	verdict := "OK  "
	if !ok {
		verdict = "FAIL"
		failures++
	}
	fmt.Printf("%s %s\n", verdict, name)
}

func execVal(v string) gateway.ExecFunc {
	return func([]byte) ([]byte, error) { return []byte(v), nil }
}

// blocking returns an ExecFunc that parks until released, plus its controls.
func blocking(v string) (gateway.ExecFunc, <-chan struct{}, chan<- struct{}) {
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	return func([]byte) ([]byte, error) {
		once.Do(func() { close(started) })
		<-release
		return []byte(v), nil
	}, started, release
}

func main() {
	clk := &clock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	g := gateway.New(clk.now, ttl)

	// 1-2: exactly once, replay distinguishable and equivalent.
	first, _ := g.Submit("order", []byte(`{"item":"book"}`), execVal("receipt-1"))
	second, _ := g.Submit("order", []byte(`{"item":"book"}`), execVal("receipt-1"))
	third, _ := g.Submit("order", []byte(`{"item":"book"}`), execVal("receipt-1"))
	check("exactly-once: 3 submits, 1 real execution", g.ExecCalls() == 1)
	check("replay distinguishable and equivalent",
		!first.Replayed && second.Replayed && third.Replayed &&
			string(second.Value) == string(first.Value))

	// 3: conflicting body rejected, nothing executed or overwritten.
	_, err := g.Submit("order", []byte(`{"item":"pen"}`), execVal("receipt-2"))
	replay, _ := g.Submit("order", []byte(`{"item":"book"}`), execVal("receipt-1"))
	check("conflict on different body, result preserved",
		errors.Is(err, gateway.ErrConflict) && g.ExecCalls() == 1 &&
			string(replay.Value) == "receipt-1")

	// 4: failure remembered, retry executes, success then fixed.
	attempts := 0
	flaky := func([]byte) ([]byte, error) {
		attempts++
		if attempts == 1 {
			return nil, errors.New("db down")
		}
		return []byte("receipt-9"), nil
	}
	_, err1 := g.Submit("pay", []byte("p"), flaky)
	ok2, _ := g.Submit("pay", []byte("p"), flaky)
	ok3, _ := g.Submit("pay", []byte("p"), flaky)
	check("failure retryable, success fixed",
		err1 != nil && attempts == 2 && !ok2.Replayed && ok3.Replayed &&
			string(ok3.Value) == "receipt-9")

	// 5: concurrent duplicates merge onto one execution.
	exec, started, release := blocking("merged")
	outs := make([]gateway.Outcome, 5)
	var wg sync.WaitGroup
	before := g.ExecCalls()
	for i := range outs {
		wg.Add(1)
		go func(i int) { defer wg.Done(); outs[i], _ = g.Submit("job", []byte("j"), exec) }(i)
	}
	<-started
	close(release)
	wg.Wait()
	fresh := 0
	merged := g.ExecCalls() == before+1
	for _, o := range outs {
		if !o.Replayed {
			fresh++
		}
		merged = merged && string(o.Value) == "merged"
	}
	check("concurrent duplicates merge to one execution", merged && fresh == 1)

	// 6: conflicting body while in-flight fails immediately.
	exec, started, release = blocking("slow")
	go func() { g.Submit("slow", []byte("a"), exec) }()
	<-started
	_, err = g.Submit("slow", []byte("b"), execVal("x"))
	check("in-flight conflict returns immediately", errors.Is(err, gateway.ErrConflict))
	close(release)

	// 7: at the exact expiry instant the record is gone and re-executes.
	clk.advance(ttl)
	before = g.ExecCalls()
	out, _ := g.Submit("order", []byte(`{"item":"book"}`), execVal("receipt-3"))
	check("expired at deadline (half-open), re-executes",
		!out.Replayed && g.ExecCalls() == before+1)

	// 8: expiry never disturbs an in-flight record.
	exec, started, release = blocking("kept")
	go func() { g.Submit("long", []byte("L"), exec) }()
	<-started
	clk.advance(1000 * time.Hour)
	dup := make(chan gateway.Outcome, 1)
	go func() { o, _ := g.Submit("long", []byte("L"), execVal("z")); dup <- o }()
	close(release)
	kept := <-dup
	check("expiry does not affect in-flight record",
		kept.Replayed && string(kept.Value) == "kept")

	// 9: inspecting an unknown key yields zero values.
	info := g.Inspect("missing")
	check("inspect unknown key returns zero info",
		!info.Exists && info.Remaining == 0 && !info.HasResult)

	// 10: a slow execution never blocks other keys.
	exec, started, release = blocking("slow")
	go func() { g.Submit("heavy", []byte("h"), exec) }()
	<-started
	fast, ferr := g.Submit("light", []byte("l"), execVal("quick"))
	check("slow key does not block other keys",
		ferr == nil && string(fast.Value) == "quick")
	close(release)

	fmt.Printf("TOTAL %d checks, %d failed\n", 10, failures)
}
