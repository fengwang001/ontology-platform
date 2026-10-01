package stickypartition

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

type naivePartitioner struct {
	partitions int
	threshold  int
	sticky     int
	bytes      int
	available  []bool
}

func newNaive(partitions, threshold int) *naivePartitioner {
	available := make([]bool, partitions)
	for i := range available {
		available[i] = true
	}
	return &naivePartitioner{
		partitions: partitions,
		threshold:  threshold,
		available:  available,
	}
}

func (m *naivePartitioner) keyless(size int) int {
	if !m.available[m.sticky] {
		for offset := 1; offset <= m.partitions; offset++ {
			next := (m.sticky + offset) % m.partitions
			if m.available[next] {
				m.sticky = next
				m.bytes = 0
				break
			}
		}
	}

	partition := m.sticky
	m.bytes += size
	if m.bytes >= m.threshold {
		for offset := 1; offset <= m.partitions; offset++ {
			next := (m.sticky + offset) % m.partitions
			if m.available[next] {
				m.sticky = next
				break
			}
		}
		m.bytes = 0
	}
	return partition
}

func (m *naivePartitioner) keyed(key []byte) int {
	return int(fnvNaive32(key) % uint32(m.partitions))
}

func (m *naivePartitioner) setAvailable(partition int, available bool) {
	m.available[partition] = available
}

func (m *naivePartitioner) snapshot() Snapshot {
	return Snapshot{
		StickyPartition:  m.sticky,
		AccumulatedBytes: m.bytes,
		Available:        append([]bool(nil), m.available...),
	}
}

func fnvNaive32(data []byte) uint32 {
	var hash uint32 = 2166136261
	for _, b := range data {
		hash ^= uint32(b)
		hash *= 16777619
	}
	return hash
}

func logDecision(t *testing.T, step int, input string, got, want int, state Snapshot, reason string) {
	t.Helper()
	t.Logf("step=%d input=%s output=partition:%d want=%d state=sticky:%d bytes:%d available:%v reason=%s",
		step, input, got, want, state.StickyPartition, state.AccumulatedBytes, state.Available, reason)
}

func assertStateMatches(t *testing.T, got Snapshot, want Snapshot) {
	t.Helper()
	if got.StickyPartition != want.StickyPartition ||
		got.AccumulatedBytes != want.AccumulatedBytes ||
		!equalBools(got.Available, want.Available) {
		t.Fatalf("state = %+v, want %+v", got, want)
	}
}

func equalBools(left, right []bool) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func TestExactThresholdSwitchesAfterCurrentMessage(t *testing.T) {
	const partitions = 3
	const threshold = 10

	p, err := New(partitions, threshold)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	model := newNaive(partitions, threshold)

	sizes := []int{4, 6, 3}
	for i, size := range sizes {
		got, err := p.SendKeyless(size)
		if err != nil {
			t.Fatalf("SendKeyless(%d) error = %v", size, err)
		}
		want := model.keyless(size)
		state := p.SnapshotState()
		t.Logf("step=%d input=keyless:%d output=partition:%d want=%d state=sticky:%d bytes:%d reason=after send accumulated bytes cross threshold only at %d",
			i, size, got, want, state.StickyPartition, state.AccumulatedBytes, threshold)
		if got != want {
			t.Fatalf("SendKeyless(%d) = %d, want %d", size, got, want)
		}
	}

	state := p.SnapshotState()
	if state.StickyPartition != 1 || state.AccumulatedBytes != 3 {
		t.Fatalf("state = %+v, want sticky 1 and 3 accumulated bytes", state)
	}
}

func TestSingleMessageLargerThanThreshold(t *testing.T) {
	const partitions = 3
	const threshold = 5

	p, err := New(partitions, threshold)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	model := newNaive(partitions, threshold)

	for i, size := range []int{8, 8} {
		got, err := p.SendKeyless(size)
		if err != nil {
			t.Fatalf("SendKeyless(%d) error = %v", size, err)
		}
		want := model.keyless(size)
		state := p.SnapshotState()
		logDecision(t, i, fmt.Sprintf("keyless:%d", size), got, want, state,
			"one message larger than B is delivered first, then accumulated bytes reset and sticky advances")
		if got != want {
			t.Fatalf("SendKeyless(%d) = %d, want %d", size, got, want)
		}
		assertStateMatches(t, state, model.snapshot())
	}
}

func TestThresholdSwitchSkipsUnavailablePartitions(t *testing.T) {
	const partitions = 4
	const threshold = 5

	p, err := New(partitions, threshold)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	model := newNaive(partitions, threshold)

	steps := []struct {
		name      string
		size      int
		disable   int
		wantAfter int
	}{
		{name: "accumulate below threshold", size: 2, disable: -1, wantAfter: 0},
		{name: "make partition 1 unavailable before threshold switch", size: 3, disable: 1, wantAfter: 2},
		{name: "continue on partition 2", size: 1, disable: -1, wantAfter: 2},
	}

	for i, step := range steps {
		if step.disable >= 0 {
			if err := p.SetAvailable(step.disable, false); err != nil {
				t.Fatalf("SetAvailable(%d, false) error = %v", step.disable, err)
			}
			model.setAvailable(step.disable, false)
		}

		got, err := p.SendKeyless(step.size)
		if err != nil {
			t.Fatalf("SendKeyless(%d) error = %v", step.size, err)
		}
		want := model.keyless(step.size)
		state := p.SnapshotState()
		logDecision(t, i, fmt.Sprintf("keyless:%d;disable:%d", step.size, step.disable), got, want, state,
			"post-threshold ring search starts after current partition and skips unavailable partitions")
		if got != want || state.StickyPartition != step.wantAfter {
			t.Fatalf("step %d got partition %d, state partition %d, want delivery %d and next %d",
				i, got, state.StickyPartition, want, step.wantAfter)
		}
		assertStateMatches(t, state, model.snapshot())
	}
}

func TestUnavailableStickySwitchesOnlyAtNextKeylessSend(t *testing.T) {
	const partitions = 4
	const threshold = 10

	p, err := New(partitions, threshold)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	model := newNaive(partitions, threshold)

	got, err := p.SendKeyless(2)
	if err != nil {
		t.Fatalf("SendKeyless(2) error = %v", err)
	}
	model.keyless(2)

	if err := p.SetAvailable(0, false); err != nil {
		t.Fatalf("SetAvailable(0, false) error = %v", err)
	}
	model.setAvailable(0, false)

	state := p.SnapshotState()
	t.Logf("step=1 input=set-available:0=false output=none state=sticky:%d bytes:%d available:%v reason=changing availability does not itself switch sticky partition or reset bytes",
		state.StickyPartition, state.AccumulatedBytes, state.Available)
	assertStateMatches(t, state, model.snapshot())

	got, err = p.SendKeyless(3)
	if err != nil {
		t.Fatalf("SendKeyless(3) error = %v", err)
	}
	want := model.keyless(3)
	state = p.SnapshotState()
	logDecision(t, 2, "keyless:3", got, want, state,
		"sticky partition was unavailable, so reset and switch happen immediately before this send")
	if got != 1 || want != 1 || state.StickyPartition != 1 || state.AccumulatedBytes != 3 {
		t.Fatalf("unavailable sticky transition = partition %d, state %+v; want delivery/sticky 1 with 3 bytes", got, state)
	}

	if err := p.SetAvailable(1, false); err != nil {
		t.Fatalf("SetAvailable(1, false) error = %v", err)
	}
	model.setAvailable(1, false)
	if err := p.SetAvailable(0, true); err != nil {
		t.Fatalf("SetAvailable(0, true) error = %v", err)
	}
	model.setAvailable(0, true)

	got, err = p.SendKeyless(2)
	if err != nil {
		t.Fatalf("SendKeyless(2) error = %v", err)
	}
	want = model.keyless(2)
	state = p.SnapshotState()
	logDecision(t, 3, "keyless:2;restore:0;disable:1", got, want, state,
		"ring search moves forward to partition 2; restoring partition 0 does not cause a rollback")
	if got != 2 || want != 2 || state.StickyPartition != 2 {
		t.Fatalf("recovery without rollback got delivery %d, sticky %d; want 2 and 2", got, state.StickyPartition)
	}
	assertStateMatches(t, state, model.snapshot())
}

func TestRecoveredBeforeSendDoesNotSwitchAndOnlyAvailablePartitionStays(t *testing.T) {
	t.Run("availability restored before next send", func(t *testing.T) {
		p, err := New(3, 10)
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		if _, err := p.SendKeyless(4); err != nil {
			t.Fatalf("SendKeyless(4) error = %v", err)
		}
		if err := p.SetAvailable(0, false); err != nil {
			t.Fatalf("SetAvailable(0, false) error = %v", err)
		}
		if err := p.SetAvailable(0, true); err != nil {
			t.Fatalf("SetAvailable(0, true) error = %v", err)
		}

		got, err := p.SendKeyless(3)
		if err != nil {
			t.Fatalf("SendKeyless(3) error = %v", err)
		}
		state := p.SnapshotState()
		t.Logf("input=mark-0-unavailable-then-recovered-before-keyless:3 output=partition:%d state=sticky:%d bytes:%d reason=no send observed the unavailable interval, so bytes are retained and there is no switch",
			got, state.StickyPartition, state.AccumulatedBytes)
		if got != 0 || state.StickyPartition != 0 || state.AccumulatedBytes != 7 {
			t.Fatalf("state after recovered availability = partition %d, %+v; want partition/sticky 0 with 7 bytes", got, state)
		}
	})

	t.Run("only current partition is available", func(t *testing.T) {
		p, err := New(3, 5)
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		for partition := 1; partition < 3; partition++ {
			if err := p.SetAvailable(partition, false); err != nil {
				t.Fatalf("SetAvailable(%d, false) error = %v", partition, err)
			}
		}

		got, err := p.SendKeyless(9)
		if err != nil {
			t.Fatalf("SendKeyless(9) error = %v", err)
		}
		state := p.SnapshotState()
		t.Logf("input=keyless:9-with-only-partition-0-available output=partition:%d state=sticky:%d bytes:%d reason=ring search wraps back to the only available current partition, then resets accumulated bytes",
			got, state.StickyPartition, state.AccumulatedBytes)
		if got != 0 || state.StickyPartition != 0 || state.AccumulatedBytes != 0 {
			t.Fatalf("only available current partition state = partition %d, %+v; want 0/0/0", got, state)
		}
	})
}

func TestKeyedMessagesIgnoreAvailabilityAndStickyBytes(t *testing.T) {
	const partitions = 4
	const threshold = 10

	p, err := New(partitions, threshold)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	model := newNaive(partitions, threshold)

	if _, err := p.SendKeyless(4); err != nil {
		t.Fatalf("SendKeyless(4) error = %v", err)
	}
	model.keyless(4)

	for partition := range partitions {
		if err := p.SetAvailable(partition, false); err != nil {
			t.Fatalf("SetAvailable(%d, false) error = %v", partition, err)
		}
		model.setAvailable(partition, false)
	}

	keys := []string{"a", "b", "c", "d"}
	for i, key := range keys {
		got, err := p.SendWithKey([]byte(key))
		if err != nil {
			t.Fatalf("SendWithKey(%q) error = %v", key, err)
		}
		want := model.keyed([]byte(key))
		state := p.SnapshotState()
		logDecision(t, i, fmt.Sprintf("keyed:%q;all-partitions-unavailable", key), got, want, state,
			"FNV-1a hash modulo N determines partition; availability and sticky accumulation are ignored")
		if got != want {
			t.Fatalf("SendWithKey(%q) = %d, want %d", key, got, want)
		}
		assertStateMatches(t, state, model.snapshot())
	}

	state := p.SnapshotState()
	if state.StickyPartition != 0 || state.AccumulatedBytes != 4 {
		t.Fatalf("keyed messages changed sticky state: %+v", state)
	}
}

func TestRejectedOperationsDoNotMutateState(t *testing.T) {
	t.Run("invalid constructor arguments", func(t *testing.T) {
		cases := []struct {
			partitions int
			threshold  int
			want       error
		}{
			{partitions: 0, threshold: 1, want: ErrInvalidPartitionCount},
			{partitions: 1, threshold: 0, want: ErrInvalidThreshold},
			{partitions: -1, threshold: -1, want: ErrInvalidPartitionCount},
		}

		for i, tc := range cases {
			got, err := New(tc.partitions, tc.threshold)
			t.Logf("step=%d input=new:N=%d,B=%d output=error:%q reason=constructor rejects before allocating state",
				i, tc.partitions, tc.threshold, err)
			if got != nil || !errors.Is(err, tc.want) {
				t.Fatalf("New(%d, %d) = (%v, %v), want nil and %v", tc.partitions, tc.threshold, got, err, tc.want)
			}
		}
	})

	t.Run("invalid size and partition", func(t *testing.T) {
		p, err := New(2, 10)
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		if _, err := p.SendKeyless(2); err != nil {
			t.Fatalf("SendKeyless(2) error = %v", err)
		}
		before := p.SnapshotState()

		_, err = p.SendKeyless(0)
		t.Logf("input=keyless:0 output=error:%q reason=size must be at least 1; state remains unchanged", err)
		if !errors.Is(err, ErrInvalidMessageSize) {
			t.Fatalf("SendKeyless(0) error = %v, want %v", err, ErrInvalidMessageSize)
		}

		err = p.SetAvailable(2, false)
		t.Logf("input=set-available:2=false output=error:%q reason=partition number must be within [0,N); state remains unchanged", err)
		if !errors.Is(err, ErrPartitionOutOfRange) {
			t.Fatalf("SetAvailable(2, false) error = %v, want %v", err, ErrPartitionOutOfRange)
		}

		assertStateMatches(t, p.SnapshotState(), before)
	})

	t.Run("no available partition", func(t *testing.T) {
		p, err := New(2, 10)
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		if _, err := p.SendKeyless(3); err != nil {
			t.Fatalf("SendKeyless(3) error = %v", err)
		}
		if err := p.SetAvailable(0, false); err != nil {
			t.Fatalf("SetAvailable(0, false) error = %v", err)
		}
		if err := p.SetAvailable(1, false); err != nil {
			t.Fatalf("SetAvailable(1, false) error = %v", err)
		}
		before := p.SnapshotState()

		_, err = p.SendKeyless(2)
		t.Logf("input=keyless:2 output=error:%q reason=ring search has no available partition before sending", err)
		if !errors.Is(err, ErrNoAvailablePartition) {
			t.Fatalf("SendKeyless(2) error = %v, want %v", err, ErrNoAvailablePartition)
		}
		assertStateMatches(t, p.SnapshotState(), before)
	})
}

type replayOperation struct {
	kind      string
	key       string
	size      int
	partition int
	available bool
}

type replayResult struct {
	partition int
	err       string
	state     Snapshot
}

func TestReplayProducesSamePartitionsAndMatchesNaiveSimulation(t *testing.T) {
	script := []replayOperation{
		{kind: "keyless", size: 4},
		{kind: "set", partition: 1, available: false},
		{kind: "keyless", size: 6},
		{kind: "keyed", key: "order-42"},
		{kind: "set", partition: 3, available: false},
		{kind: "keyless", size: 7},
		{kind: "set", partition: 1, available: true},
		{kind: "keyless", size: 2},
		{kind: "set", partition: 2, available: false},
		{kind: "set", partition: 3, available: true},
		{kind: "keyless", size: 1},
		{kind: "keyed", key: "order-42"},
	}

	first := runReplayScript(t, script, "replay-one")
	second := runReplayScript(t, script, "replay-two")
	if fmt.Sprint(first) != fmt.Sprint(second) {
		t.Fatalf("replay results differ:\nfirst=%v\nsecond=%v", first, second)
	}
}

func runReplayScript(t *testing.T, script []replayOperation, name string) []replayResult {
	t.Helper()

	const partitions = 5
	const threshold = 10
	p, err := New(partitions, threshold)
	if err != nil {
		t.Fatalf("%s: New() error = %v", name, err)
	}
	model := newNaive(partitions, threshold)
	results := make([]replayResult, 0, len(script))

	for i, op := range script {
		var partition int
		var callErr error
		input := ""
		reason := ""

		switch op.kind {
		case "keyless":
			partition, callErr = p.SendKeyless(op.size)
			if callErr == nil {
				want := model.keyless(op.size)
				if partition != want {
					t.Fatalf("%s step %d: got %d, naive model %d", name, i, partition, want)
				}
			}
			input = fmt.Sprintf("keyless:%d", op.size)
			reason = "send to current sticky, add bytes, and switch after send only when required"
		case "keyed":
			partition, callErr = p.SendWithKey([]byte(op.key))
			if callErr == nil {
				want := model.keyed([]byte(op.key))
				if partition != want {
					t.Fatalf("%s step %d: got %d, naive model %d", name, i, partition, want)
				}
			}
			input = fmt.Sprintf("keyed:%q", op.key)
			reason = "32-bit FNV-1a modulo N is independent of sticky state and availability"
		case "set":
			callErr = p.SetAvailable(op.partition, op.available)
			if callErr == nil {
				model.setAvailable(op.partition, op.available)
			}
			input = fmt.Sprintf("set-available:%d=%t", op.partition, op.available)
			reason = "availability is recorded; switching waits until the next keyless send"
		default:
			t.Fatalf("unknown replay operation %q", op.kind)
		}

		if callErr != nil {
			t.Fatalf("%s step %d (%s) error = %v", name, i, input, callErr)
		}
		state := p.SnapshotState()
		if op.kind != "set" {
			assertStateMatches(t, state, model.snapshot())
		}
		t.Logf("%s step=%d input=%s output=partition:%d state=sticky:%d bytes:%d available:%v reason=%s",
			name, i, input, partition, state.StickyPartition, state.AccumulatedBytes, state.Available, reason)

		results = append(results, replayResult{
			partition: partition,
			err:       "",
			state:     state,
		})
	}

	return results
}

func TestConcurrentCallsAreSerializable(t *testing.T) {
	const partitions = 8
	const threshold = 10

	p, err := New(partitions, threshold)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	const goroutines = 8
	const iterations = 200
	var wg sync.WaitGroup

	for worker := range goroutines {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := range iterations {
				switch {
				case worker == 0:
					if err := p.SetAvailable(0, i%2 == 0); err != nil {
						t.Errorf("SetAvailable() error = %v", err)
						return
					}
				case worker%2 == 0:
					partition, err := p.SendKeyless((worker+i)%threshold + 1)
					if err != nil {
						t.Errorf("SendKeyless() error = %v", err)
						return
					}
					if partition < 0 || partition >= partitions {
						t.Errorf("keyless partition %d out of range", partition)
						return
					}
				default:
					key := []byte(fmt.Sprintf("concurrent-key-%d-%d", worker, i))
					partition, err := p.SendWithKey(key)
					if err != nil {
						t.Errorf("SendWithKey() error = %v", err)
						return
					}
					if want := int(fnvNaive32(key) % uint32(partitions)); partition != want {
						t.Errorf("SendWithKey(%q) = %d, want %d", key, partition, want)
						return
					}
				}
			}
		}(worker)
	}

	wg.Wait()
	if err := p.SetAvailable(0, true); err != nil {
		t.Fatalf("SetAvailable(0, true) error = %v", err)
	}
	state := p.SnapshotState()
	t.Logf("input=concurrent-sends-and-availability-changes output=state:%+v reason=mutex serializes every complete operation while keyed results remain hash-only", state)
	if state.StickyPartition < 0 || state.StickyPartition >= partitions || state.AccumulatedBytes < 0 || state.AccumulatedBytes >= threshold {
		t.Fatalf("invalid state after concurrent calls: %+v", state)
	}
}
