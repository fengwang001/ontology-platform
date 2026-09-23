package fold

import (
	"strings"
	"testing"
)

func TestUnfold(t *testing.T) {
	cases := []struct {
		name  string
		first string
		conts []string
		want  string
	}{
		{"无续行", "value", nil, "value"},
		{"单个续行", "a", []string{" b"}, "a b"},
		{"前导空白压成一个空格", "a", []string{" \t  b"}, "a b"},
		{"多个续行", "a", []string{" b", "  c"}, "a b c"},
		{"续行内部空白保留", "a", []string{" b  c"}, "a b  c"},
	}
	for _, c := range cases {
		if got := Unfold(c.first, c.conts); got != c.want {
			t.Errorf("%s: Unfold(%q, %q) = %q, want %q", c.name, c.first, c.conts, got, c.want)
		}
	}
}

func TestIsContinuation(t *testing.T) {
	cases := []struct {
		line string
		ok   bool
	}{
		{" b", true}, {"\tb", true}, {"b", false}, {"", false},
	}
	for _, c := range cases {
		if got := IsContinuation(c.line); got != c.ok {
			t.Errorf("IsContinuation(%q) = %v, want %v", c.line, got, c.ok)
		}
	}
}

func TestFoldNoFold(t *testing.T) {
	cases := []struct {
		name  string
		width int
		value string
	}{
		{"宽度为零不折", 0, "some quite long value that would fold"},
		{"短行不折", 78, "short"},
		{"恰好等宽不折", 10, "ab"},
	}
	for _, c := range cases {
		lines := Fold("X-Name", c.value, c.width)
		if len(lines) != 1 || lines[0] != "X-Name: "+c.value {
			t.Errorf("%s: Fold = %q", c.name, lines)
		}
	}
}

// TestFoldRoundTrip 验证重折后再展开值逐字节不变（往返无损）。
func TestFoldRoundTrip(t *testing.T) {
	cases := []struct {
		name  string
		width int
		value string
	}{
		{"长值多处折行", 40, "alpha beta gamma delta epsilon zeta eta theta iota kappa"},
		{"无空格不可折", 20, "abcdefghijklmnopqrstuvwxyzabcdef"},
		{"连续空格不在空格串中折", 15, "a  b  c  d  e  f  g  h"},
		{"折点恰在边界", 12, "aa bb cc dd ee ff gg"},
	}
	for _, c := range cases {
		lines := Fold("X-L", c.value, c.width)
		// 模拟传输：首行 + CRLF + 续行，再展开。
		first := strings.TrimPrefix(lines[0], "X-L: ")
		got := Unfold(first, lines[1:])
		if got != c.value {
			t.Errorf("%s: 往返后 %q, want %q", c.name, got, c.value)
		}
		for i, l := range lines {
			if i > 0 && !IsContinuation(l) {
				t.Errorf("%s: 续行 %q 未以空白开头", c.name, l)
			}
		}
	}
}

// TestFoldWidthRespected 有空格可折时每行不超宽。
func TestFoldWidthRespected(t *testing.T) {
	value := "one two three four five six seven eight nine ten eleven twelve"
	for _, width := range []int{20, 30, 45} {
		for _, l := range Fold("X-L", value, width) {
			if len(l) > width {
				t.Errorf("width=%d: 行 %q 超宽", width, l)
			}
		}
	}
}
