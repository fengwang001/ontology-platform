package ontology

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func personType(extra ...PropertyType) ObjectType {
	props := []PropertyType{
		{Name: "id", Type: TypeInt, Required: true, Primary: true},
		{Name: "name", Type: TypeString, Required: true},
		{Name: "age", Type: TypeInt, Required: false},
	}
	return ObjectType{Name: "Person", Properties: append(props, extra...)}
}

func ageAsStringType() ObjectType {
	next := personType()
	for i := range next.Properties {
		if next.Properties[i].Name == "age" {
			next.Properties[i].Type = TypeString
		}
	}
	return next
}

func mustEvolve(t *testing.T, r *Registry, types []ObjectType, migrations map[string]MigrateFunc) *Schema {
	t.Helper()
	s, err := r.Evolve(types, migrations)
	if err != nil {
		t.Fatalf("Evolve failed: %v", err)
	}
	return s
}

func breakingChanges(t *testing.T, err error) map[string]BreakingChange {
	t.Helper()
	var berr *BreakingChangesError
	if !errors.As(err, &berr) {
		t.Fatalf("want BreakingChangesError, got %T: %v", err, err)
	}
	out := map[string]BreakingChange{}
	for _, c := range berr.Changes {
		out[c.ObjectType+"/"+c.Property] = c
	}
	return out
}

func TestInitialSchemaStartsAtVersionOne(t *testing.T) {
	r := NewRegistry()
	s := mustEvolve(t, r, []ObjectType{personType()}, nil)
	if s.Version != 1 {
		t.Fatalf("initial version = %d, want 1", s.Version)
	}
	cur := r.CurrentSchema()
	if cur.Version != 1 || len(cur.ObjectTypes) != 1 {
		t.Fatalf("current schema mismatch: %+v", cur)
	}
	if got, ok := r.GetSchema(1); !ok || got.Version != 1 {
		t.Fatalf("GetSchema(1) = %+v, ok=%v", got, ok)
	}
	if _, ok := r.GetSchema(2); ok {
		t.Fatalf("GetSchema(2) unexpectedly exists")
	}
}

func TestObjectTypeValidation(t *testing.T) {
	r := NewRegistry()
	var verr *ValidationError

	duplicate := ObjectType{Name: "Dup", Properties: []PropertyType{
		{Name: "id", Type: TypeInt, Required: true, Primary: true},
		{Name: "id", Type: TypeString},
	}}
	_, err := r.Evolve([]ObjectType{duplicate}, nil)
	if !errors.As(err, &verr) || verr.Property != "id" || verr.ObjectType != "Dup" {
		t.Fatalf("want duplicate property ValidationError, got %v", err)
	}

	noPrimary := ObjectType{Name: "NoKey", Properties: []PropertyType{
		{Name: "x", Type: TypeString},
	}}
	_, err = r.Evolve([]ObjectType{noPrimary}, nil)
	if !errors.As(err, &verr) || verr.ObjectType != "NoKey" {
		t.Fatalf("want missing-primary ValidationError, got %v", err)
	}

	twoPrimary := ObjectType{Name: "TwoKey", Properties: []PropertyType{
		{Name: "a", Type: TypeInt, Primary: true},
		{Name: "b", Type: TypeInt, Primary: true},
	}}
	_, err = r.Evolve([]ObjectType{twoPrimary}, nil)
	if !errors.As(err, &verr) || verr.ObjectType != "TwoKey" {
		t.Fatalf("want multi-primary ValidationError, got %v", err)
	}

	badType := ObjectType{Name: "Bad", Properties: []PropertyType{
		{Name: "id", Type: ValueType("weird"), Primary: true},
	}}
	_, err = r.Evolve([]ObjectType{badType}, nil)
	if !errors.As(err, &verr) || verr.Property != "id" {
		t.Fatalf("want bad-type ValidationError, got %v", err)
	}
}

func TestCompatibleEvolutionIncrementsVersion(t *testing.T) {
	r := NewRegistry()
	mustEvolve(t, r, []ObjectType{personType()}, nil)

	relaxed := personType(PropertyType{Name: "nickname", Type: TypeString})
	for i := range relaxed.Properties {
		if relaxed.Properties[i].Name == "name" {
			relaxed.Properties[i].Required = false
		}
	}
	address := ObjectType{Name: "Address", Properties: []PropertyType{
		{Name: "id", Type: TypeInt, Required: true, Primary: true},
		{Name: "city", Type: TypeString},
	}}
	s := mustEvolve(t, r, []ObjectType{relaxed, address}, nil)
	if s.Version != 2 || len(s.ObjectTypes) != 2 {
		t.Fatalf("version=%d typeCount=%d, want 2/2", s.Version, len(s.ObjectTypes))
	}
}

func TestHistoricalSchemasAreImmutableAfterLaterEvolution(t *testing.T) {
	r := NewRegistry()
	mustEvolve(t, r, []ObjectType{personType()}, nil)
	mustEvolve(t, r, []ObjectType{personType(PropertyType{Name: "nickname", Type: TypeString})}, nil)

	v1, ok := r.GetSchema(1)
	if !ok {
		t.Fatal("version 1 missing")
	}
	if len(v1.ObjectTypes[0].Properties) != 3 {
		t.Fatalf("v1 property count = %d, want 3", len(v1.ObjectTypes[0].Properties))
	}
	v1.ObjectTypes[0].Properties[0].Name = "hacked"
	again, _ := r.GetSchema(1)
	if again.ObjectTypes[0].Properties[0].Name != "id" {
		t.Fatalf("returned schema mutation leaked into registry: %+v", again)
	}
}

func TestBreakingChangeKindsRejectedAndClassified(t *testing.T) {
	cases := []struct {
		name     string
		mutate   func(*ObjectType)
		property string
		kind     BreakKind
	}{
		{"remove property", func(ot *ObjectType) {
			ot.Properties = ot.Properties[:2]
		}, "age", BreakPropertyRemoved},
		{"tighten nullable to required", func(ot *ObjectType) {
			for i := range ot.Properties {
				if ot.Properties[i].Name == "age" {
					ot.Properties[i].Required = true
				}
			}
		}, "age", BreakRequiredTightened},
		{"change property type", func(ot *ObjectType) {
			for i := range ot.Properties {
				if ot.Properties[i].Name == "age" {
					ot.Properties[i].Type = TypeString
				}
			}
		}, "age", BreakTypeChanged},
		{"change primary key", func(ot *ObjectType) {
			for i := range ot.Properties {
				switch ot.Properties[i].Name {
				case "id":
					ot.Properties[i].Primary = false
				case "name":
					ot.Properties[i].Primary = true
				}
			}
		}, "name", BreakPrimaryKeyChanged},
		{"add required property", func(ot *ObjectType) {
			ot.Properties = append(ot.Properties, PropertyType{Name: "ssn", Type: TypeString, Required: true})
		}, "ssn", BreakRequiredPropertyAdded},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := NewRegistry()
			mustEvolve(t, r, []ObjectType{personType()}, nil)
			next := personType()
			tc.mutate(&next)

			_, err := r.Evolve([]ObjectType{next}, nil)
			changes := breakingChanges(t, err)
			c, ok := changes["Person/"+tc.property]
			if !ok {
				t.Fatalf("missing change for Person/%s in %v", tc.property, err)
			}
			if c.Kind != tc.kind || c.ObjectType != "Person" || c.Property != tc.property {
				t.Fatalf("change = %+v, want kind %s on Person/%s", c, tc.kind, tc.property)
			}
			if r.CurrentSchema().Version != 1 {
				t.Fatalf("version advanced after rejected evolution: %d", r.CurrentSchema().Version)
			}
		})
	}
}

func TestPartialFailureAcrossMultipleChangesRejectsWholeEvolution(t *testing.T) {
	r := NewRegistry()
	company := ObjectType{Name: "Company", Properties: []PropertyType{
		{Name: "id", Type: TypeInt, Required: true, Primary: true},
		{Name: "taxId", Type: TypeString},
	}}
	mustEvolve(t, r, []ObjectType{personType(), company}, nil)

	// 同一次演进：Person 兼容变更（新增可空属性），Company 破坏变更（收紧为必填）。
	compatiblePerson := personType(PropertyType{Name: "nickname", Type: TypeString})
	for i := range company.Properties {
		if company.Properties[i].Name == "taxId" {
			company.Properties[i].Required = true
		}
	}

	_, err := r.Evolve([]ObjectType{compatiblePerson, company}, nil)
	changes := breakingChanges(t, err)
	c, ok := changes["Company/taxId"]
	if !ok || c.Kind != BreakRequiredTightened {
		t.Fatalf("want Company/taxId break, got %v", err)
	}

	cur := r.CurrentSchema()
	if cur.Version != 1 {
		t.Fatalf("version = %d, want 1", cur.Version)
	}
	for _, ot := range cur.ObjectTypes {
		if ot.Name == "Person" && len(ot.Properties) != 3 {
			t.Fatalf("rejected evolution left Person mutation behind: %d props", len(ot.Properties))
		}
		if ot.Name == "Company" {
			for _, p := range ot.Properties {
				if p.Name == "taxId" && p.Required {
					t.Fatal("rejected evolution left Company tightening behind")
				}
			}
		}
	}
}

func TestMultipleBreakingChangesAllReported(t *testing.T) {
	r := NewRegistry()
	mustEvolve(t, r, []ObjectType{personType()}, nil)

	next := personType()
	for i := range next.Properties {
		if next.Properties[i].Name == "age" {
			next.Properties[i].Required = true
			next.Properties[i].Type = TypeString
		}
	}
	_, err := r.Evolve([]ObjectType{next}, nil)
	var berr *BreakingChangesError
	errors.As(err, &berr)
	if berr == nil {
		t.Fatalf("want BreakingChangesError, got %v", err)
	}
	kinds := map[BreakKind]bool{}
	for _, c := range berr.Changes {
		if c.ObjectType != "Person" || c.Property != "age" {
			t.Fatalf("unexpected change %+v", c)
		}
		kinds[c.Kind] = true
	}
	if !kinds[BreakTypeChanged] || !kinds[BreakRequiredTightened] {
		t.Fatalf("want both type and required breaks, got %v", kinds)
	}
}

func TestBreakingChangeWithMigrationAppliesToAllInstances(t *testing.T) {
	r := NewRegistry()
	mustEvolve(t, r, []ObjectType{personType()}, nil)
	if err := r.PutInstance("Person", map[string]any{"id": 1, "name": "a"}); err != nil {
		t.Fatal(err)
	}
	if err := r.PutInstance("Person", map[string]any{"id": 2, "name": "b", "age": 30}); err != nil {
		t.Fatal(err)
	}

	called := 0
	migrate := func(inst map[string]any) (map[string]any, error) {
		called++
		if v, ok := inst["age"].(int); ok {
			inst["age"] = fmt.Sprintf("%d", v)
		} else {
			inst["age"] = "0"
		}
		return inst, nil
	}
	s := mustEvolve(t, r, []ObjectType{ageAsStringType()}, map[string]MigrateFunc{"Person": migrate})
	if s.Version != 2 || called != 2 {
		t.Fatalf("version=%d migrations=%d, want 2/2", s.Version, called)
	}
	got, ok := r.GetInstance("Person", "1")
	if !ok || got["age"] != "0" {
		t.Fatalf("instance 1 not migrated: %+v", got)
	}
	got, _ = r.GetInstance("Person", "2")
	if got["age"] != "30" {
		t.Fatalf("instance 2 not migrated: %+v", got)
	}

	v1, _ := r.GetSchema(1)
	if v1.ObjectTypes[0].Properties[2].Type != TypeInt {
		t.Fatalf("v1 schema altered by migration: %+v", v1.ObjectTypes[0])
	}
}

func TestMigrationErrorRollsBackSchemaAndInstances(t *testing.T) {
	r := NewRegistry()
	mustEvolve(t, r, []ObjectType{personType()}, nil)
	for _, id := range []int{1, 2, 3} {
		if err := r.PutInstance("Person", map[string]any{"id": id, "name": fmt.Sprintf("n%d", id)}); err != nil {
			t.Fatal(err)
		}
	}

	boom := errors.New("boom")
	migrate := func(inst map[string]any) (map[string]any, error) {
		id := inst["id"].(int)
		inst["age"] = "x"
		if id == 2 {
			return nil, boom
		}
		return inst, nil
	}

	_, err := r.Evolve([]ObjectType{ageAsStringType()}, map[string]MigrateFunc{"Person": migrate})
	var merr *MigrationError
	if !errors.As(err, &merr) {
		t.Fatalf("want MigrationError, got %v", err)
	}
	if merr.ObjectType != "Person" || merr.InstanceID != "2" || !errors.Is(err, boom) {
		t.Fatalf("MigrationError = %+v", merr)
	}

	if r.CurrentSchema().Version != 1 {
		t.Fatalf("version = %d, want 1 after rollback", r.CurrentSchema().Version)
	}
	for _, id := range []string{"1", "2", "3"} {
		inst, ok := r.GetInstance("Person", id)
		if !ok {
			t.Fatalf("instance %s lost after rollback", id)
		}
		if _, exists := inst["age"]; exists {
			t.Fatalf("instance %s kept partial migration: %+v", id, inst)
		}
	}

	okMigrate := func(inst map[string]any) (map[string]any, error) {
		inst["age"] = "0"
		return inst, nil
	}
	s := mustEvolve(t, r, []ObjectType{ageAsStringType()}, map[string]MigrateFunc{"Person": okMigrate})
	if s.Version != 2 {
		t.Fatalf("retry version = %d, want 2", s.Version)
	}
}

func TestMigrationPanicRollsBack(t *testing.T) {
	r := NewRegistry()
	mustEvolve(t, r, []ObjectType{personType()}, nil)
	if err := r.PutInstance("Person", map[string]any{"id": 1, "name": "a"}); err != nil {
		t.Fatal(err)
	}

	migrate := func(map[string]any) (map[string]any, error) {
		panic("kaboom")
	}
	_, err := r.Evolve([]ObjectType{ageAsStringType()}, map[string]MigrateFunc{"Person": migrate})
	var merr *MigrationError
	if !errors.As(err, &merr) {
		t.Fatalf("panic not converted to MigrationError: %v", err)
	}
	if r.CurrentSchema().Version != 1 {
		t.Fatalf("version advanced after panicked migration: %d", r.CurrentSchema().Version)
	}
	inst, _ := r.GetInstance("Person", "1")
	if inst["name"] != "a" {
		t.Fatalf("instance changed after panicked migration: %+v", inst)
	}
}

func TestMigrationProducingInvalidInstanceRollsBack(t *testing.T) {
	r := NewRegistry()
	mustEvolve(t, r, []ObjectType{personType()}, nil)
	if err := r.PutInstance("Person", map[string]any{"id": 1, "name": "a"}); err != nil {
		t.Fatal(err)
	}

	next := personType(PropertyType{Name: "ssn", Type: TypeString, Required: true})
	migrate := func(inst map[string]any) (map[string]any, error) {
		return inst, nil
	}
	_, err := r.Evolve([]ObjectType{next}, map[string]MigrateFunc{"Person": migrate})
	var merr *MigrationError
	if !errors.As(err, &merr) {
		t.Fatalf("want MigrationError for invalid migration output, got %v", err)
	}
	if r.CurrentSchema().Version != 1 {
		t.Fatalf("version advanced after invalid migration output: %d", r.CurrentSchema().Version)
	}
}

func TestConcurrentEvolutionProducesContiguousVersions(t *testing.T) {
	r := NewRegistry()
	mustEvolve(t, r, []ObjectType{personType()}, nil)

	const n = 50
	var wg sync.WaitGroup
	errs := make(chan error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			ot := ObjectType{Name: fmt.Sprintf("Type%d", i), Properties: []PropertyType{
				{Name: "id", Type: TypeInt, Required: true, Primary: true},
			}}
			_, err := r.Evolve([]ObjectType{personType(), ot}, nil)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent evolution failed: %v", err)
		}
	}

	final := r.CurrentSchema()
	if final.Version != n+1 {
		t.Fatalf("final version = %d, want %d", final.Version, n+1)
	}
	if len(final.ObjectTypes) != 1+n {
		t.Fatalf("final object type count = %d, want %d", len(final.ObjectTypes), 1+n)
	}
	for v := 1; v <= n+1; v++ {
		if _, ok := r.GetSchema(v); !ok {
			t.Fatalf("version %d missing, history has a hole", v)
		}
	}
}

func TestInstanceValidationOnPut(t *testing.T) {
	r := NewRegistry()
	mustEvolve(t, r, []ObjectType{personType()}, nil)

	if err := r.PutInstance("Person", map[string]any{"id": 1, "name": "a", "age": 30}); err != nil {
		t.Fatalf("valid instance rejected: %v", err)
	}
	var verr *ValidationError
	if err := r.PutInstance("Person", map[string]any{"id": 1, "name": "a", "age": "young"}); !errors.As(err, &verr) {
		t.Fatalf("want ValidationError for wrong type, got %v", err)
	}
	if err := r.PutInstance("Person", map[string]any{"id": 1}); !errors.As(err, &verr) {
		t.Fatalf("want ValidationError for missing required, got %v", err)
	}
	if err := r.PutInstance("Person", map[string]any{"id": 1, "name": "a", "ghost": 1}); !errors.As(err, &verr) {
		t.Fatalf("want ValidationError for unknown property, got %v", err)
	}

	got, _ := r.GetInstance("Person", "1")
	got["name"] = "mutated"
	again, _ := r.GetInstance("Person", "1")
	if again["name"] != "a" {
		t.Fatalf("GetInstance mutation leaked: %+v", again)
	}
}
