package compat

import (
	"testing"

	"ontology/schema"
)

func fld(name string, t schema.Type, required, hasDefault bool) schema.Field {
	return schema.Field{Name: name, Type: t, Required: required, HasDefault: hasDefault}
}

func mustVersion(t *testing.T, fields []schema.Field) *schema.Version {
	t.Helper()
	v, err := schema.NewVersion(fields)
	if err != nil {
		t.Fatalf("NewVersion: %v", err)
	}
	return v
}

func TestPromotionAndReverses(t *testing.T) {
	type tc struct {
		name     string
		from, to schema.Type
		ok       bool
	}
	cases := []tc{
		{"int32->int64", schema.Int32, schema.Int64, true},
		{"int32->float64", schema.Int32, schema.Float64, true},
		{"string->bytes", schema.String, schema.Bytes, true},
		{"identity int64", schema.Int64, schema.Int64, true},
		{"int64->int32", schema.Int64, schema.Int32, false},
		{"float64->int32", schema.Float64, schema.Int32, false},
		{"int64->float64", schema.Int64, schema.Float64, false},
		{"float64->int64", schema.Float64, schema.Int64, false},
		{"bytes->string", schema.Bytes, schema.String, false},
		{"string->int64", schema.String, schema.Int64, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := mustVersion(t, []schema.Field{fld("x", c.to, true, false)})
			w := mustVersion(t, []schema.Field{fld("x", c.from, true, false)})
			_, ok := CanRead(r, w)
			if ok != c.ok {
				t.Fatalf("CanRead = %v, want %v", ok, c.ok)
			}
		})
	}
}

func TestCanReadViolations(t *testing.T) {
	type tc struct {
		name   string
		reader []schema.Field
		writer []schema.Field
		ok     bool
		field  string
		reason Reason
	}
	cases := []tc{
		{
			name:   "missing with default ok",
			reader: []schema.Field{fld("a", schema.Int32, true, true)},
			writer: []schema.Field{fld("x", schema.Int32, true, false)},
			ok:     true,
		},
		{
			name:   "missing no default even if optional",
			reader: []schema.Field{fld("a", schema.Int32, false, false)},
			writer: []schema.Field{fld("x", schema.Int32, true, false)},
			ok:     false, field: "a", reason: MissingNoDefault,
		},
		{
			name:   "type mismatch beats later missing",
			reader: []schema.Field{fld("a", schema.Int32, true, false), fld("b", schema.Int32, true, false)},
			writer: []schema.Field{fld("a", schema.String, true, false)},
			ok:     false, field: "a", reason: TypeMismatch,
		},
		{
			name:   "type mismatch reverse direction",
			reader: []schema.Field{fld("a", schema.Int32, true, false)},
			writer: []schema.Field{fld("a", schema.Int64, true, false)},
			ok:     false, field: "a", reason: TypeMismatch,
		},
		{
			name:   "optional to required no default",
			reader: []schema.Field{fld("a", schema.Int32, true, false)},
			writer: []schema.Field{fld("a", schema.Int32, false, false)},
			ok:     false, field: "a", reason: OptionalToRequired,
		},
		{
			name:   "optional to required with default ok",
			reader: []schema.Field{fld("a", schema.Int32, true, true)},
			writer: []schema.Field{fld("a", schema.Int32, false, false)},
			ok:     true,
		},
		{
			name:   "reader optional accepts writer optional",
			reader: []schema.Field{fld("a", schema.Int32, false, false)},
			writer: []schema.Field{fld("a", schema.Int32, false, false)},
			ok:     true,
		},
		{
			name:   "extra writer fields ignored",
			reader: []schema.Field{fld("a", schema.Int32, true, false)},
			writer: []schema.Field{fld("z", schema.Bytes, true, false), fld("a", schema.Int32, true, false)},
			ok:     true,
		},
		{
			name:   "first offending field in reader order",
			reader: []schema.Field{fld("a", schema.Int32, true, false), fld("b", schema.Int32, true, false)},
			writer: []schema.Field{fld("b", schema.String, true, false)},
			ok:     false, field: "a", reason: MissingNoDefault,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := mustVersion(t, c.reader)
			w := mustVersion(t, c.writer)
			v, ok := CanRead(r, w)
			if ok != c.ok {
				t.Fatalf("ok = %v, want %v (violation=%+v)", ok, c.ok, v)
			}
			if !c.ok && (v.Field != c.field || v.Reason != c.reason) {
				t.Fatalf("violation = %+v, want field=%s reason=%v", v, c.field, c.reason)
			}
		})
	}
}

func TestComparedCounter(t *testing.T) {
	resetCompared()
	r := mustVersion(t, []schema.Field{
		fld("a", schema.Int32, true, false),
		fld("b", schema.String, false, false),
	})
	w := mustVersion(t, []schema.Field{fld("a", schema.Int64, true, false)})
	if v, ok := CanRead(r, w); ok {
		t.Fatalf("expected TypeMismatch at a, got ok; violation=%+v", v)
	}
	if got := Compared(); got != 1 {
		t.Fatalf("compared = %d, want 1 (early return at first reader field)", got)
	}
}
