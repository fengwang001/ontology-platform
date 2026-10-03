package ontology

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

type randomOperation struct {
	name  string
	txID  int
	key   int
	value int
}

func runRandomSequence(t *testing.T, seed int64) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	keys := 1 + rng.Intn(4)
	history := 2 + rng.Intn(3)
	actual, err := NewAuthenticator(keys, history)
	if err != nil {
		t.Fatalf("seed %d config K=%d H=%d: %v", seed, keys, history, err)
	}
	reference := newNaiveModel(keys, history)
	var active []int
	operations := make([]randomOperation, 0, 24)

	t.Logf("seed=%d input config K=%d H=%d", seed, keys, history)
	for step := 0; step < 24; step++ {
		var operation randomOperation
		if len(active) == 0 || rng.Intn(7) == 0 {
			operation = randomOperation{name: "begin"}
		} else {
			operation.txID = active[rng.Intn(len(active))]
			switch rng.Intn(6) {
			case 0:
				operation.name = "read"
				operation.key = rng.Intn(keys + 2)
			case 1:
				operation.name = "write"
				operation.key = rng.Intn(keys + 2)
				operation.value = rng.Intn(5)
			case 2, 3:
				operation.name = "commit"
			default:
				operation.name = "abort"
			}
		}
		operations = append(operations, operation)

		switch operation.name {
		case "begin":
			actualTx := actual.Begin()
			referenceTx := reference.begin()
			t.Logf("step=%d Begin => actual=%d naive=%d snapshot=%d", step, actualTx, referenceTx, actual.state.transactions[actualTx].snapshot)
			if actualTx != referenceTx {
				t.Fatalf("seed %d Begin mismatch", seed)
			}
			active = append(active, actualTx)
		case "read":
			actualValue, actualErr := actual.Read(operation.txID, operation.key)
			referenceValue, referenceErr := reference.read(operation.txID, operation.key)
			t.Logf("step=%d Read(tx=%d,key=%d) => actual=(%d,%v) naive=(%d,%v); %s", step, operation.txID, operation.key, actualValue, actualErr, referenceValue, referenceErr, reference.logs[len(reference.logs)-1])
			if actualValue != referenceValue || errorName(actualErr) != errorName(referenceErr) {
				t.Fatalf("seed %d Read mismatch", seed)
			}
		case "write":
			actualErr := actual.Write(operation.txID, operation.key, operation.value)
			referenceErr := reference.write(operation.txID, operation.key, operation.value)
			t.Logf("step=%d Write(tx=%d,key=%d,value=%d) => actual=%v naive=%v; %s", step, operation.txID, operation.key, operation.value, actualErr, referenceErr, reference.logs[len(reference.logs)-1])
			if errorName(actualErr) != errorName(referenceErr) {
				t.Fatalf("seed %d Write mismatch", seed)
			}
		case "commit":
			actualCommit, actualReason, actualErr := actual.Commit(operation.txID)
			referenceCommit, referenceReason, referenceErr := reference.commit(operation.txID)
			t.Logf("step=%d Commit(tx=%d) => actual=(%d,%d,%v) naive=(%d,%d,%v); %s", step, operation.txID, actualCommit, actualReason, actualErr, referenceCommit, referenceReason, referenceErr, reference.logs[len(reference.logs)-1])
			if actualCommit != referenceCommit || actualReason != referenceReason || errorName(actualErr) != errorName(referenceErr) {
				t.Fatalf("seed %d Commit mismatch", seed)
			}
			active = removeTx(active, operation.txID)
		case "abort":
			actualErr := actual.Abort(operation.txID)
			referenceErr := reference.abort(operation.txID)
			t.Logf("step=%d Abort(tx=%d) => actual=%v naive=%v; %s", step, operation.txID, actualErr, referenceErr, reference.logs[len(reference.logs)-1])
			if errorName(actualErr) != errorName(referenceErr) {
				t.Fatalf("seed %d Abort mismatch", seed)
			}
			if actualErr == nil {
				active = removeTx(active, operation.txID)
			}
		}

		actualState := actualSnapshot(actual)
		referenceState := reference.snapshot()
		if !reflect.DeepEqual(actualState, referenceState) {
			t.Fatalf("seed %d step %d state mismatch\nactual=%#v\nnaive=%#v", seed, step, actualState, referenceState)
		}
	}

	edges := reference.dependencyGraph()
	for _, edge := range edges {
		t.Logf("seed=%d dependency edge %d -> %d %s", seed, edge.from, edge.to, edge.kind)
	}
	if graphHasCycle(edges) {
		t.Fatalf("seed %d committed dependency graph has a cycle", seed)
	}
	t.Logf("seed=%d output acceptedCommits=%d acyclic=true operations=%s", seed, reference.n, formatOperations(operations))
}

func errorName(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func removeTx(active []int, txID int) []int {
	filtered := active[:0]
	for _, candidate := range active {
		if candidate != txID {
			filtered = append(filtered, candidate)
		}
	}
	return filtered
}

func formatOperations(operations []randomOperation) string {
	return fmt.Sprint(operations)
}

func TestNaiveRandomSequences(t *testing.T) {
	for seed := int64(1); seed <= 2000; seed++ {
		t.Run("seed", func(t *testing.T) {
			runRandomSequence(t, seed)
		})
	}
}
