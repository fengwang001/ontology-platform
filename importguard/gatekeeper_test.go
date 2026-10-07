package importguard

import "testing"

func newTypedStore(t *testing.T, required ...string) (*Store, *Gatekeeper, *SliceLogger) {
	t.Helper()
	req := map[string]bool{}
	for _, r := range required {
		req[r] = true
	}
	s := NewStore()
	s.AddType(ObjectType{Name: "Person", Required: req})
	s.AddSubject("alice")
	s.AddSubject("bob")
	lg := &SliceLogger{}
	return s, NewGatekeeper(s, lg), lg
}

// TestModeDifferenceSameInput: identical input under the two modes yields
// different outcomes.
func TestModeDifferenceSameInput(t *testing.T) {
	entries := []Entry{{
		ObjectID: "p1", Type: "Person", Semantic: SemanticCreate,
		Fields: map[string]PropertyValue{"name": "n", "age": 30, "nick": "x"},
	}}

	s1, g1, _ := newTypedStore(t, "name")
	s1.Grant("alice", "Person", "name", ActionWrite)
	s1.Deny("alice", "Person", "age", ActionWrite)
	s1.Grant("alice", "Person", "nick", ActionWrite)
	r1 := g1.BatchImport("alice", ModeAtomic, entries)
	if r1.Rejected || len(r1.Records) != 1 || r1.Records[0].Status != StatusFailed {
		t.Fatalf("atomic: expected single failed record, got %+v", r1)
	}
	if r1.Records[0].Failure.Category != ErrFieldPermissionDenied {
		t.Fatalf("atomic: expected field_permission_denied, got %v", r1.Records[0].Failure.Category)
	}
	if _, ok := s1.GetObject("p1"); ok {
		t.Fatal("atomic failure must create no object")
	}

	s2, g2, _ := newTypedStore(t, "name")
	s2.Grant("alice", "Person", "name", ActionWrite)
	s2.Deny("alice", "Person", "age", ActionWrite)
	s2.Grant("alice", "Person", "nick", ActionWrite)
	r2 := g2.BatchImport("alice", ModeLenient, entries)
	if r2.Rejected || r2.Records[0].Status != StatusPartial {
		t.Fatalf("lenient: expected partial success, got %+v", r2)
	}
	if len(r2.Records[0].Skipped) != 1 || r2.Records[0].Skipped[0] != "age" {
		t.Fatalf("lenient: expected [age] skipped, got %v", r2.Records[0].Skipped)
	}
	o, ok := s2.GetObject("p1")
	if !ok {
		t.Fatal("lenient partial success must create the object")
	}
	if _, wrote := o.Properties["age"]; wrote {
		t.Fatal("skipped field must not be written")
	}
	if o.Properties["name"] != "n" || o.Properties["nick"] != "x" {
		t.Fatalf("writable fields must be written, got %+v", o.Properties)
	}
}

// TestLenientRequiredRecheck covers both sides of the post-skip re-check.
func TestLenientRequiredRecheck(t *testing.T) {
	// negative side, create: required "name" skipped -> full rollback
	s, g, _ := newTypedStore(t, "name", "age")
	s.Grant("alice", "Person", "age", ActionWrite)
	s.Deny("alice", "Person", "name", ActionWrite)
	r := g.BatchImport("alice", ModeLenient, []Entry{{
		ObjectID: "p1", Type: "Person", Semantic: SemanticCreate,
		Fields: map[string]PropertyValue{"name": "n", "age": 1},
	}})
	if r.Records[0].Status != StatusFailed ||
		r.Records[0].Failure.Category != ErrRequiredPropertyMissing {
		t.Fatalf("expected required_property_missing, got %+v", r.Records[0])
	}
	if _, ok := s.GetObject("p1"); ok {
		t.Fatal("required failure on create must fully roll back (no object)")
	}

	// positive side, update: required "name" skipped but old value exists
	s2, g2, _ := newTypedStore(t, "name", "age")
	s2.PutObject(Object{ID: "p1", Type: "Person", Properties: map[string]PropertyValue{"name": "old", "age": 9}})
	s2.Grant("alice", "Person", "age", ActionWrite)
	s2.Deny("alice", "Person", "name", ActionWrite)
	r2 := g2.BatchImport("alice", ModeLenient, []Entry{{
		ObjectID: "p1", Type: "Person", Semantic: SemanticUpdate,
		Fields: map[string]PropertyValue{"name": "new", "age": 2},
	}})
	if r2.Records[0].Status != StatusPartial {
		t.Fatalf("expected partial success reusing old name, got %+v", r2.Records[0])
	}
	o, _ := s2.GetObject("p1")
	if o.Properties["name"] != "old" || o.Properties["age"] != 2 {
		t.Fatalf("old required value retained + allowed field updated, got %+v", o.Properties)
	}

	// negative side, update: required skipped and NO old value -> failure
	s3, g3, _ := newTypedStore(t, "name")
	s3.PutObject(Object{ID: "p2", Type: "Person", Properties: map[string]PropertyValue{"age": 1}})
	s3.Deny("bob", "Person", "name", ActionWrite)
	s3.Grant("bob", "Person", "age", ActionWrite)
	r3 := g3.BatchImport("bob", ModeLenient, []Entry{{
		ObjectID: "p2", Type: "Person", Semantic: SemanticUpdate,
		Fields: map[string]PropertyValue{"name": "x", "age": 2},
	}})
	if r3.Records[0].Status != StatusFailed ||
		r3.Records[0].Failure.Category != ErrRequiredPropertyMissing {
		t.Fatalf("update with no reusable old required value must fail, got %+v", r3.Records[0])
	}
	o3, _ := s3.GetObject("p2")
	if o3.Properties["age"] != 1 {
		t.Fatalf("failing update must roll back (age unchanged), got %+v", o3.Properties)
	}
}

// TestCreateUpdateOldValueDifference pins create/update old-value semantics.
func TestCreateUpdateOldValueDifference(t *testing.T) {
	s, g, _ := newTypedStore(t, "name")
	s.PutObject(Object{ID: "p", Type: "Person", Properties: map[string]PropertyValue{"name": "old"}})
	s.Grant("alice", "Person", "name", ActionWrite)
	r := g.BatchImport("alice", ModeLenient, []Entry{{
		ObjectID: "p", Type: "Person", Semantic: SemanticCreate,
		Fields: map[string]PropertyValue{"name": "new"},
	}})
	if r.Records[0].Failure == nil || r.Records[0].Failure.Category != ErrSemanticMismatch {
		t.Fatalf("create-on-existing must be semantic mismatch, got %+v", r.Records[0])
	}

	s2, g2, _ := newTypedStore(t, "name")
	s2.Grant("alice", "Person", "name", ActionWrite)
	r2 := g2.BatchImport("alice", ModeLenient, []Entry{{
		ObjectID: "ghost", Type: "Person", Semantic: SemanticUpdate,
		Fields: map[string]PropertyValue{"name": "new"},
	}})
	if r2.Records[0].Failure == nil || r2.Records[0].Failure.Category != ErrSemanticMismatch {
		t.Fatalf("update-on-missing must be semantic mismatch, got %+v", r2.Records[0])
	}
}

// TestRejectionPriorityAndIndependence covers both priority orders and
// failure isolation between records.
func TestRejectionPriorityAndIndependence(t *testing.T) {
	s := NewStore()
	g := NewGatekeeper(s, nil)
	r := g.BatchImport("ghost", Mode("weird"), []Entry{{ObjectID: "x"}})
	if !r.Rejected || r.Failure.Category != ErrBatchInvalidParameter {
		t.Fatalf("bad mode must win over missing subject, got %+v", r)
	}
	r = g.BatchImport("ghost", ModeAtomic, []Entry{{ObjectID: "x"}})
	if !r.Rejected || r.Failure.Category != ErrSubjectNotFound {
		t.Fatalf("missing subject must reject batch, got %+v", r)
	}

	s2, g2, _ := newTypedStore(t, "name")
	s2.Grant("alice", "Missing", "name", ActionWrite)
	s2.Grant("alice", "Person", "name", ActionWrite)
	r2 := g2.BatchImport("alice", ModeAtomic, []Entry{
		{ObjectID: "a", Type: "Missing", Semantic: SemanticUpdate, Fields: map[string]PropertyValue{"name": 1}},
		{ObjectID: "b", Type: "Person", Semantic: SemanticUpdate, Fields: map[string]PropertyValue{"name": 1}},
		{ObjectID: "c", Type: "Person", Semantic: SemanticCreate, Fields: map[string]PropertyValue{"name": 1}},
	})
	if cat := r2.Records[0].Failure.Category; cat != ErrObjectTypeNotFound {
		t.Fatalf("type missing priority, got %v", cat)
	}
	if cat := r2.Records[1].Failure.Category; cat != ErrSemanticMismatch {
		t.Fatalf("semantic mismatch priority, got %v", cat)
	}
	if r2.Records[2].Status != StatusSuccess {
		t.Fatalf("neighbouring record must succeed independently, got %+v", r2.Records[2])
	}

	s3, g3, _ := newTypedStore(t, "name")
	s3.PutObject(Object{ID: "u", Type: "Person", Properties: map[string]PropertyValue{"name": "keep", "age": 1}})
	s3.Grant("alice", "Person", "name", ActionWrite)
	s3.Grant("alice", "Person", "age", ActionWrite)
	s3.Deny("alice", "Person", "secret", ActionWrite)
	r3 := g3.BatchImport("alice", ModeAtomic, []Entry{
		{ObjectID: "ok", Type: "Person", Semantic: SemanticCreate, Fields: map[string]PropertyValue{"name": "y"}},
		{ObjectID: "u", Type: "Person", Semantic: SemanticUpdate,
			Fields: map[string]PropertyValue{"name": "z", "age": 2, "secret": 7}},
	})
	if r3.Records[0].Status != StatusSuccess || r3.Records[1].Status != StatusFailed {
		t.Fatalf("expected success+fail independently, got %+v", r3)
	}
	ou, _ := s3.GetObject("u")
	if ou.Properties["name"] != "keep" || ou.Properties["age"] != 1 {
		t.Fatalf("failed atomic update must change nothing on existing object, got %+v", ou.Properties)
	}
}
