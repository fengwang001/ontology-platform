package sizeline

import (
	"strconv"
	"strings"
	"testing"
)

func TestValueQuoting(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "ok-42", "ok-42"},
		{"semicolon", "a;b", `"a;b"`},
		{"equals", "a=b", `"a=b"`},
		{"quote", `a"b`, `"a\"b"`},
		{"backslash", `a\b`, `"a\\b"`},
		{"space", "a b", `"a b"`},
		{"tab", "a\tb", "\"a\tb\""},
		{"all", `;="\ `, `";=\"\\ "`},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Value(tc.in); got != tc.want {
				t.Fatalf("Value(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if (tc.want != tc.in) != NeedsQuote(tc.in) {
				t.Fatalf("NeedsQuote(%q) mismatch", tc.in)
			}
		})
	}
}

// parseLine is an independent minimal parser of a size line (no CRLF).
func parseLine(s string) (int64, map[string]string, error) {
	exts := map[string]string{}
	i := strings.IndexAny(s, ";")
	if i < 0 {
		i = len(s)
	}
	size, err := strconv.ParseInt(s[:i], 16, 64)
	if err != nil {
		return 0, nil, err
	}
	for i < len(s) {
		i++ // skip ';'
		eq := strings.IndexByte(s[i:], '=') + i
		key := s[i:eq]
		j := eq + 1
		var val string
		if j < len(s) && s[j] == '"' {
			j++
			var b strings.Builder
			for s[j] != '"' {
				if s[j] == '\\' {
					j++
				}
				b.WriteByte(s[j])
				j++
			}
			j++
			val = b.String()
		} else {
			k := strings.IndexByte(s[j:], ';')
			if k < 0 {
				k = len(s) - j
			}
			val = s[j : j+k]
			j += k
		}
		exts[key] = val
		i = j
	}
	return size, exts, nil
}

func TestLineRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		size int64
		exts []Ext
	}{
		{"none", 10, nil},
		{"plain", 20, []Ext{{Key: "k", Value: "v"}}},
		{"tricky", 7, []Ext{{Key: "note", Value: `a;b=c"d\e;f`}}},
		{"many", 256, []Ext{{Key: "a", Value: "1"}, {Key: "b", Value: "x;y=z"}}},
		{"empty-val", 1, []Ext{{Key: "k", Value: ""}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			line := string(Line(tc.size, tc.exts))
			if !strings.HasSuffix(line, "\r\n") {
				t.Fatal("missing CRLF")
			}
			gotSize, gotExts, err := parseLine(strings.TrimSuffix(line, "\r\n"))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if gotSize != tc.size || len(gotExts) != len(tc.exts) {
				t.Fatalf("size=%d exts=%v", gotSize, gotExts)
			}
			for _, e := range tc.exts {
				if gotExts[e.Key] != e.Value {
					t.Fatalf("ext %q = %q, want %q", e.Key, gotExts[e.Key], e.Value)
				}
			}
		})
	}
}
