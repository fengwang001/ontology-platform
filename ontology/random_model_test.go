package ontology

import (
	"fmt"
	"math/rand/v2"
	"os"
	"reflect"
	"testing"
)

type randomCallLog struct {
	name        string
	transaction int64
	keys        []int64
	actual      CommitResult
	expected    CommitResult
	aborted     bool
	modelAbort  bool
}

func TestRandomSequencesMatchNaiveModel(t *testing.T) {
	for seed := int64(1); seed <= 2000; seed++ {
		buckets := int(seed%16) + 1
		capacity := int((seed/17)%16) + 1
		t.Run(fmt.Sprintf("seed=%d-B=%d-A=%d", seed, buckets, capacity), func(t *testing.T) {
			rng := rand.New(rand.NewPCG(uint64(seed), uint64(seed*7919+13)))
			manager, err := NewCommitManager(buckets, capacity)
			if err != nil {
				t.Fatal(err)
			}
			model := newNaiveManager(buckets, capacity)
			logs := make([]randomCallLog, 0, 90)
			activeActual := make([]int64, 0)
			activeModel := make([]int64, 0)
			callCount := 60 + rng.IntN(41)

			for call := 0; call < callCount; call++ {
				operation := rng.IntN(10)
				switch {
				case operation == 0:
					actualTransaction := manager.Begin()
					modelTransaction := model.begin()
					logs = append(logs, randomCallLog{
						name:        "Begin",
						transaction: actualTransaction,
						actual:      CommitResult{Committed: true, Timestamp: actualTransaction, Reason: ReasonCommitted},
						expected:    CommitResult{Committed: true, Timestamp: modelTransaction, Reason: ReasonCommitted},
					})
					if actualTransaction != modelTransaction {
						t.Fatalf("begin mismatch:\n%s", renderRandomLogs(logs))
					}
					activeActual = append(activeActual, actualTransaction)
					activeModel = append(activeModel, modelTransaction)
				case operation <= 6:
					transaction, keys, usedActive := randomCommitArguments(rng, activeActual, activeModel, capacity, call)
					actual := manager.Commit(transaction, keys)
					expected := model.commit(transaction, keys)
					logs = append(logs, randomCallLog{
						name:        "Commit",
						transaction: transaction,
						keys:        append([]int64(nil), keys...),
						actual:      actual,
						expected:    expected,
					})
					if actual != expected {
						t.Fatalf("commit mismatch:\n%s", renderRandomLogs(logs))
					}
					if usedActive && (actual.Reason == ReasonCommitted ||
						actual.Reason == ReasonConflict || actual.Reason == ReasonWatermark) {
						activeActual = removeTransaction(activeActual, transaction)
						activeModel = removeTransaction(activeModel, transaction)
					}
					if actual.Committed && len(uniqueSortedKeys(keys)) > 0 {
						verifyCommittedHistory(t, model, transaction, actual.Timestamp, keys, logs)
						limit := 4 * len(uniqueSortedKeys(keys)) * capacity
						if probes := manager.probesSinceCommit(); probes > limit {
							t.Fatalf("probes=%d exceed %d:\n%s", probes, limit, renderRandomLogs(logs))
						}
					}
				default:
					transaction, usedActive := randomAbortTransaction(rng, activeActual, activeModel, call)
					actualAbort := manager.Abort(transaction)
					modelAbort := model.abort(transaction)
					logs = append(logs, randomCallLog{
						name:        "Abort",
						transaction: transaction,
						aborted:     actualAbort,
						modelAbort:  modelAbort,
					})
					if actualAbort != modelAbort {
						t.Fatalf("abort mismatch:\n%s", renderRandomLogs(logs))
					}
					if usedActive && actualAbort {
						activeActual = removeTransaction(activeActual, transaction)
						activeModel = removeTransaction(activeModel, transaction)
					}
				}

				actualSnapshot := manager.Snapshot()
				expectedSnapshot := model.snapshot()
				if !compareSnapshots(t, actualSnapshot, expectedSnapshot) {
					t.Fatalf("snapshot mismatch:\n%s", renderRandomLogs(logs))
				}
				verifyManagerInvariants(t, actualSnapshot, buckets, capacity, logs)
			}

			if os.Getenv("ONTOLOGY_VERBOSE_RANDOM") == "1" {
				for _, line := range logs {
					t.Log(renderRandomLog(line))
				}
			}
		})
	}
}

func randomCommitArguments(rng *rand.Rand, actualActive, modelActive []int64, bucketCapacity, call int) (int64, []int64, bool) {
	usedActive := len(actualActive) > 0 && rng.IntN(8) != 0
	var transaction int64
	if usedActive {
		position := rng.IntN(len(actualActive))
		transaction = actualActive[position]
		if modelActive[position] != transaction {
			panic("actual and model active transactions diverged")
		}
	} else {
		transaction = int64(call+1000) + int64(rng.IntN(10))
	}

	switch rng.IntN(12) {
	case 0:
		return transaction, nil, usedActive
	case 1:
		return transaction, make([]int64, 65), usedActive
	case 2:
		return transaction, []int64{-1}, usedActive
	case 3:
		return transaction, []int64{1_000_000_001}, usedActive
	}

	count := 1 + rng.IntN(8)
	keys := make([]int64, count)
	for index := range keys {
		switch rng.IntN(5) {
		case 0:
			keys[index] = int64(rng.IntN(12))
		case 1:
			keys[index] = int64(rng.IntN(bucketCapacity + 3))
		case 2:
			keys[index] = int64(call%13) * int64(1+rng.IntN(3))
		case 3:
			keys[index] = int64(rng.IntN(1_000_000_001))
		default:
			keys[index] = int64(rng.IntN(40))
		}
		if count > 2 && rng.IntN(3) == 0 {
			keys[index] = keys[0]
		}
	}
	return transaction, keys, usedActive
}

func randomAbortTransaction(rng *rand.Rand, actualActive, modelActive []int64, call int) (int64, bool) {
	usedActive := len(actualActive) > 0 && rng.IntN(8) != 0
	if usedActive {
		position := rng.IntN(len(actualActive))
		transaction := actualActive[position]
		if modelActive[position] != transaction {
			panic("actual and model active transactions diverged")
		}
		return transaction, true
	}
	return int64(call+2000) + int64(rng.IntN(10)), false
}

func removeTransaction(transactions []int64, target int64) []int64 {
	for position, transaction := range transactions {
		if transaction == target {
			return append(transactions[:position], transactions[position+1:]...)
		}
	}
	return transactions
}

func compareSnapshots(t *testing.T, actual Snapshot, expected Snapshot) bool {
	t.Helper()
	return reflect.DeepEqual(actual, expected)
}

func verifyManagerInvariants(t *testing.T, snapshot Snapshot, buckets, capacity int, logs []randomCallLog) {
	t.Helper()
	if snapshot.Watermark < 0 || snapshot.Watermark > snapshot.TimestampCounter {
		t.Fatalf("invalid watermark in %+v:\n%s", snapshot, renderRandomLogs(logs))
	}
	bucketSizes := make([]int, buckets)
	seen := make(map[int64]int64)
	for _, entry := range snapshot.Entries {
		if entry.Key < 0 || entry.Key > 1_000_000_000 {
			t.Fatalf("invalid key in %+v:\n%s", entry, renderRandomLogs(logs))
		}
		if entry.Timestamp <= 0 || entry.Timestamp > snapshot.TimestampCounter {
			t.Fatalf("invalid entry timestamp in %+v:\n%s", entry, renderRandomLogs(logs))
		}
		if previous, exists := seen[entry.Key]; exists && previous != entry.Timestamp {
			t.Fatalf("duplicate key %d:\n%s", entry.Key, renderRandomLogs(logs))
		}
		seen[entry.Key] = entry.Timestamp
		bucketSizes[entry.Key%int64(buckets)]++
	}
	for bucket, size := range bucketSizes {
		if size > capacity {
			t.Fatalf("bucket %d has %d entries, capacity %d:\n%s", bucket, size, capacity, renderRandomLogs(logs))
		}
	}
}

func verifyCommittedHistory(t *testing.T, model *naiveManager, transaction, commitTimestamp int64, keys []int64, logs []randomCallLog) {
	t.Helper()
	for _, key := range uniqueSortedKeys(keys) {
		lastCommit, exists := model.history[key]
		if exists && lastCommit > transaction && lastCommit < commitTimestamp {
			t.Fatalf("missed conflict key=%d s=%d c=%d lastCommit=%d:\n%s",
				key, transaction, commitTimestamp, lastCommit, renderRandomLogs(logs))
		}
	}
}

func renderRandomLogs(logs []randomCallLog) string {
	rendered := ""
	for index, log := range logs {
		rendered += fmt.Sprintf("%03d: %s\n", index+1, renderRandomLog(log))
	}
	return rendered
}

func renderRandomLog(log randomCallLog) string {
	if log.name == "Begin" {
		return fmt.Sprintf("input Begin => output s=%d, basis=shared timestamp counter", log.transaction)
	}
	if log.name == "Abort" {
		return fmt.Sprintf("input Abort(s=%d) => output active=%t, model=%t, basis=%s",
			log.transaction, log.aborted, log.modelAbort, abortBasis(log.aborted))
	}
	return fmt.Sprintf("input Commit(s=%d, keys=%v) => output actual={%t,%d,%s} expected={%t,%d,%s}, basis=%s",
		log.transaction, log.keys,
		log.actual.Committed, log.actual.Timestamp, log.actual.Reason,
		log.expected.Committed, log.expected.Timestamp, log.expected.Reason,
		commitBasis(log.actual))
}

func commitBasis(result CommitResult) string {
	switch result.Reason {
	case ReasonCommitted:
		if result.Timestamp == 0 {
			return "empty write set consumes no timestamp"
		}
		return "checks passed; allocated shared commit timestamp"
	case ReasonConflict:
		return "entry timestamp greater than transaction timestamp"
	case ReasonWatermark:
		return "eviction watermark greater than transaction timestamp"
	case ReasonInvalidTxn:
		return "transaction is not active"
	case ReasonTooManyKeys:
		return "more than 64 input keys"
	case ReasonInvalidKey:
		return "key outside 0..1000000000"
	default:
		return result.Reason
	}
}

func abortBasis(active bool) string {
	if active {
		return "active transaction removed"
	}
	return "transaction is not active"
}
