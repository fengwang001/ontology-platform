package ontology

import (
	"errors"
	"fmt"
	"math/rand"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type captureLogger struct {
	lines []string
}

func (l *captureLogger) Printf(format string, args ...any) {
	l.lines = append(l.lines, strings.TrimSpace(fmt.Sprintf(format, args...)))
}

type testMessage struct {
	partition int
	offset    int
	size      int
	payload   string
}

type naiveAssembler struct {
	partitions      int
	totalBudget     int
	partitionBudget int
	queues          [][]testMessage
	consumed        []int
	startPartition  int
}

type naiveResult struct {
	messages       []testMessage
	totalBytes     int
	partitionBytes []int
	nextStart      int
}

func newNaive(partitionCount int, totalBudget int, partitionBudget int) *naiveAssembler {
	return &naiveAssembler{
		partitions:      partitionCount,
		totalBudget:     totalBudget,
		partitionBudget: partitionBudget,
		queues:          make([][]testMessage, partitionCount),
		consumed:        make([]int, partitionCount),
	}
}

func (m *naiveAssembler) append(message testMessage) int {
	offset := len(m.queues[message.partition])
	message.offset = offset
	m.queues[message.partition] = append(m.queues[message.partition], message)
	return offset
}

func (m *naiveAssembler) fetch() (*naiveResult, error) {
	messages := make([]testMessage, 0)
	partitionBytes := make([]int, m.partitions)
	totalBytes := 0
	lastPartition := -1

	for step := 0; step < m.partitions; step++ {
		partition := (m.startPartition + step) % m.partitions
		for m.consumed[partition] < len(m.queues[partition]) {
			message := m.queues[partition][m.consumed[partition]]
			if totalBytes == 0 {
				messages = append(messages, message)
				partitionBytes[partition] += message.size
				totalBytes += message.size
				m.consumed[partition]++
				lastPartition = partition
				continue
			}
			if partitionBytes[partition]+message.size > m.partitionBudget || totalBytes+message.size > m.totalBudget {
				break
			}
			messages = append(messages, message)
			partitionBytes[partition] += message.size
			totalBytes += message.size
			m.consumed[partition]++
			lastPartition = partition
		}
	}

	if len(messages) == 0 {
		return nil, errors.New("no data")
	}
	m.startPartition = (lastPartition + 1) % m.partitions
	return &naiveResult{
		messages:       messages,
		totalBytes:     totalBytes,
		partitionBytes: partitionBytes,
		nextStart:      m.startPartition,
	}, nil
}

func newTestPair(t *testing.T, n int, w int, p int) (*Assembler[string], *naiveAssembler, *captureLogger) {
	t.Helper()
	logger := &captureLogger{}
	assembler, err := New[string](n, w, p, logger)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return assembler, newNaive(n, w, p), logger
}

func appendBoth(t *testing.T, assembler *Assembler[string], model *naiveAssembler, partition int, size int, payload string) {
	t.Helper()
	gotOffset, err := assembler.Append(partition, size, payload)
	if err != nil {
		t.Fatalf("Append(%d, %d, %q) error = %v", partition, size, payload, err)
	}
	wantOffset := model.append(testMessage{partition: partition, size: size, payload: payload})
	if gotOffset != wantOffset {
		t.Fatalf("Append offset = %d, want %d", gotOffset, wantOffset)
	}
}

func assertFetchMatches(t *testing.T, assembler *Assembler[string], model *naiveAssembler) *FetchResult[string] {
	t.Helper()
	got, err := assembler.Fetch()
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	want, err := model.fetch()
	if err != nil {
		t.Fatalf("naive Fetch() error = %v", err)
	}
	if got.TotalBytes != want.totalBytes || got.NextStartPartition != want.nextStart {
		t.Fatalf("Fetch totals = (bytes=%d,next=%d), want (bytes=%d,next=%d)", got.TotalBytes, got.NextStartPartition, want.totalBytes, want.nextStart)
	}
	if len(got.Messages) != len(want.messages) {
		t.Fatalf("Fetch messages len = %d, want %d", len(got.Messages), len(want.messages))
	}
	for i := range want.messages {
		gotMessage := got.Messages[i]
		wantMessage := want.messages[i]
		if gotMessage.Partition != wantMessage.partition || gotMessage.Offset != wantMessage.offset || gotMessage.Size != wantMessage.size || gotMessage.Payload != wantMessage.payload {
			t.Fatalf("message %d = %+v, want %+v", i, gotMessage, wantMessage)
		}
	}
	for partition, wantBytes := range want.partitionBytes {
		if got.PartitionBytes[partition] != wantBytes {
			t.Fatalf("partition %d bytes = %d, want %d", partition, got.PartitionBytes[partition], wantBytes)
		}
	}
	return got
}

func TestExactPartitionAndTotalBudgets(t *testing.T) {
	assembler, model, logger := newTestPair(t, 3, 10, 10)
	appendBoth(t, assembler, model, 0, 4, "p0-0")
	appendBoth(t, assembler, model, 0, 6, "p0-1")
	appendBoth(t, assembler, model, 0, 1, "p0-2")
	appendBoth(t, assembler, model, 1, 6, "p1-0")

	first := assertFetchMatches(t, assembler, model)
	if first.TotalBytes != 10 || first.PartitionBytes[0] != 10 || first.NextStartPartition != 1 {
		t.Fatalf("first = %+v", first)
	}
	_ = assembler.Snapshot()
	for _, want := range []string{
		"accept Append: partition=0 offset=0 size=4",
		"start Fetch: start=0",
		"take-first-exception total=4",
		"blocked-by-total",
		"finish Fetch:",
		"query Snapshot:",
	} {
		if !containsLog(logger.lines, want) {
			t.Fatalf("logs missing %q: %v", want, logger.lines)
		}
	}
}

func TestFirstOversizedMessageIsReturnedOnce(t *testing.T) {
	assembler, model, _ := newTestPair(t, 2, 5, 3)
	appendBoth(t, assembler, model, 0, 8, "oversized")

	result := assertFetchMatches(t, assembler, model)
	if len(result.Messages) != 1 || result.TotalBytes != 8 || result.NextStartPartition != 1 {
		t.Fatalf("oversized result = %+v", result)
	}

	appendBoth(t, assembler, model, 1, 1, "after")
	second := assertFetchMatches(t, assembler, model)
	if len(second.Messages) != 1 || second.Messages[0].Payload != "after" || second.NextStartPartition != 0 {
		t.Fatalf("second result = %+v", second)
	}
}

func TestFirstOversizedPartitionMessageAllowsLaterPartitions(t *testing.T) {
	assembler, model, _ := newTestPair(t, 3, 10, 4)
	appendBoth(t, assembler, model, 0, 7, "over-p-only")
	appendBoth(t, assembler, model, 0, 1, "retained")
	appendBoth(t, assembler, model, 1, 3, "fits-later")

	result := assertFetchMatches(t, assembler, model)
	if len(result.Messages) != 2 || result.TotalBytes != 10 || result.PartitionBytes[0] != 7 || result.PartitionBytes[1] != 3 {
		t.Fatalf("result = %+v", result)
	}
	if result.NextStartPartition != 2 {
		t.Fatalf("next start = %d, want 2", result.NextStartPartition)
	}

	next := assertFetchMatches(t, assembler, model)
	if len(next.Messages) != 1 || next.Messages[0].Payload != "retained" {
		t.Fatalf("next = %+v", next)
	}
}

func TestBlockedLargeMessageIsNotSkipped(t *testing.T) {
	assembler, model, _ := newTestPair(t, 1, 5, 5)
	appendBoth(t, assembler, model, 0, 2, "small")
	appendBoth(t, assembler, model, 0, 4, "large")
	appendBoth(t, assembler, model, 0, 2, "later-small")

	result := assertFetchMatches(t, assembler, model)
	if len(result.Messages) != 1 || result.Messages[0].Payload != "small" {
		t.Fatalf("result = %+v", result)
	}

	next := assertFetchMatches(t, assembler, model)
	if len(next.Messages) != 1 || next.Messages[0].Payload != "large" {
		t.Fatalf("next = %+v", next)
	}

	later := assertFetchMatches(t, assembler, model)
	if len(later.Messages) != 1 || later.Messages[0].Payload != "later-small" {
		t.Fatalf("later = %+v", later)
	}
}

func TestTotalBudgetShortageDoesNotBlockLaterPartitions(t *testing.T) {
	assembler, model, _ := newTestPair(t, 3, 6, 10)
	appendBoth(t, assembler, model, 0, 4, "first")
	appendBoth(t, assembler, model, 0, 3, "too-large")
	appendBoth(t, assembler, model, 1, 1, "still-fits")
	appendBoth(t, assembler, model, 2, 1, "also-fits")

	result := assertFetchMatches(t, assembler, model)
	if result.TotalBytes != 6 || result.NextStartPartition != 0 {
		t.Fatalf("result = %+v", result)
	}
	if len(result.Messages) != 3 {
		t.Fatalf("messages = %+v", result.Messages)
	}
}

func TestRotationAdvancesAndWraps(t *testing.T) {
	assembler, model, _ := newTestPair(t, 3, 20, 20)
	appendBoth(t, assembler, model, 0, 1, "p0")
	appendBoth(t, assembler, model, 2, 2, "p2")

	first := assertFetchMatches(t, assembler, model)
	if first.NextStartPartition != 0 {
		t.Fatalf("first next = %d, want 0", first.NextStartPartition)
	}

	appendBoth(t, assembler, model, 1, 3, "p1")
	second := assertFetchMatches(t, assembler, model)
	if second.NextStartPartition != 2 {
		t.Fatalf("second next = %d, want 2", second.NextStartPartition)
	}
	if len(second.Messages) != 1 || second.Messages[0].Partition != 1 {
		t.Fatalf("second messages = %+v", second.Messages)
	}

	appendBoth(t, assembler, model, 2, 4, "p2-next")
	third := assertFetchMatches(t, assembler, model)
	if third.NextStartPartition != 0 || third.Messages[0].Partition != 2 {
		t.Fatalf("third = %+v", third)
	}
}

func TestInvalidConstructionAndAppendErrors(t *testing.T) {
	cases := []struct {
		name string
		n    int
		w    int
		p    int
		want error
	}{
		{name: "partitions", n: 0, w: 1, p: 1, want: ErrInvalidPartitions},
		{name: "total", n: 1, w: 0, p: 1, want: ErrInvalidTotalBudget},
		{name: "partition", n: 1, w: 1, p: 0, want: ErrInvalidPartitionBudget},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New[string](tc.n, tc.w, tc.p, nil)
			if !errors.Is(err, tc.want) {
				t.Fatalf("New() error = %v, want %v", err, tc.want)
			}
		})
	}

	assembler, _, _ := newTestPair(t, 2, 10, 10)
	if _, err := assembler.Append(2, 0, "both-invalid"); !errors.Is(err, ErrPartitionOutOfRange) {
		t.Fatalf("Append() error = %v, want partition out of range", err)
	}
	if _, err := assembler.Append(0, 0, "bad-size"); !errors.Is(err, ErrInvalidMessageSize) {
		t.Fatalf("Append() error = %v, want invalid size", err)
	}
	if snapshot := assembler.Snapshot(); snapshot.NextStartPartition != 0 || snapshot.AvailableCounts[0] != 0 || snapshot.AvailableCounts[1] != 0 {
		t.Fatalf("state changed after rejected append: %+v", snapshot)
	}
}

func TestFetchAllEmptyReturnsNoDataAndKeepsState(t *testing.T) {
	assembler, _, logger := newTestPair(t, 2, 10, 10)
	_, err := assembler.Fetch()
	if !errors.Is(err, ErrNoData) {
		t.Fatalf("Fetch() error = %v, want %v", err, ErrNoData)
	}
	snapshot := assembler.Snapshot()
	if snapshot.NextStartPartition != 0 || snapshot.NextOffsets[0] != 0 || snapshot.NextOffsets[1] != 0 {
		t.Fatalf("snapshot after no data = %+v", snapshot)
	}
	if !containsLog(logger.lines, "reject Fetch") || !containsLog(logger.lines, "reason=no available data") {
		t.Fatalf("logs missing no-data decision: %v", logger.lines)
	}
}

func containsLog(lines []string, want string) bool {
	for _, line := range lines {
		if strings.Contains(line, want) {
			return true
		}
	}
	return false
}

func TestRandomizedReplayMatchesNaiveSimulation(t *testing.T) {
	rng := rand.New(rand.NewSource(20261001))
	for iteration := 0; iteration < 200; iteration++ {
		partitionCount := 1 + rng.Intn(5)
		totalBudget := 1 + rng.Intn(20)
		partitionBudget := 1 + rng.Intn(12)
		assembler, model, _ := newTestPair(t, partitionCount, totalBudget, partitionBudget)
		messageCount := 0

		for step := 0; step < 80; step++ {
			if rng.Intn(2) == 0 {
				partition := rng.Intn(partitionCount)
				size := 1 + rng.Intn(14)
				payload := fmt.Sprintf("iter-%d-msg-%d", iteration, messageCount)
				messageCount++
				appendBoth(t, assembler, model, partition, size, payload)
				continue
			}

			got, gotErr := assembler.Fetch()
			want, wantErr := model.fetch()
			if gotErr != nil || wantErr != nil {
				if !errors.Is(gotErr, ErrNoData) || wantErr == nil {
					t.Fatalf("iteration %d step %d errors = (%v, %v)", iteration, step, gotErr, wantErr)
				}
				continue
			}
			if got.TotalBytes != want.totalBytes || got.NextStartPartition != want.nextStart {
				t.Fatalf("iteration %d step %d totals mismatch", iteration, step)
			}
			if len(got.Messages) != len(want.messages) {
				t.Fatalf("iteration %d step %d message count mismatch", iteration, step)
			}
			for i, wantMessage := range want.messages {
				gotMessage := got.Messages[i]
				if gotMessage.Partition != wantMessage.partition || gotMessage.Offset != wantMessage.offset || gotMessage.Size != wantMessage.size || gotMessage.Payload != wantMessage.payload {
					t.Fatalf("iteration %d step %d message %d mismatch: %+v vs %+v", iteration, step, i, gotMessage, wantMessage)
				}
			}
		}

		for {
			got, gotErr := assembler.Fetch()
			want, wantErr := model.fetch()
			if errors.Is(gotErr, ErrNoData) && wantErr != nil {
				break
			}
			if gotErr != nil || wantErr != nil {
				t.Fatalf("iteration %d drain errors = (%v, %v)", iteration, gotErr, wantErr)
			}
			if len(got.Messages) != len(want.messages) || got.NextStartPartition != want.nextStart {
				t.Fatalf("iteration %d drain mismatch", iteration)
			}
		}
	}
}

func TestConcurrentFetchDeliversEachMessageOnce(t *testing.T) {
	const partitionCount = 4
	const totalBudget = 8
	const partitionBudget = 5
	logger := &captureLogger{}
	assembler, err := New[string](partitionCount, totalBudget, partitionBudget, logger)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	totalMessages := 80
	for partition := 0; partition < partitionCount; partition++ {
		for i := 0; i < totalMessages/partitionCount; i++ {
			if _, err := assembler.Append(partition, 1+(i%7), fmt.Sprintf("p%d-%d", partition, i)); err != nil {
				t.Fatalf("Append() error = %v", err)
			}
		}
	}

	const workers = 8
	var waitGroup sync.WaitGroup
	results := make(chan []Message[string], workers)
	start := make(chan struct{})
	waitGroup.Add(workers)
	for worker := 0; worker < workers; worker++ {
		go func() {
			defer waitGroup.Done()
			<-start
			local := make([]Message[string], 0)
			for {
				result, err := assembler.Fetch()
				if errors.Is(err, ErrNoData) {
					results <- local
					return
				}
				if err != nil {
					t.Errorf("Fetch() error = %v", err)
					return
				}
				local = append(local, result.Messages...)
				if len(result.Messages) == 0 {
					t.Errorf("invalid response total bytes %d", result.TotalBytes)
				}
				if len(result.Messages) == 1 {
					continue
				}
				if result.TotalBytes > totalBudget {
					if len(result.Messages) != 1 {
						t.Errorf("oversized first-message response contained %d messages", len(result.Messages))
					}
				}
				firstPartition := result.Messages[0].Partition
				for partition, bytes := range result.PartitionBytes {
					if partition == firstPartition && result.Messages[0].Size > partitionBudget {
						continue
					}
					if bytes > partitionBudget {
						t.Errorf("partition %d bytes = %d, messages = %+v", partition, bytes, result.Messages)
					}
				}
			}
		}()
	}
	close(start)
	waitGroup.Wait()
	close(results)

	seen := make(map[string]bool)
	delivered := 0
	for messages := range results {
		for _, message := range messages {
			key := fmt.Sprintf("%d:%d", message.Partition, message.Offset)
			if seen[key] {
				t.Fatalf("message %s delivered more than once", key)
			}
			seen[key] = true
			delivered++
		}
	}
	if delivered != totalMessages {
		t.Fatalf("delivered %d messages, want %d", delivered, totalMessages)
	}

	snapshot := assembler.Snapshot()
	for partition, count := range snapshot.AvailableCounts {
		if count != 0 {
			t.Fatalf("partition %d remaining count = %d", partition, count)
		}
	}
}

func TestConcurrentAppendFetchAndSnapshot(t *testing.T) {
	const partitionCount = 3
	const perPartition = 40
	logger := &captureLogger{}
	assembler, err := New[string](partitionCount, 9, 6, logger)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	var stop atomic.Bool
	var waitGroup sync.WaitGroup
	waitGroup.Add(1)
	go func() {
		defer waitGroup.Done()
		for partition := 0; partition < partitionCount; partition++ {
			for offset := 0; offset < perPartition; offset++ {
				if _, err := assembler.Append(partition, 1+(offset%8), fmt.Sprintf("p%d-%d", partition, offset)); err != nil {
					t.Errorf("Append() error = %v", err)
				}
				runtime.Gosched()
			}
		}
		stop.Store(true)
	}()

	waitGroup.Add(1)
	go func() {
		defer waitGroup.Done()
		for !stop.Load() {
			snapshot := assembler.Snapshot()
			if snapshot.Partitions != partitionCount || len(snapshot.NextOffsets) != partitionCount {
				t.Errorf("invalid snapshot = %+v", snapshot)
			}
			runtime.Gosched()
		}
	}()

	seen := make(map[string]bool)
	for {
		result, fetchErr := assembler.Fetch()
		if errors.Is(fetchErr, ErrNoData) {
			if stop.Load() {
				counts := assembler.Snapshot().AvailableCounts
				remaining := 0
				for _, count := range counts {
					remaining += count
				}
				if remaining == 0 {
					break
				}
			}
			runtime.Gosched()
			continue
		}
		if fetchErr != nil {
			t.Fatalf("Fetch() error = %v", fetchErr)
		}
		for _, message := range result.Messages {
			key := fmt.Sprintf("%d:%d", message.Partition, message.Offset)
			if seen[key] {
				t.Fatalf("message %s delivered more than once", key)
			}
			seen[key] = true
		}
	}
	waitGroup.Wait()

	if len(seen) != partitionCount*perPartition {
		t.Fatalf("delivered %d messages, want %d", len(seen), partitionCount*perPartition)
	}
}
