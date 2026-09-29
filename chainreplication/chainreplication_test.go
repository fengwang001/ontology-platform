package chainreplication

import (
	"bytes"
	"io"
	"log"
	"sync"
	"testing"
	"time"
)

func newBufferLogger() (Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	l := stdLogger{l: log.New(io.Writer(&buf), "[test] ", 0)}
	return l, &buf
}

func waitOutcome(t *testing.T, h *WriteHandle, want bool) {
	t.Helper()
	select {
	case got := <-h.Outcome():
		if got != want {
			t.Fatalf("seq=%d outcome = %v, want %v", h.Seq(), got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("seq=%d outcome not resolved", h.Seq())
	}
	select {
	case _, ok := <-h.Outcome():
		if ok {
			t.Fatalf("seq=%d outcome delivered more than once", h.Seq())
		}
	default:
		t.Fatalf("seq=%d outcome channel not closed", h.Seq())
	}
}

func TestRejectReasons(t *testing.T) {
	net := NewTestNetwork(nil)

	if _, err := NewCoordinator(nil, net, nil); err == nil || err.(*RejectError).Reason != RejectEmptyChain {
		t.Fatalf("empty chain: %v", err)
	}
	if _, err := NewCoordinator([]string{"a", ""}, net, nil); err == nil || err.(*RejectError).Reason != RejectEmptyNodeID {
		t.Fatalf("empty node id: %v", err)
	}
	if _, err := NewCoordinator([]string{"a", "a"}, net, nil); err == nil || err.(*RejectError).Reason != RejectDuplicateNodeID {
		t.Fatalf("duplicate node id: %v", err)
	}

	logger, _ := newBufferLogger()
	c, err := NewCoordinator([]string{"h", "m", "t"}, NewTestNetwork(logger), logger)
	if err != nil {
		t.Fatal(err)
	}

	check := func(op string, want RejectReason, fn func() error) {
		t.Helper()
		err := fn()
		if err == nil {
			t.Fatalf("%s: expected reject %s, got nil", op, want)
		}
		re, ok := err.(*RejectError)
		if !ok || re.Reason != want {
			t.Fatalf("%s: got %v, want reason %s", op, err, want)
		}
	}
	check("write unknown", RejectUnknownNode, func() error { _, e := c.Write("x", "v"); return e })
	check("read unknown", RejectUnknownNode, func() error { _, _, _, e := c.Read("x"); return e })
	check("fail unknown", RejectUnknownNode, func() error { return c.Fail("x") })
	check("write not head", RejectNotHead, func() error { _, e := c.Write("m", "v"); return e })
	check("read not tail", RejectNotTail, func() error { _, _, _, e := c.Read("m"); return e })
	check("fail last survivor after shrink", RejectLastSurvivor, func() error {
		if err := c.Fail("m"); err != nil {
			return err
		}
		if err := c.Fail("t"); err != nil {
			return err
		}
		return c.Fail("h")
	})
	check("fail last survivor repeat", RejectLastSurvivor, func() error { return c.Fail("h") })
	check("fail dead node", RejectNodeAlreadyFailed, func() error { return c.Fail("m") })

	c2, _ := NewCoordinator([]string{"a", "b"}, NewTestNetwork(nil), nil)
	if err := c2.Fail("b"); err != nil {
		t.Fatal(err)
	}
	check("read failed node", RejectNodeAlreadyFailed, func() error { _, _, _, e := c2.Read("b"); return e })
	check("write failed node", RejectNodeAlreadyFailed, func() error { _, e := c2.Write("b", "v"); return e })
}

func TestSingleNodeIsHeadAndTail(t *testing.T) {
	net := NewTestNetwork(nil)
	c, err := NewCoordinator([]string{"solo"}, net, nil)
	if err != nil {
		t.Fatal(err)
	}
	h, err := c.Write("solo", "v1")
	if err != nil {
		t.Fatal(err)
	}
	waitOutcome(t, h, true)
	v, seq, ok, err := c.Read("solo")
	if err != nil || !ok || v != "v1" || seq != 1 {
		t.Fatalf("read = %q,%d,%v,%v", v, seq, ok, err)
	}
	if err := c.Fail("solo"); err == nil || err.(*RejectError).Reason != RejectLastSurvivor {
		t.Fatalf("solo fail: %v", err)
	}
}

func TestOutOfOrderAndDuplicateDelivery(t *testing.T) {
	net := NewTestNetwork(nil)
	c, err := NewCoordinator([]string{"h", "m", "t"}, net, nil)
	if err != nil {
		t.Fatal(err)
	}
	h1, _ := c.Write("h", "a")
	h2, _ := c.Write("h", "b")
	h3, _ := c.Write("h", "c")

	// Deliver seq 2 and 3 first: middle buffers them and applies nothing.
	if !net.Deliver(c, "h", "m", MsgWrite, 2) || !net.Deliver(c, "h", "m", MsgWrite, 3) {
		t.Fatal("missing h->m seq2/seq3")
	}
	if got := c.alive["m"].applied; got != 0 {
		t.Fatalf("middle applied=%d, want 0 (gap at 1)", got)
	}

	// Duplicate the queued seq1 before delivering it.
	idx := -1
	for i, m := range net.Peek() {
		if m.From == "h" && m.To == "m" && m.Seq == 1 {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatal("h->m seq1 not queued")
	}
	if err := net.Duplicate(idx); err != nil {
		t.Fatal(err)
	}

	// First seq1 unblocks 1,2,3 in order; the duplicate seq1 is discarded.
	if !net.Deliver(c, "h", "m", MsgWrite, 1) {
		t.Fatal("deliver seq1")
	}
	if got := c.alive["m"].applied; got != 3 {
		t.Fatalf("middle applied=%d after gap fill, want 3", got)
	}
	if !net.Deliver(c, "h", "m", MsgWrite, 1) {
		t.Fatal("deliver duplicate seq1")
	}
	if got := c.alive["m"].applied; got != 3 {
		t.Fatalf("duplicate changed applied=%d, want 3", got)
	}

	net.DeliverAll(c)
	waitOutcome(t, h1, true)
	waitOutcome(t, h2, true)
	waitOutcome(t, h3, true)

	v, seq, ok, _ := c.Read("t")
	if !ok || seq != 3 || v != "c" {
		t.Fatalf("tail read = %q,%d,%v", v, seq, ok)
	}
}

func TestReadNeverSeesUncommitted(t *testing.T) {
	net := NewTestNetwork(nil)
	c, _ := NewCoordinator([]string{"h", "m", "t"}, net, nil)
	c.Write("h", "a")

	// Deliver only h->m: tail never applied the write, so it must not read it.
	if !net.Deliver(c, "h", "m", MsgWrite, 1) {
		t.Fatal("deliver h->m")
	}
	v, seq, ok, err := c.Read("t")
	if err != nil || ok || seq != 0 || v != "" {
		t.Fatalf("tail exposed uncommitted: %q,%d,%v,%v", v, seq, ok, err)
	}

	net.DeliverAll(c)
	v, seq, ok, _ = c.Read("t")
	if !ok || seq != 1 || v != "a" {
		t.Fatalf("tail read after commit = %q,%d,%v", v, seq, ok)
	}
}

func TestMiddleFailureRetransmitsExactGap(t *testing.T) {
	net := NewTestNetwork(nil)
	c, _ := NewCoordinator([]string{"h", "m", "t"}, net, nil)
	var handles []*WriteHandle
	for _, v := range []string{"a", "b", "c", "d"} {
		h, _ := c.Write("h", v)
		handles = append(handles, h)
	}

	// Commit 1..2 fully, including acks back to the head.
	net.Deliver(c, "h", "m", MsgWrite, 1)
	net.Deliver(c, "h", "m", MsgWrite, 2)
	net.Deliver(c, "m", "t", MsgWrite, 1)
	net.Deliver(c, "m", "t", MsgWrite, 2)
	net.Deliver(c, "t", "m", MsgAck, 1)
	net.Deliver(c, "t", "m", MsgAck, 2)
	net.Deliver(c, "m", "h", MsgAck, 1)
	net.Deliver(c, "m", "h", MsgAck, 2)
	waitOutcome(t, handles[0], true)
	waitOutcome(t, handles[1], true)

	// 3..4 reach the middle only; its forwarded copies stay queued.
	net.Deliver(c, "h", "m", MsgWrite, 3)
	net.Deliver(c, "h", "m", MsgWrite, 4)

	if err := c.Fail("m"); err != nil {
		t.Fatal(err)
	}

	var gap []int
	for _, m := range net.Peek() {
		if m.Type == MsgWrite && m.From == "h" && m.To == "t" {
			gap = append(gap, m.Seq)
		}
		if m.From == "m" || m.To == "m" {
			t.Fatalf("message involving failed m remains: %+v", m)
		}
	}
	if len(gap) != 2 || gap[0] != 3 || gap[1] != 4 {
		t.Fatalf("retransmitted gap = %v, want [3 4]", gap)
	}

	net.DeliverAll(c)
	waitOutcome(t, handles[2], true)
	waitOutcome(t, handles[3], true)
	v, seq, ok, _ := c.Read("t")
	if !ok || seq != 4 || v != "d" {
		t.Fatalf("tail read after reconfigure = %q,%d,%v", v, seq, ok)
	}

	h5, _ := c.Write("h", "e")
	net.DeliverAll(c)
	waitOutcome(t, h5, true)
}

func TestTailFailureCommitsImmediately(t *testing.T) {
	net := NewTestNetwork(nil)
	c, _ := NewCoordinator([]string{"h", "m", "t"}, net, nil)
	h1, _ := c.Write("h", "a")
	h2, _ := c.Write("h", "b")

	// Both writes reach the middle; nothing reaches the tail.
	net.Deliver(c, "h", "m", MsgWrite, 1)
	net.Deliver(c, "h", "m", MsgWrite, 2)

	if err := c.Fail("t"); err != nil {
		t.Fatal(err)
	}

	// The new tail immediately serves its pending writes as committed.
	v, seq, ok, err := c.Read("m")
	if err != nil || !ok || seq != 2 || v != "b" {
		t.Fatalf("new tail read = %q,%d,%v,%v", v, seq, ok, err)
	}

	net.DeliverAll(c)
	waitOutcome(t, h1, true)
	waitOutcome(t, h2, true)

	// New writes go h -> m directly and keep numbering from 3.
	h3, _ := c.Write("h", "c")
	net.DeliverAll(c)
	waitOutcome(t, h3, true)
}

func TestHeadFailureUncommittedJudgement(t *testing.T) {
	net := NewTestNetwork(nil)
	c, _ := NewCoordinator([]string{"h", "m", "t"}, net, nil)
	h1, _ := c.Write("h", "a")
	h2, _ := c.Write("h", "b")
	h3, _ := c.Write("h", "c")

	// seq1 and seq2 reach the middle; seq3 is applied only at the head.
	net.Deliver(c, "h", "m", MsgWrite, 1)
	net.Deliver(c, "h", "m", MsgWrite, 2)

	if err := c.Fail("h"); err != nil {
		t.Fatal(err)
	}

	// seq3 was never applied by the new head m -> uncommitted, exactly once.
	waitOutcome(t, h3, false)

	net.DeliverAll(c)
	waitOutcome(t, h1, true)
	waitOutcome(t, h2, true)

	v, seq, ok, _ := c.Read("t")
	if !ok || seq != 2 || v != "b" {
		t.Fatalf("tail after head fail = %q,%d,%v", v, seq, ok)
	}

	// Numbering continues without gaps above the new head's prefix (next = 3).
	h4, err := c.Write("m", "d")
	if err != nil || h4.Seq() != 3 {
		t.Fatalf("new head write seq=%d err=%v, want 3", h4.Seq(), err)
	}
	net.DeliverAll(c)
	waitOutcome(t, h4, true)
}

// TestConcurrentWritesPrefixInvariant hammers writes, deliveries, reads and
// failures concurrently under -race and checks the prefix invariant:
// at every instant, a downstream node's applied set is a prefix of every
// upstream node's applied set.
func TestConcurrentWritesPrefixInvariant(t *testing.T) {
	net := NewTestNetwork(nil)
	c, _ := NewCoordinator([]string{"h", "m1", "m2", "t"}, net, nil)

	const writers = 8
	const perWriter = 25
	var writerWG, helperWG sync.WaitGroup
	handles := make([][]*WriteHandle, writers)
	for w := 0; w < writers; w++ {
		writerWG.Add(1)
		go func(w int) {
			defer writerWG.Done()
			for i := 0; i < perWriter; i++ {
				h, err := c.Write("h", "v")
				if err != nil {
					return
				}
				handles[w] = append(handles[w], h)
			}
		}(w)
	}

	stop := make(chan struct{})
	helperWG.Add(2)
	go func() {
		defer helperWG.Done()
		for {
			select {
			case <-stop:
				return
			default:
				if !net.DeliverOne(c) {
					time.Sleep(time.Millisecond)
				}
			}
		}
	}()

	// Prefix invariant: a downstream applied set is a prefix of every
	// upstream one, so applied maxima must be non-increasing head -> tail.
	go func() {
		defer helperWG.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			c.mu.Lock()
			chain := append([]string(nil), c.chain...)
			upstreamApplied := 1 << 30
			for i := 0; i < len(chain); i++ {
				a := c.alive[chain[i]].applied
				if a > upstreamApplied {
					c.mu.Unlock()
					t.Errorf("prefix invariant violated: %s applied=%d > upstream %d", chain[i], a, upstreamApplied)
					return
				}
				upstreamApplied = a
			}
			c.mu.Unlock()
		}
	}()

	// Reconfiguration concurrently with writes, deliveries and reads.
	helperWG.Add(2)
	go func() {
		defer helperWG.Done()
		time.Sleep(2 * time.Millisecond)
		_ = c.Fail("m1")
		time.Sleep(2 * time.Millisecond)
		_ = c.Fail("m2")
	}()
	go func() {
		defer helperWG.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_, _, _, _ = c.Read("t")
		}
	}()

	writerWG.Wait()
	close(stop)
	helperWG.Wait()
	net.DeliverAll(c)

	// Every write is resolved exactly once.
	count := 0
	for _, hs := range handles {
		for _, h := range hs {
			select {
			case <-h.Outcome():
				count++
			case <-time.After(2 * time.Second):
				t.Fatalf("seq=%d unresolved", h.Seq())
			}
		}
	}
	v, seq, ok, _ := c.Read("t")
	if !ok || v != "v" || seq != count {
		t.Fatalf("final tail read seq=%d value=%q ok=%v, want seq=%d", seq, v, ok, count)
	}
}

// TestDeterministicReplay records a scripted scenario and re-runs it twice:
// identical operations and deliveries must produce identical state.
func TestDeterministicReplay(t *testing.T) {
	run := func() (string, []bool, int) {
		logger, buf := newBufferLogger()
		net := NewTestNetwork(logger)
		c, _ := NewCoordinator([]string{"h", "m", "t"}, net, logger)
		var hs []*WriteHandle
		for _, v := range []string{"a", "b", "c", "d"} {
			h, _ := c.Write("h", v)
			hs = append(hs, h)
		}
		// Reordered, partially duplicated delivery script.
		net.Deliver(c, "h", "m", MsgWrite, 2)
		net.Duplicate(0) // duplicate currently-head message
		net.Deliver(c, "h", "m", MsgWrite, 1)
		net.Deliver(c, "m", "t", MsgWrite, 1)
		net.DeliverAll(c)
		if err := c.Fail("m"); err != nil {
			t.Fatal(err)
		}
		net.DeliverAll(c)
		outcomes := make([]bool, len(hs))
		for i, h := range hs {
			outcomes[i] = <-h.Outcome()
		}
		_, seq, _, _ := c.Read("t")
		return buf.String(), outcomes, seq
	}

	log1, out1, seq1 := run()
	log2, out2, seq2 := run()
	if seq1 != seq2 {
		t.Fatalf("tail seq mismatch: %d vs %d", seq1, seq2)
	}
	for i := range out1 {
		if out1[i] != out2[i] {
			t.Fatalf("outcome %d mismatch: %v vs %v", i, out1[i], out2[i])
		}
	}
	if log1 != log2 {
		t.Fatal("replayed logs differ; delivery/operation sequence was not deterministic")
	}
}

func TestLoggerPrintsInputsOutputsAndDecisions(t *testing.T) {
	logger, buf := newBufferLogger()
	net := NewTestNetwork(logger)
	c, _ := NewCoordinator([]string{"h", "t"}, net, logger)
	h, _ := c.Write("h", "x")
	net.DeliverAll(c)
	<-h.Outcome()
	c.Read("t")
	logText := buf.String()
	for _, want := range []string{"INPUT Write", "OUTPUT assigned seq=1", "DECISION", "COMMITTED", "INPUT Read", "REJECTED"} {
		if want == "REJECTED" {
			continue
		}
		if !bytes.Contains(buf.Bytes(), []byte(want)) {
			t.Errorf("log missing %q", want)
		}
	}
	// Trigger a rejection and verify the reject line.
	if _, _, _, err := c.Read("h"); err == nil {
		t.Fatal("expected read rejection at head")
	}
	if !bytes.Contains(buf.Bytes(), []byte("REJECTED reason=not_tail")) {
		t.Fatalf("log missing REJECTED line:\n%s", logText)
	}
}
