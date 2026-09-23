package fold

import (
	"slices"
	"strings"
	"testing"
)

func TestUnfold(t *testing.T) {
	cases := []struct {
		name    string
		lines   []string
		want    []string
		wantErr bool
	}{
		{"无续行", []string{"A: 1", "B: 2"}, []string{"A: 1", "B: 2"}, false},
		{"空格续行", []string{"A: 1", " 2"}, []string{"A: 1 2"}, false},
		{"制表符续行", []string{"A: 1", "\t2"}, []string{"A: 1 2"}, false},
		{"前导空白压成一个", []string{"A: 1", "   \t  2"}, []string{"A: 1 2"}, false},
		{"多行续行", []string{"A: 1", " 2", " 3", "B: 4"}, []string{"A: 1 2 3", "B: 4"}, false},
		{"首行续行判错", []string{" 1", "A: 2"}, nil, true},
		{"仅首行续行", []string{" 1"}, nil, true},
	}
	for _, c := range cases {
		got, err := Unfold(c.lines)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err = %v, wantErr %v", c.name, err, c.wantErr)
		}
		if !c.wantErr && !slices.Equal(got, c.want) {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestFoldRoundTrip(t *testing.T) {
	cases := []struct {
		name  string
		line  string
		width int
	}{
		{"短行不折", "X-A: short", 78},
		{"长行重折", "X-Long: " + strings.Repeat("word ", 30), 40},
		{"连续空格处重折", "X-Sp: a  b  c " + strings.Repeat("z ", 40), 20},
		{"无空格超宽原样", "X-No: " + strings.Repeat("a", 200), 40},
		{"宽度为零不折", "X-A: " + strings.Repeat("w ", 50), 0},
	}
	for _, c := range cases {
		folded := Fold(c.line, c.width)
		got, err := Unfold(folded)
		if err != nil {
			t.Fatalf("%s: Unfold err %v", c.name, err)
		}
		if len(got) != 1 || got[0] != c.line {
			t.Errorf("%s: round trip = %q, want %q", c.name, got, c.line)
		}
	}
}

func TestFoldRespectsWidth(t *testing.T) {
	line := "X-Long: " + strings.Repeat("word ", 30)
	for _, l := range Fold(line, 40) {
		if len(l) > 40 {
			t.Errorf("folded line exceeds width: %q (%d > 40)", l, len(l))
		}
	}
}

func TestFoldContinuationPrefix(t *testing.T) {
	line := "X-L: " + strings.Repeat("alpha beta ", 20)
	for _, l := range Fold(line, 30)[1:] {
		if !IsContinuation(l) {
			t.Errorf("continuation line %q does not start with whitespace", l)
		}
	}
}
