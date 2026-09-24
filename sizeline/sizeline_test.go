package sizeline

import (
	"strconv"
	"strings"
	"testing"
)

// miniParse is an independent minimal parser used to verify that escaped
// bytes never change the line's parse structure.
func miniParse(line string) (uint64, map[string]string, error) {
	semi := strings.IndexByte(line, ';')
	head, rest := line, ""
	if semi >= 0 {
		head, rest = line[:semi], line[semi:]
	}
	size, err := strconv.ParseUint(head, 16, 64)
	if err != nil {
		return 0, nil, err
	}
	exts := map[string]string{}
	for rest != "" {
		rest = rest[1:] // ';'
		var key string
		if i := strings.IndexByte(rest, '='); i >= 0 {
			key, rest = rest[:i], rest[i+1:]
		}
		var val strings.Builder
		if strings.HasPrefix(rest, "\"") {
			rest = rest[1:]
			for {
				c := rest[0]
				rest = rest[1:]
				if c == '"' {
					break
				}
				if c == '\\' {
					c = rest[0]
					rest = rest[1:]
				}
				val.WriteByte(c)
			}
		} else if i := strings.IndexByte(rest, ';'); i >= 0 {
			val.WriteString(rest[:i])
			rest = rest[i:]
		} else {
			val.WriteString(rest)
			rest = ""
		}
		if rest != "" && rest[0] != ';' {
			return 0, nil, strconv.ErrSyntax
		}
		exts[key] = val.String()
	}
	return size, exts, nil
}

func TestEncodeDecode(t *testing.T) {
	cases := []struct {
		size uint64
		exts []Ext
		line string
	}{
		{0, nil, "0\r\n"},
		{26, nil, "1a\r\n"},
		{255, []Ext{{"k", "v"}}, "ff;k=v\r\n"},
		{7, []Ext{{"a", "x"}, {"b", "y"}}, "7;a=x;b=y\r\n"},
		{7, []Ext{{"k", "a;b"}}, "7;k=\"a;b\"\r\n"},
		{7, []Ext{{"k", "a=b"}}, "7;k=\"a=b\"\r\n"},
		{7, []Ext{{"k", `a"b`}}, "7;k=\"a\\\"b\"\r\n"},
		{7, []Ext{{"k", `a\b`}}, "7;k=\"a\\\\b\"\r\n"},
		{7, []Ext{{"k", `;="` + `\`}}, "7;k=\";=\\\"\\\\\"\r\n"},
		{7, []Ext{{"k", ""}}, "7;k=\"\"\r\n"},
	}
	for _, c := range cases {
		got := string(Encode(c.size, c.exts))
		if got != c.line {
			t.Errorf("Encode(%d,%v) = %q, want %q", c.size, c.exts, got, c.line)
			continue
		}
		size, exts, err := Decode([]byte(c.line[:len(c.line)-2]))
		if err != nil || size != c.size || len(exts) != len(c.exts) {
			t.Fatalf("Decode(%q) = %d,%v,%v", c.line, size, exts, err)
		}
		for i, e := range exts {
			if e != c.exts[i] {
				t.Fatalf("Decode(%q) ext %d = %v, want %v", c.line, i, e, c.exts[i])
			}
		}
		msize, mexts, err := miniParse(c.line[:len(c.line)-2])
		if err != nil || msize != c.size {
			t.Fatalf("miniParse(%q) = %d,%v", c.line, msize, err)
		}
		for _, e := range c.exts {
			if mexts[e.Key] != e.Val {
				t.Fatalf("miniParse(%q)[%q] = %q, want %q", c.line, e.Key, mexts[e.Key], e.Val)
			}
		}
	}
}

func TestDecodeMalformed(t *testing.T) {
	for _, line := range []string{"", "zz", "1f;k", "1f;k=v;x", `1f;k="unterminated`, "1f;=v", "12345678901234567"} {
		if _, _, err := Decode([]byte(line)); err == nil {
			t.Errorf("Decode(%q) unexpectedly succeeded", line)
		}
	}
}

func TestExtsLen(t *testing.T) {
	exts := []Ext{{"k", "a;b"}, {"x", "y"}}
	if got, want := ExtsLen(exts), len(`;k="a;b";x=y`); got != want {
		t.Fatalf("ExtsLen = %d, want %d", got, want)
	}
}
