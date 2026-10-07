package action

import (
	"fmt"
	"sync"
	"testing"

	oerr "ontology/errors"
	"ontology/hooks"
	"ontology/txn"
)

func newEngine() (*Engine, *hooks.Registry, *hooks.Recorder) {
	reg := hooks.NewRegistry()
	rec := hooks.NewRecorder()
	e := NewEngine(reg, rec)
	e.RegisterType(ObjectType{Name: "A", Props: map[string]PropType{"n": Int}})
	e.RegisterType(ObjectType{Name: "B", Props: map[string]PropType{"s": String}})
	return e, reg, rec
}

func mustKind(t *testing.T, err error, want oerr.Kind) {
	t.Helper()
	got, ok := oerr.AsKind(err)
	if !ok {
		t.Fatalf("error is not normalized: %v", err)
	}
	if got != want {
		t.Fatalf("error kind: want %v, got %v (err=%v)", want, got, err)
	}
}

// A nested action's pre hook must observe the outer action's not yet
// committed writes.
func TestNestedPreHookSeesOuterWrites(t *testing.T) {
	e, reg, _ := newEngine()
	defer e.Close()

	var seenOuterWrite bool
	reg.RegisterPre("B", "check-outer-write", func(c hooks.Context) error {
		v, ok := c.View.Get("A", "outer-1")
		seenOuterWrite = ok && v["n"] == 42
		return nil
	})

	e.RegisterAction(Definition{Name: "inner", Run: func(ctx Ctx, p map[string]any) error {
		return ctx.Create("B", "inner-1", map[string]any{"s": "x"})
	}})
	e.RegisterAction(Definition{Name: "outer", Run: func(ctx Ctx, p map[string]any) error {
		if err := ctx.Create("A", "outer-1", map[string]any{"n": 42}); err != nil {
			return err
		}
		return ctx.Invoke("inner", nil)
	}})

	res := e.Execute("outer", nil)
	if res.Err != nil {
		t.Fatalf("execute: %v", res.Err)
	}
	if !seenOuterWrite {
		t.Fatal("inner pre hook did not see the outer uncommitted write")
	}
	t.Logf("input: outer creates A/outer-1 then invokes inner creating B/inner-1")
	t.Logf("output: inner pre-hook observed A/outer-1=%v", seenOuterWrite)
	t.Logf("basis: pre-hook snapshot is the live (committed, overlay) facade at fire time")
}

// Post hooks fire exactly once per transaction, at the outermost boundary,
// never once per nesting level.
func TestPostHookFiresOnceAtOutermost(t *testing.T) {
	e, reg, rec := newEngine()
	defer e.Close()

	reg.RegisterPost("A", "post-a", func(c hooks.Context) error { return nil })
	reg.RegisterPre("A", "pre-a", func(c hooks.Context) error { return nil })

	e.RegisterAction(Definition{Name: "level3", Run: func(ctx Ctx, p map[string]any) error {
		return ctx.Create("A", "x3", map[string]any{"n": 3})
	}})
	e.RegisterAction(Definition{Name: "level2", Run: func(ctx Ctx, p map[string]any) error {
		if err := ctx.Create("A", "x2", map[string]any{"n": 2}); err != nil {
			return err
		}
		return ctx.Invoke("level3", nil)
	}})
	e.RegisterAction(Definition{Name: "level1", Run: func(ctx Ctx, p map[string]any) error {
		if err := ctx.Create("A", "x1", map[string]any{"n": 1}); err != nil {
			return err
		}
		return ctx.Invoke("level2", nil)
	}})

	res := e.Execute("level1", nil)
	if res.Err != nil {
		t.Fatalf("execute: %v", res.Err)
	}
	var pre, post int
	for _, r := range rec.Records() {
		switch r.Phase {
		case hooks.Pre:
			pre++
		case hooks.Post:
			post++
		}
	}
	t.Logf("input: 3-level nested action, one post hook on A")
	t.Logf("output: pre firings=%d post firings=%d records=%v", pre, post, rec.Records())
	t.Logf("basis: post hooks are dispatched only by Engine.run after the outermost body returns")
	if pre != 1 {
		t.Fatalf("pre hook firings: want 1 (once per type per txn), got %d", pre)
	}
	if post != 1 {
		t.Fatalf("post hook firings: want 1 (outermost only), got %d", post)
	}
}

// Multiple failing post hooks are all collected in registration order and
// reported as one aggregate; the whole transaction then rolls back.
func TestPostHookAggregationAndRollback(t *testing.T) {
	e, reg, _ := newEngine()
	defer e.Close()

	reg.RegisterPost("A", "fail-1", func(c hooks.Context) error { return fmt.Errorf("first") })
	reg.RegisterPost("A", "fail-2", func(c hooks.Context) error { return fmt.Errorf("second") })
	reg.RegisterPost("B", "fail-3", func(c hooks.Context) error { return fmt.Errorf("third") })

	e.RegisterAction(Definition{Name: "act", Run: func(ctx Ctx, p map[string]any) error {
		if err := ctx.Create("A", "a1", map[string]any{"n": 1}); err != nil {
			return err
		}
		return ctx.Create("B", "b1", map[string]any{"s": "v"})
	}})

	res := e.Execute("act", nil)
	mustKind(t, res.Err, oerr.KindPostHook)
	ae := res.Err.(*oerr.Error)
	if len(ae.Failures) != 3 {
		t.Fatalf("aggregated failures: want 3, got %d (%v)", len(ae.Failures), ae.Failures)
	}
	wantOrder := []string{"fail-1", "fail-2", "fail-3"}
	for i, f := range ae.Failures {
		if f.HookName != wantOrder[i] {
			t.Fatalf("failure order[%d]: want %s, got %s", i, wantOrder[i], f.HookName)
		}
	}
	if e.CountCommitted("A") != 0 || e.CountCommitted("B") != 0 {
		t.Fatal("post-hook failure must roll back every write")
	}
	t.Logf("input: action writes A/a1 and B/b1; three post hooks all fail")
	t.Logf("output: %v", res.Err)
	t.Logf("basis: FirePostAll never short-circuits; rollback discards the whole overlay")
}

// A failing pre hook aborts the transaction immediately: later writes and
// nested invocations never start, and no trace remains.
func TestPreHookFailureAbortsImmediately(t *testing.T) {
	e, reg, _ := newEngine()
	defer e.Close()

	reg.RegisterPre("B", "deny", func(c hooks.Context) error { return fmt.Errorf("denied") })

	innerRan := false
	e.RegisterAction(Definition{Name: "inner", Run: func(ctx Ctx, p map[string]any) error {
		innerRan = true
		return ctx.Create("A", "inner", map[string]any{"n": 9})
	}})
	e.RegisterAction(Definition{Name: "outer", Run: func(ctx Ctx, p map[string]any) error {
		if err := ctx.Create("A", "before", map[string]any{"n": 1}); err != nil {
			return err
		}
		if err := ctx.Create("B", "trigger", map[string]any{"s": "x"}); err != nil {
			return err // abort on the failing pre hook of B
		}
		// Never reached: the write above already failed.
		if err := ctx.Create("A", "after", map[string]any{"n": 2}); err != nil {
			return err
		}
		return ctx.Invoke("inner", nil)
	}})

	res := e.Execute("outer", nil)
	mustKind(t, res.Err, oerr.KindPreHook)
	if innerRan {
		t.Fatal("nested action started after pre-hook failure")
	}
	if e.CountCommitted("A") != 0 || e.CountCommitted("B") != 0 {
		t.Fatalf("pre-hook failure left traces: A=%d B=%d",
			e.CountCommitted("A"), e.CountCommitted("B"))
	}
	t.Logf("input: outer writes A/before, then B/trigger whose pre hook denies, then more work")
	t.Logf("output: err=%v innerRan=%v committedA=%d", res.Err, innerRan, e.CountCommitted("A"))
	t.Logf("basis: poisoned context short-circuits all later writes/invokes; rollback drops overlay")
}

// Invalid arguments outrank hook failures and never enter the post-hook
// aggregate; the three kinds are mutually distinguishable.
func TestErrorPrecedenceAndKinds(t *testing.T) {
	e, reg, _ := newEngine()
	defer e.Close()

	postFired := false
	reg.RegisterPost("A", "post", func(c hooks.Context) error {
		postFired = true
		return fmt.Errorf("post failure")
	})
	reg.RegisterPre("A", "pre", func(c hooks.Context) error { return nil })

	e.RegisterAction(Definition{Name: "bad-target", Run: func(ctx Ctx, p map[string]any) error {
		return ctx.Update("A", "missing", map[string]any{"n": 1})
	}})
	e.RegisterAction(Definition{Name: "bad-type", Run: func(ctx Ctx, p map[string]any) error {
		return ctx.Create("A", "x", map[string]any{"n": "not-an-int"})
	}})
	e.RegisterAction(Definition{Name: "arg-then-post", Run: func(ctx Ctx, p map[string]any) error {
		if err := ctx.Create("A", "ok", map[string]any{"n": 1}); err != nil {
			return err
		}
		return ctx.Delete("A", "missing") // invalid argument beats the failing post hook
	}})

	mustKind(t, e.Execute("bad-target", nil).Err, oerr.KindInvalidArgument)
	mustKind(t, e.Execute("bad-type", nil).Err, oerr.KindInvalidArgument)
	mustKind(t, e.Execute("arg-then-post", nil).Err, oerr.KindInvalidArgument)
	if postFired {
		t.Fatal("post hook fired although the call failed on invalid arguments")
	}
	t.Logf("input: three calls mixing invalid arguments with a failing post hook")
	t.Logf("output: all three normalized to %v; post hook never fired", oerr.KindInvalidArgument)
	t.Logf("basis: validation runs before hooks; post hooks only run on the commit path")
}

// Concurrent outermost calls (each with nested writes) never interleave:
// the final state equals applying them in the executor's serial order, and
// every transaction's hook records form one contiguous block.
func TestConcurrentOutermostNoInterleave(t *testing.T) {
	e, reg, rec := newEngine()
	defer e.Close()

	reg.RegisterPre("A", "pre-a", func(c hooks.Context) error { return nil })
	reg.RegisterPost("A", "post-a", func(c hooks.Context) error { return nil })

	e.RegisterAction(Definition{Name: "inner", Run: func(ctx Ctx, p map[string]any) error {
		return ctx.Create("A", p["id"].(string), map[string]any{"n": p["n"]})
	}})
	e.RegisterAction(Definition{Name: "outer", Run: func(ctx Ctx, p map[string]any) error {
		tag := p["tag"].(string)
		if err := ctx.Create("A", tag+"-a", map[string]any{"n": 1}); err != nil {
			return err
		}
		if err := ctx.Invoke("inner", map[string]any{"id": tag + "-b", "n": 2}); err != nil {
			return err
		}
		return ctx.Create("A", tag+"-c", map[string]any{"n": 3})
	}})

	const callers = 16
	results := make([]Result, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = e.Execute("outer", map[string]any{"tag": fmt.Sprintf("t%02d", i)})
		}(i)
	}
	wg.Wait()

	for _, r := range results {
		if r.Err != nil {
			t.Fatalf("seq %d failed: %v", r.Seq, r.Err)
		}
	}
	if got := e.CountCommitted("A"); got != 3*callers {
		t.Fatalf("committed instances: want %d, got %d", 3*callers, got)
	}
	// Every transaction's hook records must be contiguous in the global log:
	// no other transaction's records may sit inside its boundary.
	seen := map[int64]bool{}
	prev := int64(-1)
	for _, r := range rec.Records() {
		if r.TxnID != prev {
			if seen[r.TxnID] {
				t.Fatalf("txn %d records interleaved at record %+v", r.TxnID, r)
			}
			seen[r.TxnID] = true
			prev = r.TxnID
		}
	}
	t.Logf("input: %d concurrent outermost calls, each 3 nested writes", callers)
	t.Logf("output: committed=%d, hook records contiguous per txn", e.CountCommitted("A"))
	t.Logf("basis: single FIFO executor worker => global serial order, no interleaving")
}

// Replaying the same outermost call sequence yields identical final state
// and identical hook firing records.
func TestReplayDeterminism(t *testing.T) {
	run := func() (map[string]txn.Value, []hooks.Record) {
		e, reg, rec := newEngine()
		defer e.Close()
		reg.RegisterPre("A", "pre", func(c hooks.Context) error { return nil })
		reg.RegisterPost("A", "post", func(c hooks.Context) error { return nil })
		e.RegisterAction(Definition{Name: "inner", Run: func(ctx Ctx, p map[string]any) error {
			return ctx.Update("A", "acc", map[string]any{"n": p["n"]})
		}})
		e.RegisterAction(Definition{Name: "seed", Run: func(ctx Ctx, p map[string]any) error {
			return ctx.Create("A", "acc", map[string]any{"n": 0})
		}})
		e.RegisterAction(Definition{Name: "bump", Run: func(ctx Ctx, p map[string]any) error {
			cur, _ := ctx.Get("A", "acc")
			return ctx.Invoke("inner", map[string]any{"n": cur["n"].(int) + p["delta"].(int)})
		}})
		if r := e.Execute("seed", nil); r.Err != nil {
			t.Fatalf("seed: %v", r.Err)
		}
		for _, d := range []int{1, 2, 3, 4} {
			if r := e.Execute("bump", map[string]any{"delta": d}); r.Err != nil {
				t.Fatalf("bump: %v", r.Err)
			}
		}
		v, _ := e.Get("A", "acc")
		return map[string]txn.Value{"acc": v}, rec.Records()
	}

	state1, rec1 := run()
	state2, rec2 := run()
	if fmt.Sprintf("%v", state1) != fmt.Sprintf("%v", state2) {
		t.Fatalf("replay diverged: %v vs %v", state1, state2)
	}
	if len(rec1) != len(rec2) {
		t.Fatalf("hook record count diverged: %d vs %d", len(rec1), len(rec2))
	}
	for i := range rec1 {
		a, b := rec1[i], rec2[i]
		if a.Phase != b.Phase || a.TypeName != b.TypeName || a.HookName != b.HookName || a.Action != b.Action {
			t.Fatalf("hook record %d diverged: %+v vs %+v", i, a, b)
		}
	}
	t.Logf("input: fixed sequence seed,bump(1..4) executed twice on fresh engines")
	t.Logf("output: final state %v, %d identical hook records", state1, len(rec1))
	t.Logf("basis: FIFO executor + deterministic dispatch order => replay equality")
}
