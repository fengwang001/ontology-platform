package record_test

import (
	"bytes"
	"errors"
	"testing"

	"ontology/record"
)

func TestCodecRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		rec  *record.Record
	}{
		{"empty", &record.Record{}},
		{"depth1", &record.Record{TraceID: "t1", Level: record.LevelInfo, Fields: map[string]any{"k": "v"}}},
		{"nested", &record.Record{TraceID: "t2", Fields: map[string]any{"a": map[string]any{"b": map[string]any{"c": 1.5, "ok": true}}}}},
		{"array-of-maps", &record.Record{Fields: map[string]any{"l": []any{map[string]any{"x": "y"}, "s", nil}}}},
		{"empty-string-value", &record.Record{Fields: map[string]any{"e": ""}}},
		{"empty-trace-id", &record.Record{TraceID: "", Fields: map[string]any{"k": "v"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b1, err := tc.rec.Encode()
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			back, err := record.Decode(b1)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			b2, err := back.Encode()
			if err != nil {
				t.Fatalf("re-encode: %v", err)
			}
			if !bytes.Equal(b1, b2) {
				t.Fatalf("round trip mismatch:\n%s\n%s", b1, b2)
			}
		})
	}
}

func nest(depth int) map[string]any {
	m := map[string]any{"leaf": "v"}
	for i := 1; i < depth; i++ {
		m = map[string]any{"n": m}
	}
	return m
}

func TestDepthLimit(t *testing.T) {
	cases := []struct {
		name  string
		depth int
		want  error
	}{
		{"at-limit", record.MaxDepth, nil},
		{"over-limit", record.MaxDepth + 1, record.ErrDepth},
		{"way-over", record.MaxDepth + 100, record.ErrDepth},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := record.New("t", record.LevelInfo, nest(tc.depth))
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestCycleDetection(t *testing.T) {
	self := map[string]any{}
	self["me"] = self
	inner := map[string]any{}
	outer := map[string]any{"inner": inner}
	inner["outer"] = outer
	slice := make([]any, 1)
	slice[0] = slice
	sliceHolder := map[string]any{"s": slice}
	cases := []struct {
		name   string
		fields map[string]any
	}{
		{"self-map", self},
		{"indirect-map", outer},
		{"slice", sliceHolder},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := record.New("t", record.LevelInfo, tc.fields)
			if !errors.Is(err, record.ErrCycle) {
				t.Fatalf("got %v, want ErrCycle", err)
			}
			if errors.Is(err, record.ErrDepth) {
				t.Fatalf("cycle misclassified as depth: %v", err)
			}
		})
	}
}

func TestCloneIndependence(t *testing.T) {
	orig, err := record.New("t", record.LevelWarn, map[string]any{"a": map[string]any{"b": "v"}})
	if err != nil {
		t.Fatal(err)
	}
	dup := record.Clone(orig)
	dup.Fields["a"].(map[string]any)["b"] = "changed"
	if orig.Fields["a"].(map[string]any)["b"] != "v" {
		t.Fatal("clone aliases original")
	}
}
