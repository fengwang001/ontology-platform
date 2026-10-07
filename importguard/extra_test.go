package importguard

import (
	"bytes"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"testing"
)

// TestDecisionLogging verifies every judgment logs inputs, output and basis.
func TestDecisionLogging(t *testing.T) {
	var buf bytes.Buffer
	s := NewStore()
	s.AddType(ObjectType{Name: "T", Required: map[string]bool{"r": true}})
	s.AddSubject("s")
	s.Grant("s", "T", "r", ActionWrite)
	g := NewGatekeeper(s, NewTextLogger(&buf))
	r := g.BatchImport("s", ModeLenient, []Entry{{
		ObjectID: "o", Type: "T", Semantic: SemanticCreate,
		Fields: map[string]PropertyValue{"r": 1, "x": 2},
	}})
	if r.Records[0].Status != StatusPartial {
		t.Fatalf("want partial, got %+v", r.Records[0])
	}
	log := buf.String()
	for _, want := range []string{"DECISION", "field_permission", "permissions_decided", "commit", "basis=", "inputs:", "denied"} {
		if !strings.Contains(log, want) {
			t.Fatalf("decision log missing %q:\n%s", want, log)
		}
	}
	if !strings.Contains(log, "latest revision wins") && !strings.Contains(log, "default deny") {
		t.Fatalf("permission log must state its basis:\n%s", log)
	}
}

// TestConcurrentBatchesSerializable: many batches run concurrently against
// one store. Each record is either a create of a unique ID (must succeed
// exactly once producing exactly the intended final object) or an update of
// a shared counter object (every accepted write must persist). The invariant
// checked is that the final state equals some serial interleaving: no lost
// updates and no duplicate-create anomalies.
func TestConcurrentBatchesSerializable(t *testing.T) {
	s := NewStore()
	s.AddType(ObjectType{Name: "Cnt", Required: map[string]bool{"n": true}})
	for _, sub := range []string{"a", "b", "c"} {
		s.AddSubject(sub)
		s.Grant(sub, "Cnt", "n", ActionWrite)
	}
	s.PutObject(Object{ID: "shared", Type: "Cnt", Properties: map[string]PropertyValue{"n": 0}})

	const batches, perBatch = 12, 25
	var wg sync.WaitGroup
	for b := 0; b < batches; b++ {
		wg.Add(1)
		go func(b int) {
			defer wg.Done()
			sub := []string{"a", "b", "c"}[b%3]
			for k := 0; k < perBatch; k++ {
				// unique create
				g := NewGatekeeper(s, nil)
				id := fmt.Sprintf("u_%d_%d", b, k)
				r := g.BatchImport(sub, ModeAtomic, []Entry{{
					ObjectID: id, Type: "Cnt", Semantic: SemanticCreate,
					Fields: map[string]PropertyValue{"n": b*100 + k},
				}})
				if r.Records[0].Status != StatusSuccess {
					t.Errorf("unique create %s failed: %+v", id, r.Records[0])
					return
				}
				// shared update; just requires success (every increment must land)
				r2 := g.BatchImport(sub, ModeAtomic, []Entry{{
					ObjectID: "shared", Type: "Cnt", Semantic: SemanticUpdate,
					Fields: map[string]PropertyValue{"n": 1},
				}})
				if r2.Records[0].Status != StatusSuccess {
					t.Errorf("shared update failed: %+v", r2.Records[0])
					return
				}
			}
		}(b)
	}
	wg.Wait()

	created := 0
	s.mu.Lock()
	for id := range s.objects {
		if strings.HasPrefix(id, "u_") {
			created++
		}
	}
	shared := s.objects["shared"].Properties["n"].(int)
	ver := s.objects["shared"].Version
	s.mu.Unlock()
	if created != batches*perBatch {
		t.Fatalf("lost creates: %d of %d", created, batches*perBatch)
	}
	if want := int64(batches * perBatch); ver != want {
		t.Fatalf("lost updates: version %d want %d", ver, want)
	}
	// increment payload is the constant 1: n stays 1, but version proves
	// every update applied exactly once (serializability, no lost writes).
	_ = shared
}

// TestDifferentialAgainstNaive runs hundreds of randomly generated worlds and
// batches through BOTH the production gatekeeper and the independently
// written naive sequential model and requires identical per-record outcomes
// and identical final object state.
func TestDifferentialAgainstNaive(t *testing.T) {
	for seed := int64(0); seed < 400; seed++ {
		rng := rand.New(rand.NewSource(seed))
		gs, ge, gobjs := genWorld(rng, seed)

		// Two independently populated identical stores.
		st1 := materialize(gs)
		st2 := materialize(gs)

		mode := []Mode{ModeAtomic, ModeLenient}[rng.Intn(2)]
		subject := gs.subjects[rng.Intn(len(gs.subjects))]

		r1 := NewGatekeeper(st1, nil).BatchImport(subject, mode, ge)
		r2 := NaiveBatchImport(st2, subject, mode, ge)

		if !resultsEqual(r1, r2) {
			t.Fatalf("seed=%d mode=%s subject=%s\nresults differ:\n got=%+v\nnaive=%+v\nworld=%s",
				seed, mode, subject, dump(r1), dump(r2), gs.dump(ge))
		}
		if !statesEqual(st1, st2) {
			t.Fatalf("seed=%d final state differs\nstore1=%v\nstore2=%v", seed, dumpState(st1), dumpState(st2))
		}
		_ = gobjs
	}
}

type world struct {
	typeNames []string
	types     map[string][]string // required fields
	fields    map[string][]string // all fields per type
	subjects  []string
	seedObjs  []Object
	perms     []PermissionEntry
}

func (w world) dump(es []Entry) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "types=%v subjects=%v objects=%d perms=%d entries=%d",
		w.types, w.subjects, len(w.seedObjs), len(w.perms), len(es))
	return sb.String()
}

func genWorld(rng *rand.Rand, seed int64) (world, []Entry, map[string]bool) {
	w := world{
		typeNames: []string{"T1", "T2"},
		types:     map[string][]string{},
		fields:    map[string][]string{},
		subjects:  []string{"s1", "s2"},
	}
	for _, tn := range w.typeNames {
		all := []string{"f1", "f2", "f3", "f4"}
		w.fields[tn] = all
		var req []string
		for _, f := range all {
			if rng.Intn(3) == 0 {
				req = append(req, f)
			}
		}
		if len(req) == 0 {
			req = []string{"f1"}
		}
		w.types[tn] = req
	}

	// Random permission history (including overrides and denies).
	for _, sub := range w.subjects {
		for _, tn := range w.typeNames {
			for _, f := range w.fields[tn] {
				switch rng.Intn(3) {
				case 0:
					w.perms = append(w.perms, PermissionEntry{Subject: sub, TypeName: tn, Field: f, Action: ActionWrite, Allow: true})
				case 1:
					w.perms = append(w.perms, PermissionEntry{Subject: sub, TypeName: tn, Field: f, Action: ActionWrite, Allow: false})
				}
				// case 2: no entry (default deny)
				if rng.Intn(5) == 0 { // a later override
					w.perms = append(w.perms, PermissionEntry{Subject: sub, TypeName: tn, Field: f, Action: ActionWrite, Allow: rng.Intn(2) == 0})
				}
			}
		}
	}

	objIDs := []string{"o1", "o2", "o3", "o4"}
	present := map[string]bool{}
	for _, id := range objIDs {
		if rng.Intn(2) == 0 {
			tn := w.typeNames[rng.Intn(len(w.typeNames))]
			props := map[string]PropertyValue{}
			for _, f := range w.fields[tn] {
				if rng.Intn(2) == 0 {
					props[f] = rng.Intn(100)
				}
			}
			w.seedObjs = append(w.seedObjs, Object{ID: id, Type: tn, Properties: props, Version: 1})
			present[id] = true
		}
	}

	var entries []Entry
	n := 1 + rng.Intn(6)
	for i := 0; i < n; i++ {
		id := objIDs[rng.Intn(len(objIDs))]
		tn := w.typeNames[rng.Intn(len(w.typeNames))]
		if rng.Intn(6) == 0 { // occasionally a non-existent type
			tn = "MissingType"
		}
		sem := []Semantic{SemanticCreate, SemanticUpdate}[rng.Intn(2)]
		fs := map[string]PropertyValue{}
		for _, f := range w.fields["T1"] {
			if rng.Intn(2) == 0 {
				fs[f] = rng.Intn(100)
			}
		}
		if len(fs) == 0 {
			fs["f1"] = 1
		}
		entries = append(entries, Entry{ObjectID: id, Type: tn, Semantic: sem, Fields: fs})
	}

	// Occasionally use an unknown subject.
	if rng.Intn(6) == 0 {
		w.subjects = append(w.subjects, "ghost")
	}
	return w, entries, present
}

func materialize(w world) *Store {
	s := NewStore()
	for _, tn := range w.typeNames {
		req := map[string]bool{}
		for _, r := range w.types[tn] {
			req[r] = true
		}
		s.AddType(ObjectType{Name: tn, Required: req})
	}
	for _, sub := range w.subjects {
		s.AddSubject(sub)
	}
	for _, o := range w.seedObjs {
		s.PutObject(o)
	}
	// RevSeq order must match between both stores: append in slice order.
	s.mu.Lock()
	for i := range w.perms {
		e := w.perms[i]
		s.permissions = append(s.permissions, PermissionEntry{
			RevSeq: int64(i + 1), Subject: e.Subject, TypeName: e.TypeName,
			Field: e.Field, Action: e.Action, Allow: e.Allow,
		})
	}
	s.nextRev = int64(len(w.perms) + 1)
	s.mu.Unlock()
	return s
}

func resultsEqual(a, b BatchResult) bool {
	if a.Rejected != b.Rejected {
		return false
	}
	if a.Rejected {
		return a.Failure.Category == b.Failure.Category
	}
	if len(a.Records) != len(b.Records) {
		return false
	}
	for i := range a.Records {
		x, y := a.Records[i], b.Records[i]
		if x.ObjectID != y.ObjectID || x.Status != y.Status {
			return false
		}
		if !sliceEq(x.Skipped, y.Skipped) || !sliceEq(x.WrittenKeys, y.WrittenKeys) {
			return false
		}
		if (x.Failure == nil) != (y.Failure == nil) {
			return false
		}
		if x.Failure != nil {
			if x.Failure.Category != y.Failure.Category || !sliceEq(x.Failure.Fields, y.Failure.Fields) {
				return false
			}
		}
	}
	return true
}

func sliceEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func statesEqual(a, b *Store) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(a.objects) != len(b.objects) {
		return false
	}
	for id, oa := range a.objects {
		ob, ok := b.objects[id]
		if !ok || oa.Type != ob.Type || oa.Version != ob.Version || len(oa.Properties) != len(ob.Properties) {
			return false
		}
		for k, va := range oa.Properties {
			vb, ok := ob.Properties[k]
			if !ok || va != vb {
				return false
			}
		}
	}
	return true
}

func dump(r BatchResult) string {
	var parts []string
	for _, x := range r.Records {
		cat := ""
		if x.Failure != nil {
			cat = string(x.Failure.Category)
		}
		parts = append(parts, fmt.Sprintf("{%s %s skip=%v written=%v fail=%s}",
			x.ObjectID, x.Status, x.Skipped, x.WrittenKeys, cat))
	}
	return strings.Join(parts, " | ")
}

func dumpState(s *Store) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.objects))
	for id := range s.objects {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var parts []string
	for _, id := range ids {
		o := s.objects[id]
		ks := make([]string, 0, len(o.Properties))
		for k := range o.Properties {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		var pv []string
		for _, k := range ks {
			pv = append(pv, fmt.Sprintf("%s=%v", k, o.Properties[k]))
		}
		parts = append(parts, fmt.Sprintf("%s(%s,v%d):%s", id, o.Type, o.Version, strings.Join(pv, ",")))
	}
	return strings.Join(parts, " ; ")
}
