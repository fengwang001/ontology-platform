package stack

import "testing"

func fr(name string) Frame { return Frame{Func: name} }

func TestNormalize(t *testing.T) {
	cases := []struct {
		name      string
		in        []Frame
		depth     int
		wantLen   int
		wantDrop  int
		wantTrunc bool
		wantDedup int
		invalid   bool
		lastMark  bool
	}{
		{"empty", nil, 8, 0, 0, false, 0, true, false},
		{"single", []Frame{fr("A")}, 8, 1, 0, false, 0, false, false},
		{"empty-name-legal", []Frame{{Func: ""}}, 8, 1, 0, false, 0, false, false},
		{"adj-dedup", []Frame{fr("A"), fr("A"), fr("B")}, 8, 2, 0, false, 1, false, false},
		{"exact-limit", []Frame{fr("A"), fr("B"), fr("C")}, 3, 3, 0, false, 0, false, false},
		{"over-limit", []Frame{fr("A"), fr("B"), fr("C"), fr("D")}, 3, 3, 1, true, 0, false, true},
		{"nonadj-recursion-kept", []Frame{fr("F"), fr("G"), fr("F")}, 8, 3, 0, false, 0, false, false},
		{"zero-depth-uses-default", []Frame{fr("A")}, 0, 1, 0, false, 0, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Normalize(tc.in, tc.depth)
			if got.Invalid != tc.invalid {
				t.Fatalf("invalid=%v want %v", got.Invalid, tc.invalid)
			}
			if tc.invalid {
				return
			}
			if len(got.Frames) != tc.wantLen || got.Dropped != tc.wantDrop ||
				got.Truncated != tc.wantTrunc || got.DedupedAdj != tc.wantDedup {
				t.Fatalf("got len=%d drop=%d trunc=%v dedup=%d",
					len(got.Frames), got.Dropped, got.Truncated, got.DedupedAdj)
			}
			if tc.lastMark != got.Frames[len(got.Frames)-1].Truncated {
				t.Fatalf("last truncated mark=%v want %v",
					got.Frames[len(got.Frames)-1].Truncated, tc.lastMark)
			}
		})
	}
}

func TestNormalizeDoesNotMutateInput(t *testing.T) {
	in := []Frame{fr("A"), fr("B"), fr("C"), fr("D")}
	_ = Normalize(in, 2)
	for _, f := range in {
		if f.Truncated {
			t.Fatal("input frame was marked truncated")
		}
	}
}
