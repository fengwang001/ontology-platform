package reconcile

import (
	"encoding/json"
	"errors"
	"math/rand"
	"reflect"
	"testing"
)

// vv builds a versioned value.
func vv(value string, writtenAt Position) VersionedValue {
	return VersionedValue{Value: value, WrittenAt: writtenAt}
}

// snap builds and serializes one replica snapshot.
func snap(t *testing.T, id string, priority uint64, pos Position, objects map[string]map[string]VersionedValue) []byte {
	t.Helper()
	objs := map[string]ObjectInstance{}
	for id, props := range objects {
		objs[id] = ObjectInstance{Props: props}
	}
	raw, err := json.Marshal(Snapshot{
		Replica: ReplicaMeta{ID: ReplicaID(id), Priority: priority},
		Pos:     pos,
		Objects: objs,
	})
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	return raw
}

func mustReconcile(t *testing.T, raws ...[]byte) *Result {
	t.Helper()
	res, err := New(Options{}).Reconcile(raws)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	return res
}

// The reconciled state corresponds to the earliest position; later writes
// from richer replicas are excluded.
func TestBaselineEarliestPositionWins(t *testing.T) {
	early := snap(t, "r1", 1, 5, map[string]map[string]VersionedValue{
		"o1": {"name": vv("old", 3)},
	})
	late := snap(t, "r2", 0, 10, map[string]map[string]VersionedValue{
		"o1": {"name": vv("new", 7)},        // written after baseline
		"o2": {"extra": vv("only-late", 8)}, // object only visible later
		"o3": {"kept": vv("from-r2", 4)},    // written before baseline
	})
	res := mustReconcile(t, early, late)
	if res.Baseline != 5 {
		t.Fatalf("baseline = %d, want 5", res.Baseline)
	}
	if got := res.Objects["o1"]["name"]; got != "old" {
		t.Fatalf("o1.name = %q, want %q (post-baseline write must not win)", got, "old")
	}
	if _, ok := res.Objects["o2"]; ok {
		t.Fatalf("o2 must not appear: all its writes are after the baseline")
	}
	if got := res.Objects["o3"]["kept"]; got != "from-r2" {
		t.Fatalf("o3.kept = %q, want %q", got, "from-r2")
	}
	if res.Stats.ValuesFiltered != 2 {
		t.Fatalf("ValuesFiltered = %d, want 2", res.Stats.ValuesFiltered)
	}
}

// Same position, same object/property, different values: the fixed rule
// (smallest priority wins) decides, independent of arrival order.
func TestArbitrationFixedRuleAndOrderIndependence(t *testing.T) {
	a := snap(t, "rA", 2, 5, map[string]map[string]VersionedValue{
		"o1": {"color": vv("red", 5)},
	})
	b := snap(t, "rB", 1, 5, map[string]map[string]VersionedValue{
		"o1": {"color": vv("blue", 5)},
	})
	for _, raws := range [][][]byte{{a, b}, {b, a}} {
		res := mustReconcile(t, raws...)
		if got := res.Objects["o1"]["color"]; got != "blue" {
			t.Fatalf("winner = %q, want %q (replica rB has smaller priority)", got, "blue")
		}
		if len(res.Conflicts) != 1 || res.Conflicts[0].Resolved["color"].Winner.Replica != "rB" {
			t.Fatalf("conflict record missing or wrong: %+v", res.Conflicts)
		}
	}
}

// The verdict must not change when more replicas participate: adding a
// low-priority replica that agrees with the loser cannot flip the winner.
func TestArbitrationIndependentOfReplicaCount(t *testing.T) {
	a := snap(t, "rA", 2, 5, map[string]map[string]VersionedValue{"o1": {"p": vv("va", 5)}})
	b := snap(t, "rB", 1, 5, map[string]map[string]VersionedValue{"o1": {"p": vv("vb", 5)}})
	c := snap(t, "rC", 9, 5, map[string]map[string]VersionedValue{"o1": {"p": vv("va", 5)}})
	two := mustReconcile(t, a, b)
	three := mustReconcile(t, a, b, c)
	if two.Objects["o1"]["p"] != three.Objects["o1"]["p"] {
		t.Fatalf("verdict drifted with replica count: %q vs %q",
			two.Objects["o1"]["p"], three.Objects["o1"]["p"])
	}
}

// A corrupted snapshot is quarantined; the rest reconcile normally, even
// when the corrupted replica would have won arbitration.
func TestCorruptedSnapshotQuarantined(t *testing.T) {
	good := snap(t, "rGood", 5, 5, map[string]map[string]VersionedValue{
		"o1": {"p": vv("good-value", 5)},
	})
	corrupted := []byte(`{"replica":{"id":"rBad","priority":0},"position":5,"objects":{broken`)
	res := mustReconcile(t, good, corrupted)
	if len(res.Quarantined) != 1 {
		t.Fatalf("quarantined = %d, want 1", len(res.Quarantined))
	}
	q := res.Quarantined[0]
	if q.Kind != KindCorruptedSnapshot || q.Replica != "rBad" {
		t.Fatalf("unexpected quarantine record: %+v", q)
	}
	if got := res.Objects["o1"]["p"]; got != "good-value" {
		t.Fatalf("o1.p = %q, want %q", got, "good-value")
	}
}

// Structural inconsistency (a write positioned after the snapshot
// position) also quarantines the whole replica.
func TestStructurallyInconsistentSnapshotQuarantined(t *testing.T) {
	bad := snap(t, "rBad", 0, 5, map[string]map[string]VersionedValue{
		"o1": {"p": vv("x", 9)}, // written_at > snapshot position
	})
	good := snap(t, "rGood", 1, 5, map[string]map[string]VersionedValue{
		"o1": {"p": vv("y", 5)},
	})
	res := mustReconcile(t, bad, good)
	if len(res.Quarantined) != 1 || res.Quarantined[0].Replica != "rBad" {
		t.Fatalf("expected rBad quarantined: %+v", res.Quarantined)
	}
	if got := res.Objects["o1"]["p"]; got != "y" {
		t.Fatalf("o1.p = %q, want %q", got, "y")
	}
}

// Zero readable replicas: fatal ErrInsufficientReplicas, evaluated before
// any other category; quarantine records are still reported.
func TestInsufficientReplicasIsFatalAndFirst(t *testing.T) {
	res, err := New(Options{MinReplicas: 1}).Reconcile([][]byte{
		[]byte(`garbage`),
		[]byte(`{"replica":{"id":"r2","priority":1},"position":3,"objects":{"":{"p":{"value":"x","written_at":1}}}}`),
	})
	if !errors.Is(err, ErrInsufficientReplicas) {
		t.Fatalf("err = %v, want ErrInsufficientReplicas", err)
	}
	if res == nil {
		t.Fatalf("result must be non-nil to carry quarantine records")
	}
	if len(res.Quarantined) != 2 {
		t.Fatalf("quarantined = %d, want 2", len(res.Quarantined))
	}
}

// Corruption and irreconcilable conflict in the same run: both reported,
// in distinct categories, neither masking the other.
func TestCorruptionAndIrreconcilableCoexist(t *testing.T) {
	a := snap(t, "rA", 1, 5, map[string]map[string]VersionedValue{"o1": {"p": vv("x", 5)}})
	b := snap(t, "rB", 1, 5, map[string]map[string]VersionedValue{"o1": {"p": vv("y", 5)}}) // same priority: tie
	corrupted := []byte(`not json`)
	res := mustReconcile(t, a, corrupted, b)
	if len(res.Quarantined) != 1 || res.Quarantined[0].Kind != KindCorruptedSnapshot {
		t.Fatalf("quarantine records: %+v", res.Quarantined)
	}
	if !reflect.DeepEqual(res.Irreconcilable, []string{"o1"}) {
		t.Fatalf("irreconcilable = %v, want [o1]", res.Irreconcilable)
	}
	if _, ok := res.Objects["o1"]; ok {
		t.Fatalf("irreconcilable object must not appear in reconciled content")
	}
	if len(res.Conflicts) != 1 || len(res.Conflicts[0].Unresolved) != 1 {
		t.Fatalf("conflict record: %+v", res.Conflicts)
	}
	if res.Conflicts[0].Unresolved[0].Reason == "" {
		t.Fatalf("tie must carry a reason")
	}
}

// A tie on the comparable identifier with distinct values is
// irreconcilable; the component must not fall back to arrival order.
func TestTieNeverFallsBackToArrivalOrder(t *testing.T) {
	a := snap(t, "rA", 1, 5, map[string]map[string]VersionedValue{"o1": {"p": vv("x", 5)}})
	b := snap(t, "rB", 1, 5, map[string]map[string]VersionedValue{"o1": {"p": vv("y", 5)}})
	for _, raws := range [][][]byte{{a, b}, {b, a}} {
		res := mustReconcile(t, raws...)
		if !reflect.DeepEqual(res.Irreconcilable, []string{"o1"}) {
			t.Fatalf("irreconcilable = %v, want [o1]", res.Irreconcilable)
		}
	}
}

// A tie on one property poisons the whole object instance, but other
// objects reconcile normally.
func TestTiePoisonsWholeObjectOnly(t *testing.T) {
	a := snap(t, "rA", 1, 5, map[string]map[string]VersionedValue{
		"o1": {"p": vv("x", 5), "q": vv("same", 5)},
		"o2": {"p": vv("keep", 5)},
	})
	b := snap(t, "rB", 1, 5, map[string]map[string]VersionedValue{
		"o1": {"p": vv("y", 5), "q": vv("same", 5)},
		"o2": {"p": vv("keep", 5)},
	})
	res := mustReconcile(t, a, b)
	if !reflect.DeepEqual(res.Irreconcilable, []string{"o1"}) {
		t.Fatalf("irreconcilable = %v, want [o1]", res.Irreconcilable)
	}
	if got := res.Objects["o2"]["p"]; got != "keep" {
		t.Fatalf("o2.p = %q, want keep", got)
	}
}

// Cost audit: identical replicas cause zero arbitrations; exactly the
// conflicting properties are arbitrated, and the scan stays linear.
func TestStatsScaleWithConflictsOnly(t *testing.T) {
	objects := map[string]map[string]VersionedValue{}
	for i := 0; i < 50; i++ {
		objects[string(rune('a'+i%26))+string(rune('0'+i/26))] = map[string]VersionedValue{
			"p": vv("same", 1),
		}
	}
	var raws [][]byte
	for i := 0; i < 8; i++ {
		raws = append(raws, snap(t, "r"+string(rune('A'+i)), uint64(i), 5, objects))
	}
	res := mustReconcile(t, raws...)
	if res.Stats.ArbitrationsRun != 0 || res.Stats.ConflictsFound != 0 {
		t.Fatalf("no conflicts expected: %+v", res.Stats)
	}
	if res.Stats.ValuesScanned != 8*50 {
		t.Fatalf("ValuesScanned = %d, want %d (linear scan only)", res.Stats.ValuesScanned, 8*50)
	}

	// Introduce k conflicting properties on one replica.
	conflicted := snap(t, "rZ", 0, 5, map[string]map[string]VersionedValue{
		"a0": {"p": vv("different", 1)},
		"b0": {"p": vv("different", 1)},
		"c0": {"p": vv("different", 1)},
	})
	res = mustReconcile(t, append(raws, conflicted)...)
	if res.Stats.ArbitrationsRun != 3 || res.Stats.ConflictsFound != 3 {
		t.Fatalf("arbitrations = %d, conflicts = %d, want 3/3",
			res.Stats.ArbitrationsRun, res.Stats.ConflictsFound)
	}
	if res.Stats.ArbitrationsRun != res.Stats.ConflictsFound {
		t.Fatalf("invariant violated: arbitrations must equal conflicts")
	}
}

// Repeated reconciliation of the same input set, in any order, yields
// byte-identical results and never mutates the inputs.
func TestDeterministicAcrossRunsAndInputOrder(t *testing.T) {
	base := [][]byte{
		snap(t, "r1", 3, 7, map[string]map[string]VersionedValue{
			"o1": {"a": vv("1", 2), "b": vv("x", 6)},
			"o2": {"a": vv("keep", 7)},
		}),
		snap(t, "r2", 1, 9, map[string]map[string]VersionedValue{
			"o1": {"a": vv("2", 8), "b": vv("y", 6)}, // o1.a filtered (8 > 7)
			"o3": {"z": vv("late", 9)},               // filtered
		}),
		snap(t, "r3", 2, 7, map[string]map[string]VersionedValue{
			"o1": {"a": vv("1", 2), "b": vv("x", 6)},
		}),
		[]byte(`corrupted`),
	}
	frozen := make([][]byte, len(base))
	for i, r := range base {
		frozen[i] = append([]byte(nil), r...)
	}

	r := New(Options{})
	want, err := r.Reconcile(base)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	wantJSON, _ := json.Marshal(want)

	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 100; trial++ {
		perm := rng.Perm(len(base))
		shuffled := make([][]byte, len(base))
		for i, j := range perm {
			shuffled[i] = base[j]
		}
		got, err := r.Reconcile(shuffled)
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		gotJSON, _ := json.Marshal(got)
		if !reflect.DeepEqual(wantJSON, gotJSON) {
			t.Fatalf("trial %d: result drifted\nwant: %s\ngot:  %s", trial, wantJSON, gotJSON)
		}
	}
	for i := range base {
		if !reflect.DeepEqual(base[i], frozen[i]) {
			t.Fatalf("input %d was mutated", i)
		}
	}
}
