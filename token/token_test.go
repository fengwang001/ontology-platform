package token

import "testing"

func TestIsValidName(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"Content-Type", true},
		{"x", true},
		{"X-Custom_1.~", true},
		{"", false},
		{"has space", false},
		{"has:colon", false},
		{"has\ttab", false},
		{"中文", false},
		{"bad\x7f", false},
	}
	for _, c := range cases {
		if got := IsValidName(c.name); got != c.want {
			t.Errorf("IsValidName(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestIsValidValue(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"plain text", true},
		{"", true},
		{"with\ttab", true},
		{"obs-\x80\xff-text", true},
		{"50% off", true},
		{"cr\r", false},
		{"lf\n", false},
		{"crlf\r\n", false},
		{"nul\x00", false},
		{"bell\a", false},
		{"del\x7f", false},
		{"esc\x1b", false},
	}
	for _, c := range cases {
		if got := IsValidValue(c.value); got != c.want {
			t.Errorf("IsValidValue(%q) = %v, want %v", c.value, got, c.want)
		}
	}
}

func TestHasForbiddenEscape(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"a%0d%0aEvil", true},
		{"a%0AEvil", true},
		{"a%00Evil", true},
		{"a%1fEvil", true},
		{"a%7FEvil", true},
		{"a%08backspace", true},
		{"a%09tab", false}, // HTAB 在值中合法，其编码同样放行
		{"100%25 sure", false},
		{"%41%42", false},
		{"trailing%", false},
		{"%zz", false},
		{"plain", false},
	}
	for _, c := range cases {
		if got := HasForbiddenEscape(c.value); got != c.want {
			t.Errorf("HasForbiddenEscape(%q) = %v, want %v", c.value, got, c.want)
		}
	}
}

func TestCanonical(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"content-type", "Content-Type"},
		{"CONTENT-TYPE", "Content-Type"},
		{"Content-Type", "Content-Type"},
		{"x", "X"},
		{"ETag", "Etag"},
		{"x-a-b", "X-A-B"},
		{"1st", "1st"},
	}
	for _, c := range cases {
		if got := Canonical(c.in); got != c.want {
			t.Errorf("Canonical(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
