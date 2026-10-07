package ontology

import (
	"runtime"
	"sync"
	"testing"
)

func internalConfig() Config {
	return Config{
		ObjectTypes: map[string]ObjectType{
			"Person": {Name: "Person"},
			"Org":    {Name: "Org"},
		},
		LinkTypes: map[string]LinkType{
			"member": {
				Name:       "member",
				SourceType: "Person",
				TargetType: "Org",
				MaxSource:  2,
				MaxTarget:  -1,
			},
		},
	}
}

func instID(i int) ID { return ID("i" + itoa(i)) }

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	p := len(buf)
	for i > 0 {
		p--
		buf[p] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[p:])
}

// TestCompetingBatchesExactlyOneWins constructs the exact adversarial
// interleaving: batch A holds alice's lock at version 1, batch B is parked
// waiting for that same lock. A commits (bumping alice to 2), then B is
// released and must be rejected on the joint version gate. No scheduling
// run is involved; the result is therefore deterministic.
func TestCompetingBatchesExactlyOneWins(t *testing.T) {
	s := New(internalConfig())
	if err := s.CreateInstance("alice", "Person"); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateInstance("bob", "Person"); err != nil {
		t.Fatal(err)
	}

	acquired := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})
	var once sync.Once
	s.testHookAcquired = func(ids []ID) {
		for _, id := range ids {
			if id == "alice" {
				once.Do(func() { close(acquired) })
				<-release
			}
		}
	}

	batchA := Batch{
		ID:            "A",
		Preconditions: []Precondition{{Instance: "alice", ExpectedVersion: 1}},
		Ops:           []Op{{Kind: OpSetAttr, Instance: "alice", Attr: Attr("a"), Value: 1}},
	}
	batchB := Batch{
		ID: "B",
		Preconditions: []Precondition{
			{Instance: "alice", ExpectedVersion: 1},
			{Instance: "bob", ExpectedVersion: 1},
		},
		Ops: []Op{{Kind: OpSetAttr, Instance: "bob", Attr: Attr("b"), Value: 2}},
	}

	type outcome struct{ r Result }
	resA := make(chan outcome, 1)
	resB := make(chan outcome, 1)
	go func() {
		resA <- outcome{s.Commit(batchA)}
		close(finished)
	}()
	<-acquired
	s.testHookAcquired = nil

	bStarted := make(chan struct{})
	go func() {
		close(bStarted)
		resB <- outcome{s.Commit(batchB)}
	}()
	<-bStarted
	// Give B time to block on alice's mutex.
	runtime.Gosched()
	runtime.Gosched()

	close(release)
	<-finished
	// A is done; B must now run to completion without a deadlock.
	a := <-resA
	b := <-resB
	resAResult, resBResult := a.r, b.r

	if resAResult.Status != StatusCommitted {
		t.Fatalf("A = %+v, want committed", resAResult)
	}
	if resBResult.Status != StatusVersionMismatch {
		t.Fatalf("B = %+v, want version_mismatch", resBResult)
	}
	if len(resBResult.Mismatches) != 1 || resBResult.Mismatches[0].Instance != "alice" ||
		resBResult.Mismatches[0].Expected != 1 || resBResult.Mismatches[0].Actual != 2 {
		t.Fatalf("B mismatch evidence = %+v", resBResult.Mismatches)
	}
	alice, _ := s.Get("alice")
	bob, _ := s.Get("bob")
	if alice.Version != 2 {
		t.Fatalf("alice = %d, want 2", alice.Version)
	}
	if bob.Version != 1 {
		t.Fatalf("bob = %d, want 1 (rejected B must not touch bob)", bob.Version)
	}
	if _, ok := bob.Attrs["b"]; ok {
		t.Fatal("rejected B mutated bob's attributes")
	}
}

// TestManyConcurrentSameVersionContest fires many overlapping batches that
// all declare the same starting precondition version for the same instance.
// Across every contest exactly one must commit and each contest advances the
// instance by exactly one version; losers leave no trace.
func TestManyConcurrentSameVersionContest(t *testing.T) {
	s := New(internalConfig())
	const n = 6
	for i := 0; i < n; i++ {
		if err := s.CreateInstance(instID(i), "Person"); err != nil {
			t.Fatal(err)
		}
	}

	const contests = 30
	const contenders = 6
	for round := 0; round < contests; round++ {
		expect := uint64(1 + round)
		start := make(chan struct{})
		var wg sync.WaitGroup
		wins := make(chan Status, n*contenders)
		for i := 0; i < n; i++ {
			for c := 0; c < contenders; c++ {
				wg.Add(1)
				id := instID(i)
				tag := round*100 + c
				go func() {
					defer wg.Done()
					<-start
					r := s.Commit(Batch{
						ID:            "b-" + itoa(tag) + "-" + string(id),
						Preconditions: []Precondition{{Instance: id, ExpectedVersion: expect}},
						Ops:           []Op{{Kind: OpSetAttr, Instance: id, Attr: Attr("round"), Value: expect}},
					})
					wins <- r.Status
				}()
			}
		}
		close(start)
		wg.Wait()
		close(wins)
		committed := 0
		for st := range wins {
			if st == StatusCommitted {
				committed++
			}
		}
		if committed != n {
			t.Fatalf("round %d: %d commits, want exactly %d (one per instance)", round, committed, n)
		}
	}

	for i := 0; i < n; i++ {
		snap, ok := s.Get(instID(i))
		if !ok {
			t.Fatalf("instance %d missing", i)
		}
		if snap.Version != uint64(1+contests) {
			t.Fatalf("instance %d version = %d, want %d", i, snap.Version, 1+contests)
		}
		if snap.Attrs[Attr("round")] != uint64(contests) {
			t.Fatalf("instance %d attr = %v, want %d", i, snap.Attrs["round"], contests)
		}
	}

	orders := map[uint64]struct{}{}
	for _, rec := range s.Journal().Records() {
		if rec.Kind != "batch" {
			continue
		}
		if _, dup := orders[rec.Order]; dup {
			t.Fatalf("duplicate journal order %d", rec.Order)
		}
		orders[rec.Order] = struct{}{}
	}
}
