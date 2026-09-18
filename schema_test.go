package ontology

import (
	"errors"
	"testing"
)

func TestObjectTypeValidate(t *testing.T) {
	tests := []struct {
		name string
		ot   ObjectType
	}{
		{
			name: "empty object type name",
			ot: ObjectType{
				Properties: []PropertyType{{Name: "id", Type: TypeString, PrimaryKey: true}},
			},
		},
		{
			name: "duplicate property names",
			ot: ObjectType{
				Name: "Employee",
				Properties: []PropertyType{
					{Name: "id", Type: TypeString, PrimaryKey: true},
					{Name: "id", Type: TypeInteger},
				},
			},
		},
		{
			name: "no primary key",
			ot: ObjectType{
				Name:       "Employee",
				Properties: []PropertyType{{Name: "id", Type: TypeString}},
			},
		},
		{
			name: "two primary keys",
			ot: ObjectType{
				Name: "Employee",
				Properties: []PropertyType{
					{Name: "id", Type: TypeString, PrimaryKey: true},
					{Name: "ssn", Type: TypeString, PrimaryKey: true},
				},
			},
		},
		{
			name: "empty property name",
			ot: ObjectType{
				Name: "Employee",
				Properties: []PropertyType{
					{Name: "id", Type: TypeString, PrimaryKey: true},
					{Type: TypeString},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.ot.validate()
			var invalid *InvalidObjectTypeError
			if !errors.As(err, &invalid) {
				t.Fatalf("validate() = %v, want *InvalidObjectTypeError", err)
			}
		})
	}
}

func TestObjectTypeValidateOK(t *testing.T) {
	ot := ObjectType{
		Name: "Employee",
		Properties: []PropertyType{
			{Name: "id", Type: TypeString, Required: true, PrimaryKey: true},
			{Name: "name", Type: TypeString},
		},
	}
	if err := ot.validate(); err != nil {
		t.Fatalf("validate() = %v, want nil", err)
	}
}

func TestEvolveRejectsInvalidObjectType(t *testing.T) {
	r := NewRegistry()
	bad := ObjectType{
		Name: "Broken",
		Properties: []PropertyType{
			{Name: "id", Type: TypeString, PrimaryKey: true},
			{Name: "id", Type: TypeInteger},
		},
	}
	_, err := r.Evolve(Evolution{ObjectTypes: []ObjectType{bad}})
	var invalid *InvalidObjectTypeError
	if !errors.As(err, &invalid) {
		t.Fatalf("Evolve() = %v, want *InvalidObjectTypeError", err)
	}
	if got := r.Version(); got != 1 {
		t.Fatalf("Version() = %d, want 1 after rejected evolution", got)
	}
}

func TestEvolveRejectsDuplicateObjectTypeInOneStep(t *testing.T) {
	r := NewRegistry()
	ot := ObjectType{
		Name:       "Employee",
		Properties: []PropertyType{{Name: "id", Type: TypeString, PrimaryKey: true}},
	}
	_, err := r.Evolve(Evolution{ObjectTypes: []ObjectType{ot, ot}})
	var invalid *InvalidObjectTypeError
	if !errors.As(err, &invalid) {
		t.Fatalf("Evolve() = %v, want *InvalidObjectTypeError", err)
	}
	if got := r.Version(); got != 1 {
		t.Fatalf("Version() = %d, want 1 after rejected evolution", got)
	}
}
