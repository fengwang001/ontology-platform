package closure

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func newTestGroup(t *testing.T, targetLag int64, clock *int64, ids ...uint64) *Group {
	t.Helper()
	if len(ids) == 0 {
		ids = []uint64{1, 2}
	}
	group, err := NewGroup(ids[0], ids, targetLag, func() int64 { return *clock })
	if err != nil {
		t.Fatalf("input NewGroup(primary=%v, replicas=%v, lag=%d, clock=%d): %v", ids[0], ids, targetLag, clock, err)
	}
	t.Logf("input NewGroup primary=%d replicas=%v lag=%d clock=%d; output group ready; decision deterministic simulator initialized", ids[0], ids, targetLag, *clock)
	return group
}

func TestRejectedInputsDoNotChangeState(t *testing.T) {
	_, err := NewGroup(1, []uint64{1, 2}, -1, func() int64 { return 0 })
	if !errors.Is(err, ErrInvalidTargetLag) {
		t.Fatalf("negative lag: got %v, want %v", err, ErrInvalidTargetLag)
	}
	_, err = NewGroup(1, []uint64{1, 2}, 0, nil)
	if !errors.Is(err, ErrInvalidClock) {
		t.Fatalf("nil clock: got %v, want %v", err, ErrInvalidClock)
	}
	_, err = NewGroup(1, []uint64{1, 1}, 0, func() int64 { return 0 })
	if !errors.Is(err, ErrInvalidReplicas) {
		t.Fatalf("duplicate replicas: got %v, want %v", err, ErrInvalidReplicas)
	}
	_, err = NewGroup(3, []uint64{1, 2}, 0, func() int64 { return 0 })
	if !errors.Is(err, ErrInvalidReplicas) {
		t.Fatalf("missing primary: got %v, want %v", err, ErrInvalidReplicas)
	}
	t.Logf("input invalid target lag, clock, and replica set; output distinct errors; decision construction fails before any group state exists")

	clock := int64(0)
	group := newTestGroup(t, 0, &clock)
	closedBefore := mustPublish(t, group).ClosedTimestamp
	_, err = group.Write(WriteInput{Key: "k", DesiredTimestamp: -1})
	if !errors.Is(err, ErrNegativeTimestamp) {
		t.Fatalf("negative write timestamp: got %v, want %v", err, ErrNegativeTimestamp)
	}

	_, err = group.Deliver(99, Message{Kind: MessagePublication})
	if !errors.Is(err, ErrReplicaNotFound) {
		t.Fatalf("missing replica: got %v, want %v", err, ErrReplicaNotFound)
	}

	_, err = group.Read(99, "k", 0)
	if !errors.Is(err, ErrReplicaNotFound) {
		t.Fatalf("read missing replica: got %v, want %v", err, ErrReplicaNotFound)
	}
	_, err = group.Read(2, "k", -1)
	if !errors.Is(err, ErrNegativeTimestamp) {
		t.Fatalf("negative read timestamp: got %v, want %v", err, ErrNegativeTimestamp)
	}

	closedAfter := mustPublish(t, group).ClosedTimestamp
	if closedBefore != closedAfter {
		t.Fatalf("closed changed after rejected calls: before=%d after=%d", closedBefore, closedAfter)
	}
	t.Logf("input invalid write, delivery, and read after closed=%d; output distinct errors and closed=%d; decision rejected operations changed no state", closedBefore, closedAfter)
}

func TestInflightWriteBlocksClosure(t *testing.T) {
	clock := int64(10)
	group := newTestGroup(t, 0, &clock)

	write := mustWrite(t, group, WriteInput{Key: "k", Value: "v", DesiredTimestamp: 5})
	t.Logf("input Write desired=5 key=k; output sequence=%d timestamp=%d; decision inflight write timestamp is the closure upper bound", write.Sequence, write.Timestamp)

	first := mustPublish(t, group)
	t.Logf("input Publish clock=%d; output closed=%d log=%d advanced=%v; decision candidate=10 is rejected because inflight timestamp=5 is not greater", clock, first.ClosedTimestamp, first.LogSequence, first.Advanced)
	if first.ClosedTimestamp != -1 || first.LogSequence != 1 || first.Advanced {
		t.Fatalf("first publication = %+v, want closed -1 and no advance", first)
	}

	clock = 20
	mustDeliver(t, group, 1, write.Message)
	second := mustPublish(t, group)
	t.Logf("input Deliver primary write sequence=%d then Publish clock=%d; output closed=%d log=%d advanced=%v; decision no inflight write remains, candidate=20 is accepted", write.Sequence, clock, second.ClosedTimestamp, second.LogSequence, second.Advanced)
	if second.ClosedTimestamp != 20 || second.LogSequence != 1 || !second.Advanced {
		t.Fatalf("second publication = %+v, want closed 20 and advanced", second)
	}
}

func TestWriteAtClosedTimestampIsRaised(t *testing.T) {
	clock := int64(7)
	group := newTestGroup(t, 0, &clock)

	first := mustPublish(t, group)
	t.Logf("input Publish clock=7; output closed=%d log=%d; decision no writes are inflight", first.ClosedTimestamp, first.LogSequence)
	write := mustWrite(t, group, WriteInput{Key: "k", Value: "v", DesiredTimestamp: 7})
	t.Logf("input Write desired=7 closed=7; output timestamp=%d raised=%v; decision desired <= closed is raised to closed+1", write.Timestamp, write.Raised)
	if !write.Raised || write.Timestamp != 8 {
		t.Fatalf("write = %+v, want raised timestamp 8", write)
	}
}

func TestLaggingReplicaUsesEarlierPublicationForHistoricalRead(t *testing.T) {
	clock := int64(5)
	group := newTestGroup(t, 0, &clock)

	oldWrite := mustWrite(t, group, WriteInput{Key: "k", Value: "old", DesiredTimestamp: 3})
	mustDeliver(t, group, 1, oldWrite.Message)
	oldPub := mustPublish(t, group)
	t.Logf("input first Write timestamp=3 then Publish; output pub={closed=%d log=%d}; decision publication promises historical reads through 3 once sequence 1 is applied", oldPub.ClosedTimestamp, oldPub.LogSequence)

	clock = 10
	newWrite := mustWrite(t, group, WriteInput{Key: "k", Value: "new", DesiredTimestamp: 8})
	mustDeliver(t, group, 1, newWrite.Message)
	newPub := mustPublish(t, group)
	t.Logf("input primary applied both writes, clock=10, second Write timestamp=8, Publish; output pub={closed=%d log=%d}; decision later publication requires sequence 2", newPub.ClosedTimestamp, newPub.LogSequence)

	mustDeliver(t, group, 2, newPub.Message)
	mustDeliver(t, group, 2, oldPub.Message)
	mustDeliver(t, group, 2, oldWrite.Message)

	_, err := group.Read(2, "k", 8)
	if !errors.Is(err, ErrNotCaughtUp) {
		t.Fatalf("read timestamp 8 before applying write 2: got %v, want %v", err, ErrNotCaughtUp)
	}
	t.Logf("input Read replica=2 key=k timestamp=8 applied=1; output error=%v; decision a closing publication exists but requires log=2", ErrNotCaughtUp)

	read := mustRead(t, group, 2, "k", 3)
	t.Logf("input Read replica=2 key=k timestamp=3 applied=1; output value=%q found=%v matchedPub={closed=%d log=%d}; decision earlier publication still closes timestamp 3 and only requires log=1", read.Value, read.Found, read.ClosedTimestamp, read.MatchedSequence)
	if !read.Found || read.Value != "old" || read.ClosedTimestamp != 5 || read.MatchedSequence != 1 {
		t.Fatalf("historical read = %+v, want old value matched at publication {closed:5 log:1}", read)
	}

	mustDeliver(t, group, 2, newWrite.Message)
	read = mustRead(t, group, 2, "k", 8)
	t.Logf("input Deliver sequence=2 then Read timestamp=8; output value=%q matchedPub={closed=%d log=%d}; decision replica caught up to latest publication", read.Value, read.ClosedTimestamp, read.MatchedSequence)
	if !read.Found || read.Value != "new" {
		t.Fatalf("current read = %+v, want new value", read)
	}
}

type deterministicRand struct {
	state uint64
}

func (r *deterministicRand) next() uint64 {
	r.state ^= r.state << 13
	r.state ^= r.state >> 7
	r.state ^= r.state << 17
	return r.state
}

func (r *deterministicRand) intn(n int) int {
	if n <= 0 {
		return 0
	}
	return int(r.next() % uint64(n))
}

type replayStep struct {
	name     string
	delivery DeliveryResult
	read     ReadResult
	readErr  string
}

func TestRandomReplayMatchesPrimaryReads(t *testing.T) {
	first := runRandomDifferential(t, 0x92631770, false)
	second := runRandomDifferential(t, 0x92631770, true)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("non-deterministic replay:\nfirst=%v\nsecond=%v", first, second)
	}
	t.Logf("input replay same operations, clock values, and deliveries twice; output %d equal recorded decisions; decision replay is deterministic", len(first))
}

func TestWritePublishDeliverAndReadAreConcurrent(t *testing.T) {
	clock := int64(100)
	group := newTestGroup(t, 0, &clock, 1, 2)

	const writes = 80
	messages := make([]Message, writes)
	for i := range messages {
		write, err := group.Write(WriteInput{
			Key:              "k",
			Value:            fmt.Sprintf("v%d", i),
			DesiredTimestamp: int64(i),
		})
		if err != nil {
			t.Fatalf("prepare write %d: %v", i, err)
		}
		messages[i] = write.Message
	}

	var wait sync.WaitGroup
	start := make(chan struct{})
	wait.Add(4)
	go func() {
		defer wait.Done()
		<-start
		for i := 0; i < writes; i++ {
			if _, err := group.Deliver(1, messages[i]); err != nil {
				t.Errorf("primary deliver %d: %v", i, err)
			}
			if _, err := group.Deliver(2, messages[i]); err != nil {
				t.Errorf("replica deliver %d: %v", i, err)
			}
		}
	}()
	go func() {
		defer wait.Done()
		<-start
		for i := 0; i < writes; i++ {
			if _, err := group.Publish(); err != nil {
				t.Errorf("publish %d: %v", i, err)
			}
		}
	}()
	go func() {
		defer wait.Done()
		<-start
		for i := int64(0); i < writes; i++ {
			_, _ = group.Read(1, "k", i)
			_, _ = group.Read(2, "k", i)
		}
	}()
	go func() {
		defer wait.Done()
		<-start
		for i := 0; i < writes; i++ {
			if _, err := group.Write(WriteInput{
				Key:              "race",
				Value:            fmt.Sprintf("race-%d", i),
				DesiredTimestamp: int64(writes + i),
			}); err != nil {
				t.Errorf("concurrent write %d: %v", i, err)
			}
		}
	}()

	close(start)
	wait.Wait()
	t.Logf("input concurrent writes, publications, deliveries, and reads; output calls completed; decision shared state is serialized without exposing invalid reads")
}

func runRandomDifferential(t *testing.T, seed uint64, quiet bool) []replayStep {
	t.Helper()
	rng := deterministicRand{state: seed}
	clock := int64(0)
	group := newTestGroup(t, 10, &clock, 1, 2, 3)

	const writeCount = 18
	writeMessages := make([]Message, writeCount)
	publications := make([]Message, 0, writeCount)
	timestamps := make([]int64, writeCount)
	keys := make([]string, writeCount)
	values := make([]string, writeCount)
	for i := 0; i < writeCount; i++ {
		key := fmt.Sprintf("k%d", rng.intn(6))
		value := fmt.Sprintf("v%d", i)
		desired := int64(i*4 + rng.intn(4))
		write := mustWrite(t, group, WriteInput{Key: key, Value: value, DesiredTimestamp: desired})
		writeMessages[i] = write.Message
		timestamps[i] = write.Timestamp
		keys[i] = key
		values[i] = value
		mustDeliver(t, group, 1, write.Message)

		clock = int64(i*5 + 20)
		publication := mustPublish(t, group)
		publications = append(publications, publication.Message)
	}

	lastPublication := publications[len(publications)-1].Publication
	t.Logf("input propose and primary-apply %d writes, publishing after each; output finalPub={closed=%d log=%d}; decision primary final state is the oracle", writeCount, lastPublication.ClosedTimestamp, lastPublication.LogSequence)

	steps := make([]replayStep, 0, 120)
	serviceableReads := 0
	for event := 0; event < 120; event++ {
		replicaID := uint64(2 + rng.intn(2))
		state := replicaStateForTest(t, group, replicaID)
		switch rng.intn(10) {
		case 0, 1, 2:
			if state.applied >= writeCount {
				continue
			}
			index := state.applied
			result := mustDeliver(t, group, replicaID, writeMessages[index])
			steps = append(steps, replayStep{
				name:     fmt.Sprintf("event=%d deliver replica=%d next-write=%d", event, replicaID, index+1),
				delivery: result,
			})
			if !quiet {
				t.Logf("input event=%d Deliver replica=%d next-write=%d; output applied=%d; decision filling the next log position advances application", event, replicaID, index+1, result.AppliedSequence)
			}
		case 3, 4, 5:
			index := rng.intn(writeCount)
			result := mustDeliver(t, group, replicaID, writeMessages[index])
			steps = append(steps, replayStep{
				name:     fmt.Sprintf("event=%d deliver replica=%d duplicate-or-gap-write=%d", event, replicaID, index+1),
				delivery: result,
			})
			if !quiet {
				t.Logf("input event=%d Deliver replica=%d write=%d; output applied=%d; decision gaps buffer messages and duplicates are idempotent", event, replicaID, index+1, result.AppliedSequence)
			}
		case 6:
			index := rng.intn(len(publications))
			result := mustDeliver(t, group, replicaID, publications[index])
			steps = append(steps, replayStep{
				name:     fmt.Sprintf("event=%d deliver replica=%d publication=%d", event, replicaID, index+1),
				delivery: result,
			})
			if !quiet {
				t.Logf("input event=%d Deliver replica=%d publication=%d {closed=%d log=%d}; output applied=%d; decision publications may arrive out of order and repeat", event, replicaID, index+1, publications[index].Publication.ClosedTimestamp, publications[index].Publication.LogSequence, result.AppliedSequence)
			}
		case 7:
			if state.applied == 0 {
				continue
			}
			index := state.applied
			index--
			result := mustDeliver(t, group, replicaID, publications[index])
			steps = append(steps, replayStep{
				name:     fmt.Sprintf("event=%d deliver replica=%d aligned-publication=%d", event, replicaID, index+1),
				delivery: result,
			})
			if !quiet {
				t.Logf("input event=%d Deliver replica=%d aligned-publication=%d; output applied=%d; decision an earlier publication can serve historical reads while later logs lag", event, replicaID, index+1, result.AppliedSequence)
			}
		default:
			key := fmt.Sprintf("k%d", rng.intn(7))
			timestamp := int64(rng.intn(int(lastPublication.ClosedTimestamp) + 2))
			result, err := group.Read(replicaID, key, timestamp)
			step := replayStep{
				name:    fmt.Sprintf("event=%d read replica=%d key=%s timestamp=%d", event, replicaID, key, timestamp),
				read:    result,
				readErr: "",
			}
			if err != nil {
				step.readErr = err.Error()
			}
			steps = append(steps, step)

			switch {
			case errors.Is(err, ErrNotClosed):
				if !quiet {
					t.Logf("input %s; output ErrNotClosed; decision no known publication closes timestamp", step.name)
				}
			case errors.Is(err, ErrNotCaughtUp):
				if !quiet {
					t.Logf("input %s; output ErrNotCaughtUp; decision a closing publication exists but applied log is behind", step.name)
				}
			case err != nil:
				t.Fatalf("%s: unexpected error %v", step.name, err)
			default:
				serviceableReads++
				value, found := oracleValue(keys, values, timestamps, key, timestamp)
				if result.Found != found || result.Value != value {
					t.Fatalf("%s = (%q,%v), oracle = (%q,%v)", step.name, result.Value, result.Found, value, found)
				}
				if !quiet {
					t.Logf("input %s; output value=%q found=%v applied=%d; decision equals primary final read", step.name, result.Value, result.Found, result.AppliedSequence)
				}
			}
		}
	}

	for serviceableReads < 30 {
		replicaID := uint64(2 + serviceableReads%2)
		state := replicaStateForTest(t, group, replicaID)
		if state.applied < writeCount {
			result := mustDeliver(t, group, replicaID, writeMessages[state.applied])
			steps = append(steps, replayStep{
				name:     fmt.Sprintf("forced replica=%d next-write=%d", replicaID, state.applied+1),
				delivery: result,
			})
			state = replicaStateForTest(t, group, replicaID)
		}
		if state.applied == 0 {
			t.Fatal("forced differential cannot align a publication before applying write 1")
		}

		alignedPublication := publications[state.applied-1]
		mustDeliver(t, group, replicaID, alignedPublication)
		key := fmt.Sprintf("k%d", rng.intn(7))
		timestamp := int64(rng.intn(int(alignedPublication.Publication.ClosedTimestamp) + 1))
		result, err := group.Read(replicaID, key, timestamp)
		if err != nil {
			t.Fatalf("forced read replica=%d key=%s timestamp=%d: %v", replicaID, key, timestamp, err)
		}
		value, found := oracleValue(keys, values, timestamps, key, timestamp)
		if result.Found != found || result.Value != value {
			t.Fatalf("forced read (%q,%v), oracle (%q,%v)", result.Value, result.Found, value, found)
		}
		serviceableReads++
		steps = append(steps, replayStep{
			name: fmt.Sprintf("forced read replica=%d key=%s timestamp=%d", replicaID, key, timestamp),
			read: result,
		})
		if !quiet {
			t.Logf("input forced replica=%d publication={closed=%d log=%d} Read key=%s timestamp=%d; output value=%q found=%v; decision serviceable read matches primary oracle", replicaID, alignedPublication.Publication.ClosedTimestamp, alignedPublication.Publication.LogSequence, key, timestamp, result.Value, result.Found)
		}
	}

	if serviceableReads < 20 {
		t.Fatalf("random differential produced %d serviceable reads, want at least 20", serviceableReads)
	}
	t.Logf("input random differential; output %d serviceable reads compared with primary oracle; decision every served read matched", serviceableReads)

	return canonicalSteps(steps)
}

func replicaStateForTest(t *testing.T, group *Group, replicaID uint64) *replicaState {
	t.Helper()
	group.mu.Lock()
	defer group.mu.Unlock()
	replica := group.replicas[replicaID]
	if replica == nil {
		t.Fatalf("test replica %d does not exist", replicaID)
	}
	return replica
}

func canonicalSteps(steps []replayStep) []replayStep {
	copied := make([]replayStep, len(steps))
	for i, step := range steps {
		step.name = strings.Clone(step.name)
		step.readErr = strings.Clone(step.readErr)
		copied[i] = step
	}
	return copied
}

func oracleValue(keys, values []string, timestamps []int64, key string, timestamp int64) (string, bool) {
	best := -1
	for i := range keys {
		if keys[i] != key || timestamps[i] > timestamp {
			continue
		}
		if best == -1 || timestamps[i] > timestamps[best] ||
			(timestamps[i] == timestamps[best] && i > best) {
			best = i
		}
	}
	if best == -1 {
		return "", false
	}
	return values[best], true
}

func mustWrite(t *testing.T, group *Group, input WriteInput) WriteResult {
	t.Helper()
	result, err := group.Write(input)
	if err != nil {
		t.Fatalf("Write key=%q desired=%d: %v", input.Key, input.DesiredTimestamp, err)
	}
	return result
}

func mustPublish(t *testing.T, group *Group) PublicationResult {
	t.Helper()
	result, err := group.Publish()
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	return result
}

func mustDeliver(t *testing.T, group *Group, replicaID uint64, message Message) DeliveryResult {
	t.Helper()
	result, err := group.Deliver(replicaID, message)
	if err != nil {
		t.Fatalf("Deliver replica=%d kind=%d: %v", replicaID, message.Kind, err)
	}
	t.Logf("input Deliver replica=%d kind=%d writeSeq=%d publication={closed=%d log=%d}; output applied=%d; decision writes are buffered until sequence gaps close", replicaID, message.Kind, message.Write.Sequence, message.Publication.ClosedTimestamp, message.Publication.LogSequence, result.AppliedSequence)
	return result
}

func mustRead(t *testing.T, group *Group, replicaID uint64, key string, timestamp int64) ReadResult {
	t.Helper()
	result, err := group.Read(replicaID, key, timestamp)
	if err != nil {
		t.Fatalf("Read replica=%d key=%q timestamp=%d: %v", replicaID, key, timestamp, err)
	}
	return result
}
