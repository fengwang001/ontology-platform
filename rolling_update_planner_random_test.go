package ontology

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

type naivePlanner struct {
	n, s, u int
	mr      int64
	p       int
	a, b, d int
	c       int
	stall   int
	now     int64
	batches []ReadyBatch
}

type naiveEvent struct {
	kind string
	k    int
	now  int64
}

func newNaivePlanner(config Config) *naivePlanner {
	s := ceilDiv(config.DesiredReplicas*config.MaxSurgePercent, 100)
	u := config.DesiredReplicas * config.MaxUnavailablePercent / 100
	if s == 0 && u == 0 {
		u = 1
	}
	return &naivePlanner{
		n:   config.DesiredReplicas,
		s:   s,
		u:   u,
		mr:  config.MinReadyMillis,
		p:   config.StallSteps,
		a:   config.DesiredReplicas,
		now: -1,
	}
}

func (p *naivePlanner) stable(now int64) int {
	if p.mr == 0 {
		return p.c
	}
	stable := 0
	for _, batch := range p.batches {
		if now-batch.ReadyAt >= p.mr {
			stable += batch.Count
		}
	}
	return stable
}

func (p *naivePlanner) step(now int64) StepResult {
	total := p.a + p.b + p.c + p.d
	available := p.a + p.stable(now)
	up := maxInt(0, minInt(p.n+p.s-total, p.n-(p.c+p.d)))
	r1 := p.b
	r2 := minInt(p.a, maxInt(0, available-(p.n-p.u)))

	p.d += up
	p.b -= r1
	p.a -= r2
	p.now = now

	if p.done(now) {
		p.stall = 0
	} else if up+r1+r2 > 0 {
		p.stall = 0
	} else {
		p.stall++
	}

	return StepResult{Created: up, Cleaned: r1, Reduced: r2, Stalled: p.stall >= p.p}
}

func (p *naivePlanner) newReady(k int, now int64) {
	p.d -= k
	p.c += k
	p.batches = append(p.batches, ReadyBatch{ReadyAt: now, Count: k})
	p.now = now
	p.stall = 0
}

func (p *naivePlanner) newFail(k int, now int64) {
	remaining := k
	for index := len(p.batches) - 1; index >= 0 && remaining > 0; index-- {
		removed := minInt(remaining, p.batches[index].Count)
		p.batches[index].Count -= removed
		remaining -= removed
	}

	compact := p.batches[:0]
	for _, batch := range p.batches {
		if batch.Count > 0 {
			compact = append(compact, batch)
		}
	}
	p.batches = compact

	p.c -= k
	p.d += k
	p.now = now
}

func (p *naivePlanner) oldUnready(k int, now int64) {
	p.a -= k
	p.b += k
	p.now = now
}

func (p *naivePlanner) done(now int64) bool {
	return p.a == 0 && p.b == 0 && p.c == p.n && p.stable(now) == p.n
}

func TestRandomSequencesMatchNaiveModel(t *testing.T) {
	t.Parallel()

	for iteration := 0; iteration < 2000; iteration++ {
		rng := rand.New(rand.NewSource(int64(iteration + 1)))
		config := Config{
			DesiredReplicas:       1 + rng.Intn(20),
			MaxSurgePercent:       rng.Intn(101),
			MaxUnavailablePercent: rng.Intn(101),
			MinReadyMillis:        int64(rng.Intn(5)),
			StallSteps:            1 + rng.Intn(4),
		}

		planner, err := NewRollingUpdatePlanner(config)
		if err != nil {
			t.Fatalf("iteration %d config %+v: %v", iteration, config, err)
		}
		reference := newNaivePlanner(config)
		events := make([]naiveEvent, 0, 60)
		now := int64(0)

		for eventIndex := 0; eventIndex < 60; eventIndex++ {
			kind := rng.Intn(4)
			if (kind == 1 && reference.d == 0) || (kind == 2 && reference.c == 0) || (kind == 3 && reference.a == 0) {
				kind = 0
			}
			var k int
			var event naiveEvent

			switch kind {
			case 0:
				event = naiveEvent{kind: "Step", now: now}
				beforeAvailable := reference.a + reference.stable(now)
				minimumAvailable := minInt(beforeAvailable, reference.n-reference.u)
				result, actualErr := planner.Step(now)
				want := reference.step(now)
				if result != want || actualErr != nil {
					t.Fatalf("iteration %d events %v: Step got (%+v,%v), want %+v", iteration, events, result, actualErr, want)
				}
				events = append(events, event)
				if postSnapshot := planner.Snapshot(); postSnapshot.Available < minimumAvailable {
					t.Fatalf("iteration %d available %d below minimum %d after %v", iteration, postSnapshot.Available, minimumAvailable, events)
				}
				t.Logf("iteration=%d config=%+v event=%v output=up:%d r1:%d r2:%d stalled:%t basis=total<=N+S c+d<=N A:%d>=min(%d,%d)",
					iteration, config, event, result.Created, result.Cleaned, result.Reduced, result.Stalled,
					planner.Snapshot().Available, beforeAvailable, reference.n-reference.u)
			case 1:
				if reference.d == 0 {
					k = 1
				} else {
					k = 1 + rng.Intn(reference.d)
				}
				event = naiveEvent{kind: "NewReady", k: k, now: now}
				err := planner.NewReady(k, now)
				if err != nil {
					t.Fatalf("iteration %d NewReady(%d,%d): %v", iteration, k, now, err)
				}
				reference.newReady(k, now)
				events = append(events, event)
				t.Logf("iteration=%d event=%v output=accepted basis=move d->c with readyAt=%d", iteration, event, now)
			case 2:
				if reference.c == 0 {
					k = 1
				} else {
					k = 1 + rng.Intn(reference.c)
				}
				event = naiveEvent{kind: "NewFail", k: k, now: now}
				err := planner.NewFail(k, now)
				if err != nil {
					t.Fatalf("iteration %d NewFail(%d,%d): %v", iteration, k, now, err)
				}
				reference.newFail(k, now)
				events = append(events, event)
				t.Logf("iteration=%d event=%v output=accepted basis=remove newest ready batches then c->d", iteration, event)
			case 3:
				if reference.a == 0 {
					k = 1
				} else {
					k = 1 + rng.Intn(reference.a)
				}
				event = naiveEvent{kind: "OldUnready", k: k, now: now}
				err := planner.OldUnready(k, now)
				if err != nil {
					t.Fatalf("iteration %d OldUnready(%d,%d): %v", iteration, k, now, err)
				}
				reference.oldUnready(k, now)
				events = append(events, event)
				t.Logf("iteration=%d event=%v output=accepted basis=move a->b without consuming unavailable budget", iteration, event)
			}

			assertNaiveMatch(t, iteration, planner, reference, events)
			if rng.Intn(3) == 0 {
				now += int64(rng.Intn(3))
			}
		}

		final := planner.Snapshot()
		t.Logf("iteration=%d final state=%+v done=%t events=%v", iteration, final, planner.Done(), events)
	}
}

func TestRandomInvalidEventsAreRejectedAtomically(t *testing.T) {
	t.Parallel()

	for iteration := 0; iteration < 100; iteration++ {
		rng := rand.New(rand.NewSource(int64(2000 + iteration)))
		config := Config{
			DesiredReplicas:       1 + rng.Intn(10),
			MaxSurgePercent:       rng.Intn(101),
			MaxUnavailablePercent: rng.Intn(101),
			MinReadyMillis:        int64(rng.Intn(5)),
			StallSteps:            1 + rng.Intn(4),
		}
		planner, _ := NewRollingUpdatePlanner(config)
		reference := newNaivePlanner(config)
		now := int64(0)

		if _, err := planner.Step(now); err != nil {
			t.Fatalf("Step() error = %v", err)
		}
		reference.step(now)
		if rng.Intn(2) == 0 {
			now++
		}

		before := planner.Snapshot()
		invalid := []naiveEvent{
			{kind: "Step", now: -1},
			{kind: "Step", now: reference.now - 1},
			{kind: "NewReady", k: 0, now: now},
			{kind: "NewReady", k: reference.d + 1, now: now},
			{kind: "NewReady", k: 1, now: reference.now - 1},
			{kind: "NewFail", k: reference.c + 1, now: now},
			{kind: "OldUnready", k: reference.a + 1, now: now},
		}
		event := invalid[rng.Intn(len(invalid))]

		var err error
		switch event.kind {
		case "Step":
			_, err = planner.Step(event.now)
		case "NewReady":
			err = planner.NewReady(event.k, event.now)
		case "NewFail":
			err = planner.NewFail(event.k, event.now)
		case "OldUnready":
			err = planner.OldUnready(event.k, event.now)
		}
		if err == nil {
			t.Fatalf("iteration %d invalid event %+v succeeded", iteration, event)
		}
		t.Logf("iteration=%d event=%v output=%v basis=invalid|regression|out-of-range checked before mutation", iteration, event, err)

		after := planner.Snapshot()
		if !snapshotsEqual(before, after) {
			t.Fatalf("iteration %d rejected event %+v changed state: before %+v after %+v", iteration, event, before, after)
		}
	}
}

func TestConcurrentOperationsAreSerializable(t *testing.T) {
	t.Parallel()

	planner, _ := NewRollingUpdatePlanner(Config{
		DesiredReplicas:       100,
		MaxSurgePercent:       100,
		MaxUnavailablePercent: 50,
		MinReadyMillis:        0,
		StallSteps:            3,
	})

	var waitGroup sync.WaitGroup
	var resultsMu sync.Mutex
	var results []StepResult
	for worker := 0; worker < 16; worker++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			for index := 0; index < 100; index++ {
				result, err := planner.Step(0)
				if err != nil {
					t.Errorf("Step() error = %v", err)
					return
				}
				resultsMu.Lock()
				results = append(results, result)
				resultsMu.Unlock()
				snapshot := planner.Snapshot()
				if snapshot.OldReady+snapshot.OldUnready+snapshot.NewReady+snapshot.NewUnready > 200 {
					t.Errorf("total exceeds N+S: %+v", snapshot)
					return
				}
				if snapshot.NewReady+snapshot.NewUnready > 100 {
					t.Errorf("new replicas exceed N: %+v", snapshot)
					return
				}
			}
		}()
	}
	waitGroup.Wait()

	if len(results) != 1600 {
		t.Fatalf("results length = %d, want 1600", len(results))
	}
	created := 0
	reduced := 0
	for _, result := range results {
		created += result.Created
		reduced += result.Reduced
	}
	if created != 100 || reduced != 50 {
		t.Fatalf("aggregate created/reduced = %d/%d, want 100/50", created, reduced)
	}
}

func assertNaiveMatch(t *testing.T, iteration int, planner *RollingUpdatePlanner, reference *naivePlanner, events []naiveEvent) {
	t.Helper()

	snapshot := planner.Snapshot()
	stable := reference.stable(reference.now)
	if snapshot.OldReady != reference.a || snapshot.OldUnready != reference.b ||
		snapshot.NewUnready != reference.d || snapshot.NewReady != reference.c ||
		snapshot.StallCount != reference.stall || snapshot.StableReady != stable ||
		snapshot.Available != reference.a+stable || planner.Done() != reference.done(reference.now) {
		t.Fatalf("iteration %d mismatch after %v: snapshot=%+v reference=a:%d b:%d c:%d d:%d cs:%d stall:%d",
			iteration, events, snapshot, reference.a, reference.b, reference.c, reference.d, stable, reference.stall)
	}

	batchSum := 0
	for _, batch := range snapshot.ReadyBatches {
		batchSum += batch.Count
	}
	if batchSum != snapshot.NewReady {
		t.Fatalf("iteration %d batch sum %d != c %d after %v", iteration, batchSum, snapshot.NewReady, events)
	}
	if snapshot.NewReady+snapshot.NewUnready > reference.n {
		t.Fatalf("iteration %d c+d exceeds N after %v: %+v", iteration, events, snapshot)
	}
	if snapshot.OldReady+snapshot.OldUnready+snapshot.NewReady+snapshot.NewUnready > reference.n+reference.s {
		t.Fatalf("iteration %d total exceeds N+S after %v: %+v", iteration, events, snapshot)
	}

	description := fmt.Sprintf("iteration=%d events=%v snapshot=%+v", iteration, events, snapshot)
	_ = description
}
