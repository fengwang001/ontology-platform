package cue_test

import (
	"errors"
	"strings"
	"testing"

	"ontology/cue"
)

func TestValidate(t *testing.T) {
	good := cue.Cue{Start: 0, End: 500, Text: "a"}
	tests := []struct {
		name  string
		cues  []cue.Cue
		dmin  int64
		index int
		cat   error
	}{
		{"empty ok", nil, 500, -1, nil},
		{"single ok", []cue.Cue{good}, 500, -1, nil},
		{"exact dmin ok", []cue.Cue{{0, 500, "a"}}, 500, -1, nil},
		{"touching ok", []cue.Cue{{0, 500, "a"}, {500, 1000, "b"}}, 500, -1, nil},
		{"start==end badtime", []cue.Cue{{10, 10, "a"}}, 500, 0, cue.ErrBadTime},
		{"neg start badtime", []cue.Cue{{-1, 10, "a"}}, 500, 0, cue.ErrBadTime},
		{"end over max badtime", []cue.Cue{{0, 1_000_000_001, "a"}}, 500, 0, cue.ErrBadTime},
		{"too short", []cue.Cue{{0, 499, "a"}}, 500, 0, cue.ErrTooShort},
		{"overlap", []cue.Cue{{0, 600, "a"}, {500, 1100, "b"}}, 500, 1, cue.ErrOrder},
		{"unsorted", []cue.Cue{{500, 1000, "a"}, {0, 500, "b"}}, 500, 1, cue.ErrOrder},
		{"empty text", []cue.Cue{{0, 500, ""}}, 500, 0, cue.ErrBadText},
		// 同一条多种问题：时间 > 过短 > 乱序 > 文本。
		{"order before text", []cue.Cue{{0, 600, "a"}, {100, 700, ""}}, 500, 1, cue.ErrOrder},
		{"short before text", []cue.Cue{{0, 10, ""}}, 500, 0, cue.ErrTooShort},
		{"time before short", []cue.Cue{{5, 5, "x"}}, 500, 0, cue.ErrBadTime},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := cue.Validate(tc.cues, tc.dmin)
			if tc.cat == nil {
				if err != nil {
					t.Fatalf("want nil, got %v", err)
				}
				return
			}
			var inv *cue.InvalidError
			if !errors.As(err, &inv) {
				t.Fatalf("want *InvalidError, got %T %v", err, err)
			}
			if inv.Index != tc.index || !errors.Is(err, tc.cat) {
				t.Fatalf("want index=%d cat=%v, got index=%d cat=%v", tc.index, tc.cat, inv.Index, inv.Err)
			}
		})
	}
}

func TestText200Bytes(t *testing.T) {
	s200 := strings.Repeat("a", 200)
	s201 := strings.Repeat("a", 201)
	if err := cue.Validate([]cue.Cue{{0, 500, s200}}, 500); err != nil {
		t.Fatalf("200 bytes should be valid: %v", err)
	}
	var inv *cue.InvalidError
	err := cue.Validate([]cue.Cue{{0, 500, s201}}, 500)
	if !errors.As(err, &inv) || !errors.Is(err, cue.ErrBadText) {
		t.Fatalf("201 bytes should be ErrBadText, got %v", err)
	}
}

func TestTooMany(t *testing.T) {
	cs := make([]cue.Cue, cue.MaxCues+1)
	for i := range cs {
		cs[i] = cue.Cue{Start: int64(i) * 1000, End: int64(i)*1000 + 500, Text: "x"}
	}
	err := cue.Validate(cs, 500)
	var inv *cue.InvalidError
	if !errors.As(err, &inv) || inv.Index != cue.MaxCues || !errors.Is(err, cue.ErrTooMany) {
		t.Fatalf("want ErrTooMany at %d, got %v", cue.MaxCues, err)
	}
}

func TestDMinRange(t *testing.T) {
	if !errors.Is(cue.Validate(nil, 0), cue.ErrDMinRange) {
		t.Fatal("dmin=0 must be rejected")
	}
	if !errors.Is(cue.Validate(nil, cue.MaxDMin+1), cue.ErrDMinRange) {
		t.Fatal("dmin=1e6+1 must be rejected")
	}
	if err := cue.Validate(nil, cue.MaxDMin); err != nil {
		t.Fatalf("dmin=1e6 valid: %v", err)
	}
}
