package chunk

import (
	"reflect"
	"testing"
)

func TestIsDigit(t *testing.T) {
	cases := []struct {
		b    byte
		want bool
	}{
		{'0', true}, {'9', true}, {'5', true},
		{'a', false}, {' ', false}, {0xff, false}, {"１"[0], false},
	}
	for _, c := range cases {
		if got := IsDigit(c.b); got != c.want {
			t.Errorf("IsDigit(%q) = %v, want %v", c.b, got, c.want)
		}
	}
}

func TestSplit(t *testing.T) {
	cases := []struct {
		in   string
		want []Segment
	}{
		{"", nil},
		{"a", []Segment{{Text: "a", Digit: false}}},
		{"1", []Segment{{Text: "1", Digit: true}}},
		{"file10", []Segment{
			{Text: "file", Digit: false},
			{Text: "10", Digit: true},
		}},
		{"a01b22", []Segment{
			{Text: "a", Digit: false},
			{Text: "01", Digit: true},
			{Text: "b", Digit: false},
			{Text: "22", Digit: true},
		}},
		{"１2", []Segment{
			{Text: "１", Digit: false},
			{Text: "2", Digit: true},
		}},
	}
	for _, c := range cases {
		if got := Split(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Split(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

func TestNextMatchesSplit(t *testing.T) {
	inputs := []string{"", "a", "1", "file10x2", "a01", "１２3", "\xff00a"}
	for _, in := range inputs {
		var got []Segment
		pos := 0
		for pos < len(in) {
			seg, next := Next(in, pos)
			got = append(got, seg)
			pos = next
		}
		if want := Split(in); !reflect.DeepEqual(got, want) {
			t.Errorf("Next over %q = %+v, want %+v", in, got, want)
		}
	}
}
