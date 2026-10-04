package compat

import (
	"testing"

	"ontology/schema"
)

func f(name string, typ schema.Type, required, hasDefault bool) schema.Field {
	return schema.Field{Name: name, Type: typ, Required: required, HasDefault: hasDefault}
}

func TestPromotable(t *testing.T) {
	tests := []struct {
		from, to schema.Type
		want     bool
	}{
		// 同型恒可。
		{schema.Int32, schema.Int32, true},
		{schema.Int64, schema.Int64, true},
		{schema.Float64, schema.Float64, true},
		{schema.String, schema.String, true},
		{schema.Bytes, schema.Bytes, true},
		// 四种可提升。
		{schema.Int32, schema.Int64, true},
		{schema.Int32, schema.Float64, true},
		{schema.String, schema.Bytes, true},
		// 以上均不可反向。
		{schema.Int64, schema.Int32, false},
		{schema.Float64, schema.Int32, false},
		{schema.Bytes, schema.String, false},
		// 其余组合不可。
		{schema.Int64, schema.Float64, false},
		{schema.Float64, schema.Int64, false},
		{schema.Int32, schema.String, false},
		{schema.String, schema.Int32, false},
		{schema.Bytes, schema.Int64, false},
		{schema.Float64, schema.Bytes, false},
	}
	for _, tt := range tests {
		if got := Promotable(tt.from, tt.to); got != tt.want {
			t.Errorf("Promotable(%v, %v) = %v, want %v", tt.from, tt.to, got, tt.want)
		}
	}
}

func TestCanRead(t *testing.T) {
	tests := []struct {
		name string
		R, W []schema.Field
		want *Violation
	}{
		{
			name: "empty R always ok",
			R:    nil,
			W:    []schema.Field{f("a", schema.Int32, true, false)},
			want: nil,
		},
		{
			name: "extra fields in W ignored",
			R:    []schema.Field{f("a", schema.Int32, true, false)},
			W:    []schema.Field{f("a", schema.Int32, true, false), f("b", schema.String, true, false)},
			want: nil,
		},
		{
			name: "missing without default, not required",
			R:    []schema.Field{f("x", schema.Int32, false, false)},
			W:    []schema.Field{f("a", schema.Int32, true, false)},
			want: &Violation{Field: "x", Reason: MissingNoDefault},
		},
		{
			name: "missing without default, required",
			R:    []schema.Field{f("x", schema.Int32, true, false)},
			W:    nil,
			want: &Violation{Field: "x", Reason: MissingNoDefault},
		},
		{
			name: "missing with default ok",
			R:    []schema.Field{f("x", schema.Int32, true, true)},
			W:    nil,
			want: nil,
		},
		{
			name: "type mismatch int64 to int32",
			R:    []schema.Field{f("a", schema.Int32, true, false)},
			W:    []schema.Field{f("a", schema.Int64, true, false)},
			want: &Violation{Field: "a", Reason: TypeMismatch},
		},
		{
			name: "promotion int32 to int64 ok",
			R:    []schema.Field{f("a", schema.Int64, true, false)},
			W:    []schema.Field{f("a", schema.Int32, true, false)},
			want: nil,
		},
		{
			name: "optional to required without default",
			R:    []schema.Field{f("a", schema.Int32, true, false)},
			W:    []schema.Field{f("a", schema.Int32, false, false)},
			want: &Violation{Field: "a", Reason: OptionalToRequired},
		},
		{
			name: "optional to required with default ok",
			R:    []schema.Field{f("a", schema.Int32, true, true)},
			W:    []schema.Field{f("a", schema.Int32, false, false)},
			want: nil,
		},
		{
			name: "optional to optional ok",
			R:    []schema.Field{f("a", schema.Int32, false, false)},
			W:    []schema.Field{f("a", schema.Int32, false, false)},
			want: nil,
		},
		{
			name: "required to optional ok",
			R:    []schema.Field{f("a", schema.Int32, false, false)},
			W:    []schema.Field{f("a", schema.Int32, true, false)},
			want: nil,
		},
		{
			// R 序首个违规胜出：b 在 a 之前。
			name: "first violation by R order",
			R: []schema.Field{
				f("b", schema.Int32, true, false), // W 中 b 为 string → TypeMismatch
				f("a", schema.Int32, true, false), // W 无 a → MissingNoDefault
			},
			W:    []schema.Field{f("b", schema.String, true, false)},
			want: &Violation{Field: "b", Reason: TypeMismatch},
		},
		{
			// 交换 R 序后首个违规变为 a。
			name: "first violation flips with R order",
			R: []schema.Field{
				f("a", schema.Int32, true, false),
				f("b", schema.Int32, true, false),
			},
			W:    []schema.Field{f("b", schema.String, true, false)},
			want: &Violation{Field: "a", Reason: MissingNoDefault},
		},
		{
			// 同名字段先查类型，再查 optional→required。
			name: "type mismatch precedes optional-to-required",
			R:    []schema.Field{f("a", schema.Int32, true, false)},
			W:    []schema.Field{f("a", schema.String, false, false)},
			want: &Violation{Field: "a", Reason: TypeMismatch},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CanRead(tt.R, tt.W)
			if tt.want == nil {
				if got != nil {
					t.Fatalf("CanRead = %v, want nil", got)
				}
				return
			}
			if got == nil || *got != *tt.want {
				t.Fatalf("CanRead = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestComparedCounter(t *testing.T) {
	ResetCompared()
	R := []schema.Field{
		f("a", schema.Int32, true, false),
		f("b", schema.Int32, true, false),
		f("c", schema.Int32, true, false),
	}
	W := []schema.Field{
		f("a", schema.Int32, true, false),
		f("b", schema.String, true, false),
		f("c", schema.Int32, true, false),
	}
	if v := CanRead(R, W); v == nil || v.Field != "b" {
		t.Fatalf("CanRead = %v, want b TypeMismatch", v)
	}
	if got := Compared(); got != 2 {
		t.Fatalf("Compared = %d, want 2 (a ok, b violated)", got)
	}
	ResetCompared()
	if v := CanRead(R, R); v != nil {
		t.Fatalf("CanRead(R, R) = %v, want nil", v)
	}
	if got := Compared(); got != 3 {
		t.Fatalf("Compared = %d, want 3", got)
	}
}
