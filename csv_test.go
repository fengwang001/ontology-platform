package csv_test

import (
	"errors"
	"strings"
	"testing"

	"ontology/cell"
	"ontology/lexer"
	"ontology/table"
	"ontology/writer"
)

type wantCell struct {
	v      string
	quoted bool
}

type parseCase struct {
	name string
	in   string
	recs [][]wantCell
}

func TestParseCases(t *testing.T) {
	cases := []parseCase{
		{"simple", "a,b,c\n", [][]wantCell{{{"a", false}, {"b", false}, {"c", false}}}},
		{"no trailing nl", "a,b", [][]wantCell{{{"a", false}, {"b", false}}}},
		{"crlf", "a,b\r\n", [][]wantCell{{{"a", false}, {"b", false}}}},
		{"quoted empty vs bare empty", `,"",a`+"\n", [][]wantCell{{{"", false}, {"", true}, {"a", false}}}},
		{"embedded comma", `"a,b",c` + "\n", [][]wantCell{{{"a,b", true}, {"c", false}}}},
		{"embedded nl and crlf", "\"x\ny\r\nz\"" + "\n", [][]wantCell{{{"x\ny\r\nz", true}}}},
		{"escaped quote", `"a""b"` + "\n", [][]wantCell{{{`a"b`, true}}}},
		{"blank line skipped", "\n\na\n", [][]wantCell{{{"a", false}}}},
		{"two records", "a\nb\n", [][]wantCell{{{"a", false}}, {{"b", false}}}},
		{"only newline", "\n", [][]wantCell{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tab, err := table.Parse([]byte(tc.in), table.Options{})
			if err != nil {
				t.Fatalf("unexpected err %v", err)
			}
			if len(tab.Records) != len(tc.recs) {
				t.Fatalf("records got %d want %d", len(tab.Records), len(tc.recs))
			}
			for ri, want := range tc.recs {
				got := tab.Records[ri]
				if len(got) != len(want) {
					t.Fatalf("r%d fields got %d want %d", ri, len(got), len(want))
				}
				for fi, wc := range want {
					if got[fi].Value != wc.v || got[fi].Quoted != wc.quoted {
						t.Fatalf("r%df%d got (%q,%v) want (%q,%v)", ri, fi, got[fi].Value, got[fi].Quoted, wc.v, wc.quoted)
					}
				}
			}
		})
	}
}

func TestStreamChunkedIdentical(t *testing.T) {
	inputs := []string{
		"a,b\r\n\"c\r\nd\",e\n\"q\"\"q\"",
		"\n\r\n\"x\"\r",
		"",
		",\"\",,\n",
	}
	for _, in := range inputs {
		ref, refErr := table.Parse([]byte(in), table.Options{})
		for size := 1; size <= len(in)+1; size++ {
			p := table.NewParser(table.Options{})
			var ferr error
			for i := 0; i < len(in); i += size {
				end := i + size
				if end > len(in) {
					end = len(in)
				}
				if e := p.Feed([]byte(in[i:end])); e != nil {
					ferr = e
					break
				}
			}
			got, gerr := p.Close()
			if ferr != nil {
				gerr = ferr
			}
			if (gerr == nil) != (refErr == nil) || errKey(gerr) != errKey(refErr) {
				t.Fatalf("in=%q size=%d err mismatch got %v want %v", in, size, gerr, refErr)
			}
			if gerr == nil && !tablesEqual(ref, got) {
				t.Fatalf("in=%q size=%d mismatch", in, size)
			}
		}
	}
}

func errKey(e error) string {
	if e == nil {
		return ""
	}
	for _, s := range []error{lexer.ErrBareQuote, lexer.ErrQuoteJunk, lexer.ErrUnclosedQuote, lexer.ErrLoneCR, table.ErrColumnCount, lexer.ErrLimited, lexer.ErrTerminal} {
		if errors.Is(e, s) {
			return s.Error()
		}
	}
	return e.Error()
}

func cellsEqual(a, b []cell.Cell) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !a[i].Equal(b[i]) || a[i].Start != b[i].Start || a[i].End != b[i].End {
			return false
		}
	}
	return true
}

func tablesEqual(a, b *table.Table) bool {
	if len(a.Records) != len(b.Records) {
		return false
	}
	for i := range a.Records {
		if !cellsEqual(a.Records[i], b.Records[i]) {
			return false
		}
	}
	return true
}

type errCase struct {
	in     string
	want   error
	offset int
	record int
	field  int
}

func TestSyntaxErrors(t *testing.T) {
	cases := []errCase{
		{`a"b`, lexer.ErrBareQuote, 1, 1, 1},
		{`"ab"c`, lexer.ErrQuoteJunk, 4, 1, 1},
		{`"abc`, lexer.ErrUnclosedQuote, 4, 1, 1},
		{"ab\rc", lexer.ErrLoneCR, 2, 1, 1},
		{`"ab"\r`, lexer.ErrQuoteJunk, 4, 1, 1},
		{"a,b\nc", table.ErrColumnCount, 4, 2, 1},
		{"a,b\nc\n", table.ErrColumnCount, 3, 2, 1},
		{"x\r", lexer.ErrLoneCR, 1, 1, 1},
	}
	for _, tc := range cases {
		t.Run(tc.want.Error()+tc.in, func(t *testing.T) {
			_, err := table.Parse([]byte(strings.ReplaceAll(tc.in, `\r`, "\r")), table.Options{})
			var le *lexer.Error
			if !errors.As(err, &le) || !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
			if le.Offset != tc.offset || le.Record != tc.record || le.Field != tc.field {
				t.Fatalf("pos got (%d,%d,%d) want (%d,%d,%d)", le.Offset, le.Record, le.Field, tc.offset, tc.record, tc.field)
			}
		})
	}
}

func TestWriterRoundTrip(t *testing.T) {
	canonical := []string{
		"a,b\n", "\"a,b\",\"x\"\"y\"\n", "\"x\ny\r\nz\"\n", "single\n", ",,\n", "\"\"\n",
	}
	for _, in := range canonical {
		tab, err := table.Parse([]byte(in), table.Options{})
		if err != nil {
			t.Fatalf("parse %q: %v", in, err)
		}
		out := string(writer.Write(tab.Records))
		if out != in {
			t.Fatalf("canonical byte roundtrip got %q want %q", out, in)
		}
}
	// single-column bare empty cell round-trips semantically, becomes quoted.
	tab2, _ := table.Parse([]byte{'"', '"', '\n'}, table.Options{})
	if string(writer.Write([][]cell.Cell{{{Value: "", Quoted: false}}})) != "\"\"\n" {
		t.Fatal("single bare empty must be quoted")
	}
	_ = tab2
}
