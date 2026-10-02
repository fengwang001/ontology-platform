package undo

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

type randomOperation struct {
	name string
	t    int
	n    int
}

type actualSnapshotInfo struct {
	usedSlots int
	usedPages int
	caches    [2][]int
	history   []int
}

func TestRandomNaiveComparison(t *testing.T) {
	for seed := int64(1); seed <= 2000; seed++ {
		t.Run(fmt.Sprintf("seed_%04d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			s := 1 + rng.Intn(8)
			k := 1 + rng.Intn(8)
			pb := 1 + rng.Intn(20)

			manager, err := NewManager(s, k, pb)
			if err != nil {
				t.Fatal(err)
			}
			reference := newNaiveManager(s, k, pb)
			var log strings.Builder
			fmt.Fprintf(&log, "input: S=%d K=%d PB=%d; judgment=return values, slot/page totals, both cache stacks, and history chain\n", s, k, pb)

			ops := 40 + rng.Intn(80)
			for i := 0; i < ops; i++ {
				op := makeRandomOperation(rng)
				fmt.Fprintf(&log, "input op %d: %s", i+1, op.name)
				if op.t != 0 {
					fmt.Fprintf(&log, " t=%d", op.t)
				}
				if op.n != 0 {
					fmt.Fprintf(&log, " n=%d", op.n)
				}
				log.WriteByte('\n')

				runAndCompare(t, manager, reference, op, &log)
			}

			actual := snapshotManager(manager)
			expected := reference.snapshot()
			t.Logf("input seed=%d S=%d K=%d PB=%d ops=%d; output=PASS; final actual=%+v final naive=%+v; judgment=all per-operation return values and snapshots matched the stated rules",
				seed, s, k, pb, ops, actual, expected)
		})
	}
}

func makeRandomOperation(rng *rand.Rand) randomOperation {
	t := 1 + rng.Intn(18)
	switch rng.Intn(9) {
	case 0:
		if rng.Intn(30) == 0 {
			t = 0
		}
		return randomOperation{name: "begin", t: t}
	case 1:
		return randomOperation{name: "insert", t: t}
	case 2:
		return randomOperation{name: "modify", t: t}
	case 3:
		return randomOperation{name: "commit", t: t}
	case 4:
		return randomOperation{name: "rollback", t: t}
	case 5:
		return randomOperation{name: "open_view"}
	case 6:
		return randomOperation{name: "close_view", t: 1 + rng.Intn(30)}
	case 7:
		return randomOperation{name: "purge", n: 1 + rng.Intn(6)}
	default:
		if rng.Intn(20) == 0 {
			return randomOperation{name: "purge", n: 0}
		}
		return randomOperation{name: "purge", n: 1 + rng.Intn(6)}
	}
}

func runAndCompare(t *testing.T, manager *Manager, reference *naiveManager, op randomOperation, log *strings.Builder) {
	t.Helper()

	var commitNo, viewID int
	var recycled []int
	var actualErr error
	switch op.name {
	case "begin":
		actualErr = manager.Begin(op.t)
	case "insert":
		actualErr = manager.Insert(op.t)
	case "modify":
		actualErr = manager.Modify(op.t)
	case "commit":
		commitNo, actualErr = manager.Commit(op.t)
	case "rollback":
		actualErr = manager.Rollback(op.t)
	case "open_view":
		viewID = manager.OpenView()
	case "close_view":
		actualErr = manager.CloseView(op.t)
	case "purge":
		recycled, actualErr = manager.Purge(op.n)
	}

	var refCommit, refView int
	var refRecycled []int
	var refErr error
	switch op.name {
	case "begin":
		refErr = reference.Begin(op.t)
	case "insert":
		refErr = reference.Record(op.t, insertUndo)
	case "modify":
		refErr = reference.Record(op.t, updateUndo)
	case "commit":
		refCommit, refErr = reference.Commit(op.t)
	case "rollback":
		refErr = reference.Rollback(op.t)
	case "open_view":
		refView = reference.OpenView()
	case "close_view":
		refErr = reference.CloseView(op.t)
	case "purge":
		refRecycled, refErr = reference.Purge(op.n)
	}

	fmt.Fprintf(log, "actual output: commit=%d view=%d recycled=%v err=%v\n", commitNo, viewID, recycled, actualErr)
	fmt.Fprintf(log, "naive output:  commit=%d view=%d recycled=%v err=%v\n", refCommit, refView, refRecycled, refErr)

	if !errors.Is(actualErr, refErr) {
		t.Logf("\n%s", log.String())
		t.Fatalf("error mismatch: actual=%v reference=%v", actualErr, refErr)
	}
	if commitNo != refCommit || viewID != refView || !equalIntSlices(recycled, refRecycled) {
		t.Logf("\n%s", log.String())
		t.Fatalf("return-value mismatch")
	}
	compareRandomSnapshots(t, manager, reference, log)
}

func compareRandomSnapshots(t *testing.T, manager *Manager, reference *naiveManager, log *strings.Builder) {
	t.Helper()

	actual := snapshotManager(manager)
	expected := reference.snapshot()
	fmt.Fprintf(log, "actual state: slots=%d pages=%d cacheI=%v cacheU=%v history=%v\n",
		actual.usedSlots, actual.usedPages, actual.caches[insertUndo], actual.caches[updateUndo], actual.history)
	fmt.Fprintf(log, "naive state:  slots=%d pages=%d cacheI=%v cacheU=%v history=%v\n",
		expected.usedSlots, expected.usedPages, expected.caches[insertUndo], expected.caches[updateUndo], expected.history)

	if actual.usedSlots != expected.usedSlots || actual.usedPages != expected.usedPages {
		t.Logf("\n%s", log.String())
		t.Fatalf("slot/page usage mismatch")
	}
	if !equalIntSlices(actual.history, expected.history) {
		t.Logf("\n%s", log.String())
		t.Fatalf("history mismatch")
	}
	for kind := range actual.caches {
		if !equalIntSlices(actual.caches[kind], expected.caches[kind]) {
			t.Logf("\n%s", log.String())
			t.Fatalf("cache %d mismatch", kind)
		}
	}
}

func snapshotManager(manager *Manager) actualSnapshotInfo {
	manager.mu.Lock()
	defer manager.mu.Unlock()

	out := actualSnapshotInfo{usedSlots: manager.usedSlots, usedPages: manager.usedPages}
	for kind, stack := range manager.caches {
		for _, seg := range stack {
			out.caches[kind] = append(out.caches[kind], seg.slot+1)
		}
	}
	for _, entry := range manager.history {
		out.history = append(out.history, entry.trxNo)
	}
	return out
}

func equalIntSlices(left, right []int) bool {
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
