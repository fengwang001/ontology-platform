package ontology

import (
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
)

type modelOpKind int

const (
	modelBegin modelOpKind = iota
	modelCommit
	modelAbort
)

type modelOp struct {
	kind modelOpKind
	id   int64
	keys []int64
}

type naiveManager struct {
	buckets   int
	capacity  int
	next      int64
	watermark int64
	active    map[int64]bool
	table     map[int64]int64
	bucketOf  [][]int64
}

type naiveResult struct {
	kind      CommitKind
	timestamp int64
	reason    RejectReason
}

func newNaiveManager(buckets, capacity int) *naiveManager {
	return &naiveManager{
		buckets:  buckets,
		capacity: capacity,
		active:   make(map[int64]bool),
		table:    make(map[int64]int64),
		bucketOf: make([][]int64, buckets),
	}
}

func (m *naiveManager) begin() int64 {
	m.next++
	m.active[m.next] = true
	return m.next
}

func (m *naiveManager) commit(id int64, keys []int64) naiveResult {
	if !m.active[id] {
		return naiveResult{kind: CommitRejected, reason: RejectUnknownTransaction}
	}
	if len(keys) > maxCommitKeys {
		return naiveResult{kind: CommitRejected, reason: RejectTooManyKeys}
	}

	writeSet := uniqueSorted(keys)
	for _, key := range writeSet {
		if key < 0 || key > maxKey {
			return naiveResult{kind: CommitRejected, reason: RejectInvalidKey}
		}
	}

	delete(m.active, id)
	if len(writeSet) == 0 {
		return naiveResult{kind: CommitOk}
	}

	for _, key := range writeSet {
		if timestamp, exists := m.table[key]; exists && timestamp > id {
			return naiveResult{kind: CommitConflict}
		}
	}

	if m.watermark > id {
		return naiveResult{kind: CommitWatermark}
	}

	m.next++
	commitTimestamp := m.next

	oldestActive := int64(math.MaxInt64)
	for active := range m.active {
		if active < oldestActive {
			oldestActive = active
		}
	}

	for _, key := range writeSet {
		bucketID := int(key % int64(m.buckets))
		if _, exists := m.table[key]; exists {
			m.table[key] = commitTimestamp
			continue
		}

		bucket := m.bucketOf[bucketID]
		if len(bucket) == m.capacity {
			kept := bucket[:0]
			for _, bucketKey := range bucket {
				if m.table[bucketKey] > oldestActive {
					kept = append(kept, bucketKey)
				} else {
					delete(m.table, bucketKey)
				}
			}
			bucket = kept
		}

		if len(bucket) == m.capacity {
			victim := 0
			for index := 1; index < len(bucket); index++ {
				currentTime := m.table[bucket[index]]
				victimTime := m.table[bucket[victim]]
				if currentTime < victimTime || (currentTime == victimTime && bucket[index] < bucket[victim]) {
					victim = index
				}
			}

			victimKey := bucket[victim]
			if m.table[victimKey] > m.watermark {
				m.watermark = m.table[victimKey]
			}
			delete(m.table, victimKey)
			bucket = append(bucket[:victim], bucket[victim+1:]...)
		}

		m.table[key] = commitTimestamp
		m.bucketOf[bucketID] = append(bucket, key)
	}

	return naiveResult{kind: CommitOk, timestamp: commitTimestamp}
}

func (m *naiveManager) abort(id int64) bool {
	if !m.active[id] {
		return false
	}
	delete(m.active, id)
	return true
}

func uniqueSorted(keys []int64) []int64 {
	seen := make(map[int64]bool, len(keys))
	unique := make([]int64, 0, len(keys))
	for _, key := range keys {
		if !seen[key] {
			seen[key] = true
			unique = append(unique, key)
		}
	}
	sort.Slice(unique, func(i, j int) bool { return unique[i] < unique[j] })
	return unique
}

func TestRandomSequencesMatchNaiveSimulation(t *testing.T) {
	for seed := int64(1); seed <= 2000; seed++ {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			random := rand.New(rand.NewSource(seed))
			buckets := 1 + random.Intn(8)
			capacity := 1 + random.Intn(4)
			actual, err := NewSnapshotCommitManager(buckets, capacity)
			if err != nil {
				t.Fatal(err)
			}
			model := newNaiveManager(buckets, capacity)
			activeIDs := make([]int64, 0, 12)
			history := make(map[int64]map[int64]int64)
			var log strings.Builder
			fmt.Fprintf(&log, "config B=%d A=%d\n", buckets, capacity)

			operations := 30 + random.Intn(50)
			for operation := 0; operation < operations; operation++ {
				var op modelOp
				switch {
				case len(activeIDs) == 0 || random.Intn(3) == 0:
					op = modelOp{kind: modelBegin}
				case random.Intn(5) == 0:
					op = modelOp{kind: modelAbort, id: activeIDs[random.Intn(len(activeIDs))]}
				default:
					id := activeIDs[random.Intn(len(activeIDs))]
					keyCount := random.Intn(5)
					keys := make([]int64, keyCount)
					for index := range keys {
						switch random.Intn(12) {
						case 0:
							keys[index] = -1
						case 1:
							keys[index] = maxKey + 1
						default:
							keys[index] = int64(random.Intn(24))
						}
					}
					op = modelOp{kind: modelCommit, id: id, keys: keys}
				}

				switch op.kind {
				case modelBegin:
					actualID := actual.Begin()
					modelID := model.begin()
					fmt.Fprintf(&log, "Begin actual=%d naive=%d\n", actualID, modelID)
					if actualID != modelID {
						t.Fatalf("begin mismatch\n%s", log.String())
					}
					activeIDs = append(activeIDs, actualID)
				case modelCommit:
					before := actual.Snapshot()
					actualResult := actual.Commit(op.id, op.keys)
					modelResult := model.commit(op.id, op.keys)
					fmt.Fprintf(&log, "Commit(s=%d,keys=%v) actual={kind:%s timestamp:%d reason:%s} naive={kind:%s timestamp:%d reason:%s} basis=%s\n",
						op.id, op.keys,
						actualResult.Kind, actualResult.Timestamp, actualResult.Reason,
						modelResult.kind, modelResult.timestamp, modelResult.reason,
						commitBasis(actualResult, op, before.Watermark))
					if actualResult.Kind != modelResult.kind || actualResult.Timestamp != modelResult.timestamp || actualResult.Reason != modelResult.reason {
						t.Fatalf("commit result mismatch\n%s", log.String())
					}

					if actualResult.Kind == CommitOk && actualResult.Timestamp > 0 {
						validateNoMissedConflict(t, op.id, op.keys, actualResult.Timestamp, history, log.String())
						recordHistory(op.id, op.keys, actualResult.Timestamp, history)
					}
					if actualResult.Kind != CommitRejected && removeActive(activeIDs, op.id) >= 0 {
						activeIDs = removeAt(activeIDs, removeActive(activeIDs, op.id))
					}
				case modelAbort:
					actualErr := actual.Abort(op.id)
					modelOK := model.abort(op.id)
					fmt.Fprintf(&log, "Abort(s=%d) actual=%v naive=%t\n", op.id, actualErr, modelOK)
					if (actualErr == nil) != modelOK {
						t.Fatalf("abort mismatch\n%s", log.String())
					}
					if modelOK {
						activeIDs = removeAt(activeIDs, removeActive(activeIDs, op.id))
					}
				}

				assertModelState(t, actual, model, log.String())
			}

			if testing.Verbose() {
				t.Logf("\n%s", log.String())
			}
		})
	}
}

func TestNoEvictionMatchesUnboundedFirstCommitterWins(t *testing.T) {
	for seed := int64(10000); seed < 10500; seed++ {
		random := rand.New(rand.NewSource(seed))
		actual, _ := NewSnapshotCommitManager(1024, 16)
		unbounded := newNaiveManager(1024, 256)

		for operation := 0; operation < 80; operation++ {
			sequence := actual.Begin()
			modelSequence := unbounded.begin()
			if sequence != modelSequence {
				t.Fatalf("seed %d begin mismatch %d != %d", seed, sequence, modelSequence)
			}
			keys := []int64(nil)
			for index := random.Intn(5); index > 0; index-- {
				keys = append(keys, int64(random.Intn(20000)))
			}

			if random.Intn(6) == 0 {
				if err := actual.Abort(sequence); err != nil {
					t.Fatal(err)
				}
				if !unbounded.abort(sequence) {
					t.Fatal("unbounded abort rejected")
				}
				continue
			}

			actualResult := actual.Commit(sequence, keys)
			modelResult := unbounded.commit(sequence, keys)
			if actualResult.Kind != modelResult.kind || actualResult.Timestamp != modelResult.timestamp || actualResult.Reason != modelResult.reason {
				t.Fatalf("seed %d result mismatch bounded=%#v unbounded=%#v keys=%v",
					seed, actualResult, modelResult, keys)
			}
		}

		actualState := actual.Snapshot()
		if actualState.Watermark != 0 {
			t.Fatalf("seed %d unexpectedly evicted: watermark=%d", seed, actualState.Watermark)
		}
		boundedTable := tableFromSnapshot(actualState)
		unboundedTable := make(map[int64]int64, len(unbounded.table))
		for key, timestamp := range unbounded.table {
			unboundedTable[key] = timestamp
		}
		if !reflect.DeepEqual(boundedTable, unboundedTable) {
			t.Fatalf("seed %d tables differ bounded=%v unbounded=%v", seed, boundedTable, unboundedTable)
		}
	}
}

func commitBasis(result CommitResult, op modelOp, beforeWatermark int64) string {
	switch result.Kind {
	case CommitRejected:
		return "rejected-before-state-change:" + fmt.Sprint(result.Reason)
	case CommitConflict:
		return "existing-table-entry-timestamp-greater-than-start"
	case CommitWatermark:
		return fmt.Sprintf("watermark-%d-greater-than-start-%d", beforeWatermark, op.id)
	case CommitOk:
		if len(op.keys) == 0 {
			return "empty-write-set"
		}
		return "checks-passed"
	default:
		return "unknown"
	}
}

func assertModelState(t *testing.T, actual *SnapshotCommitManager, model *naiveManager, log string) {
	t.Helper()

	got := actual.Snapshot()
	if got.NextTimestamp != model.next || got.Watermark != model.watermark {
		t.Fatalf("timestamp state mismatch got=(%d,%d) want=(%d,%d)\n%s",
			got.NextTimestamp, got.Watermark, model.next, model.watermark, log)
	}

	gotActive := append([]int64(nil), got.Active...)
	wantActive := make([]int64, 0, len(model.active))
	for id := range model.active {
		wantActive = append(wantActive, id)
	}
	sort.Slice(wantActive, func(i, j int) bool { return wantActive[i] < wantActive[j] })
	if !int64SlicesEqual(gotActive, wantActive) {
		t.Fatalf("active mismatch got=%v want=%v\n%s", gotActive, wantActive, log)
	}

	wantTable := make(map[int64]int64)
	for bucketID, keys := range model.bucketOf {
		if len(keys) > model.capacity {
			t.Fatalf("model bucket %d overflow: %v\n%s", bucketID, keys, log)
		}
		for _, key := range keys {
			wantTable[key] = model.table[key]
		}
	}

	gotTable := make(map[int64]int64)
	for bucketID, bucket := range got.Buckets {
		if len(bucket) > model.capacity {
			t.Fatalf("actual bucket %d overflow: %#v\n%s", bucketID, bucket, log)
		}
		for _, entry := range bucket {
			if entry.Key%int64(model.buckets) != int64(bucketID) {
				t.Fatalf("key %d stored in wrong bucket %d\n%s", entry.Key, bucketID, log)
			}
			gotTable[entry.Key] = entry.Timestamp
		}
	}
	if !reflect.DeepEqual(gotTable, wantTable) {
		t.Fatalf("table mismatch got=%v want=%v\n%s", gotTable, wantTable, log)
	}
}

func int64SlicesEqual(left, right []int64) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func tableFromSnapshot(state StateSnapshot) map[int64]int64 {
	table := make(map[int64]int64)
	for _, bucket := range state.Buckets {
		for _, entry := range bucket {
			table[entry.Key] = entry.Timestamp
		}
	}
	return table
}

func recordHistory(start int64, keys []int64, commitTimestamp int64, history map[int64]map[int64]int64) {
	for _, key := range uniqueSorted(keys) {
		if history[key] == nil {
			history[key] = make(map[int64]int64)
		}
		history[key][start] = commitTimestamp
	}
}

func validateNoMissedConflict(t *testing.T, start int64, keys []int64, commitTimestamp int64, history map[int64]map[int64]int64, log string) {
	t.Helper()

	for _, key := range uniqueSorted(keys) {
		for previousStart, previousCommit := range history[key] {
			if previousStart < start && previousCommit > start && previousCommit < commitTimestamp {
				t.Fatalf("missed conflict key=%d s=%d c=%d previous s=%d c=%d\n%s",
					key, start, commitTimestamp, previousStart, previousCommit, log)
			}
		}
	}
}

func removeActive(active []int64, id int64) int {
	for index, activeID := range active {
		if activeID == id {
			return index
		}
	}
	return -1
}

func removeAt(active []int64, index int) []int64 {
	return append(active[:index], active[index+1:]...)
}
