package chunk

import "testing"

func TestNext(t *testing.T) {
	cases := []struct {
		in, head, tail string
		digit          bool
	}{
		{"abc123x", "abc", "123x", false},
		{"123abc", "123", "abc", true},
		{"007", "007", "", true},
		{"a", "a", "", false},
		{"１２3", "１２", "3", false}, // full-width digits are not digits
		{"9é8", "9", "é8", true},
	}
	for _, tc := range cases {
		head, tail, digit := Next(tc.in)
		if head != tc.head || tail != tc.tail || digit != tc.digit {
			t.Errorf("Next(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tc.in, head, tail, digit, tc.head, tc.tail, tc.digit)
		}
	}
}

func TestNextRoundTrip(t *testing.T) {
	cases := []string{"", "a1", "1a", "aa11bb22", "１２00x", "000"}
	for _, s := range cases {
		got := ""
		digit := false
		alternates := true
		prev := -1
		for rest := s; len(rest) > 0; {
			var head string
			head, rest, digit = Next(rest)
			if len(head) == 0 {
				t.Fatalf("Next(%q) made no progress", s)
			}
			if prev >= 0 && (digit == (prev == 1)) {
				alternates = false
			}
			if digit {
				prev = 1
			} else {
				prev = 0
			}
			got += head
		}
		if got != s {
			t.Errorf("chunks of %q concatenate to %q", s, got)
		}
		if !alternates {
			t.Errorf("chunks of %q do not alternate", s)
		}
	}
}
