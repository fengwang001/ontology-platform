package ontology

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

type randomEntryLog struct {
	Parent int64  `json:"parent"`
	Name   string `json:"name"`
	Dir    bool   `json:"dir"`
	Hash   string `json:"hash"`
}

type randomActionLog struct {
	Op     string `json:"op"`
	ID     int64  `json:"id"`
	Parent int64  `json:"parent,omitempty"`
	Name   string `json:"name,omitempty"`
	Hash   string `json:"hash,omitempty"`
}

type randomCaseLog struct {
	Index     int                       `json:"index"`
	Base      map[string]randomEntryLog `json:"base"`
	Local     map[string]randomEntryLog `json:"local"`
	Remote    map[string]randomEntryLog `json:"remote"`
	Reasons   map[string][]string       `json:"reasons"`
	ToLocal   []randomActionLog         `json:"to_local"`
	ToRemote  []randomActionLog         `json:"to_remote"`
	Conflicts []map[string]string       `json:"conflicts"`
	Error     string                    `json:"error,omitempty"`
}

type naiveState map[int64]Entry

type naiveResult struct {
	toLocal   []Action
	toRemote  []Action
	conflicts []Conflict
	err       error
}

func TestRandom2000CompareNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(1253))
	for index := 0; index < 2000; index++ {
		base := generateValidSnapshot(rng, 8)
		local, localReasons := mutateSnapshot(rng, base, 1)
		remote, remoteReasons := mutateSnapshot(rng, base, 2)

		planner, err := NewPlanner(base)
		if err != nil {
			t.Fatalf("case %d: bad generated base: %v", index, err)
		}
		got, gotErr := planner.Plan(local, remote)
		want := naivePlan(base, local, remote)

		randomLog(t, index, base, local, remote, got, gotErr, mergeReasons(localReasons, remoteReasons))

		if !sameErr(gotErr, want.err) {
			t.Fatalf("case %d: error %v, want %v", index, gotErr, want.err)
		}
		if gotErr == nil {
			if !reflect.DeepEqual(got.ToLocal, want.toLocal) {
				t.Fatalf("case %d ToLocal %#v want %#v", index, got.ToLocal, want.toLocal)
			}
			if !reflect.DeepEqual(got.ToRemote, want.toRemote) {
				t.Fatalf("case %d ToRemote %#v want %#v", index, got.ToRemote, want.toRemote)
			}
			if !reflect.DeepEqual(got.Conflicts, want.conflicts) {
				t.Fatalf("case %d conflicts %#v want %#v", index, got.Conflicts, want.conflicts)
			}
		}
	}
}

func generateValidSnapshot(rng *rand.Rand, max int64) Snapshot {
	count := 1 + rng.Int63n(max)
	dirCount := rng.Int63n(count)
	dirs := make(map[int64]bool)
	for id := int64(1); id <= dirCount; id++ {
		dirs[id] = true
	}

	result := make(Snapshot, count)
	used := make(map[locationKey]bool)
	for id := int64(1); id <= count; id++ {
		parent := int64(0)
		if id > 1 {
			candidates := []int64{0}
			for candidate := int64(1); candidate < id; candidate++ {
				if dirs[candidate] {
					candidates = append(candidates, candidate)
				}
			}
			parent = candidates[rng.Intn(len(candidates))]
		}
		name := fmt.Sprintf("n%d-%d", parent, rng.Int63n(1000000))
		for used[locationKey{parent, name}] {
			name = fmt.Sprintf("n%d-%d", parent, rng.Int63n(1000000))
		}
		used[locationKey{parent, name}] = true
		entry := Entry{Parent: parent, Name: name, Dir: dirs[id]}
		if !entry.Dir {
			entry.Hash = fmt.Sprintf("h%d", rng.Int63n(1000000))
		}
		result[id] = entry
	}
	return result
}

func mutateSnapshot(rng *rand.Rand, base Snapshot, side int64) (Snapshot, map[string][]string) {
	result := cloneSnapshot(base)
	reasons := make(map[string][]string)
	chosen := make(map[int64]int)

	for _, id := range sortedIDs(base) {
		chosen[id] = rng.Intn(6)
	}

	removed := make(map[int64]bool)
	for _, id := range sortedIDs(base) {
		if chosen[id] == 0 {
			removed[id] = true
			for descendant := range base {
				if hasAncestor(base, descendant, id) {
					removed[descendant] = true
				}
			}
		}
	}

	for id := range removed {
		delete(result, id)
		reasons[keyID(id)] = append(reasons[keyID(id)], fmt.Sprintf("side %d deleted subtree entry", side))
	}

	for _, id := range sortedIDs(base) {
		if removed[id] {
			continue
		}
		entry := result[id]
		switch chosen[id] {
		case 1:
			if !entry.Dir {
				entry.Hash = fmt.Sprintf("l%d-%d", side, rng.Int63n(1000000))
				result[id] = entry
				reasons[keyID(id)] = append(reasons[keyID(id)], fmt.Sprintf("side %d changed hash", side))
			}
		case 2:
			entry.Name = fmt.Sprintf("r%d-%d-%d", side, id, rng.Int63n(1000000))
			result[id] = entry
			reasons[keyID(id)] = append(reasons[keyID(id)], fmt.Sprintf("side %d renamed", side))
		}
	}

	start := 20 + side*40 + rng.Int63n(10)
	added := 1 + rng.Int63n(4)
	for index := int64(0); index < added; index++ {
		id := start + index
		parents := []int64{0}
		for parentID, parent := range result {
			if parent.Dir {
				parents = append(parents, parentID)
			}
		}
		sort.Slice(parents, func(i, j int) bool { return parents[i] < parents[j] })
		parent := parents[rng.Intn(len(parents))]
		name := fmt.Sprintf("shared-%d", parent)
		if rng.Intn(2) == 1 || sameParentNameExists(result, parent, name) {
			name = fmt.Sprintf("c%d-%d", side, id)
		}
		result[id] = Entry{Parent: parent, Name: name, Hash: fmt.Sprintf("c%d-%d", side, rng.Int63n(1000000))}
		reasons[keyID(id)] = append(reasons[keyID(id)], fmt.Sprintf("side %d created", side))
	}

	return result, reasons
}

func hasAncestor(snapshot Snapshot, id, ancestor int64) bool {
	seen := make(map[int64]bool)
	parent := snapshot[id].Parent
	for parent != 0 {
		if parent == ancestor {
			return true
		}
		if seen[parent] {
			return false
		}
		seen[parent] = true
		entry, ok := snapshot[parent]
		if !ok {
			return false
		}
		parent = entry.Parent
	}
	return false
}

func sameParentNameExists(snapshot Snapshot, parent int64, name string) bool {
	for _, entry := range snapshot {
		if entry.Parent == parent && entry.Name == name {
			return true
		}
	}
	return false
}

func keyID(id int64) string {
	return fmt.Sprintf("%d", id)
}

func mergeReasons(maps ...map[string][]string) map[string][]string {
	result := make(map[string][]string)
	for _, input := range maps {
		for key, values := range input {
			result[key] = append(result[key], values...)
		}
	}
	return result
}

func sameErr(left, right error) bool {
	if left == nil || right == nil {
		return left == right
	}
	return errors.Is(left, right) || errors.Is(right, left)
}

func randomLog(t *testing.T, index int, base, local, remote Snapshot, plan PlanResult, err error, reasons map[string][]string) {
	entry := randomCaseLog{
		Index:     index,
		Base:      logEntries(base),
		Local:     logEntries(local),
		Remote:    logEntries(remote),
		Reasons:   reasons,
		ToLocal:   logActionList(plan.ToLocal),
		ToRemote:  logActionList(plan.ToRemote),
		Conflicts: logConflictList(plan.Conflicts),
	}
	if err != nil {
		entry.Error = err.Error()
	}
	data, marshalErr := json.Marshal(entry)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	t.Log(string(data))
}

func logEntries(snapshot Snapshot) map[string]randomEntryLog {
	result := make(map[string]randomEntryLog, len(snapshot))
	for id, entry := range snapshot {
		result[keyID(id)] = randomEntryLog{Parent: entry.Parent, Name: entry.Name, Dir: entry.Dir, Hash: entry.Hash}
	}
	return result
}

func logActionList(actions []Action) []randomActionLog {
	result := make([]randomActionLog, 0, len(actions))
	for _, action := range actions {
		result = append(result, randomActionLog{Op: action.Op.String(), ID: action.ID, Parent: action.Parent, Name: action.Name, Hash: action.Hash})
	}
	return result
}

func logConflictList(conflicts []Conflict) []map[string]string {
	result := make([]map[string]string, 0, len(conflicts))
	for _, conflict := range conflicts {
		result = append(result, map[string]string{"id": keyID(conflict.ID), "kind": conflict.Kind.String()})
	}
	return result
}
