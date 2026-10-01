package deadlock

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// newTestCluster builds a cluster with three sites (S1,S2,S3), one lock per
// site (A@S1, B@S2, C@S3), and an in-memory audit log.
func newTestCluster(t *testing.T, seed int64) (*Cluster, *bytes.Buffer) {
	t.Helper()
	var log bytes.Buffer
	c := NewCluster(seed, &log)
	locks := map[string]string{"S1": "A", "S2": "B", "S3": "C"}
	for _, site := range []string{"S1", "S2", "S3"} {
		if err := c.AddSite(site); err != nil {
			t.Fatalf("AddSite %s: %v", site, err)
		}
		if err := c.AddLock(site, locks[site]); err != nil {
			t.Fatalf("AddLock %s@%s: %v", locks[site], site, err)
		}
	}
	return c, &log
}

func mustRequest(t *testing.T, c *Cluster, txn, site, lock string, mode Mode) {
	t.Helper()
	if err := c.Request(txn, site, lock, mode); err != nil {
		t.Fatalf("Request(%s,%s,%s,%s): %v", txn, site, lock, mode, err)
	}
}

func mustBegin(t *testing.T, c *Cluster, txn string, ts int64) {
	t.Helper()
	if err := c.Begin(txn, ts); err != nil {
		t.Fatalf("Begin(%s,%d): %v", txn, ts, err)
	}
}

func mustState(t *testing.T, c *Cluster, txn string, want TxnState) {
	t.Helper()
	got, err := c.TxnState(txn)
	if err != nil {
		t.Fatalf("TxnState(%s): %v", txn, err)
	}
	if got != want {
		t.Fatalf("TxnState(%s) = %s, want %s", txn, got, want)
	}
}

// Three sites, three transactions, one cross-site cycle T1->T2->T3->T1.
// The detector must find the real cycle and abort exactly the transaction
// with the largest start timestamp (T3).
func TestThreeSiteCycle(t *testing.T) {
	c, log := newTestCluster(t, 42)
	mustBegin(t, c, "T1", 1)
	mustBegin(t, c, "T2", 2)
	mustBegin(t, c, "T3", 3)

	mustRequest(t, c, "T1", "S1", "A", Exclusive)
	mustRequest(t, c, "T2", "S2", "B", Exclusive)
	mustRequest(t, c, "T3", "S3", "C", Exclusive)

	mustRequest(t, c, "T1", "S2", "B", Exclusive) // waits for T2
	mustRequest(t, c, "T2", "S3", "C", Exclusive) // waits for T3
	mustRequest(t, c, "T3", "S1", "A", Exclusive) // waits for T1: cycle

	c.DeliverAll()

	if got := c.Victims(); len(got) != 1 || got[0] != "T3" {
		t.Fatalf("Victims = %v, want [T3]", got)
	}
	mustState(t, c, "T3", StateAborted)
	mustState(t, c, "T1", StateWaiting) // still blocked on T2's B
	mustState(t, c, "T2", StateActive)  // granted C after T3 aborted
	if c.HasCycle() {
		t.Fatal("wait-for cycle remains after all messages delivered")
	}
	if n := c.PendingMessages(); n != 0 {
		t.Fatalf("PendingMessages = %d, want 0", n)
	}
	if !strings.Contains(log.String(), "[DEADLOCK]") {
		t.Fatal("log missing deadlock decision record")
	}
	if !strings.Contains(log.String(), "victim=T3") {
		t.Fatal("log missing victim justification")
	}
	t.Logf("audit log:\n%s", log.String())
}

// A cycle that runs through a shared lock with multiple holders: T3 waits
// for both shared holders of A, but only T1 closes the cycle. The victim
// must be on the real cycle and the uninvolved shared holder T2 must
// survive.
func TestSharedLockMultiHolderCycle(t *testing.T) {
	c, log := newTestCluster(t, 7)
	mustBegin(t, c, "T1", 1)
	mustBegin(t, c, "T2", 2)
	mustBegin(t, c, "T3", 3)

	mustRequest(t, c, "T1", "S1", "A", Shared)
	mustRequest(t, c, "T2", "S1", "A", Shared) // compatible: granted
	mustRequest(t, c, "T3", "S2", "B", Exclusive)

	mustRequest(t, c, "T3", "S1", "A", Exclusive) // waits for T1 and T2
	mustRequest(t, c, "T1", "S2", "B", Exclusive) // waits for T3: cycle T1<->T3

	c.DeliverAll()

	if got := c.Victims(); len(got) != 1 || got[0] != "T3" {
		t.Fatalf("Victims = %v, want [T3] (max startTS on cycle {T1,T3})", got)
	}
	mustState(t, c, "T2", StateActive) // uninvolved holder untouched
	mustState(t, c, "T1", StateActive) // granted B after T3 aborted
	if !c.Holds("T2", "S1", "A") {
		t.Fatal("T2 should still hold A@S1")
	}
	if c.HasCycle() {
		t.Fatal("wait-for cycle remains after delivery")
	}
	t.Logf("audit log:\n%s", log.String())
}

// A wait that is resolved while its probe is still in flight must not be
// misjudged: every in-flight probe is dropped and nobody is aborted.
func TestProbeDroppedAfterWaitResolved(t *testing.T) {
	c, log := newTestCluster(t, 1)
	mustBegin(t, c, "T1", 1)
	mustBegin(t, c, "T2", 2)

	mustRequest(t, c, "T1", "S1", "A", Exclusive)
	mustRequest(t, c, "T2", "S1", "A", Exclusive) // queued, probe T2->T1 sent
	if n := c.PendingMessages(); n == 0 {
		t.Fatal("expected an in-flight probe")
	}

	// Resolve the wait before any delivery.
	if err := c.Release("T1", "S1", "A"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	mustState(t, c, "T2", StateActive) // granted from queue

	c.DeliverAll()
	if got := c.Victims(); len(got) != 0 {
		t.Fatalf("Victims = %v, want none (wait was resolved)", got)
	}
	if !strings.Contains(log.String(), "[DROP]") {
		t.Fatal("log missing drop record for the stale probe")
	}
	if strings.Contains(log.String(), "[DEADLOCK]") {
		t.Fatal("false deadlock reported for a resolved wait")
	}
	t.Logf("audit log:\n%s", log.String())
}

// Two transactions on the same cycle both initiate probes; both probes
// report the cycle, but exactly one transaction is aborted.
func TestTwoInitiatorsSameCycleAbortOnce(t *testing.T) {
	c, log := newTestCluster(t, 99)
	mustBegin(t, c, "T1", 1)
	mustBegin(t, c, "T2", 2)

	mustRequest(t, c, "T1", "S1", "A", Exclusive)
	mustRequest(t, c, "T2", "S2", "B", Exclusive)
	mustRequest(t, c, "T1", "S2", "B", Exclusive) // T1 waits T2
	mustRequest(t, c, "T2", "S1", "A", Exclusive) // T2 waits T1: cycle

	c.DeliverAll()

	if got := c.Victims(); len(got) != 1 || got[0] != "T2" {
		t.Fatalf("Victims = %v, want exactly [T2]", got)
	}
	mustState(t, c, "T2", StateAborted)
	mustState(t, c, "T1", StateActive) // granted B after T2 aborted
	if c.HasCycle() {
		t.Fatal("cycle remains after delivery")
	}
	t.Logf("audit log:\n%s", log.String())
}

// A probe that wanders into a cycle not containing its initiator must not
// loop forever: it is dropped at the repeated transaction, while the cycle
// members' own probes still detect the deadlock.
func TestCycleWithoutInitiatorTerminates(t *testing.T) {
	c, log := newTestCluster(t, 5)
	mustBegin(t, c, "T1", 1)
	mustBegin(t, c, "T2", 2)
	mustBegin(t, c, "T3", 3)

	mustRequest(t, c, "T2", "S2", "B", Exclusive)
	mustRequest(t, c, "T3", "S3", "C", Exclusive)
	mustRequest(t, c, "T1", "S2", "B", Exclusive) // T1 -> T2 (outside the cycle)
	mustRequest(t, c, "T2", "S3", "C", Exclusive) // T2 -> T3
	mustRequest(t, c, "T3", "S2", "B", Exclusive) // T3 -> T2: cycle T2<->T3

	c.DeliverAll() // must terminate

	if got := c.Victims(); len(got) != 1 || got[0] != "T3" {
		t.Fatalf("Victims = %v, want [T3]", got)
	}
	if !strings.Contains(log.String(), "cycle without initiator") {
		t.Fatal("log missing drop record for probe in foreign cycle")
	}
	mustState(t, c, "T1", StateWaiting) // still blocked on T2, not aborted
	if c.HasCycle() {
		t.Fatal("cycle remains after delivery")
	}
	t.Logf("audit log:\n%s", log.String())
}

// FIFO discipline: a request queues whenever anyone is already queued, even
// if it is compatible with the current holders; grants follow queue order.
func TestFIFOQueueing(t *testing.T) {
	c, _ := newTestCluster(t, 3)
	mustBegin(t, c, "T1", 1)
	mustBegin(t, c, "T2", 2)
	mustBegin(t, c, "T3", 3)

	mustRequest(t, c, "T1", "S1", "A", Shared)
	mustRequest(t, c, "T2", "S1", "A", Exclusive) // incompatible: queued
	mustRequest(t, c, "T3", "S1", "A", Shared)    // compatible with holders but queued behind T2
	mustState(t, c, "T3", StateWaiting)

	if err := c.Release("T1", "S1", "A"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	mustState(t, c, "T2", StateActive) // FIFO: T2 granted first
	mustState(t, c, "T3", StateWaiting)

	if err := c.Release("T2", "S1", "A"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	mustState(t, c, "T3", StateActive)
}

// Every invalid operation is rejected with a distinguishable reason and
// leaves the lock table and wait-for graph untouched.
func TestValidationRejections(t *testing.T) {
	c, _ := newTestCluster(t, 11)
	mustBegin(t, c, "T1", 1)
	mustBegin(t, c, "T2", 2)
	mustRequest(t, c, "T1", "S1", "A", Exclusive)
	mustRequest(t, c, "T2", "S1", "A", Exclusive) // T2 now waiting

	cases := []struct {
		name string
		op   func() error
		want error
	}{
		{"unknown site", func() error { return c.Request("T1", "SX", "A", Shared) }, ErrUnknownSite},
		{"unknown lock", func() error { return c.Request("T1", "S1", "ZZ", Shared) }, ErrUnknownLock},
		{"unknown txn", func() error { return c.Request("T9", "S1", "A", Shared) }, ErrUnknownTxn},
		{"waiting txn requests", func() error { return c.Request("T2", "S2", "B", Shared) }, ErrTxnWaiting},
		{"duplicate txn id", func() error { return c.Begin("T1", 100) }, ErrDuplicateTxn},
		{"duplicate start ts", func() error { return c.Begin("T9", 2) }, ErrDuplicateStartTS},
		{"release not held", func() error { return c.Release("T2", "S1", "A") }, ErrLockNotHeld},
		{"release unknown site", func() error { return c.Release("T1", "SX", "A") }, ErrUnknownSite},
		{"end waiting txn", func() error { return c.End("T2") }, ErrTxnWaiting},
		{"end unknown txn", func() error { return c.End("T9") }, ErrUnknownTxn},
	}
	for _, tc := range cases {
		if err := tc.op(); !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.want)
		}
	}

	// Finished transactions are rejected too.
	if err := c.End("T1"); err != nil {
		t.Fatalf("End(T1): %v", err)
	}
	if err := c.Request("T1", "S2", "B", Shared); !errors.Is(err, ErrTxnFinished) {
		t.Errorf("finished txn request: got %v, want %v", err, ErrTxnFinished)
	}
	if err := c.End("T1"); !errors.Is(err, ErrTxnFinished) {
		t.Errorf("double end: got %v, want %v", err, ErrTxnFinished)
	}

	// Rejected operations changed nothing: T1's commit released A, so T2
	// must have been granted; no stray waits or holders exist.
	mustState(t, c, "T2", StateActive)
	if !c.Holds("T2", "S1", "A") {
		t.Fatal("T2 should hold A@S1 after T1 committed")
	}
	c.DeliverAll() // flush T2's stale probe; it must be dropped
	if c.HasCycle() {
		t.Fatal("unexpected cycle")
	}
	if n := c.PendingMessages(); n != 0 {
		t.Fatalf("PendingMessages = %d, want 0", n)
	}
}

// Re-requesting a held lock succeeds as a no-op.
func TestReRequestHeldLock(t *testing.T) {
	c, _ := newTestCluster(t, 13)
	mustBegin(t, c, "T1", 1)
	mustRequest(t, c, "T1", "S1", "A", Exclusive)
	mustRequest(t, c, "T1", "S1", "A", Exclusive) // already held: success
	mustRequest(t, c, "T1", "S1", "A", Shared)    // still success
	mustState(t, c, "T1", StateActive)
}

// The same operation and delivery sequence replayed with the same seed
// yields the same victim sequence.
func TestReplayDeterminism(t *testing.T) {
	run := func() []string {
		c, _ := newTestCluster(t, 2024)
		mustBegin(t, c, "T1", 1)
		mustBegin(t, c, "T2", 2)
		mustBegin(t, c, "T3", 3)
		mustRequest(t, c, "T1", "S1", "A", Exclusive)
		mustRequest(t, c, "T2", "S2", "B", Exclusive)
		mustRequest(t, c, "T3", "S3", "C", Exclusive)
		mustRequest(t, c, "T1", "S2", "B", Exclusive)
		mustRequest(t, c, "T2", "S3", "C", Exclusive)
		mustRequest(t, c, "T3", "S1", "A", Exclusive)
		c.DeliverAll()
		return c.Victims()
	}
	first := run()
	second := run()
	if len(first) != 1 || first[0] != "T3" {
		t.Fatalf("victims = %v, want [T3]", first)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("replay mismatch: %v vs %v", first, second)
	}
}

// Requests, releases, ends and deliveries may be invoked concurrently.
// Run with -race.
func TestConcurrentAccess(t *testing.T) {
	c, _ := newTestCluster(t, 77)
	const txns = 8
	for i := 0; i < txns; i++ {
		mustBegin(t, c, fmt.Sprintf("T%d", i), int64(i+1))
	}
	var wg sync.WaitGroup
	for i := 0; i < txns; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			txn := fmt.Sprintf("T%d", i)
			site := fmt.Sprintf("S%d", i%3+1)
			lock := map[string]string{"S1": "A", "S2": "B", "S3": "C"}[site]
			_ = c.Request(txn, site, lock, Exclusive)
			_ = c.Request(txn, fmt.Sprintf("S%d", (i+1)%3+1), "A", Shared)
			_ = c.Release(txn, site, lock)
			_ = c.End(txn)
		}(i)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for c.DeliverOne() {
		}
	}()
	wg.Wait()
	c.DeliverAll()
	if c.HasCycle() {
		t.Fatal("cycle remains after quiescence")
	}
}
