package record_test

import (
	"errors"
	"strings"
	"testing"

	"ontology/record"
)

func TestCodecAndLevel(t *testing.T) {
	cases := []struct {
		name string
		rec  record.Record
		want string
	}{
		{"empty", record.Record{}, `"level":"debug"`},
		{"basic", record.Record{Level: record.Error, TraceID: "t1", Fields: record.Fields{"a": "b"}}, `"level":"error"`},
		{"nested", record.Record{Fields: record.Fields{"m": record.Fields{"x": []any{"s", 1}}}}, `"m":{"x":["s",1]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := tc.rec.Encode()
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), tc.want) {
				t.Fatalf("encoded %s missing %s", data, tc.want)
			}
			dec, err := record.Decode(data)
			if err != nil {
				t.Fatal(err)
			}
			if dec.Level != tc.rec.Level || dec.TraceID != tc.rec.TraceID {
				t.Fatalf("round trip mismatch: %+v vs %+v", dec, tc.rec)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	deep := record.Fields{"k": "v"}
	for i := 0; i < record.MaxDepth-2; i++ {
		deep = record.Fields{"k": deep}
	}
	tooDeep := record.Fields{"k": deep}

	shared := record.Fields{"x": "1"}
	siblings := record.Fields{"a": record.Fields{"s": shared}, "b": record.Fields{"s": shared}}

	self := record.Fields{}
	self["loop"] = self

	cases := []struct {
		name string
		f    record.Fields
		want error
	}{
		{"nil", nil, nil},
		{"flat", record.Fields{"a": "", "b": int64(1), "c": nil}, nil},
		{"array nested map", record.Fields{"a": []any{record.Fields{"k": "v"}}}, nil},
		{"depth at limit", deep, nil},
		{"sibling share is legal", siblings, nil},
		{"depth exceeded", tooDeep, record.ErrDepthExceeded},
		{"self reference", self, record.ErrCycle},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := record.Validate(tc.f)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
}

func TestWalkStrings(t *testing.T) {
	r := record.Fields{
		"name": "alice",
		"meta": record.Fields{"tags": []any{"x", "y", "z"}},
		"n":    int64(3),
	}
	got := []string{}
	record.WalkStrings(r, func(_ []record.Seg, v string) { got = append(got, v) })
	if len(got) != 4 {
		t.Fatalf("walked %d string leaves, want 4: %v", len(got), got)
	}
}
