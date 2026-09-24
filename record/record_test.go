package record

import (
	"errors"
	"testing"
)

func nested(depth int) map[string]any {
	root := map[string]any{}
	cur := root
	for i := 1; i < depth; i++ {
		nxt := map[string]any{}
		cur["k"] = nxt
		cur = nxt
	}
	return root
}

// TestValidate 以一张表覆盖深度边界与循环引用。
func TestValidate(t *testing.T) {
	cycleA := map[string]any{}
	cycleA["self"] = cycleA
	cycleB := nested(3)
	b2 := cycleB["k"].(map[string]any)
	b2["back"] = cycleB
	cases := []struct {
		name   string
		fields map[string]any
		want   error
	}{
		{"nil", nil, nil},
		{"empty", map[string]any{}, nil},
		{"depth 1", map[string]any{"a": 1}, nil},
		{"depth at limit", nested(MaxDepth), nil},
		{"depth over limit", nested(MaxDepth + 1), ErrDepthExceeded},
		{"deep over by 3", nested(MaxDepth + 3), ErrDepthExceeded},
		{"self cycle", cycleA, ErrCycle},
		{"nested cycle", cycleB, ErrCycle},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.fields)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
}

// TestCodec 以一张表覆盖编解码往返（含空记录与数组内映射）。
func TestCodec(t *testing.T) {
	cases := []struct {
		name string
		rec  *Record
	}{
		{"empty", &Record{}},
		{"nil fields", &Record{Fields: nil, TraceID: "", Level: LevelInfo}},
		{"depth1", &Record{Fields: map[string]any{"a": "b"}, TraceID: "t1", Level: LevelError}},
		{
			"array of map",
			&Record{Fields: map[string]any{"arr": []any{map[string]any{"x": "1"}, map[string]any{"y": "2"}}}, TraceID: "t2", Level: LevelWarn},
		},
		{
			"nested",
			&Record{Fields: map[string]any{"a": map[string]any{"b": map[string]any{"c": 42}}}, TraceID: "t3", Level: LevelDebug},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := tc.rec.Marshal()
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			got, err := Unmarshal(data)
			if err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			data2, err := got.Marshal()
			if err != nil || string(data) != string(data2) {
				t.Fatalf("roundtrip mismatch:\n%s\n%s", data, data2)
			}
		})
	}
}
