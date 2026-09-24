package record_test

import (
	"errors"
	"testing"

	"ontology/record"
)

func TestValidate(t *testing.T) {
	depth17 := func() map[string]any {
		var root any = map[string]any{"k": ""}
		for i := 0; i < record.MaxDepth; i++ {
			root = map[string]any{"child": root}
		}
		return root.(map[string]any)
	}
	cyclic := map[string]any{}
	cyclic["self"] = cyclic
	cases := []struct {
		name   string
		fields map[string]any
		want   error
	}{
		{"empty", map[string]any{}, nil},
		{"depth one", map[string]any{"a": ""}, nil},
		{"empty string value", map[string]any{"a": "", "b": int64(1), "c": 1.5, "d": true, "e": nil}, nil},
		{"nested map in array", map[string]any{"users": []any{map[string]any{"phone": "x"}}}, nil},
		{"too deep", depth17(), record.ErrTooDeep},
		{"cycle", cyclic, record.ErrCycle},
		{"bad value", map[string]any{"f": make(chan int)}, record.ErrValue},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := record.New(record.Info, "t", tc.fields)
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v got %v", tc.want, err)
			}
		})
	}
	if record.Counters.TooDeep.Load() == 0 || record.Counters.Cycle.Load() == 0 {
		t.Fatal("reject counters not incremented")
	}
}

func TestCodec(t *testing.T) {
	cases := []struct {
		name    string
		traceID string
		fields  map[string]any
	}{
		{"empty", "", map[string]any{}},
		{"no trace empty id", "", map[string]any{"a": "b"}},
		{"nested array map", "T1", map[string]any{"users": []any{map[string]any{"p": "v"}}, "n": int64(3)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := record.New(record.Error, tc.traceID, tc.fields)
			if err != nil {
				t.Fatal(err)
			}
			data, err := r.Encode()
			if err != nil {
				t.Fatal(err)
			}
			r2, err := record.Decode(data)
			if err != nil {
				t.Fatal(err)
			}
			d2, _ := r2.Encode()
			if string(data) != string(d2) {
				t.Fatalf("roundtrip mismatch:\n%s\n%s", data, d2)
			}
		})
	}
}

func TestCloneIsolation(t *testing.T) {
	r, _ := record.New(record.Info, "t", map[string]any{"a": map[string]any{"b": "x"}})
	c := r.Clone()
	c.Fields["a"].(map[string]any)["b"] = "y"
	if r.Fields["a"].(map[string]any)["b"] != "x" {
		t.Fatal("clone mutated original")
	}
}
