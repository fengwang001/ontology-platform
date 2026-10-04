package schema

import (
	"errors"
	"fmt"
	"testing"
)

func fld(name string, typ Type) Field {
	return Field{Name: name, Type: typ}
}

func TestValidateFields(t *testing.T) {
	tooMany := make([]Field, MaxFields+1)
	for i := range tooMany {
		tooMany[i] = fld(fmt.Sprintf("f%d", i), Int32)
	}
	maxOK := tooMany[:MaxFields]

	tests := []struct {
		name   string
		fields []Field
		want   error
	}{
		{"ok single", []Field{fld("a", Int32)}, nil},
		{"ok all types", []Field{fld("a", Int32), fld("b", Int64), fld("c", Float64), fld("d", String), fld("e", Bytes)}, nil},
		{"ok max 64", maxOK, nil},
		{"empty list", nil, ErrInvalidFields},
		{"too many", tooMany, ErrInvalidFields},
		{"empty name", []Field{fld("", Int32)}, ErrInvalidFields},
		{"dup name", []Field{fld("a", Int32), fld("a", Int64)}, ErrInvalidFields},
		{"bad type", []Field{fld("a", Type(99))}, ErrInvalidFields},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateFields(tt.fields)
			if !errors.Is(err, tt.want) {
				t.Fatalf("ValidateFields = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestModeAndTypeValid(t *testing.T) {
	for _, m := range []Mode{Backward, Forward, Full} {
		if !m.Valid() {
			t.Fatalf("mode %v should be valid", m)
		}
	}
	if Mode(3).Valid() || Mode(-1).Valid() {
		t.Fatal("unknown mode should be invalid")
	}
	for _, typ := range []Type{Int32, Int64, Float64, String, Bytes} {
		if !typ.Valid() {
			t.Fatalf("type %v should be valid", typ)
		}
	}
	if Type(5).Valid() {
		t.Fatal("unknown type should be invalid")
	}
}
