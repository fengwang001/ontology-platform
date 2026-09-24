package record

import (
	"errors"
	"testing"
)

func TestRecord(t *testing.T) {
	deep := func(n int) map[string]any {
		root := map[string]any{}
		cur := root
		for i := 0; i < n; i++ {
			next := map[string]any{}
			cur["k"] = next
			cur = next
		}
		cur["v"] = "end"
		return root
	}
	cyclic := map[string]any{"a": "x"}
	cyclic["self"] = cyclic

	cases := []struct {
		name    string
		level   Level
		trace   string
		fields  map[string]any
		wantErr error
		check   func(*testing.T, *Record)
	}{
		{
			name:   "empty record",
			fields: map[string]any{},
			check: func(t *testing.T, r *Record) {
				if len(r.Fields) != 0 {
					t.Fatalf("empty fields expected")
				}
			},
		},
		{
			name:   "nil fields stays usable",
			fields: nil,
			check: func(t *testing.T, r *Record) {
				if r.Fields == nil {
					t.Fatalf("Fields should be non-nil map")
				}
			},
		},
		{
			name:   "depth 1",
			fields: map[string]any{"k": "v"},
			check: func(t *testing.T, r *Record) {
				if r.Fields["k"] != "v" {
					t.Fatalf("depth1 value mismatch")
				}
			},
		},
		{
			name:   "nested map inside array",
			fields: map[string]any{"arr": []any{map[string]any{"k": "v"}}},
			check: func(t *testing.T, r *Record) {
				arr := r.Fields["arr"].([]any)
				if arr[0].(map[string]any)["k"] != "v" {
					t.Fatalf("array nested map mismatch")
				}
			},
		},
		{
			name:   "depth at limit 16 accepted",
			fields: deep(MaxDepth - 1),
			check: func(t *testing.T, r *Record) {
				if r.Fields == nil {
					t.Fatalf("expected record at depth limit")
				}
			},
		},
		{
			name:    "depth exceeds limit",
			fields:  deep(MaxDepth + 2),
			wantErr: ErrTooDeep,
		},
		{
			name:    "cyclic reference rejected",
			fields:  cyclic,
			wantErr: ErrCycle,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := New(tc.level, tc.trace, tc.fields)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if err != nil {
				return
			}
			if tc.check != nil {
				tc.check(t, r)
			}
			data, err := Encode(r)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			r2, err := Decode(data)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			data2, _ := Encode(r2)
			if string(data) != string(data2) {
				t.Fatalf("roundtrip changed bytes:\n%s\n%s", data, data2)
			}
		})
	}
}
