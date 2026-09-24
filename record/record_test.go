package record_test

import (
	"errors"
	"strings"
	"testing"

	"ontology/record"
)

// nested 构造深度为 d 的嵌套映射（根映射深度为 1）。
func nested(d int) map[string]any {
	if d < 1 {
		return nil
	}
	root := map[string]any{"v": "x"}
	cur := root
	for i := 1; i < d; i++ {
		next := map[string]any{"v": "x"}
		cur["child"] = next
		cur = next
	}
	return root
}

func TestCodec(t *testing.T) {
	cases := []struct {
		name  string
		level record.Level
		trace string
		has   bool
		flds  map[string]any
	}{
		{"empty", record.LevelInfo, "", false, nil},
		{"depth1", record.LevelInfo, "t1", true, map[string]any{"a": "b"}},
		{"no-trace", record.LevelWarn, "", false, map[string]any{"n": 1.0}},
		{"empty-trace", record.LevelDebug, "", true, map[string]any{"k": ""}},
		{"array-map", record.LevelError, "t2", true, map[string]any{"l": []any{map[string]any{"x": "世界"}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := record.New(tc.level, tc.trace, tc.has, tc.flds)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			b, err := r.Encode()
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			got, err := record.Decode(b)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if got.TraceID != r.TraceID || got.HasTrace != r.HasTrace || got.Level != r.Level {
				t.Fatalf("roundtrip mismatch: %+v vs %+v", got, r)
			}
			gb, _ := got.Encode()
			if string(gb) != string(b) {
				t.Fatalf("byte roundtrip mismatch:\n%s\n%s", b, gb)
			}
			if tc.level >= record.LevelError && !strings.Contains(string(b), `"error"`) {
				t.Fatalf("error level not serialized: %s", b)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	shared := map[string]any{"x": 1.0}
	dag := map[string]any{"a": shared, "b": shared}
	cyclic := map[string]any{}
	cyclic["self"] = cyclic
	var selfSlice []any
	selfSlice = append(selfSlice, map[string]any{})
	selfSlice[0].(map[string]any)["arr"] = selfSlice

	before := record.DepthRejections()
	cases := []struct {
		name     string
		flds     map[string]any
		wantErr  error
		depthBad bool
	}{
		{"depth32", nested(record.MaxDepth), nil, false},
		{"depth33", nested(record.MaxDepth + 1), record.ErrDepth, true},
		{"cycle", cyclic, record.ErrCycle, false},
		{"slice-cycle", map[string]any{"a": selfSlice}, record.ErrCycle, false},
		{"dag-shared", dag, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := record.New(record.LevelInfo, "t", true, tc.flds)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr != nil {
				bad, _ := record.New(record.LevelInfo, "t", true, tc.flds)
				if bad != nil {
					t.Fatalf("invalid record must not be constructible")
				}
			}
		})
	}
	if got := record.DepthRejections() - before; got != 2 {
		t.Fatalf("depth rejection count = %d, want 2", got)
	}
}
