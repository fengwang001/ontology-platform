package vvsync

import (
	"bytes"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"
)

func newTestCluster(t *testing.T, maxLog int) *Cluster {
	t.Helper()
	c := NewCluster(maxLog)
	c.SetLogWriter(&bytes.Buffer{})
	return c
}

func mustRegister(t *testing.T, c *Cluster, id string) *Replica {
	t.Helper()
	r, err := c.Register(id)
	if err != nil {
		t.Fatalf("register %s: %v", id, err)
	}
	return r
}

func reasonOf(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error with reason %s, got nil", want)
	}
	se, ok := err.(*SyncError)
	if !ok {
		t.Fatalf("expected *SyncError, got %T", err)
	}
	if se.Reason != want {
		t.Fatalf("expected reason %s, got %s (%v)", want, se.Reason, err)
	}
}

func canonical(cs []Change) []Change {
	out := copyChanges(cs)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Origin != out[j].Origin {
			return out[i].Origin < out[j].Origin
		}
		return out[i].Seq < out[j].Seq
	})
	return out
}

func changesEqual(a, b []Change) bool {
	return reflect.DeepEqual(canonical(a), canonical(b))
}

// TestMinimalDiff: only changes with seq above the target's vector value per
// origin are sent; a second sync sends nothing.
func TestMinimalDiff(t *testing.T) {
	c := newTestCluster(t, 1000)
	a := mustRegister(t, c, "a")
	b := mustRegister(t, c, "b")

	mustWrite(t, a, "k1", "a1", 1)
	mustWrite(t, b, "k2", "b1", 2)
	mustWrite(t, a, "k1", "a2", 3)
	mustWrite(t, a, "k3", "a3", 4)

	sent, err := c.SyncOnce(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(sent) != 3 {
		t.Fatalf("expected 3 changes in minimal diff, got %d: %v", len(sent), sent)
	}
	// Ordering by (origin, seq).
	for i := 1; i < len(sent); i++ {
		if sent[i-1].Origin > sent[i].Origin ||
			(sent[i-1].Origin == sent[i].Origin && sent[i-1].Seq >= sent[i].Seq) {
			t.Fatalf("diff not ordered by (origin, seq): %v", sent)
		}
	}

	// Re-sync must be a true no-op: zero changes, identical state.
	beforeVec, beforeLog := b.Snapshot()
	sent2, err := c.SyncOnce(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(sent2) != 0 {
		t.Fatalf("expected empty diff on second sync, got %v", sent2)
	}
	afterVec, afterLog := b.Snapshot()
	if !reflect.DeepEqual(beforeVec, afterVec) || !changesEqual(beforeLog, afterLog) {
		t.Fatalf("idempotent sync changed state: %v/%v vs %v/%v", beforeVec, beforeLog, afterVec, afterLog)
	}
}

func mustWrite(t *testing.T, r *Replica, key, value string, ts int64) Change {
	t.Helper()
	ch, err := r.Write(key, value, ts)
	if err != nil {
		t.Fatalf("write on %s: %v", r.id, err)
	}
	return ch
}

// TestUnknownSourceNoVector: applying changes from an origin the target has
// never seen works (it is a registered origin), but origins that never
// produce changes never appear in any vector.
func TestUnknownSourceNoVector(t *testing.T) {
	c := newTestCluster(t, 1000)
	a := mustRegister(t, c, "a")
	b := mustRegister(t, c, "b")
	silent := mustRegister(t, c, "silent")

	mustWrite(t, a, "k", "v", 1)
	if _, err := c.SyncOnce(a, b); err != nil {
		t.Fatal(err)
	}
	v, _ := b.Snapshot()
	if _, ok := v["silent"]; ok {
		t.Fatalf("silent origin must not appear in vector: %v", v)
	}
	if len(v) != 1 || v["a"] != 1 {
		t.Fatalf("unexpected vector: %v", v)
	}

	// A vector presented from a peer must not introduce silent origins either;
	// zero entries are illegal.
	if err := c.ValidateVector(VersionVector{"silent": 0}); err == nil {
		t.Fatal("zero vector entry must be rejected")
	}
	_ = silent
}

// TestReadViewWinner: lexicographically largest (timestamp, origin, seq).
func TestReadViewWinner(t *testing.T) {
	c := newTestCluster(t, 1000)
	a := mustRegister(t, c, "a")
	b := mustRegister(t, c, "b")

	mustWrite(t, a, "k", "old", 1)
	mustWrite(t, b, "k", "newer-ts", 5)
	mustWrite(t, a, "other", "x", 2)

	if err := c.SyncBoth(a, b); err != nil {
		t.Fatal(err)
	}
	for _, r := range []*Replica{a, b} {
		view := r.View()
		if view["k"] != "newer-ts" || view["other"] != "x" {
			t.Fatalf("replica %s bad view: %v", r.id, view)
		}
	}

	// Same timestamp: larger origin name wins.
	mustWrite(t, a, "tie", "from-a", 9)
	mustWrite(t, b, "tie", "from-b", 9)
	if err := c.SyncBoth(a, b); err != nil {
		t.Fatal(err)
	}
	if v := a.View()["tie"]; v != "from-b" {
		t.Fatalf("tie-break by origin: want from-b, got %q", v)
	}
}

// TestRejectionLeavesNoTrace: every illegal rejection class leaves both
// vector and log exactly as they were.
func TestRejectionLeavesNoTrace(t *testing.T) {
	t.Run("unknown replica cannot sync", func(t *testing.T) {
		c1 := newTestCluster(t, 1000)
		c2 := newTestCluster(t, 1000)
		a := mustRegister(t, c1, "a")
		rogue := mustRegister(t, c2, "a") // same id, different cluster
		b := mustRegister(t, c1, "b")
		mustWrite(t, a, "k", "v", 1)
		mustWrite(t, b, "k", "v", 1)
		snap(t, b)
		_, err := c1.SyncOnce(rogue, b)
		reasonOf(t, err, ReasonUnknownReplica)
		_, err = c1.SyncOnce(a, rogue)
		reasonOf(t, err, ReasonUnknownReplica)
	})

	t.Run("change from unregistered origin", func(t *testing.T) {
		c := newTestCluster(t, 1000)
		b := mustRegister(t, c, "b")
		before := snapshotState(t, b)
		b.mu.Lock()
		err := validateAndApplyLocked(b, []Change{{Origin: "ghost", Seq: 1, Key: "k", Value: "v"}}, c.maxLog)
		b.mu.Unlock()
		reasonOf(t, err, ReasonUnknownReplica)
		assertUnchanged(t, b, before)
	})

	t.Run("gap in sequence", func(t *testing.T) {
		c := newTestCluster(t, 1000)
		a := mustRegister(t, c, "a")
		b := mustRegister(t, c, "b")
		mustWrite(t, a, "k1", "v1", 1)
		mustWrite(t, a, "k2", "v2", 2)
		// b has a:1; a batch jumping straight to a:3 must be rejected.
		b.mu.Lock()
		err := validateAndApplyLocked(b, []Change{{Origin: "a", Seq: 1, Key: "k1", Value: "v1"}}, c.maxLog)
		b.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		before := snapshotState(t, b)
		b.mu.Lock()
		err = validateAndApplyLocked(b, []Change{{Origin: "a", Seq: 3, Key: "k2", Value: "v2"}}, c.maxLog)
		b.mu.Unlock()
		reasonOf(t, err, ReasonNonContiguous)
		assertUnchanged(t, b, before)
	})

	t.Run("duplicate and out of order", func(t *testing.T) {
		c := newTestCluster(t, 1000)
		a := mustRegister(t, c, "a")
		b := mustRegister(t, c, "b")
		mustWrite(t, a, "k1", "v1", 1)
		mustWrite(t, a, "k2", "v2", 2)
		// Deliver a:2 then a:1 within one batch.
		before := snapshotState(t, b)
		b.mu.Lock()
		err := validateAndApplyLocked(b, []Change{
			{Origin: "a", Seq: 2, Key: "k2", Value: "v2"},
			{Origin: "a", Seq: 1, Key: "k1", Value: "v1"},
		}, c.maxLog)
		b.mu.Unlock()
		reasonOf(t, err, ReasonNonContiguous)
		assertUnchanged(t, b, before)
	})

	t.Run("invalid vector", func(t *testing.T) {
		c := newTestCluster(t, 1000)
		a := mustRegister(t, c, "a")
		mustWrite(t, a, "k", "v", 1)
		cases := []VersionVector{
			{"a": -1},
			{"a": 0},
			{"ghost": 1},
			{"a": 99},
		}
		for i, v := range cases {
			if err := c.ValidateVector(v); err == nil {
				t.Fatalf("case %d: expected rejection for %v", i, v)
			} else if se, _ := err.(*SyncError); se.Reason != ReasonInvalidVector {
				t.Fatalf("case %d: reason = %s, want invalid_version_vector", i, se.Reason)
			}
		}
	})

	t.Run("log limit on write and sync batch", func(t *testing.T) {
		c := newTestCluster(t, 2)
		a := mustRegister(t, c, "a")
		b := mustRegister(t, c, "b")
		mustWrite(t, a, "k1", "v1", 1)
		mustWrite(t, a, "k2", "v2", 2)
		mustWrite(t, b, "keep", "x", 1) // b already has 1 entry
		before := snapshotState(t, b)
		_, err := c.SyncOnce(a, b) // batch of 2 would yield 3 > limit 2
		reasonOf(t, err, ReasonLogLimit)
		assertUnchanged(t, b, before)

		// Local write beyond the limit is also rejected without consuming a seq.
		_, err = a.Write("k3", "v3", 3)
		reasonOf(t, err, ReasonLogLimit)
		vec, log := a.Snapshot()
		if vec["a"] != 2 || len(log) != 2 {
			t.Fatalf("rejected write consumed sequence: vec=%v log=%d", vec, len(log))
		}
	})
}

type state struct {
	vec VersionVector
	log []Change
}

func snapshotState(t *testing.T, r *Replica) state {
	t.Helper()
	v, l := r.Snapshot()
	return state{vec: v, log: canonical(l)}
}

func snap(t *testing.T, r *Replica) state { return snapshotState(t, r) }

func assertUnchanged(t *testing.T, r *Replica, before state) {
	t.Helper()
	v, l := r.Snapshot()
	after := state{vec: v, log: canonical(l)}
	if !reflect.DeepEqual(before.vec, after.vec) || !reflect.DeepEqual(before.log, after.log) {
		t.Fatalf("state changed after rejection:\nbefore vec=%v log=%v\nafter  vec=%v log=%v",
			before.vec, before.log, after.vec, after.log)
	}
}

// naiveFullSync is the reference implementation: the source sends its ENTIRE
// log every time; the target validates against the same contiguity rules and
// applies what is missing. The final state must match the incremental diff.
func naiveFullSync(t *testing.T, c *Cluster, source, target *Replica) {
	t.Helper()
	source.mu.Lock()
	full := copyChanges(source.log)
	targetVec := copyVector(target.vector)
	source.mu.Unlock()
	// Naive protocol: the ENTIRE log is shipped every time. The receiver
	// still discards entries it has already seen (it cannot apply them twice)
	// before running the same validation as the incremental path.
	batch := make([]Change, 0, len(full))
	for _, ch := range full {
		if ch.Seq > targetVec[ch.Origin] {
			batch = append(batch, ch)
		}
	}
	sort.SliceStable(batch, func(i, j int) bool {
		if batch[i].Origin != batch[j].Origin {
			return batch[i].Origin < batch[j].Origin
		}
		return batch[i].Seq < batch[j].Seq
	})
	// Simulate wire transport into a fresh cluster world: target validates
	// membership via its cluster, so reuse the same validation path.
	target.mu.Lock()
	defer target.mu.Unlock()
	if err := validateAndApplyLocked(target, batch, c.maxLog); err != nil {
		t.Fatalf("naive sync %s->%s: %v", source.id, target.id, err)
	}
}

func replicateSetup(t *testing.T, n, writesPerReplica int) (*Cluster, []*Replica) {
	c := newTestCluster(t, n*writesPerReplica*2)
	replicas := make([]*Replica, n)
	for i := range replicas {
		id := fmt.Sprintf("r%02d", i)
		replicas[i] = mustRegister(t, c, id)
	}
	for i, r := range replicas {
		for j := 0; j < writesPerReplica; j++ {
			ts := int64(i*100 + j)
			mustWrite(t, r, fmt.Sprintf("key-%d", j%5), fmt.Sprintf("%s-v%d", r.id, j), ts)
		}
	}
	return c, replicas
}

func assertAllConverged(t *testing.T, c *Cluster, replicas []*Replica) {
	t.Helper()
	var wantVec VersionVector
	var wantLog []Change
	var wantView map[string]string
	for i, r := range replicas {
		vec, log := r.Snapshot()
		view := r.View()
		if i == 0 {
			wantVec, wantLog, wantView = vec, canonical(log), view
			continue
		}
		if !reflect.DeepEqual(wantVec, vec) {
			t.Fatalf("vector mismatch on %s: %v vs %v", r.id, vec, wantVec)
		}
		if !reflect.DeepEqual(wantLog, canonical(log)) {
			t.Fatalf("log mismatch on %s:\n%v\nvs\n%v", r.id, canonical(log), wantLog)
		}
		if !reflect.DeepEqual(wantView, view) {
			t.Fatalf("view mismatch on %s: %v vs %v", r.id, view, wantView)
		}
	}
	// Every registered origin must be represented and every log entry covered.
	total := 0
	for _, n := range wantVec {
		total += int(n)
	}
	if total != len(wantLog) {
		t.Fatalf("vector sums to %d but log has %d", total, len(wantLog))
	}
}

// TestConvergenceRandomOrders: any set of replicas, after pairwise bidirectional
// syncs in random (possibly repeated) orders that form a connected schedule,
// converge to identical vectors, logs and views.
func TestConvergenceRandomOrders(t *testing.T) {
	for seed := int64(0); seed < 20; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			c, replicas := replicateSetup(t, 5, 6)
			rng := rand.New(rand.NewSource(seed))
			// Repeat random bidirectional edges enough to guarantee propagation.
			for round := 0; round < 40; round++ {
				i := rng.Intn(len(replicas))
				j := rng.Intn(len(replicas) - 1)
				if j >= i {
					j++
				}
				if err := c.SyncBoth(replicas[i], replicas[j]); err != nil {
					t.Fatal(err)
				}
			}
			assertAllConverged(t, c, replicas)
		})
	}
}

// TestIncrementalMatchesNaiveReference: the minimal-diff protocol and the
// naive send-everything protocol must produce identical converged state.
func TestIncrementalMatchesNaiveReference(t *testing.T) {
	setup := func(t *testing.T) (*Cluster, []*Replica) {
		return replicateSetup(t, 4, 5)
	}

	c1, r1 := setup(t)
	c2, r2 := setup(t)

	// Same fixed pair schedule: star around replica 0, then a ring.
	schedule := [][2]int{{0, 1}, {0, 2}, {0, 3}, {1, 2}, {2, 3}, {3, 1}}
	for _, p := range schedule {
		if err := c1.SyncBoth(r1[p[0]], r1[p[1]]); err != nil {
			t.Fatal(err)
		}
		naiveFullSync(t, c2, r2[p[0]], r2[p[1]])
		naiveFullSync(t, c2, r2[p[1]], r2[p[0]])
	}

	for i := range r1 {
		v1, l1 := r1[i].Snapshot()
		v2, l2 := r2[i].Snapshot()
		if !reflect.DeepEqual(v1, v2) {
			t.Fatalf("replica %d vector differs: %v vs %v", i, v1, v2)
		}
		if !reflect.DeepEqual(canonical(l1), canonical(l2)) {
			t.Fatalf("replica %d log differs:\ninc=%v\nnaive=%v", i, canonical(l1), canonical(l2))
		}
		if !reflect.DeepEqual(r1[i].View(), r2[i].View()) {
			t.Fatalf("replica %d view differs: %v vs %v", i, r1[i].View(), r2[i].View())
		}
	}
}

// TestConcurrentWritesAndSyncs: many goroutines writing and synchronizing must
// run cleanly under -race and still converge.
func TestConcurrentWritesAndSyncs(t *testing.T) {
	c := newTestCluster(t, 100000)
	const n = 4
	replicas := make([]*Replica, n)
	for i := range replicas {
		replicas[i] = mustRegister(t, c, fmt.Sprintf("c%d", i))
	}
	var wg sync.WaitGroup
	for i, r := range replicas {
		wg.Add(1)
		go func(id int, rep *Replica) {
			defer wg.Done()
			for j := 0; j < 30; j++ {
				if _, err := rep.Write(fmt.Sprintf("k%d", j), fmt.Sprintf("%d-%d", id, j), int64(j)); err != nil {
					t.Errorf("write: %v", err)
					return
				}
			}
		}(i, r)
	}
	for s := 0; s < 6; s++ {
		for i := 0; i < n; i++ {
			for j := i + 1; j < n; j++ {
				wg.Add(1)
				go func(a, b int) {
					defer wg.Done()
					if err := c.SyncBoth(replicas[a], replicas[b]); err != nil {
						t.Errorf("sync: %v", err)
					}
				}(i, j)
			}
		}
	}
	wg.Wait()
	// Final full exchange until convergence deterministically.
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if err := c.SyncBoth(replicas[i], replicas[j]); err != nil {
				t.Fatal(err)
			}
		}
	}
	assertAllConverged(t, c, replicas)
}

// TestDecisionLog: the decision log records the target vector, exactly which
// changes were sent, and the basis for the decision.
func TestDecisionLog(t *testing.T) {
	var buf bytes.Buffer
	c := NewCluster(1000)
	c.SetLogWriter(&buf)
	a := mustRegister(t, c, "a")
	b := mustRegister(t, c, "b")
	mustWrite(t, a, "k1", "v1", 1)
	if _, err := c.SyncOnce(a, b); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"[sync-begin] source=a target=b",
		"targetVector={}",
		"[sync-diff]",
		"sent=1",
		"(a,1,\"k1\"=\"v1\"@1)",
		"seq > target vector value per origin",
		"[sync-accept]",
	} {
		if !bytes.Contains(buf.Bytes(), []byte(want)) {
			t.Fatalf("decision log missing %q\n--- log ---\n%s", want, out)
		}
	}
}
