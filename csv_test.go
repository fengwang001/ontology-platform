package ontology_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"ontology/lexer"
	"ontology/table"
	"ontology/writer"
)

func parse(t *testing.T, in string, lim ...table.Limits) (*table.Table, error) {
	t.Helper()
	l := table.Limits{}
	if len(lim) > 0 {
		l = lim[0]
	}
	return table.Parse([]byte(in), l)
}

func eqRows(a, b *table.Table) bool {
	if len(a.Rows) != len(b.Rows) {
		return false
	}
	for i := range a.Rows {
		if !a.Rows[i].Equal(b.Rows[i]) {
			return false
		}
	}
	return true
}

func TestDialect(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want [][]string
	}{
		{"comma", `a,b,c` + "\n", [][]string{{"a", "b", "c"}}},
		{"comma-in-quote", `"a,b"` + "\n", [][]string{{"a,b"}}},
		{"lf-in-quote", "\"a\nb\"\n", [][]string{{"a\nb"}}},
		{"crlf-in-quote", "\"a\r\nb\"\n", [][]string{{"a\r\nb"}}},
		{"escaped-quote", `"a""b"` + "\n", [][]string{{`a"b`}}},
		{"quoted-empty", `""` + "\n", [][]string{{""}}},
		{"empty-fields", `,a,` + "\n", [][]string{{"", "a", ""}}},
		{"no-final-nl", "a,b", [][]string{{"a", "b"}}},
		{"blank-line-skipped", "a\n\nb\n", [][]string{{"a"}, {"b"}}},
		{"only-newline", "\n", nil},
		{"empty", "", nil},
		{"crlf-record", "a\r\nb\r\n", [][]string{{"a"}, {"b"}}},
		{"multi-col-tail-comma", "a,\n", [][]string{{"a", ""}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tab, err := parse(t, tc.in)
			if err != nil {
				t.Fatalf("err %v", err)
			}
			if len(tab.Rows) != len(tc.want) {
				t.Fatalf("rows=%d want=%d (%v)", len(tab.Rows), len(tc.want), tab.Rows)
			}
			for i, w := range tc.want {
				if len(tab.Rows[i]) != len(w) {
					t.Fatalf("row %d cols %d want %d", i, len(tab.Rows[i]), len(w))
				}
				for j := range w {
					if tab.Rows[i][j].Value != w[j] {
						t.Fatalf("r%dc%d=%q want %q", i, j, tab.Rows[i][j].Value, w[j])
					}
				}
			}
		})
	}
}

func TestQuotedEmptyDistinctionAndOffsets(t *testing.T) {
	tab, err := parse(t, `""`+"\n")
	if err != nil {
		t.Fatal(err)
	}
	c := tab.Rows[0][0]
	if c.Quoted != true || c.Start != 0 || c.End != 2 {
		t.Fatalf("quoted empty %+v", c)
	}
	tab2, _ := parse(t, "a,\n")
	c2 := tab2.Rows[0][1]
	if c2.Quoted || c2.Start != 2 || c2.End != 2 {
		t.Fatalf("bare empty %+v", c2)
	}
}

func TestSyntaxErrors(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		want   error
		off    int
		rec    int
		fld    int
	}{
		{"bare-quote", `a"b` + "\n", lexer.ErrBareQuote, 1, 1, 1},
		{"junk-after-quote", `"ab"c` + "\n", lexer.ErrQuoteJunk, 3, 1, 1},
		{"unterminated", `"ab`, lexer.ErrUnterminated, 3, 1, 1},
		{"lone-cr", "a\rb", lexer.ErrLoneCarriage, 1, 1, 1},
		{"lone-cr-eof", "a\r", lexer.ErrLoneCarriage, 1, 1, 1},
		{"bare-quote-2nd", "x,y\na,\"b\n", lexer.ErrBareQuote, 7, 2, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parse(t, tc.in)
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v got %v", tc.want, err)
			}
			var le *lexer.Error
			var te *table.Error
			var off, rec, fld int
			if errors.As(err, &le) {
				off, rec, fld = le.Offset, le.Record, le.Field
			} else if errors.As(err, &te) {
				off, rec, fld = te.Offset, te.Record, te.Field
			}
			if off != tc.off || rec != tc.rec || fld != tc.fld {
				t.Fatalf("pos (%d,%d,%d) want (%d,%d,%d)", off, rec, fld, tc.off, tc.rec, tc.fld)
			}
		})
	}
}

func TestColumnCount(t *testing.T) {
	_, err := parse(t, "a,b\nc\n")
	if !errors.Is(err, table.ErrColumnCount) {
		t.Fatalf("got %v", err)
	}
}

func chunkParse(in string, size int) (*table.Table, error) {
	p := table.NewParser(table.Limits{})
	for i := 0; i < len(in); i += size {
		e := i + size
		if e > len(in) {
			e = len(in)
		}
		if err := p.Feed([]byte(in[i:e])); err != nil {
			return p.Table(), err
		}
	}
	err := p.Close()
	return p.Table(), err
}

func TestChunkedIdentical(t *testing.T) {
	ins := []string{
		"a,b\n\"c,d\",e\n\"x\"\"y\"\r\nz\r\n",
		"\"line1\r\nline2\",\"quo\"\"te\"\n,,\nlast",
		"\n\n\r\na\r\n",
	}
	for _, in := range ins {
		ref, rerr := parse(t, in)
		for size := 1; size <= len(in)+1; size++ {
			got, gerr := chunkParse(in, size)
			if fmt.Sprint(gerr) != fmt.Sprint(rerr) || (gerr == nil && !eqRows(ref, got)) {
				t.Fatalf("size %d mismatch: %v vs %v", size, gerr, rerr)
			}
		}
	}
}

func TestRoundtrip(t *testing.T) {
	ins := []string{
		"a,b\n\"c,d\",\"x\"\"y\"\n\"a\r\nb\",\"l\nn\"\n",
		"\"\"\n\n\"v\"\n",
		"a,\n,b\n,\n",
	}
	for _, in := range ins {
		tab, err := parse(t, in)
		if err != nil {
			t.Fatal(err)
		}
		out := string(writer.Write(tab.Rows))
		tab2, err := parse(t, out)
		if err != nil {
			t.Fatalf("reparse %q: %v", out, err)
		}
		if !eqRows(tab, tab2) {
			t.Fatalf("roundtrip mismatch\n%s\n%s", in, out)
		}
		for i := range tab2.Rows {
			for j := range tab2.Rows[i] {
				if tab.Rows[i][j].Quoted != tab2.Rows[i][j].Quoted {
					t.Fatalf("quote flag lost r%dc%d", i, j)
				}
			}
		}
	}
}

func TestSingleColumnEmptyRoundtrip(t *testing.T) {
	in := "\"\"\n"
	tab, _ := parse(t, in)
	if strings.TrimSpace(string(writer.Write(tab.Rows))) != `""` {
		t.Fatalf("single empty must be quoted: %q", writer.Write(tab.Rows))
	}
}

func TestLimitsTerminal(t *testing.T) {
	cases := []struct {
		name string
		lim  table.Limits
		in   string
		want error
	}{
		{"field-bytes", table.Limits{MaxFieldBytes: 2}, `"abc"` + "\n", table.ErrFieldTooLarge},
		{"fields", table.Limits{MaxFields: 2}, "a,b,c\n", table.ErrTooManyFields},
		{"records", table.Limits{MaxRecords: 1}, "a\nb\n", table.ErrTooManyRows},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := table.NewParser(tc.lim)
			err := p.Feed([]byte(tc.in))
			if err == nil {
				err = p.Close()
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v", err)
			}
			if e := p.Feed([]byte("x")); !errors.Is(e, tc.want) {
				t.Fatalf("after terminal: %v", e)
			}
			if len(p.Table().Rows) != 0 {
				t.Fatalf("no complete row should remain, got %d", len(p.Table().Rows))
			}
		})
	}
}
