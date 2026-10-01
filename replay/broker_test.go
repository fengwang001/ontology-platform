package replay

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

type naiveMessage struct {
	oseq    int
	payload string
	replays int
}

type naiveDeadEntry struct {
	dseq int
	msg  naiveMessage
}

type naiveBroker struct {
	capacity   int
	maxReplays int
	nextOSeq   int
	nextDSeq   int
	queues     map[string][]naiveMessage
	dead       map[string][]naiveDeadEntry
}

func newNaiveBroker(capacity, maxReplays int, queueNames ...string) *naiveBroker {
	broker := &naiveBroker{
		capacity:   capacity,
		maxReplays: maxReplays,
		nextOSeq:   1,
		nextDSeq:   1,
		queues:     make(map[string][]naiveMessage),
		dead:       make(map[string][]naiveDeadEntry),
	}
	for _, name := range queueNames {
		broker.queues[name] = nil
		broker.dead[name] = nil
	}
	return broker
}

func (b *naiveBroker) enqueue(queue, payload string) (int, error) {
	if queue == "" {
		return 0, ErrInvalidArgument
	}
	messages, ok := b.queues[queue]
	if !ok {
		return 0, ErrQueueNotFound
	}
	if len(messages) >= b.capacity {
		return 0, ErrQueueFull
	}
	oseq := b.nextOSeq
	b.nextOSeq++
	b.queues[queue] = append(messages, naiveMessage{oseq: oseq, payload: payload})
	return oseq, nil
}

func (b *naiveBroker) dequeue(queue string) (naiveMessage, error) {
	if queue == "" {
		return naiveMessage{}, ErrInvalidArgument
	}
	messages, ok := b.queues[queue]
	if !ok {
		return naiveMessage{}, ErrQueueNotFound
	}
	if len(messages) == 0 {
		return naiveMessage{}, ErrQueueEmpty
	}
	message := messages[0]
	b.queues[queue] = messages[1:]
	return message, nil
}

func (b *naiveBroker) sendToDeadLetter(queue string) (int, error) {
	if queue == "" {
		return 0, ErrInvalidArgument
	}
	messages, ok := b.queues[queue]
	if !ok {
		return 0, ErrQueueNotFound
	}
	if len(messages) == 0 {
		return 0, ErrQueueEmpty
	}
	message := messages[0]
	b.queues[queue] = messages[1:]
	dseq := b.nextDSeq
	b.nextDSeq++
	b.dead[queue] = append(b.dead[queue], naiveDeadEntry{dseq: dseq, msg: message})
	return dseq, nil
}

func (b *naiveBroker) replay(queue string, max int) (int, error) {
	if max < 1 || queue == "" {
		return 0, ErrInvalidArgument
	}
	messages, ok := b.queues[queue]
	if !ok {
		return 0, ErrQueueNotFound
	}
	entries := b.dead[queue]
	if len(entries) == 0 {
		return 0, ErrNoDeadEntries
	}

	candidates := make([]int, 0, len(entries))
	for i := range entries {
		if entries[i].msg.replays < b.maxReplays {
			candidates = append(candidates, i)
		}
	}
	if len(candidates) == 0 {
		return 0, ErrReplayLimitReached
	}
	if len(messages) >= b.capacity {
		return 0, ErrQueueFull
	}

	sortNaiveCandidates(entries, candidates)
	remainingDead := make([]naiveDeadEntry, 0, len(entries))
	replayedIndexes := make(map[int]bool)
	replayed := 0

	for _, index := range candidates {
		if replayed >= max || len(messages) >= b.capacity {
			break
		}
		entries[index].msg.replays++
		messages = append(messages, entries[index].msg)
		replayedIndexes[index] = true
		replayed++
	}
	for i, entry := range entries {
		if !replayedIndexes[i] {
			remainingDead = append(remainingDead, entry)
		}
	}

	b.queues[queue] = messages
	b.dead[queue] = remainingDead
	return replayed, nil
}

func sortNaiveCandidates(entries []naiveDeadEntry, indexes []int) {
	for i := 1; i < len(indexes); i++ {
		for j := i; j > 0 && entries[indexes[j-1]].msg.oseq > entries[indexes[j]].msg.oseq; j-- {
			indexes[j-1], indexes[j] = indexes[j], indexes[j-1]
		}
	}
}

func (b *naiveBroker) snapshot() (map[string][]Message, map[string][]DeadEntry) {
	queues := make(map[string][]Message, len(b.queues))
	dead := make(map[string][]DeadEntry, len(b.dead))
	for name, messages := range b.queues {
		copied := make([]Message, len(messages))
		for i, message := range messages {
			copied[i] = Message{
				OSeq:    message.oseq,
				Payload: message.payload,
				Replays: message.replays,
			}
		}
		queues[name] = copied
	}
	for name, entries := range b.dead {
		copied := make([]DeadEntry, len(entries))
		for i, entry := range entries {
			copied[i] = DeadEntry{
				Message: Message{
					OSeq:    entry.msg.oseq,
					Payload: entry.msg.payload,
					Replays: entry.msg.replays,
				},
				DSeq: entry.dseq,
			}
		}
		dead[name] = copied
	}
	return queues, dead
}

type testStep struct {
	name        string
	queue       string
	payload     string
	maxReplay   int
	wantOSeq    int
	wantDSeq    int
	wantReplays int
	wantErr     error
}

func TestReplayRulesAgainstNaiveSimulation(t *testing.T) {
	broker, err := NewBroker(4, 2, "main", "partial", "capped", "empty")
	if err != nil {
		t.Fatalf("NewBroker input(capacity=4, limit=2): %v", err)
	}
	naive := newNaiveBroker(4, 2, "main", "partial", "capped", "empty")

	steps := []testStep{
		{name: "enqueue", queue: "main", payload: "a", wantOSeq: 1},
		{name: "enqueue", queue: "main", payload: "b", wantOSeq: 2},
		{name: "enqueue", queue: "main", payload: "c", wantOSeq: 3},
		{name: "enqueue", queue: "main", payload: "d", wantOSeq: 4},
		{name: "kill", queue: "main", wantDSeq: 1},
		{name: "kill", queue: "main", wantDSeq: 2},
		{name: "kill", queue: "main", wantDSeq: 3},
		{name: "replay", queue: "main", maxReplay: 1, wantReplays: 1},
		{name: "kill", queue: "main", wantDSeq: 4},
		{name: "replay", queue: "main", maxReplay: 2, wantReplays: 2},
		{name: "kill", queue: "main", wantDSeq: 5},
		{name: "replay", queue: "main", maxReplay: 2, wantReplays: 2},
		{name: "kill", queue: "main", wantDSeq: 6},
		{name: "replay", queue: "main", maxReplay: 1, wantReplays: 1},
		{name: "kill", queue: "main", wantDSeq: 7},
		{name: "enqueue", queue: "main", payload: "e", wantOSeq: 5},
		{name: "kill", queue: "main", wantDSeq: 8},
		{name: "replay", queue: "main", maxReplay: 1, wantReplays: 1},

		{name: "enqueue", queue: "partial", payload: "p1", wantOSeq: 6},
		{name: "enqueue", queue: "partial", payload: "p2", wantOSeq: 7},
		{name: "kill", queue: "partial", wantDSeq: 9},
		{name: "kill", queue: "partial", wantDSeq: 10},
		{name: "replay", queue: "partial", maxReplay: 1, wantReplays: 1},
		{name: "kill", queue: "partial", wantDSeq: 11},
		{name: "enqueue", queue: "partial", payload: "p3", wantOSeq: 8},
		{name: "enqueue", queue: "partial", payload: "p4", wantOSeq: 9},
		{name: "enqueue", queue: "partial", payload: "p5", wantOSeq: 10},
		{name: "replay", queue: "partial", maxReplay: 3, wantReplays: 1},
		{name: "replay", queue: "partial", maxReplay: 3, wantErr: ErrQueueFull},
		{name: "kill", queue: "partial", wantDSeq: 12},
		{name: "replay", queue: "partial", maxReplay: 2, wantReplays: 1},
		{name: "replay", queue: "partial", maxReplay: 2, wantErr: ErrQueueFull},

		{name: "enqueue", queue: "capped", payload: "x", wantOSeq: 11},
		{name: "kill", queue: "capped", wantDSeq: 13},
		{name: "replay", queue: "capped", maxReplay: 1, wantReplays: 1},
		{name: "kill", queue: "capped", wantDSeq: 14},
		{name: "replay", queue: "capped", maxReplay: 1, wantReplays: 1},
		{name: "kill", queue: "capped", wantDSeq: 15},
		{name: "replay", queue: "capped", maxReplay: 1, wantErr: ErrReplayLimitReached},

		{name: "replay", queue: "empty", maxReplay: 1, wantErr: ErrNoDeadEntries},
		{name: "replay", queue: "", maxReplay: 1, wantErr: ErrInvalidArgument},
		{name: "replay", queue: "missing", maxReplay: 0, wantErr: ErrInvalidArgument},
		{name: "replay", queue: "missing", maxReplay: 1, wantErr: ErrQueueNotFound},
	}

	for i, step := range steps {
		i, step := i, step
		t.Run(fmt.Sprintf("%02d_%s_%s", i+1, step.name, step.queue), func(t *testing.T) {
			beforeQueues, beforeDead := broker.Snapshot()

			var actualValue int
			var actualErr error
			var naiveValue int
			var naiveErr error

			switch step.name {
			case "enqueue":
				t.Logf("input: Enqueue(queue=%q, payload=%q)", step.queue, step.payload)
				actualValue, actualErr = broker.Enqueue(step.queue, step.payload)
				naiveValue, naiveErr = naive.enqueue(step.queue, step.payload)
			case "kill":
				t.Logf("input: SendToDeadLetter(queue=%q)", step.queue)
				actualValue, actualErr = broker.SendToDeadLetter(step.queue)
				naiveValue, naiveErr = naive.sendToDeadLetter(step.queue)
			case "replay":
				t.Logf("input: Replay(queue=%q, max=%d); basis: skip replays=2 first, order eligible entries by oseq", step.queue, step.maxReplay)
				actualValue, actualErr = broker.Replay(step.queue, step.maxReplay)
				naiveValue, naiveErr = naive.replay(step.queue, step.maxReplay)
			default:
				t.Fatalf("unknown step %q", step.name)
			}

			if !errors.Is(actualErr, step.wantErr) {
				t.Fatalf("output: value=%d, error=%v; want error=%v", actualValue, actualErr, step.wantErr)
			}
			if !errors.Is(naiveErr, step.wantErr) {
				t.Fatalf("naive output: value=%d, error=%v; want error=%v", naiveValue, naiveErr, step.wantErr)
			}
			if actualValue != naiveValue {
				t.Fatalf("output=%d does not match naive output=%d", actualValue, naiveValue)
			}

			switch step.name {
			case "enqueue":
				assertInt(t, "oseq", actualValue, step.wantOSeq)
			case "kill":
				assertInt(t, "dseq", actualValue, step.wantDSeq)
			case "replay":
				assertInt(t, "replayed count", actualValue, step.wantReplays)
			}

			afterQueues, afterDead := broker.Snapshot()
			if step.wantErr != nil && !snapshotsEqual(beforeQueues, beforeDead, afterQueues, afterDead) {
				t.Fatalf("rejected operation changed state; before queues=%v dead=%v after queues=%v dead=%v", beforeQueues, beforeDead, afterQueues, afterDead)
			}

			expectedQueues, expectedDead := naive.snapshot()
			if !snapshotsEqual(expectedQueues, expectedDead, afterQueues, afterDead) {
				t.Fatalf("state differs from naive simulation\nqueues got=%v\nqueues want=%v\ndead got=%v\ndead want=%v", afterQueues, expectedQueues, afterDead, expectedDead)
			}

			t.Logf("output: value=%d, error=%v; decision=%s; queues=%v dead=%v", actualValue, actualErr, decision(step.wantErr), afterQueues, afterDead)
		})
	}

	queues, dead := broker.Snapshot()
	assertOSeqOrder(t, queues["main"], []int{4, 2, 5, 3})
	mainDead := dead["main"]
	if len(mainDead) != 1 {
		t.Fatalf("main dead length=%d; want 1: %+v", len(mainDead), mainDead)
	}
	if mainDead[0].OSeq != 1 || mainDead[0].Replays != 2 || mainDead[0].DSeq != 8 {
		t.Fatalf("main dead = %+v; want oseq=1, replays=2, new dseq=8", mainDead[0])
	}

	partialQueue := queues["partial"]
	assertOSeqOrder(t, partialQueue, []int{9, 10, 6, 7})
	partialDead := dead["partial"]
	if len(partialDead) != 1 {
		t.Fatalf("partial dead length=%d; want 1: %+v", len(partialDead), partialDead)
	}
	if partialDead[0].OSeq != 8 || partialDead[0].Replays != 0 || partialDead[0].DSeq != 12 {
		t.Fatalf("partial dead = %+v; want oseq=8, replays=0, dseq=12", partialDead[0])
	}

	cappedDead := dead["capped"]
	if len(cappedDead) != 1 {
		t.Fatalf("capped dead length=%d; want 1: %+v", len(cappedDead), cappedDead)
	}
	if cappedDead[0].OSeq != 11 || cappedDead[0].Replays != 2 || cappedDead[0].DSeq != 15 {
		t.Fatalf("capped dead = %+v; want oseq=11, replays=2, dseq=15", cappedDead[0])
	}
}

func assertInt(t *testing.T, name string, got, want int) {
	t.Helper()
	if got != want {
		t.Fatalf("%s=%d; want %d", name, got, want)
	}
}

func assertOSeqOrder(t *testing.T, messages []Message, want []int) {
	t.Helper()
	if len(messages) != len(want) {
		t.Fatalf("queue length=%d (%+v); want %d (%v)", len(messages), messages, len(want), want)
	}
	for i, message := range messages {
		if message.OSeq != want[i] {
			t.Fatalf("queue oseq order=%v; want %v at position %d", oseqOrder(messages), want, i)
		}
	}
}

func oseqOrder(messages []Message) []int {
	result := make([]int, len(messages))
	for i, message := range messages {
		result[i] = message.OSeq
	}
	return result
}

func decision(wantErr error) string {
	if wantErr == nil {
		return "accepted"
	}
	return "rejected: " + wantErr.Error()
}

func snapshotsEqual(aQueues map[string][]Message, aDead map[string][]DeadEntry, bQueues map[string][]Message, bDead map[string][]DeadEntry) bool {
	return messageMapsEqual(aQueues, bQueues) && deadMapsEqual(aDead, bDead)
}

func messageMapsEqual(a, b map[string][]Message) bool {
	if len(a) != len(b) {
		return false
	}
	for name, aMessages := range a {
		bMessages, ok := b[name]
		if !ok || len(aMessages) != len(bMessages) {
			return false
		}
		for i := range aMessages {
			if aMessages[i] != bMessages[i] {
				return false
			}
		}
	}
	return true
}

func deadMapsEqual(a, b map[string][]DeadEntry) bool {
	if len(a) != len(b) {
		return false
	}
	for name, aEntries := range a {
		bEntries, ok := b[name]
		if !ok || len(aEntries) != len(bEntries) {
			return false
		}
		for i := range aEntries {
			if aEntries[i] != bEntries[i] {
				return false
			}
		}
	}
	return true
}

func TestConcurrentOperationsAreSerializable(t *testing.T) {
	broker, err := NewBroker(8, 2, "race")
	if err != nil {
		t.Fatalf("NewBroker: %v", err)
	}

	const goroutines = 8
	const operationsPerGoroutine = 200

	var wg sync.WaitGroup
	var stateMu sync.Mutex
	acceptedOSeqs := make(map[int]bool)
	acceptedDSeqs := make(map[int]bool)
	acceptedReplays := 0
	rejectedReplays := 0

	for worker := 0; worker < goroutines; worker++ {
		worker := worker
		wg.Add(1)
		go func() {
			defer wg.Done()
			random := rand.New(rand.NewSource(int64(worker + 1)))

			for range operationsPerGoroutine {
				switch random.Intn(4) {
				case 0, 1:
					oseq, enqueueErr := broker.Enqueue("race", "payload")
					if enqueueErr == nil {
						stateMu.Lock()
						acceptedOSeqs[oseq] = true
						stateMu.Unlock()
					} else if !errors.Is(enqueueErr, ErrQueueFull) {
						t.Errorf("concurrent Enqueue returned unexpected error: %v", enqueueErr)
					}
				case 2:
					dseq, deadErr := broker.SendToDeadLetter("race")
					if deadErr == nil {
						stateMu.Lock()
						acceptedDSeqs[dseq] = true
						stateMu.Unlock()
					} else if !errors.Is(deadErr, ErrQueueEmpty) {
						t.Errorf("concurrent SendToDeadLetter returned unexpected error: %v", deadErr)
					}
				default:
					count, replayErr := broker.Replay("race", 1+random.Intn(3))
					stateMu.Lock()
					if replayErr == nil {
						acceptedReplays += count
					} else {
						rejectedReplays++
					}
					stateMu.Unlock()
					if replayErr != nil &&
						!errors.Is(replayErr, ErrNoDeadEntries) &&
						!errors.Is(replayErr, ErrReplayLimitReached) &&
						!errors.Is(replayErr, ErrQueueFull) {
						t.Errorf("concurrent Replay returned unexpected error: %v", replayErr)
					}
				}
			}
		}()
	}

	wg.Wait()

	queues, dead := broker.Snapshot()
	t.Logf("input: %d goroutines x %d mixed Enqueue/SendToDeadLetter/Replay calls; output: %d enqueued, %d sent dead, %d replayed, %d replay rejections; basis: final state must match one serial interleaving",
		goroutines, operationsPerGoroutine, len(acceptedOSeqs), len(acceptedDSeqs), acceptedReplays, rejectedReplays)

	queue := queues["race"]
	deadEntries := dead["race"]
	if len(queue) > 8 {
		t.Fatalf("queue length=%d exceeds capacity 8", len(queue))
	}
	if len(queue)+len(deadEntries) != len(acceptedOSeqs) {
		t.Fatalf("message locations=%d; want %d accepted messages", len(queue)+len(deadEntries), len(acceptedOSeqs))
	}

	locations := make(map[int]int)
	dseqs := make(map[int]bool)
	for _, message := range queue {
		if !acceptedOSeqs[message.OSeq] {
			t.Fatalf("queued message has unknown oseq=%d", message.OSeq)
		}
		if message.Replays < 0 || message.Replays > 2 {
			t.Fatalf("queued message oseq=%d has replays=%d", message.OSeq, message.Replays)
		}
		locations[message.OSeq]++
	}
	for _, entry := range deadEntries {
		if !acceptedOSeqs[entry.OSeq] {
			t.Fatalf("dead message has unknown oseq=%d", entry.OSeq)
		}
		if !acceptedDSeqs[entry.DSeq] {
			t.Fatalf("dead message has unknown dseq=%d", entry.DSeq)
		}
		if dseqs[entry.DSeq] {
			t.Fatalf("dseq=%d appears more than once", entry.DSeq)
		}
		if entry.Replays < 0 || entry.Replays > 2 {
			t.Fatalf("dead message oseq=%d has replays=%d", entry.OSeq, entry.Replays)
		}
		dseqs[entry.DSeq] = true
		locations[entry.OSeq]++
	}

	for oseq := range acceptedOSeqs {
		if locations[oseq] != 1 {
			t.Fatalf("oseq=%d has %d locations; want exactly one queue or dead-letter location", oseq, locations[oseq])
		}
	}
	for dseq := range dseqs {
		if !acceptedDSeqs[dseq] {
			t.Fatalf("current dseq=%d was not returned by a successful send", dseq)
		}
	}
}

func TestNonReplayRejectionsDoNotMutateState(t *testing.T) {
	broker, err := NewBroker(1, 2, "q")
	if err != nil {
		t.Fatalf("NewBroker: %v", err)
	}

	if _, err := broker.Dequeue("q"); !errors.Is(err, ErrQueueEmpty) {
		t.Fatalf("Dequeue empty error=%v; want ErrQueueEmpty", err)
	}
	if _, err := broker.SendToDeadLetter("q"); !errors.Is(err, ErrQueueEmpty) {
		t.Fatalf("SendToDeadLetter empty error=%v; want ErrQueueEmpty", err)
	}

	oseq, err := broker.Enqueue("q", "first")
	if err != nil || oseq != 1 {
		t.Fatalf("Enqueue input(q, first) output=(%d, %v); want (1, nil)", oseq, err)
	}
	if _, err := broker.Enqueue("q", "full"); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("Enqueue full error=%v; want ErrQueueFull", err)
	}

	queues, dead := broker.Snapshot()
	t.Logf("input: empty dequeue, empty send-dead, second enqueue into capacity 1; output: only oseq=1 accepted; decision: all other calls rejected without state mutation; queues=%v dead=%v", queues, dead)
	assertOSeqOrder(t, queues["q"], []int{1})
	if len(dead["q"]) != 0 {
		t.Fatalf("dead entries=%+v; want none", dead["q"])
	}

	if err := broker.AddQueue("q"); !errors.Is(err, ErrQueueAlreadyExists) {
		t.Fatalf("AddQueue existing error=%v; want ErrQueueAlreadyExists", err)
	}
	if _, err := NewBroker(0, 2, "q"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("NewBroker capacity=0 error=%v; want ErrInvalidArgument", err)
	}
	if _, err := NewBroker(1, -1, "q"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("NewBroker limit=-1 error=%v; want ErrInvalidArgument", err)
	}
}
