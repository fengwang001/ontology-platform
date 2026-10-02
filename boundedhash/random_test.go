package boundedhash

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func randomAddInput(rng *rand.Rand) (uint64, []uint64) {
	id := uint64(1 + rng.Intn(8))
	pointCount := 1 + rng.Intn(4)
	if rng.Intn(20) == 0 {
		pointCount = 65
	}
	if rng.Intn(30) == 0 {
		id = 0
	}
	points := make([]uint64, pointCount)
	for i := range points {
		points[i] = uint64(rng.Intn(64))
	}
	if pointCount <= 4 && rng.Intn(5) == 0 {
		points[rng.Intn(len(points))] = points[0]
	}
	return id, points
}

func randomExistingOrNewKey(rng *rand.Rand, keys []string) string {
	if len(keys) > 0 && rng.Intn(3) == 0 {
		return keys[rng.Intn(len(keys))]
	}
	return "k" + fmt.Sprint(rng.Intn(45))
}

func sortedActualNodeIDs(r *Ring) []uint64 {
	ids := make([]uint64, 0, len(r.nodes))
	for id := range r.nodes {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func assertRingsEqual(t *testing.T, actual *Ring, model *naiveRing, trace []string) {
	t.Helper()
	actualIDs := sortedActualNodeIDs(actual)
	modelIDs := model.sortedNodeIDs()
	if !reflect.DeepEqual(actualIDs, modelIDs) {
		t.Fatalf("node IDs differ actual=%v model=%v\n%s", actualIDs, modelIDs, strings.Join(trace, "\n"))
	}

	for _, id := range actualIDs {
		actualNode := actual.nodes[id]
		modelNode := model.nodes[id]
		if !reflect.DeepEqual(actualNode.points, modelNode.points) {
			t.Fatalf("node %d points differ actual=%v model=%v\n%s", id, actualNode.points, modelNode.points, strings.Join(trace, "\n"))
		}
		if len(actualNode.keys) != len(modelNode.keys) {
			t.Fatalf("node %d load differs actual=%d model=%d\n%s", id, len(actualNode.keys), len(modelNode.keys), strings.Join(trace, "\n"))
		}

		expectedSeq := make([]string, 0, len(modelNode.keys))
		for key := range modelNode.keys {
			expectedSeq = append(expectedSeq, key)
		}
		sort.Slice(expectedSeq, func(i, j int) bool {
			left := modelNode.keys[expectedSeq[i]]
			right := modelNode.keys[expectedSeq[j]]
			if left.seq == right.seq {
				return expectedSeq[i] < expectedSeq[j]
			}
			return left.seq > right.seq
		})
		if len(actualNode.seq) != len(expectedSeq) {
			t.Fatalf("node %d seq order differs actual=%v model=%v\n%s", id, actualNode.seq, expectedSeq, strings.Join(trace, "\n"))
		}
		for i := range expectedSeq {
			if actualNode.seq[i] != expectedSeq[i] {
				t.Fatalf("node %d seq order differs actual=%v model=%v\n%s", id, actualNode.seq, expectedSeq, strings.Join(trace, "\n"))
			}
		}

		for key, actualRecord := range actualNode.keys {
			modelRecord, ok := modelNode.keys[key]
			if !ok || actualRecord.seq != modelRecord.seq || actualRecord.pos != modelRecord.pos || actualRecord.nodeID != modelRecord.nodeID {
				t.Fatalf("node %d key %q differs actual=%+v model=%+v exists=%v\n%s", id, key, actualRecord, modelRecord, ok, strings.Join(trace, "\n"))
			}
		}
	}

	if len(actual.points) != len(model.points) {
		t.Fatalf("point count differs actual=%d model=%d\n%s", len(actual.points), len(model.points), strings.Join(trace, "\n"))
	}
	for i := range actual.points {
		if actual.points[i].pos != model.points[i].pos || actual.points[i].nodeID != model.points[i].nodeID {
			t.Fatalf("point %d differs actual=%+v model=%+v\n%s", i, actual.points[i], model.points[i], strings.Join(trace, "\n"))
		}
	}

	if len(actual.keys) != len(model.keys) || actual.totalKeys != model.totalKeys || actual.nextSeq != model.nextSeq {
		t.Fatalf("global state differs actual=(keys=%d total=%d seq=%d) model=(keys=%d total=%d seq=%d)\n%s",
			len(actual.keys), actual.totalKeys, actual.nextSeq, len(model.keys), model.totalKeys, model.nextSeq, strings.Join(trace, "\n"))
	}
	loadSum := 0
	for _, id := range actualIDs {
		loadSum += len(actual.nodes[id].keys)
	}
	if loadSum != actual.totalKeys {
		t.Fatalf("load sum=%d totalKeys=%d\n%s", loadSum, actual.totalKeys, strings.Join(trace, "\n"))
	}
	for key, actualRecord := range actual.keys {
		modelRecord, ok := model.keys[key]
		if !ok || actualRecord.seq != modelRecord.seq || actualRecord.pos != modelRecord.pos || actualRecord.nodeID != modelRecord.nodeID {
			t.Fatalf("global key %q differs actual=%+v model=%+v exists=%v\n%s", key, actualRecord, modelRecord, ok, strings.Join(trace, "\n"))
		}
	}
}

func TestRandomSequencesAgainstNaiveModel(t *testing.T) {
	for trial := 0; trial < 2000; trial++ {
		seed := int64(70_000 + trial)
		rng := rand.New(rand.NewSource(seed))
		cnum := 1 + rng.Intn(6)
		cden := 1 + rng.Intn(cnum)
		actual, err := New(cnum, cden)
		if err != nil {
			t.Fatal(err)
		}
		model := newNaive(cnum, cden)
		trace := []string{fmt.Sprintf("seed=%d c=%d/%d", seed, cnum, cden)}

		for op := 0; op < 50; op++ {
			ids := sortedActualNodeIDs(actual)
			basis := "state-equal-after-operation"
			input := ""

			if len(ids) == 0 {
				if rng.Intn(5) == 0 {
					limit := []int{-1, 1_000_000_001}[rng.Intn(2)]
					input = fmt.Sprintf("Rebalance(limit=%d)", limit)
					_, _, actualErr := actual.Rebalance(limit)
					_, _, modelErr := model.rebalance(limit)
					trace = append(trace, fmt.Sprintf("%s => actual=%s model=%s basis=%s", input, errorName(actualErr), errorName(modelErr), basis))
					if !sameError(actualErr, modelErr) {
						t.Fatalf("error mismatch\n%s", strings.Join(trace, "\n"))
					}
				} else {
					id, points := randomAddInput(rng)
					input = fmt.Sprintf("AddNode(id=%d,points=%v)", id, points)
					actualErr := actual.AddNode(id, points)
					modelErr := model.addNode(id, points)
					trace = append(trace, fmt.Sprintf("%s => actual=%s model=%s basis=%s", input, errorName(actualErr), errorName(modelErr), basis))
					if !sameError(actualErr, modelErr) {
						t.Fatalf("error mismatch\n%s", strings.Join(trace, "\n"))
					}
				}
				assertRingsEqual(t, actual, model, trace)
				continue
			}

			switch rng.Intn(100) {
			case 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24:
				id, points := randomAddInput(rng)
				input = fmt.Sprintf("AddNode(id=%d,points=%v)", id, points)
				actualErr := actual.AddNode(id, points)
				modelErr := model.addNode(id, points)
				trace = append(trace, fmt.Sprintf("%s => actual=%s model=%s basis=%s", input, errorName(actualErr), errorName(modelErr), basis))
				if !sameError(actualErr, modelErr) {
					t.Fatalf("error mismatch\n%s", strings.Join(trace, "\n"))
				}
			case 25, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 38, 39, 40, 41, 42, 43, 44, 45, 46, 47, 48, 49, 50, 51:
				key := randomExistingOrNewKey(rng, modelKeySlice(model))
				if rng.Intn(30) == 0 {
					key = ""
				}
				pos := uint64(rng.Intn(80))
				input = fmt.Sprintf("Put(key=%q,pos=%d)", key, pos)
				actualErr := actual.Put(key, pos)
				modelErr := model.put(key, pos)
				trace = append(trace, fmt.Sprintf("%s => actual=%s model=%s basis=%s", input, errorName(actualErr), errorName(modelErr), basis))
				if !sameError(actualErr, modelErr) {
					t.Fatalf("error mismatch\n%s", strings.Join(trace, "\n"))
				}
			case 52, 53, 54, 55, 56, 57, 58, 59, 60, 61:
				key := randomExistingOrNewKey(rng, modelKeySlice(model))
				input = fmt.Sprintf("Delete(key=%q)", key)
				actualErr := actual.Delete(key)
				modelErr := model.delete(key)
				trace = append(trace, fmt.Sprintf("%s => actual=%s model=%s basis=%s", input, errorName(actualErr), errorName(modelErr), basis))
				if !sameError(actualErr, modelErr) {
					t.Fatalf("error mismatch\n%s", strings.Join(trace, "\n"))
				}
			case 62, 63, 64, 65, 66, 67, 68, 69, 70, 71, 72, 73:
				id := ids[rng.Intn(len(ids))]
				if rng.Intn(4) == 0 {
					id = uint64(1 + rng.Intn(8))
				}
				input = fmt.Sprintf("RemoveNode(id=%d)", id)
				actualErr := actual.RemoveNode(id)
				modelErr := model.removeNode(id)
				trace = append(trace, fmt.Sprintf("%s => actual=%s model=%s basis=%s", input, errorName(actualErr), errorName(modelErr), basis))
				if !sameError(actualErr, modelErr) {
					t.Fatalf("error mismatch\n%s", strings.Join(trace, "\n"))
				}
			case 74, 75, 76, 77, 78, 79, 80, 81, 82, 83, 84, 85, 86, 87:
				limitChoices := []int{-1, 0, 1, 2, 5, 1_000_000_001}
				limit := limitChoices[rng.Intn(len(limitChoices))]
				input = fmt.Sprintf("Rebalance(limit=%d)", limit)
				actualMigrations, actualExcess, actualErr := actual.Rebalance(limit)
				modelMigrations, modelExcess, modelErr := model.rebalance(limit)
				trace = append(trace, fmt.Sprintf("%s => actual=(%v,excess=%d,%s) model=(%v,excess=%d,%s) basis=%s",
					input, actualMigrations, actualExcess, errorName(actualErr), modelMigrations, modelExcess, errorName(modelErr), basis))
				if !sameError(actualErr, modelErr) || !reflect.DeepEqual(actualMigrations, modelMigrations) || actualExcess != modelExcess {
					t.Fatalf("rebalance result mismatch\n%s", strings.Join(trace, "\n"))
				}
			default:
				key := randomExistingOrNewKey(rng, modelKeySlice(model))
				if rng.Intn(20) == 0 {
					key = ""
				}
				input = fmt.Sprintf("Lookup(key=%q)", key)
				actualID, actualErr := actual.Lookup(key)
				modelID, modelErr := model.lookup(key)
				trace = append(trace, fmt.Sprintf("%s => actual=(%d,%s) model=(%d,%s) basis=%s", input, actualID, errorName(actualErr), modelID, errorName(modelErr), basis))
				if !sameError(actualErr, modelErr) || actualID != modelID {
					t.Fatalf("lookup result mismatch\n%s", strings.Join(trace, "\n"))
				}
			}
			assertRingsEqual(t, actual, model, trace)
		}

		t.Logf("trial=%d %s finalLoads=%v judgment=PASS", trial, strings.Join(trace, " | "), loads(actual))
	}
}

func modelKeySlice(model *naiveRing) []string {
	keys := make([]string, 0, len(model.keys))
	for key := range model.keys {
		keys = append(keys, key)
	}
	return keys
}
