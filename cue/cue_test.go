package cue

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateTable(t *testing.T) {
	long := strings.Repeat("字", 101) // 303 bytes
	cases := []struct {
		name string
		dmin int64
		cues []Cue
		want *CueError // nil means valid
	}{
		{"empty ok", 500, nil, nil},
		{"single ok", 500, []Cue{{0, 500, "a"}}, nil},
		{"exact dmin ok", 500, []Cue{{10, 510, "a"}}, nil},
		{"touching ok", 500, []Cue{{0, 500, "a"}, {500, 1000, "b"}}, nil},
		{"start equals end", 500, []Cue{{100, 100, "a"}}, &CueError{0, KindTime}},
		{"start after end", 500, []Cue{{300, 100, "a"}}, &CueError{0, KindTime}},
		{"negative start", 500, []Cue{{-1, 100, "a"}}, &CueError{0, KindTime}},
		{"end over max", 500, []Cue{{0, MaxTime + 1, "a"}}, &CueError{0, KindTime}},
		{"too short", 500, []Cue{{0, 499, "a"}}, &CueError{0, KindDuration}},
		{"too short one below dmin", 500, []Cue{{0, 500, "a"}, {600, 601, "b"}}, &CueError{1, KindDuration}},
		{"unsorted start", 500, []Cue{{1000, 1600, "b"}, {0, 500, "a"}}, &CueError{1, KindOrder}},
		{"overlap", 500, []Cue{{0, 600, "a"}, {599, 1200, "b"}}, &CueError{1, KindOrder}},
		{"empty text", 500, []Cue{{0, 500, ""}}, &CueError{0, KindText}},
		{"text too long", 500, []Cue{{0, 500, long}}, &CueError{0, KindText}},
		{"200 bytes ok", 500, []Cue{{0, 500, strings.Repeat("a", MaxBytes)}}, nil},
		{"kind order within one cue: time over duration", 500, []Cue{{500, 400, ""}}, &CueError{0, KindTime}},
		{"duration over order", 500, []Cue{{0, 600, "a"}, {100, 101, "b"}}, &CueError{1, KindDuration}},
		{"order over text", 500, []Cue{{0, 600, "a"}, {500, 1100, ""}}, &CueError{1, KindOrder}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.dmin, tc.cues)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("want nil, got %v", err)
				}
				return
			}
			var ce *CueError
			if !errors.As(err, &ce) {
				t.Fatalf("want *CueError, got %T %v", err, err)
			}
			if ce.Index != tc.want.Index || ce.Kind != tc.want.Kind {
				t.Fatalf("want index=%d kind=%d, got index=%d kind=%d", tc.want.Index, tc.want.Kind, ce.Index, ce.Kind)
			}
		})
	}
}

func TestValidateParams(t *testing.T) {
	if err := Validate(0, nil); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("dmin=0: %v", err)
	}
	if err := Validate(MaxDmin+1, nil); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("dmin too big: %v", err)
	}
	big := make([]Cue, MaxCues+1)
	for i := range big {
		big[i] = Cue{int64(i) * 1000, int64(i)*1000 + 500, "x"}
	}
	if err := Validate(500, big); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("too many cues: %v", err)
	}
}
