package filename_test

import (
	"errors"
	"reflect"
	"testing"

	"ontology/filename"
	"ontology/paramjoin"
	"ontology/paramlex"
)

func join(t *testing.T, h string) ([]paramjoin.Param, error) {
	t.Helper()
	_, items, err := paramlex.Parse(h)
	if err != nil {
		t.Fatalf("lex %q: %v", h, err)
	}
	return paramjoin.Join(items)
}

func TestLex(t *testing.T) {
	good := []struct {
		header, typ string
		items       []paramlex.Item
	}{
		{`attachment; filename="a b;c.txt"`, "attachment", []paramlex.Item{{Name: "filename", Section: -1, Value: "a b;c.txt"}}},
		{`INLINE; Filename*=utf-8''x`, "inline", []paramlex.Item{{Name: "filename", Section: -1, Ext: true, Value: "utf-8''x"}}},
		{`a; f*0*=x; f*1=y`, "a", []paramlex.Item{{Name: "f", Section: 0, Ext: true, Value: "x"}, {Name: "f", Section: 1, Value: "y"}}},
		{`a; f="x\"y"`, "a", []paramlex.Item{{Name: "f", Section: -1, Value: `x"y`}}},
		{`attachment`, "attachment", nil},
	}
	for _, c := range good {
		typ, items, err := paramlex.Parse(c.header)
		if err != nil || typ != c.typ || !reflect.DeepEqual(items, c.items) {
			t.Errorf("Parse(%q) = %q, %v, %v", c.header, typ, items, err)
		}
	}
	bad := []string{
		`; f=x`, `a; f`, `a; =x`, `a; f="x`, `a; f=x y`, `a; f=x=y`,
		`a; f="x" junk`, `a; f=x; f=y`, `a; f*0=x; f*0=y`, `a; f*-=x`, `a; f*1*=x;`,
	}
	for _, h := range bad {
		if _, _, err := paramlex.Parse(h); !errors.Is(err, paramlex.ErrSyntax) {
			t.Errorf("Parse(%q): want ErrSyntax, got %v", h, err)
		}
	}
}

func TestJoin(t *testing.T) {
	good := []struct {
		header string
		want   []paramjoin.Param
	}{
		{`a; filename*=utf-8''%E4%B8%AD%E6%96%87.txt`, []paramjoin.Param{{Name: "filename", Value: "中文.txt", Ext: true}}},
		{`a; n*0*=utf-8''hel; n*1=lo-; n*2=world`, []paramjoin.Param{{Name: "n", Value: "hello-world", Ext: true}}},
		{`a; n*0=x; n*1=y`, []paramjoin.Param{{Name: "n", Value: "xy"}}},
		{`a; f*=ISO-8859-1''%E9`, []paramjoin.Param{{Name: "f", Value: "é", Ext: true}}},
		{`a; f=x; f*=utf-8''y`, []paramjoin.Param{{Name: "f", Value: "x"}, {Name: "f", Value: "y", Ext: true}}},
	}
	for _, c := range good {
		if got, err := join(t, c.header); err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("Join(%q) = %v, %v", c.header, got, err)
		}
	}
	bad := []struct {
		header   string
		sentinel error
	}{
		{`a; n*0=x; n*2=z`, paramjoin.ErrIncomplete},
		{`a; n*1=x`, paramjoin.ErrIncomplete},
		{`a; f*=gbk''%41`, paramjoin.ErrCharset},
		{`a; f*=utf-8''%ff`, paramjoin.ErrEncoding},
		{`a; f*=utf-8''%4`, paramlex.ErrSyntax},
		{`a; f*=utf-8''%zz`, paramlex.ErrSyntax},
		{`a; f*=noquotes`, paramlex.ErrSyntax},
		{`a; n*0=x; n*0*=y`, paramlex.ErrSyntax},
	}
	for _, c := range bad {
		if _, err := join(t, c.header); !errors.Is(err, c.sentinel) {
			t.Errorf("Join(%q): want %v, got %v", c.header, c.sentinel, err)
		}
	}
	var inc *paramjoin.IncompleteError
	if _, err := join(t, `a; n*0=x; n*2=z`); !errors.As(err, &inc) || inc.Missing != 1 {
		t.Errorf("missing-section detail: %v", err)
	}
	var cs *paramjoin.CharsetError
	if _, err := join(t, `a; f*=gbk''%41`); !errors.As(err, &cs) || cs.Charset != "gbk" {
		t.Errorf("charset detail: %v", err)
	}
}

func TestName(t *testing.T) {
	cases := []struct{ header, want string }{
		{`attachment; filename="a b;c.txt"`, "a b;c.txt"},
		{`attachment; filename="plain.txt"; filename*=utf-8''star.txt`, "star.txt"},
		{`attachment; filename="../../etc/passwd"`, ".etcpasswd"},
		{`attachment; filename=".."`, filename.Fallback},
		{`attachment; filename="%2e%2e"`, filename.Fallback},
		{`attachment; filename="  pad  "`, "pad"},
		{`attachment; filename="a\\b"`, "ab"},
		{"attachment; filename=\"a\x01b\"", "ab"},
		{`attachment; filename="a..b"`, "a.b"},
		{`attachment; filename*=iso-8859-1''%E9.txt`, "é.txt"},
		{`attachment`, filename.Fallback},
	}
	for _, c := range cases {
		if got, err := filename.FromHeader(c.header); err != nil || got != c.want {
			t.Errorf("FromHeader(%q) = %q, %v; want %q", c.header, got, err, c.want)
		}
	}
	_, err := filename.FromHeader(`attachment; filename="a"; filename="b"`)
	if !errors.Is(err, paramlex.ErrSyntax) {
		t.Errorf("duplicate filename: want ErrSyntax, got %v", err)
	}
}
