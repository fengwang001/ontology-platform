package filename_test

import (
	"errors"
	"testing"

	"ontology/filename"
	"ontology/paramjoin"
	"ontology/paramlex"
)

func run(h string) (string, []paramjoin.Param, error) {
	typ, items, err := paramlex.Parse(h)
	if err != nil {
		return "", nil, err
	}
	ps, err := paramjoin.Join(items)
	return typ, ps, err
}

func TestLex(t *testing.T) {
	good := []struct {
		in, typ string
		raws    []string
	}{
		{"attachment", "attachment", nil},
		{"ATTACHMENT; FileName=a.txt", "attachment", []string{"a.txt"}},
		{`inline; filename="a;b=c \"d\".txt"`, "inline", []string{`a;b=c "d".txt`}},
		{"attachment; name*0*=x; name*1=y", "attachment", []string{"x", "y"}},
	}
	for _, c := range good {
		typ, items, err := paramlex.Parse(c.in)
		if err != nil || typ != c.typ || len(items) != len(c.raws) {
			t.Fatalf("Parse(%q) = %q, %v, %v", c.in, typ, items, err)
		}
		for i, raw := range c.raws {
			if items[i].Raw != raw {
				t.Errorf("Parse(%q) item %d raw = %q, want %q", c.in, i, items[i].Raw, raw)
			}
		}
	}
	bad := []string{
		"", "; filename=a", "attachment; filename", "attachment; =a",
		`attachment; filename="open`, "attachment; filename=a b",
		"attachment; filename=a; filename=b", "attachment; a==b",
		"attachment; name**=x", "attachment; name*x=y",
	}
	for _, in := range bad {
		if _, _, err := paramlex.Parse(in); !errors.Is(err, paramlex.ErrSyntax) {
			t.Errorf("Parse(%q) err = %v, want ErrSyntax", in, err)
		}
	}
}

func TestJoin(t *testing.T) {
	good := []struct{ in, want string }{
		{`a; filename*=utf-8''%E4%B8%AD.txt`, "中.txt"},
		{`a; filename*=UTF-8''%41`, "A"},
		{`a; filename*=iso-8859-1''%E9`, "é"},
		{`a; filename*0="ab"; filename*1="cd"`, "abcd"},
		{`a; filename*0*=utf-8''x; filename*1='y`, "x'y"},
	}
	for _, c := range good {
		_, ps, err := run(c.in)
		if err != nil || len(ps) != 1 || ps[0].Value != c.want {
			t.Errorf("join %q = %v, %v; want %q", c.in, ps, err, c.want)
		}
	}
	bad := []struct {
		in  string
		err error
	}{
		{`a; filename*0=x; filename*2=z`, paramjoin.ErrIncomplete},
		{`a; filename*1=x`, paramjoin.ErrIncomplete},
		{`a; filename*=gbk''%41`, paramjoin.ErrCharset},
		{`a; filename*=utf-8''%ff`, paramjoin.ErrEncoding},
		{`a; filename*=utf-8''%4`, paramlex.ErrSyntax},
		{`a; filename*=noquotes`, paramlex.ErrSyntax},
	}
	for _, c := range bad {
		if _, _, err := run(c.in); !errors.Is(err, c.err) {
			t.Errorf("join %q err = %v, want %v", c.in, err, c.err)
		}
	}
	var inc *paramjoin.IncompleteError
	var ce *paramjoin.CharsetError
	if _, _, err := run(`a; filename*0=x; filename*2=z`); !errors.As(err, &inc) || inc.Missing != 1 {
		t.Errorf("missing segment = %v", err)
	}
	if _, _, err := run(`a; filename*=GBK''%41`); !errors.As(err, &ce) || ce.Charset != "GBK" {
		t.Errorf("charset = %v", err)
	}
}

func TestDecide(t *testing.T) {
	cases := []struct{ in, want string }{
		{`attachment`, filename.Fallback},
		{`attachment; filename="plain.txt"; filename*=utf-8''fancy.txt`, "fancy.txt"},
		{`attachment; filename="  a.txt  "`, "a.txt"},
		{`attachment; filename="."`, filename.Fallback},
		{`attachment; filename=".."`, filename.Fallback},
		{`attachment; filename="%2e%2e"`, filename.Fallback},
		{`attachment; filename="a/b\\c"`, "a%2Fb%5Cc"},
		{`attachment; filename="a..b"`, "a%2E%2Eb"},
		{"attachment; filename=\"a\tb\"", "a%09b"},
		{`attachment; filename="a b.txt"`, "a b.txt"},
		{`attachment; filename="../../etc/passwd"`, "%2E%2E%2F%2E%2E%2Fetc%2Fpasswd"},
	}
	for _, c := range cases {
		_, ps, err := run(c.in)
		if err != nil {
			t.Fatalf("run %q: %v", c.in, err)
		}
		if got := filename.Decide("attachment", ps); got != c.want {
			t.Errorf("Decide %q = %q, want %q", c.in, got, c.want)
		}
	}
}
