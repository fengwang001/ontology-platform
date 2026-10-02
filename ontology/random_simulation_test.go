package ontology

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

type simulationAction struct {
	op   string
	s    int
	n    int
	cp   int
	newP int
}

type naiveRegistry struct {
	P, M             int
	lb, ln, life     int
	txns             map[Name]transaction
	committedRecords int
	abortedRecords   int
	probes           int
	visited          int
}

type operationResult struct {
	err     error
	names   []Name
	restore RestoreResult
	info    TxnInfo
	stats   Stats
}

func newNaive(parallelism, missLimit int) (*naiveRegistry, error) {
	if parallelism < 1 || parallelism > 64 || missLimit < 1 || missLimit > 1000 {
		return nil, ErrInvalidArgument
	}
	return &naiveRegistry{P: parallelism, M: missLimit, txns: make(map[Name]transaction)}, nil
}

func sortBySubtask(names []Name) {
	sort.Slice(names, func(i, j int) bool {
		if names[i].Subtask != names[j].Subtask {
			return names[i].Subtask < names[j].Subtask
		}
		return names[i].Checkpoint < names[j].Checkpoint
	})
}

func sortByCheckpoint(names []Name) {
	sort.Slice(names, func(i, j int) bool {
		if names[i].Checkpoint != names[j].Checkpoint {
			return names[i].Checkpoint < names[j].Checkpoint
		}
		return names[i].Subtask < names[j].Subtask
	})
}

func sameError(got, want error) bool {
	if got == nil || want == nil {
		return got == want
	}
	return errors.Is(got, want)
}

func applyNaive(m *naiveRegistry, action simulationAction) operationResult {
	result := operationResult{}
	switch action.op {
	case "write":
		result.err = m.write(action.s, action.n)
	case "barrier":
		result.names, result.err = m.barrier(action.cp)
	case "complete":
		result.names, result.err = m.complete(action.cp)
	case "restore":
		result.restore, result.err = m.restore(action.cp, action.newP)
	case "txn":
		result.info, result.err = m.txn(action.s, action.cp)
	case "pending":
		result.names = m.pending()
	case "stats":
		result.stats = m.stats()
	}
	return result
}

func applyReal(r *Registry, action simulationAction) operationResult {
	result := operationResult{}
	switch action.op {
	case "write":
		result.err = r.Write(action.s, action.n)
	case "barrier":
		result.names, result.err = r.Barrier(action.cp)
	case "complete":
		result.names, result.err = r.Complete(action.cp)
	case "restore":
		result.restore, result.err = r.Restore(action.cp, action.newP)
	case "txn":
		result.info, result.err = r.Txn(action.s, action.cp)
	case "pending":
		result.names = r.Pending()
	case "stats":
		result.stats = r.Stats()
	}
	return result
}

func currentPreparedIndex(m *naiveRegistry) []Name {
	prepared := make([]Name, 0)
	for key, txn := range m.txns {
		if txn.status == PREPARED && txn.life == m.life {
			prepared = append(prepared, key)
		}
	}
	sortByCheckpoint(prepared)
	return prepared
}

func assertModelState(t *testing.T, r *Registry, m *naiveRegistry, action simulationAction) {
	t.Helper()
	if r.parallelism != m.P || r.missLimit != m.M || r.lastBarrier != m.lb || r.lastNotify != m.ln || r.life != m.life {
		t.Fatalf("input=%#v state mismatch real=(P=%d M=%d lb=%d ln=%d life=%d) naive=(P=%d M=%d lb=%d ln=%d life=%d)",
			action, r.parallelism, r.missLimit, r.lastBarrier, r.lastNotify, r.life, m.P, m.M, m.lb, m.ln, m.life)
	}
	if !reflect.DeepEqual(r.txns, m.txns) {
		t.Fatalf("input=%#v transaction table mismatch\nreal=%#v\nnaive=%#v", action, r.txns, m.txns)
	}
	preparedIndex := currentPreparedIndex(m)
	if len(r.prepared) != len(preparedIndex) {
		t.Fatalf("input=%#v prepared index mismatch real=%#v naive=%#v", action, r.prepared, currentPreparedIndex(m))
	}
	for i := range preparedIndex {
		if r.prepared[i] != preparedIndex[i] {
			t.Fatalf("input=%#v prepared index mismatch real=%#v naive=%#v", action, r.prepared, preparedIndex)
		}
	}
	if r.committedRecords != m.committedRecords || r.abortedRecords != m.abortedRecords || r.probes != m.probes || r.visited != m.visited {
		t.Fatalf("input=%#v counters mismatch real=(commit=%d abort=%d probes=%d visited=%d) naive=(commit=%d abort=%d probes=%d visited=%d)",
			action, r.committedRecords, r.abortedRecords, r.probes, r.visited, m.committedRecords, m.abortedRecords, m.probes, m.visited)
	}
	stats := r.Stats()
	if want := m.stats(); !reflect.DeepEqual(stats, want) {
		t.Fatalf("input=%#v stats mismatch real=%#v naive=%#v", action, stats, want)
	}
	if total := stats.CommittedRecords + stats.AbortedRecords + stats.OpenRecords + stats.PreparedRecords; total < 0 {
		t.Fatalf("input=%#v record conservation overflow", action)
	}
}

func (m *naiveRegistry) write(subtask, records int) error {
	if subtask < 0 || subtask >= m.P || records < 1 || records > 1_000_000 {
		return ErrInvalidArgument
	}

	key := Name{Subtask: subtask, Checkpoint: m.lb + 1}
	current, exists := m.txns[key]
	if exists && current.status == OPEN && current.life == m.life {
		current.records += records
		m.txns[key] = current
		return nil
	}

	if exists && current.status != COMMITTED && current.status != ABORTED && current.life != m.life {
		current.status = ABORTED
		m.txns[key] = current
		m.abortedRecords += current.records
	}

	epoch := 0
	if exists {
		epoch = current.epoch + 1
	}
	m.txns[key] = transaction{status: OPEN, epoch: epoch, records: records, life: m.life}
	return nil
}

func (m *naiveRegistry) barrier(checkpoint int) ([]Name, error) {
	if checkpoint < 1 {
		return nil, ErrInvalidArgument
	}
	if checkpoint != m.lb+1 {
		return nil, ErrCheckpointOrder
	}

	prepared := make([]Name, 0, m.P)
	for subtask := 0; subtask < m.P; subtask++ {
		key := Name{Subtask: subtask, Checkpoint: checkpoint}
		txn := m.txns[key]
		if txn.status == OPEN && txn.life == m.life {
			txn.status = PREPARED
			m.txns[key] = txn
			prepared = append(prepared, key)
		}
	}
	m.lb = checkpoint
	return prepared, nil
}

func (m *naiveRegistry) currentPreparedThrough(checkpoint int) ([]Name, bool) {
	prepared := make([]Name, 0)
	hasLater := false
	for key, txn := range m.txns {
		if txn.status != PREPARED || txn.life != m.life {
			continue
		}
		if key.Checkpoint <= checkpoint {
			prepared = append(prepared, key)
		} else {
			hasLater = true
		}
	}
	sortByCheckpoint(prepared)
	return prepared, hasLater
}

func (m *naiveRegistry) commitPrepared(checkpoint int) []Name {
	committed, hasLater := m.currentPreparedThrough(checkpoint)
	m.visited += len(committed)
	if hasLater {
		m.visited++
	}
	for _, key := range committed {
		txn := m.txns[key]
		txn.status = COMMITTED
		m.txns[key] = txn
		m.committedRecords += txn.records
	}
	return committed
}

func (m *naiveRegistry) complete(checkpoint int) ([]Name, error) {
	if checkpoint < 1 {
		return nil, ErrInvalidArgument
	}
	if checkpoint <= m.ln {
		return nil, ErrStaleNotification
	}
	if checkpoint > m.lb {
		return nil, ErrFutureNotification
	}

	committed := m.commitPrepared(checkpoint)
	m.ln = checkpoint
	return committed, nil
}

func (m *naiveRegistry) restore(checkpoint, parallelism int) (RestoreResult, error) {
	if checkpoint < 0 || parallelism < 1 || parallelism > 64 {
		return RestoreResult{}, ErrInvalidArgument
	}
	if checkpoint < m.ln {
		return RestoreResult{}, ErrRestoreRollback
	}
	if checkpoint > m.lb {
		return RestoreResult{}, ErrRestoreFuture
	}

	result := RestoreResult{Committed: make([]Name, 0), Aborted: make([]Name, 0)}
	result.Committed = m.commitPrepared(checkpoint)

	sweepSubtasks := m.P
	if parallelism > sweepSubtasks {
		sweepSubtasks = parallelism
	}
	for subtask := 0; subtask < sweepSubtasks; subtask++ {
		misses := 0
		nextCheckpoint := checkpoint + 1
		for misses < m.M {
			key := Name{Subtask: subtask, Checkpoint: nextCheckpoint}
			nextCheckpoint++
			m.probes++
			result.Probes++

			txn := m.txns[key]
			if txn.status == OPEN || txn.status == PREPARED {
				txn.status = ABORTED
				m.txns[key] = txn
				m.abortedRecords += txn.records
				result.Aborted = append(result.Aborted, key)
				misses = 0
			} else {
				misses++
			}
		}
	}

	m.P = parallelism
	m.lb = checkpoint
	m.ln = checkpoint
	m.life++
	return result, nil
}

func (m *naiveRegistry) txn(subtask, checkpoint int) (TxnInfo, error) {
	if subtask < 0 || subtask >= 64 || checkpoint < 1 {
		return TxnInfo{}, ErrInvalidArgument
	}
	txn, exists := m.txns[Name{Subtask: subtask, Checkpoint: checkpoint}]
	if !exists {
		return TxnInfo{}, ErrNotFound
	}
	return TxnInfo{Status: txn.status, Epoch: txn.epoch, Records: txn.records, Life: txn.life}, nil
}

func (m *naiveRegistry) pending() []Name {
	pending := make([]Name, 0)
	for key, txn := range m.txns {
		if txn.status == OPEN || txn.status == PREPARED {
			pending = append(pending, key)
		}
	}
	sortBySubtask(pending)
	return pending
}

func (m *naiveRegistry) stats() Stats {
	stats := Stats{CommittedRecords: m.committedRecords, AbortedRecords: m.abortedRecords, Probes: m.probes}
	for _, txn := range m.txns {
		switch txn.status {
		case OPEN:
			stats.OpenRecords += txn.records
		case PREPARED:
			stats.PreparedRecords += txn.records
		}
	}
	return stats
}

func chooseAction(rng *rand.Rand, m *naiveRegistry) simulationAction {
	switch rng.Intn(100) {
	case 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27:
		subtask := rng.Intn(m.P+2) - 1
		records := 1 + rng.Intn(12)
		if rng.Intn(12) == 0 {
			records = 0
		}
		return simulationAction{op: "write", s: subtask, n: records}
	case 28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 38, 39:
		checkpoint := m.lb + 1
		switch rng.Intn(8) {
		case 0:
			checkpoint = 0
		case 1:
			if m.lb > 0 {
				checkpoint = m.lb
			}
		case 2:
			checkpoint = m.lb + 2
		}
		return simulationAction{op: "barrier", cp: checkpoint}
	case 40, 41, 42, 43, 44, 45, 46, 47, 48, 49, 50, 51:
		if m.lb <= m.ln {
			return simulationAction{op: "pending"}
		}
		checkpoint := m.ln + 1 + rng.Intn(m.lb-m.ln)
		switch rng.Intn(8) {
		case 0:
			checkpoint = 0
		case 1:
			if m.ln > 0 {
				checkpoint = m.ln
			}
		case 2:
			checkpoint = m.lb + 1
		}
		return simulationAction{op: "complete", cp: checkpoint}
	case 52, 53, 54, 55, 56, 57, 58, 59, 60, 61, 62, 63, 64:
		checkpoint := m.ln + rng.Intn(m.lb-m.ln+1)
		switch rng.Intn(10) {
		case 0:
			checkpoint = -1
		case 1:
			if m.ln > 0 {
				checkpoint = m.ln - 1
			}
		case 2:
			checkpoint = m.lb + 1
		}
		parallelism := 1 + rng.Intn(5)
		if rng.Intn(12) == 0 {
			parallelism = 0
		}
		return simulationAction{op: "restore", cp: checkpoint, newP: parallelism}
	case 65, 66, 67, 68, 69, 70, 71, 72, 73, 74, 75, 76:
		return simulationAction{op: "txn", s: rng.Intn(m.P + 1), cp: 1 + rng.Intn(m.lb+3)}
	case 77, 78, 79, 80, 81, 82, 83, 84, 85, 86, 87:
		return simulationAction{op: "pending"}
	default:
		return simulationAction{op: "stats"}
	}
}

func normalizeNames(names []Name) []Name {
	if len(names) == 0 {
		return []Name{}
	}
	return names
}

func assertSameResult(t *testing.T, got, want operationResult, action simulationAction) {
	t.Helper()
	if !sameError(got.err, want.err) {
		t.Fatalf("input=%#v error real=%v naive=%v; judgment: errors must use the first applicable rule", action, got.err, want.err)
	}
	if !reflect.DeepEqual(normalizeNames(got.names), normalizeNames(want.names)) {
		t.Fatalf("input=%#v names real=%#v naive=%#v; judgment: returned names must use the specified total order", action, got.names, want.names)
	}
	if !reflect.DeepEqual(got.restore, want.restore) {
		t.Fatalf("input=%#v restore real=%#v naive=%#v; judgment: restore must commit, sweep, then advance life", action, got.restore, want.restore)
	}
	if !reflect.DeepEqual(got.info, want.info) {
		t.Fatalf("input=%#v txn real=%#v naive=%#v; judgment: Txn must expose status, epoch, records and life", action, got.info, want.info)
	}
	if !reflect.DeepEqual(got.stats, want.stats) {
		t.Fatalf("input=%#v stats real=%#v naive=%#v; judgment: counters and record conservation must match", action, got.stats, want.stats)
	}
}

func TestRandomSimulationAgainstNaiveModel(t *testing.T) {
	const sequences = 2000
	const operationsPerSequence = 24

	for seed := int64(0); seed < sequences; seed++ {
		t.Run(fmt.Sprintf("seed_%04d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			initialP := 1 + rng.Intn(5)
			initialM := 1 + rng.Intn(3)
			real, err := New(initialP, initialM)
			if err != nil {
				t.Fatal(err)
			}
			model, err := newNaive(initialP, initialM)
			if err != nil {
				t.Fatal(err)
			}

			writtenRecords := 0
			for step := 0; step < operationsPerSequence; step++ {
				action := chooseAction(rng, model)
				realResult := applyReal(real, action)
				wantResult := applyNaive(model, action)
				t.Logf("step=%d input=%#v output_real={err=%v names=%v restore=%#v info=%#v stats=%#v} judgment=compare error/names/restore/txn/stats then full internal state",
					step, action, realResult.err, normalizeNames(realResult.names), realResult.restore, realResult.info, realResult.stats)
				assertSameResult(t, realResult, wantResult, action)
				assertModelState(t, real, model, action)
				if action.op == "write" && realResult.err == nil {
					writtenRecords += action.n
				}
				stats := real.Stats()
				if total := stats.CommittedRecords + stats.AbortedRecords + stats.OpenRecords + stats.PreparedRecords; total != writtenRecords {
					t.Fatalf("input=%#v conservation total=%d written=%d", action, total, writtenRecords)
				}
			}
		})
	}
}
