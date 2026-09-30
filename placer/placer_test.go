package placer

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

func mustNew(t *testing.T, nodes []Node) *Placer {
	t.Helper()
	p, err := NewPlacer(nodes)
	if err != nil {
		t.Fatalf("NewPlacer unexpected error: %v", err)
	}
	return p
}

func assertUsage(t *testing.T, p *Placer, id string, cpu, mem int64) {
	t.Helper()
	gotCPU, gotMem, ok := p.NodeUsage(id)
	if !ok {
		t.Fatalf("node %s missing", id)
	}
	if gotCPU != cpu || gotMem != mem {
		t.Fatalf("node %s usage = (%d,%d), want (%d,%d)", id, gotCPU, gotMem, cpu, mem)
	}
}

// Request (20,100):
//
//	A (100,100): after ratios (.8, 0) -> max .8, sum .8
//	B (40,200):  after ratios (.5,.5) -> max .5, sum 1.0
//
// max-of-ratios picks B; a sum-based score would pick A.
func TestScoreUsesMaxRatioNotSum(t *testing.T) {
	p := mustNew(t, []Node{
		{ID: "A", CPU: 100, Memory: 100},
		{ID: "B", CPU: 40, Memory: 200},
	})
	if err := p.Place(Job{ID: "j", Replicas: 1, CPU: 20, Memory: 100, MaxPerNode: 1}); err != nil {
		t.Fatalf("Place: %v", err)
	}
	nodes, _ := p.JobNodes("j")
	if nodes[0] != "B" {
		t.Fatalf("max-ratio score should pick B, got %v", nodes)
	}
	assertUsage(t, p, "A", 0, 0)
	assertUsage(t, p, "B", 20, 100)
}

// Equal scores break by ascending node ID. Equal ratios with different
// denominators are compared exactly via big-int cross products.
func TestTieBreakByNodeID(t *testing.T) {
	p := mustNew(t, []Node{
		{ID: "zeta", CPU: 10, Memory: 10},
		{ID: "alpha", CPU: 10, Memory: 10},
	})
	if err := p.Place(Job{ID: "j", Replicas: 1, CPU: 3, Memory: 3, MaxPerNode: 1}); err != nil {
		t.Fatalf("Place: %v", err)
	}
	nodes, _ := p.JobNodes("j")
	if nodes[0] != "alpha" {
		t.Fatalf("tie should resolve to alpha, got %v", nodes)
	}

	const m = int64(3_000_000_000_000_000_000)
	q := mustNew(t, []Node{
		{ID: "big2", CPU: 2 * m, Memory: 2 * m},
		{ID: "big1", CPU: m, Memory: m},
	})
	// Remaining-ratio after placing m/3 is 2/3 on both; cross products are
	// ~3.6e37, beyond float exact comparison, so big.Int is required.
	if err := q.Place(Job{ID: "g", Replicas: 1, CPU: m / 3, Memory: m / 3, MaxPerNode: 1}); err != nil {
		t.Fatalf("Place: %v", err)
	}
	got, _ := q.JobNodes("g")
	if got[0] != "big1" {
		t.Fatalf("exact-ratio tie should resolve by id, got %v", got)
	}
}

// The second replica sees the residual left by the first: n1 is best for
// replica 0 and is then exhausted, so replica 1 must go to n2.
func TestLaterReplicasSeeUpdatedRemaining(t *testing.T) {
	p := mustNew(t, []Node{
		{ID: "n1", CPU: 10, Memory: 10}, // score .4 after (6,6)
		{ID: "n2", CPU: 12, Memory: 6},  // score max(.5,0)=.5
	})
	if err := p.Place(Job{ID: "j", Replicas: 2, CPU: 6, Memory: 6, MaxPerNode: 1}); err != nil {
		t.Fatalf("Place: %v", err)
	}
	nodes, _ := p.JobNodes("j")
	if len(nodes) != 2 || nodes[0] != "n1" || nodes[1] != "n2" {
		t.Fatalf("placement = %v, want [n1 n2]", nodes)
	}
	assertUsage(t, p, "n1", 6, 6)
	assertUsage(t, p, "n2", 6, 6)
}

func TestMaxPerNodeLimit(t *testing.T) {
	p := mustNew(t, []Node{
		{ID: "n1", CPU: 100, Memory: 100},
		{ID: "n2", CPU: 100, Memory: 100},
	})
	// k=4, a=2 -> exactly two per node even though one node has room.
	if err := p.Place(Job{ID: "j", Replicas: 4, CPU: 10, Memory: 10, MaxPerNode: 2}); err != nil {
		t.Fatalf("Place: %v", err)
	}
	snap := p.Snapshot()
	if snap["n1"]["j"] != 2 || snap["n2"]["j"] != 2 {
		t.Fatalf("snapshot = %v, want 2 replicas per node", snap)
	}

	// k=3, a=1 on two nodes -> permanent: k > a*n (3 > 1*2) even though
	// the cluster currently has ample free capacity.
	err := p.Place(Job{ID: "g", Replicas: 3, CPU: 1, Memory: 1, MaxPerNode: 1})
	if err != ErrPermanentlyInfeasible {
		t.Fatalf("want ErrPermanentlyInfeasible, got %v", err)
	}
	if _, ok := p.JobNodes("g"); ok {
		t.Fatal("rejected job must not be recorded")
	}
}

// Greedy failure rolls back every tentative replica.
//
// n1,n2 = (10,10), request (6,6), k=3, a=3:
// r0 -> n1 (tie, ascending id); r1 -> n2 (n1 cannot fit another copy);
// r2 has no candidate. The whole job is rejected as temporarily
// insufficient and both previously chosen replicas are undone.
func TestGreedyFailureRollsBackAll(t *testing.T) {
	p := mustNew(t, []Node{
		{ID: "n1", CPU: 10, Memory: 10},
		{ID: "n2", CPU: 10, Memory: 10},
	})
	err := p.Place(Job{ID: "j", Replicas: 3, CPU: 6, Memory: 6, MaxPerNode: 3})
	if err != ErrTemporarilyInsufficient {
		t.Fatalf("want ErrTemporarilyInsufficient, got %v", err)
	}
	if _, ok := p.JobNodes("j"); ok {
		t.Fatal("rolled-back job must not be recorded")
	}
	assertUsage(t, p, "n1", 0, 0)
	assertUsage(t, p, "n2", 0, 0)

	// Permanent takes precedence over the same mid-greedy failure.
	err = p.Place(Job{ID: "g", Replicas: 5, CPU: 6, Memory: 6, MaxPerNode: 2})
	if err != ErrPermanentlyInfeasible {
		t.Fatalf("want ErrPermanentlyInfeasible, got %v", err)
	}
	assertUsage(t, p, "n1", 0, 0)
	assertUsage(t, p, "n2", 0, 0)
}

func TestPermanentNoFittingNode(t *testing.T) {
	p := mustNew(t, []Node{
		{ID: "n1", CPU: 4, Memory: 100},
		{ID: "n2", CPU: 100, Memory: 4},
	})
	err := p.Place(Job{ID: "j", Replicas: 1, CPU: 5, Memory: 5, MaxPerNode: 1})
	if err != ErrPermanentlyInfeasible {
		t.Fatalf("want ErrPermanentlyInfeasible, got %v", err)
	}
}

func TestRemoveThenReplace(t *testing.T) {
	p := mustNew(t, []Node{
		{ID: "n1", CPU: 10, Memory: 10},
		{ID: "n2", CPU: 10, Memory: 10},
	})
	job := Job{ID: "j", Replicas: 2, CPU: 6, Memory: 6, MaxPerNode: 1}
	if err := p.Place(job); err != nil {
		t.Fatalf("Place: %v", err)
	}
	if err := p.Remove("j"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	assertUsage(t, p, "n1", 0, 0)
	assertUsage(t, p, "n2", 0, 0)

	// The same job fits again after removal; removing an unknown job fails.
	if err := p.Place(job); err != nil {
		t.Fatalf("re-Place after remove: %v", err)
	}
	if err := p.Remove("missing"); err != ErrJobNotFound {
		t.Fatalf("want ErrJobNotFound, got %v", err)
	}
}

func TestDuplicatePlacementRejected(t *testing.T) {
	p := mustNew(t, []Node{{ID: "n1", CPU: 10, Memory: 10}})
	job := Job{ID: "j", Replicas: 1, CPU: 1, Memory: 1, MaxPerNode: 1}
	if err := p.Place(job); err != nil {
		t.Fatalf("Place: %v", err)
	}
	if err := p.Place(job); err != ErrDuplicateJob {
		t.Fatalf("want ErrDuplicateJob, got %v", err)
	}
}

func TestValidationErrors(t *testing.T) {
	if _, err := NewPlacer([]Node{{ID: "n1", CPU: 0, Memory: 10}}); err != ErrNodeCapacityNonPositive {
		t.Fatalf("want ErrNodeCapacityNonPositive, got %v", err)
	}
	// Node parameter errors precede duplicate-id errors.
	if _, err := NewPlacer([]Node{{ID: "x", CPU: -1, Memory: 10}, {ID: "x", CPU: 1, Memory: 1}}); err != ErrNodeCapacityNonPositive {
		t.Fatalf("node parameter error must precede duplicate: %v", err)
	}
	if _, err := NewPlacer([]Node{{ID: "x", CPU: 1, Memory: 1}, {ID: "x", CPU: 1, Memory: 1}}); err != ErrDuplicateNodeID {
		t.Fatalf("want ErrDuplicateNodeID, got %v", err)
	}

	p := mustNew(t, []Node{{ID: "n1", CPU: 10, Memory: 10}})

	// Job parameter order: k first, then request, then a.
	if err := p.Place(Job{ID: "b", Replicas: 0, CPU: 0, Memory: 0, MaxPerNode: 0}); err != ErrReplicasTooSmall {
		t.Fatalf("want ErrReplicasTooSmall, got %v", err)
	}
	if err := p.Place(Job{ID: "b", Replicas: 1, CPU: 0, Memory: 0, MaxPerNode: 0}); err != ErrRequestNonPositive {
		t.Fatalf("want ErrRequestNonPositive, got %v", err)
	}
	if err := p.Place(Job{ID: "b", Replicas: 1, CPU: 1, Memory: 1, MaxPerNode: 0}); err != ErrMaxPerNodeTooSmall {
		t.Fatalf("want ErrMaxPerNodeTooSmall, got %v", err)
	}

	// Parameter validation precedes duplicate-job detection.
	good := Job{ID: "j", Replicas: 1, CPU: 1, Memory: 1, MaxPerNode: 1}
	if err := p.Place(good); err != nil {
		t.Fatalf("Place: %v", err)
	}
	bad := good
	bad.Replicas = 0
	if err := p.Place(bad); err != ErrReplicasTooSmall {
		t.Fatalf("params must be checked before duplicate, got %v", err)
	}

	// All rejected operations left occupancy untouched.
	assertUsage(t, p, "n1", 1, 1)
}

func TestDeterministicReplay(t *testing.T) {
	nodes := []Node{
		{ID: "n3", CPU: 10, Memory: 20},
		{ID: "n1", CPU: 20, Memory: 10},
		{ID: "n2", CPU: 15, Memory: 15},
	}
	ops := []Job{
		{ID: "a", Replicas: 3, CPU: 4, Memory: 4, MaxPerNode: 2},
		{ID: "b", Replicas: 2, CPU: 6, Memory: 3, MaxPerNode: 1},
	}
	run := func() map[string]map[string]int {
		p := mustNew(t, nodes)
		for _, j := range ops {
			if err := p.Place(j); err != nil {
				t.Fatal(err)
			}
		}
		if err := p.Remove("a"); err != nil {
			t.Fatal(err)
		}
		if err := p.Place(ops[0]); err != nil {
			t.Fatal(err)
		}
		return p.Snapshot()
	}
	first := run()
	second := run()
	if !snapEqual(first, second) {
		t.Fatalf("replay mismatch:\nfirst=%v\nsecond=%v", first, second)
	}
}

func snapEqual(a, b map[string]map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for n, ja := range a {
		jb, ok := b[n]
		if !ok || len(ja) != len(jb) {
			return false
		}
		for j, v := range ja {
			if jb[j] != v {
				return false
			}
		}
	}
	return true
}

func TestConcurrentAccess(t *testing.T) {
	p := mustNew(t, []Node{
		{ID: "n1", CPU: 1000, Memory: 1000},
		{ID: "n2", CPU: 1000, Memory: 1000},
		{ID: "n3", CPU: 1000, Memory: 1000},
	})

	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			j := Job{ID: "job" + itoa(int64(i)), Replicas: 3, CPU: 2, Memory: 2, MaxPerNode: 1}
			if err := p.Place(j); err != nil {
				t.Errorf("Place %d: %v", i, err)
				return
			}
			_ = p.Snapshot()
			if nodes, ok := p.JobNodes(j.ID); !ok || len(nodes) != 3 {
				t.Errorf("job %d: expected 3 placed replicas, got %v", i, nodes)
				return
			}
			if err := p.Remove(j.ID); err != nil {
				t.Errorf("Remove %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	// After all removals every node must be back to zero occupancy.
	for _, id := range []string{"n1", "n2", "n3"} {
		assertUsage(t, p, id, 0, 0)
	}
}

func TestLogsContainInputsOutputsAndReasoning(t *testing.T) {
	var buf bytes.Buffer
	p := mustNew(t, []Node{
		{ID: "n1", CPU: 10, Memory: 10},
		{ID: "n2", CPU: 10, Memory: 10},
	})
	p.SetLogger(&buf)

	if err := p.Place(Job{ID: "j", Replicas: 2, CPU: 6, Memory: 6, MaxPerNode: 1}); err != nil {
		t.Fatalf("Place: %v", err)
	}
	if err := p.Remove("j"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	// Failing placement records the reason and classification basis.
	if err := p.Place(Job{ID: "bad", Replicas: 5, CPU: 6, Memory: 6, MaxPerNode: 1}); err != ErrPermanentlyInfeasible {
		t.Fatalf("want permanent, got %v", err)
	}

	log := buf.String()
	for _, want := range []string{
		"place job=j input={k=2 cpu=6 mem=6 a=1}",
		"pick job=j replica=0 node=n1",
		"place job=j OK output=[n1,n2]",
		"release job=j",
		"remove job=j OK",
		"classify job=bad permanent",
		"REJECT",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("log missing %q\n--- log ---\n%s", want, log)
		}
	}
}
