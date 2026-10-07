package reconcile

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// propertyLogPath is where the randomized cross-check records every case:
// inputs, output and decision rationale, one JSON line per reconciliation.
// Override with RECONCILE_PROPERTY_LOG.
func propertyLogPath() string {
	if p := os.Getenv("RECONCILE_PROPERTY_LOG"); p != "" {
		return p
	}
	return filepath.Join(os.TempDir(), "reconcile_property_log.jsonl")
}

type propertyCaseLog struct {
	Seed           int64                        `json:"seed"`
	Case           int                          `json:"case"`
	Inputs         []string                     `json:"inputs"`
	Baseline       Position                     `json:"baseline,omitempty"`
	Objects        map[string]map[string]string `json:"objects,omitempty"`
	Irreconcilable []string                     `json:"irreconcilable,omitempty"`
	Quarantined    []QuarantineRecord           `json:"quarantined,omitempty"`
	Conflicts      []ConflictRecord             `json:"conflicts,omitempty"`
	Error          string                       `json:"error,omitempty"`
}

// randomSnapshot builds one randomized serialized snapshot. With
// probability ~1/8 it is structurally corrupted instead.
func randomSnapshot(t *testing.T, rng *rand.Rand, i int) (raw []byte, valid *Snapshot) {
	t.Helper()
	id := fmt.Sprintf("r%d", i)
	switch rng.Intn(16) {
	case 0: // undecodable
		return []byte(fmt.Sprintf(`{"replica":{"id":%q,"priority":%d},"position":`, id, rng.Intn(4))), nil
	case 1: // structurally inconsistent: write positioned after the snapshot
		s := &Snapshot{
			Replica: ReplicaMeta{ID: ReplicaID(id), Priority: uint64(rng.Intn(4))},
			Pos:     Position(1 + rng.Intn(5)),
			Objects: map[string]ObjectInstance{
				"oX": {Props: map[string]VersionedValue{"p": vv("bad", Position(100))}},
			},
		}
		raw, _ := json.Marshal(s)
		return raw, nil
	}
	s := &Snapshot{
		Replica: ReplicaMeta{ID: ReplicaID(id), Priority: uint64(rng.Intn(4))},
		Pos:     Position(1 + rng.Intn(10)),
		Objects: map[string]ObjectInstance{},
	}
	nObj := 1 + rng.Intn(5)
	for j := 0; j < nObj; j++ {
		objID := fmt.Sprintf("o%d", rng.Intn(6))
		props := s.Objects[objID].Props
		if props == nil {
			props = map[string]VersionedValue{}
			s.Objects[objID] = ObjectInstance{Props: props}
		}
		nProp := 1 + rng.Intn(3)
		for k := 0; k < nProp; k++ {
			prop := fmt.Sprintf("p%d", rng.Intn(3))
			props[prop] = vv(fmt.Sprintf("v%d", rng.Intn(3)), Position(rng.Intn(int(s.Pos)+1)))
		}
	}
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw, s
}

// TestAgainstNaiveReferenceModel runs the optimized reconciler and the
// step-by-step naive model over many randomized multi-replica snapshot
// combinations — including corruption and identifier ties — and requires
// identical verdicts. Every case (inputs, output, decision rationale) is
// appended to a JSONL log for audit.
func TestAgainstNaiveReferenceModel(t *testing.T) {
	const cases = 3000
	seed := int64(20261007)
	if s := os.Getenv("RECONCILE_PROPERTY_SEED"); s != "" {
		if _, err := fmt.Sscan(s, &seed); err != nil {
			t.Fatalf("bad RECONCILE_PROPERTY_SEED: %v", err)
		}
	}
	rng := rand.New(rand.NewSource(seed))

	logPath := propertyLogPath()
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("create log: %v", err)
	}
	defer logFile.Close()
	enc := json.NewEncoder(logFile)
	t.Logf("recording every reconciliation (inputs/output/decisions) to %s", logPath)

	r := New(Options{})
	for tc := 0; tc < cases; tc++ {
		n := 1 + rng.Intn(8)
		raws := make([][]byte, 0, n)
		valid := []*Snapshot{}
		for i := 0; i < n; i++ {
			raw, s := randomSnapshot(t, rng, i)
			raws = append(raws, raw)
			if s != nil {
				valid = append(valid, s)
			}
		}

		res, err := r.Reconcile(raws)

		entry := propertyCaseLog{Seed: seed, Case: tc, Error: ""}
		for _, raw := range raws {
			entry.Inputs = append(entry.Inputs, string(raw))
		}
		if err != nil {
			entry.Error = err.Error()
		} else {
			entry.Baseline = res.Baseline
			entry.Objects = res.Objects
			entry.Irreconcilable = res.Irreconcilable
			entry.Quarantined = res.Quarantined
			entry.Conflicts = res.Conflicts
		}
		if encErr := enc.Encode(entry); encErr != nil {
			t.Fatalf("log case %d: %v", tc, encErr)
		}

		// Cross-check against the naive reference model.
		if len(valid) == 0 {
			if !errors.Is(err, ErrInsufficientReplicas) {
				t.Fatalf("case %d: no valid replicas, err = %v, want ErrInsufficientReplicas", tc, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("case %d: unexpected error: %v", tc, err)
		}
		wantBase, wantObjs, wantIrrec := naiveReconcile(valid)
		if res.Baseline != wantBase {
			t.Fatalf("case %d: baseline = %d, naive = %d", tc, res.Baseline, wantBase)
		}
		if !reflect.DeepEqual(res.Objects, wantObjs) {
			t.Fatalf("case %d: objects mismatch\ngot:  %v\nwant: %v", tc, res.Objects, wantObjs)
		}
		gotIrrec := res.Irreconcilable
		if gotIrrec == nil {
			gotIrrec = []string{}
		}
		if wantIrrec == nil {
			wantIrrec = []string{}
		}
		if !reflect.DeepEqual(gotIrrec, wantIrrec) {
			t.Fatalf("case %d: irreconcilable = %v, naive = %v", tc, gotIrrec, wantIrrec)
		}
		if len(res.Quarantined) != n-len(valid) {
			t.Fatalf("case %d: quarantined = %d, want %d", tc, len(res.Quarantined), n-len(valid))
		}

		// Cost invariants: arbitration work is exactly the conflict work.
		if res.Stats.ArbitrationsRun != res.Stats.ConflictsFound {
			t.Fatalf("case %d: arbitrations %d != conflicts %d", tc,
				res.Stats.ArbitrationsRun, res.Stats.ConflictsFound)
		}
		wantScanned := 0
		for _, s := range valid {
			for _, obj := range s.Objects {
				wantScanned += len(obj.Props)
			}
		}
		if res.Stats.ValuesScanned != wantScanned {
			t.Fatalf("case %d: ValuesScanned = %d, want %d (one linear pass)",
				tc, res.Stats.ValuesScanned, wantScanned)
		}
	}
}
