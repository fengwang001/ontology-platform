package ontology

import (
	"errors"
	"fmt"
	"testing"

	"ontology/cell"
	"ontology/par"
	"ontology/table"
	"ontology/writer"
)

func mustParse(t *testing.T, src string) *table.Table {
	t.Helper()
	tab, err := table.Parse([]byte(src), cell.Limits{})
	if err != nil {
		t.Fatalf("parse %q: %v", src, err)
	}
	return tab
}

func flatten(tab *table.Table) []cell.Cell {
	var out []cell.Cell
	out = append(out, tab.Header...)
	for _, r := range tab.Records {
		out = append(out, r...)
	}
	return out
}

func TestBasicDialect(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want [][2]string // value, quoted(""=true)
	}{
		{"simple", "a,b\nc,d\n", [][2]string{{"a", ""}, {"b", ""}, {"c", ""}, {"d", ""}}},
		{"noTrailingNL", "a,b\nc,d", [][2]string{{"a", ""}, {"b", ""}, {"c", ""}, {"d", ""}}},
		{"quotedComma", `"a,b"\n`, [][2]string{{"a,b", "q"}}},
		{"quotedNL", `"a\nb"\n`, [][2]string{{"a\nb", "q"}}},
		{"quotedCRLF", "\"a\r\nb\"\n", [][2]string{{"a\r\nb", "q"}}},
		{"escapedQuote", `"a""b"\n`, [][2]string{{`a"b`, "q"}}},
		{"emptyBare", "a,,b\n", [][2]string{{"a", ""}, {"", ""}, {"b", ""}}},
		{"emptyQuoted", `"",""\n`, [][2]string{{"", "q"}, {"", "q"}}},
		{"crlfRecord", "a,b\r\nc,d\r\n", [][2]string{{"a", ""}, {"b", ""}, {"c", ""}, {"d", ""}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tab := mustParse(t, tc.src)
			got := flatten(tab)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d cells %v, want %d", len(got), got, len(tc.want))
			}
			for i, w := range tc.want {
				if got[i].Value != w[0] || got[i].Quoted != (w[1] == "q") {
					t.Fatalf("cell %d: got (%q,q=%v) want (%q,q=%v)", i, got[i].Value, got[i].Quoted, w[0], w[1] == "q")
				}
			}
		})
	}
}

func TestOffsets(t *testing.T) {
	tab := mustParse(t, `ab,"cd"`)
	cs := flatten(tab)
	want := [][2]int{{0, 2}, {3, 7}}
	for i, w := range want {
			if cs[i].Start != w[0] || cs[i].End != w[1] {
				t.Fatalf("cell %d offset got (%d,%d) want (%d,%d)", i, cs[i].Start, cs[i].End, w[0], w[1])
			}
		}
	tab2 := mustParse(t, "a,\n")
	c2 := flatten(tab2)
	if c2[1].Start != 2 || c2[1].End != 2 {
		t.Fatalf("bare empty offset got (%d,%d) want (2,2)", c2[1].Start, c2[1].End)
	}
}

func TestSyntaxErrors(t *testing.T) {
	cases := []struct {
		src    string
		want   error
		off    int
		record int
		field  int
	}{
		{`a"b`, cell.ErrQuoteInBare, 1, 1, 1},
		{`"ab"c`, cell.ErrGarbageAfterQuote, 4, 1, 1},
		{`"ab`, cell.ErrUnclosedQuote, 3, 1, 1},
		{"a\rb", cell.ErrLoneCR, 1, 1, 1},
		{"a\r", cell.ErrLoneCR, 1, 1, 1},
		{"a,b\nc\n", cell.ErrColumnCount, 3, 2, 1},
		{"a,b\n\"x\"y", cell.ErrGarbageAfterQuote, 7, 2, 1},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%q", tc.src), func(t *testing.T) {
			_, err := table.Parse([]byte(tc.src), cell.Limits{})
			pe := cell.AsPosError(err)
			if pe == nil || !errors.Is(err, tc.want) {
				t.Fatalf("err=%v want %v", err, tc.want)
			}
			if pe.Offset != tc.off || pe.Record != tc.record || pe.Field != tc.field {
				t.Fatalf("pos got (%d,%d,%d) want (%d,%d,%d)",
					pe.Offset, pe.Record, pe.Field, tc.off, tc.record, tc.field)
			}
		})
	}
}

func TestTrailingAndBlank(t *testing.T) {
	if len(mustParse(t, "").Header) != 0 {
		t.Fatal("empty input must yield 0 records")
	}
	if n := len(mustParse(t, "\n").Header); n != 0 {
		t.Fatalf("blank line must be skipped, got header len %d", n)
	}
	if n := len(mustParse(t, "a\n\n").Records); n != 0 {
		t.Fatalf("blank line after record skipped, got %d", n)
	}
	if n := len(mustParse(t, "\"\"\n").Header); n != 1 {
		t.Fatal("single-column quoted empty must be a record")
	}
}

func TestLimitsImmediate(t *testing.T) {
	cases := []struct {
		name string
		src  string
		lim  cell.Limits
		want error
	}{
		{"fieldBytes", "abcdef", cell.Limits{MaxFieldBytes: 3}, cell.ErrFieldTooLong},
		{"fields", "a,b,c", cell.Limits{MaxFields: 2}, cell.ErrTooManyFields},
		{"records", "a\nb\nc\n", cell.Limits{MaxRecords: 1}, cell.ErrTooManyRecords},
		{"insideQuote", `"abcdef"`, cell.Limits{MaxFieldBytes: 3}, cell.ErrFieldTooLong},
		{"escapedCounts1", `"a""b"`, cell.Limits{MaxFieldBytes: 3}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := table.Parse([]byte(tc.src), tc.lim)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("want ok got %v", err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
	// 终态后再写入返回同一错误
	p := table.NewFeedParser(cell.Limits{MaxFieldBytes: 2})
	e1 := p.Feed([]byte("abcd"))
	e2 := p.Feed([]byte("x"))
	if !errors.Is(e1, cell.ErrFieldTooLong) || !errors.Is(e2, cell.ErrTerminal) {
		t.Fatalf("terminal behavior: %v %v", e1, e2)
	}
}

func TestRoundtrip(t *testing.T) {
	// Parse(Write(T)) 与 T 逐字段相等且引号标记相同
	tab := mustParse(t, "a,\"b,c\"\n\"\"\n\"x\ny\",d\n")
	out := writer.Write(tab)
	tab2 := mustParse(t, string(out))
	a, b := flatten(tab), flatten(tab2)
	if len(a) != len(b) {
		t.Fatalf("cell count %d != %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("cell %d: %+v != %+v", i, a[i], b[i])
		}
	}
}

func TestCanonicalByteRoundtrip(t *testing.T) {
	// 规范输入：\n 结束每条记录、字段内 CRLF 原样。
	canon := []string{
		"",
		"a,b\n",
		"\"a,b\"\n",
		"\"a\r\nb\"\n",
		"\"\"\n",
		"a,\"b\"\"c\"\n",
	}
	for _, s := range canon {
		tab := mustParse(t, s)
		if got := string(writer.Write(tab)); got != s {
			t.Fatalf("canonical %q -> %q", s, got)
		}
	}
}

func TestSingleColEmptyRoundtrip(t *testing.T) {
	tab := mustParse(t, "\"\"\n")
	if got := string(writer.Write(tab)); got != "\"\"\n" {
		t.Fatalf("single-col empty must write quoted, got %q", got)
	}
	if _, err := table.Parse(writer.Write(tab), cell.Limits{}); err != nil {
		t.Fatal(err)
	}

	_ = par.Parse // keep import
}
