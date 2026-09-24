package record

import (
	"errors"
	"testing"
)

func nested(depth int) map[string]any {
	root := map[string]any{}
	cur := root
	for i := 1; i < depth; i++ {
		next := map[string]any{}
		cur["k"] = next
		cur = next
	}
	cur["v"] = "x"
	return root
}

func TestNewAndCodec(t *testing.T) {
	cases := []struct {
		name   string
		trace  string
		level  Level
		fields map[string]any
	}{
		{"empty", "", Info, map[string]any{}},
		{"depth1", "t", Debug, map[string]any{"a": "", "b": 1.0, "c": true, "d": nil}},
		{"array map", "t2", Error, map[string]any{"arr": []any{map[string]any{"x": "y"}, "z"}}},
		{"at limit", "t3", Info, nested(MaxDepth)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := New(tc.trace, tc.level, tc.fields)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			data, err := r.Marshal()
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			r2, err := Unmarshal(data)
			if err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if r2.Trace != tc.trace || r2.Level != tc.level {
				t.Fatalf("roundtrip mismatch: %+v", r2)
			}
			d2, _ := r2.Marshal()
			if string(data) != string(d2) {
				t.Fatalf("byte mismatch:\n%s\n%s", data, d2)
			}
		})
	}
}

func TestValidationErrors(t *testing.T) {
	self := map[string]any{"a": "b"}
	self["self"] = self
	parent := map[string]any{}
	child := map[string]any{"up": parent}
	parent["child"] = child
	arrSelf := map[string]any{"arr": []any{}}
	arrSelf["arr"] = []any{arrSelf}
	cases := []struct {
		name   string
		fields map[string]any
		want   error
	}{
		{"too deep map", nested(MaxDepth + 1), ErrTooDeep},
		{"cycle self", self, ErrCycle},
		{"cycle ancestor", parent, ErrCycle},
		{"cycle in array", arrSelf, ErrCycle},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New("t", Info, tc.fields); !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
		})
	}
}

func TestLevelString(t *testing.T) {
	cases := []struct {
		l    Level
		want string
	}{
		{Debug, "debug"}, {Info, "info"}, {Warn, "warn"}, {Error, "error"},
	}
	for _, tc := range cases {
		if got := tc.l.String(); got != tc.want {
			t.Fatalf("got %s want %s", got, tc.want)
		}
	}
}
