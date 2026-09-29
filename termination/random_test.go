package termination

import (
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

type randomResult struct {
	message      Message
	err          error
	announced    bool
	announcement Announcement
}

type randomStep struct {
	kind    int
	from    int
	to      int
	holder  int
	message Message
}

const (
	randomSend = iota
	randomDeliver
	randomIdle
	randomToken
)

type randomRun struct {
	n         int
	actives   []int
	steps     []randomStep
	results   []randomResult
	snapshots []Snapshot
}

func TestOneThousandRandomInterleavingsAndReplay(t *testing.T) {
	for seed := int64(1); seed <= 1000; seed++ {
		run := buildRandomRun(t, seed)

		replay := replayRandomRun(t, run)
		if !reflect.DeepEqual(replay.results, run.results) {
			t.Fatalf("seed %d results differ", seed)
		}
		if len(replay.snapshots) != len(run.snapshots) {
			t.Fatalf("seed %d snapshot count differs", seed)
		}
		for i := range run.snapshots {
			if !reflect.DeepEqual(replay.snapshots[i], run.snapshots[i]) {
				t.Fatalf("seed %d snapshot %d differs", seed, i)
			}
		}
		if len(run.results) == 0 || !run.results[len(run.results)-1].announced {
			t.Fatalf("seed %d did not terminate", seed)
		}

		announcementAt := -1
		for i, result := range run.results {
			if result.announced {
				if announcementAt != -1 {
					t.Fatalf("seed %d announced twice at %d and %d", seed, announcementAt, i)
				}
				announcementAt = i
			}
			if announcementAt == -1 && result.announced {
				t.Fatalf("seed %d announced unexpectedly", seed)
			}
		}
	}
}

func TestConcurrentOperations(t *testing.T) {
	const n = 4
	d, err := New(n, WithLogger(discardLogger{}), WithActiveProcesses(0, 1, 2, 3))
	if err != nil {
		t.Fatal(err)
	}

	messages := make(chan Message, 1000)
	var wait sync.WaitGroup
	for worker := 0; worker < n; worker++ {
		wait.Add(1)
		go func(process int) {
			defer wait.Done()
			for messageCount := 0; messageCount < 25; messageCount++ {
				target := (process + 1 + messageCount) % n
				message, sendErr := d.Send(process, target)
				if sendErr == nil {
					messages <- message
					continue
				}
				_ = d.BecomeIdle(process)
			}
		}(worker)
	}
	wait.Wait()
	close(messages)

	var deliveryWait sync.WaitGroup
	for message := range messages {
		deliveryWait.Add(1)
		go func(message Message) {
			defer deliveryWait.Done()
			if err := d.Deliver(message, message.To); err != nil {
				t.Error(err)
			}
			if err := d.BecomeIdle(message.To); err != nil {
				t.Error(err)
			}
		}(message)
	}

	deliveryWait.Add(1)
	go func() {
		defer deliveryWait.Done()
		for {
			snapshot := d.Snapshot()
			if snapshot.Announced {
				return
			}
			holder := snapshot.TokenHolder
			if snapshot.Processes[holder].State == Active {
				continue
			}
			_, announced, passErr := d.PassToken(holder)
			if passErr != nil {
				t.Error(passErr)
				return
			}
			if announced {
				return
			}
		}
	}()
	deliveryWait.Wait()

	assertRandomInvariant(t, 0, d.Snapshot())
	if !d.Snapshot().Announced {
		t.Fatal("concurrent run did not terminate")
	}
}

func buildRandomRun(t *testing.T, seed int64) randomRun {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	n := 2 + rng.Intn(5)
	activeCount := 1 + rng.Intn(n)
	actives := rng.Perm(n)[:activeCount]

	run := randomRun{n: n, actives: actives}
	logger := discardLogger{}
	d, err := New(n, WithLogger(logger), WithActiveProcesses(actives...))
	if err != nil {
		t.Fatal(err)
	}

	pending := []Message{}
	sentRemaining := 1 + rng.Intn(8)
	maxSteps := 1000

	for step := 0; step < maxSteps && !d.Snapshot().Announced; step++ {
		snapshot := d.Snapshot()
		step := randomStep{}

		switch {
		case sentRemaining > 0 && hasActiveProcess(snapshot):
			step.kind = randomSend
			step.from = randomActiveProcess(rng, snapshot)
			step.to = rng.Intn(n - 1)
			if step.to >= step.from {
				step.to++
			}
			if rng.Intn(3) == 0 {
				sentRemaining--
			}
		case len(pending) > 0:
			index := rng.Intn(len(pending))
			step.kind = randomDeliver
			step.message = pending[index]
			step.to = step.message.To
			pending = append(pending[:index], pending[index+1:]...)
		case hasActiveProcess(snapshot):
			step.kind = randomIdle
			step.from = randomActiveProcess(rng, snapshot)
		case snapshot.TokenHolder != 0:
			step.kind = randomToken
			step.holder = snapshot.TokenHolder
		default:
			step.kind = randomToken
			step.holder = 0
		}

		result := executeRandomStep(t, d, step)
		if step.kind == randomSend && result.err == nil {
			pending = append(pending, result.message)
		}

		run.steps = append(run.steps, step)
		run.results = append(run.results, result)
		run.snapshots = append(run.snapshots, d.Snapshot())
		assertRandomInvariant(t, seed, d.Snapshot())
	}

	return run
}

func replayRandomRun(t *testing.T, run randomRun) randomRun {
	t.Helper()
	replay := randomRun{n: run.n, actives: run.actives}
	d, err := New(run.n, WithLogger(discardLogger{}), WithActiveProcesses(run.actives...))
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range run.steps {
		result := executeRandomStep(t, d, step)
		replay.results = append(replay.results, result)
		replay.snapshots = append(replay.snapshots, d.Snapshot())
	}
	return replay
}

func executeRandomStep(t *testing.T, d *Detector, step randomStep) randomResult {
	t.Helper()
	result := randomResult{}
	switch step.kind {
	case randomSend:
		result.message, result.err = d.Send(step.from, step.to)
	case randomDeliver:
		result.err = d.Deliver(step.message, step.to)
	case randomIdle:
		result.err = d.BecomeIdle(step.from)
	case randomToken:
		result.announcement, result.announced, result.err = d.PassToken(step.holder)
	default:
		t.Fatalf("unknown step kind %d", step.kind)
	}
	if result.err != nil {
		t.Fatalf("valid random step %+v failed: %v", step, result.err)
	}
	return result
}

func hasActiveProcess(snapshot Snapshot) bool {
	for _, process := range snapshot.Processes {
		if process.State == Active {
			return true
		}
	}
	return false
}

func randomActiveProcess(rng *rand.Rand, snapshot Snapshot) int {
	active := []int{}
	for i, process := range snapshot.Processes {
		if process.State == Active {
			active = append(active, i)
		}
	}
	return active[rng.Intn(len(active))]
}

func assertRandomInvariant(t *testing.T, seed int64, snapshot Snapshot) {
	t.Helper()
	var sum int64
	active := false
	for _, process := range snapshot.Processes {
		sum += process.Counter
		if process.State == Active {
			active = true
		}
	}
	if sum != int64(snapshot.PendingMessages) {
		t.Fatalf("seed %d: counters sum %d != pending %d", seed, sum, snapshot.PendingMessages)
	}
	if snapshot.Announced && (active || snapshot.PendingMessages != 0) {
		t.Fatalf("seed %d announced with active=%v pending=%d", seed, active, snapshot.PendingMessages)
	}
}

type discardLogger struct{}

func (discardLogger) Printf(string, ...any) {}
