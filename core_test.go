package ontology_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"ontology/cell"
	"ontology/lexer"
	"ontology/table"
	"ontology/writer"
)

func TestDialectAndRoundTrip(t *testing.T) {
	cases := []struct {
		name, in, norm string
		values         [][]string
		quoted         [][]bool
	}{
		{"basic", "a,b\r\nc,d\n", "a,b\nc,d", [][]string{{"a", "b"}, {"c", "d"}}, nil},
		{"specials", "\"a,b\",b\r\n\"x\r\ny\",\"q\"\"z\"\n", "\"a,b\",b\n\"x\r\ny\",\"q\"\"z\"", [][]string{{"a,b", "b"}, {"x\r\ny", "q\"z"}}, nil},
		{"empty", "\"\",a\n,b\n", "\"\",a\n,b", [][]string{{"", "a"}, {"", "b"}}, [][]bool{{true, false}, {false, false}}},
		{"single", "\"\"\n\"\"\n", "\"\"\n\"\"", [][]string{{""}, {""}}, [][]bool{{true}, {true}}},
		{"blank", "a\n\nb\n", "a\nb", [][]string{{"a"}, {"b"}}, nil},
		{"no-newline", "a\nb", "a\nb", [][]string{{"a"}, {"b"}}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tab, err := table.Parse([]byte(tc.in), lexer.Limits{})
			if err != nil {
				t.Fatal(err)
			}
			assertCells(t, tab, tc.values, tc.quoted)
			if got := string(writer.Write(tab)); got != tc.norm {
				t.Fatalf("write=%q want %q", got, tc.norm)
			}
			tab2, err := table.Parse(writer.Write(tab), lexer.Limits{})
			if err != nil || !sameValues(tab, tab2) {
				t.Fatalf("round trip %#v %v", tab2, err)
			}
		})
	}
}

func TestErrorsAndPositions(t *testing.T) {
	cases := []struct {
		name, in, kind string
		off            int64
		rec, field     int
	}{
		{"bare", `a"`, lexer.KindBareQuote, 1, 1, 1},
		{"close", `"ab"c`, lexer.KindQuoteAfterClose, 4, 1, 1},
		{"unterminated", `"ab`, lexer.KindUnterminated, 0, 1, 1},
		{"orphan", "a\rb", lexer.KindOrphanCR, 1, 1, 1},
		{"columns", "a,b\nc\n", lexer.KindColumnCount, 4, 2, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := table.Parse([]byte(tc.in), lexer.Limits{})
			var e *lexer.Error
			if !errors.As(err, &e) || e.Kind != tc.kind || e.Offset != tc.off || e.Record != tc.rec || e.Field != tc.field {
				t.Fatalf("err=%#v", err)
			}
		})
	}
}

func TestStreamingSplits(t *testing.T) {
	in := []byte("a,\"x\"\"\r\ny\",c\r\n\"\"\n")
	_ = in
	in = []byte("a,\"x\"\"\r\ny\",c\r\n\"\"\n")
	want, err := table.Parse(in, lexer.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	for step := 1; step <= len(in); step++ {
		p := table.NewParser(lexer.Limits{})
		for at := 0; at < len(in); at += step {
			end := min(at+step, len(in))
			if err := p.Feed(in[at:end]); err != nil {
				t.Fatalf("step=%d at=%d: %v", step, at, err)
			}
		}
		if err := p.Close(); err != nil || !sameTable(want, p.Table()) {
			t.Fatalf("step=%d err=%v rows=%d", step, err, len(p.Table().Records))
		}
	}
}

func TestLimitsImmediate(t *testing.T) {
	cases := []struct {
		name, in, kind string
		lim            lexer.Limits
		keep           int
	}{
		{"bytes", "ok\nabc\n", lexer.KindFieldLimit, lexer.Limits{MaxFieldBytes: 2}, 1},
		{"fields", "a,b\n", lexer.KindFieldsLimit, lexer.Limits{MaxFields: 1}, 0},
		{"records", "a\nb\nc\n", lexer.KindRecordsLimit, lexer.Limits{MaxRecords: 2}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tab, err := table.Parse([]byte(tc.in), tc.lim)
			if err == nil || !errors.Is(err, &lexer.Error{Kind: tc.kind}) || len(tab.Header)+len(tab.Records) < tc.keep {
				t.Fatalf("%#v rows=%d", err, len(tab.Records))
			}
			p := table.NewParser(tc.lim)
			if err := p.Feed([]byte(tc.in[:1])); err == nil {
				if err := p.Feed(nil); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func assertCells(t *testing.T, tab table.Table, vals [][]string, quoted [][]bool) {
	t.Helper()
	rows := append([][]cell.Cell{tab.Header}, tab.Records...)
	if len(rows) != len(vals) {
		t.Fatalf("rows=%v want %v", cellsValues(rows), vals)
	}
	for ri := range vals {
		for fi, v := range vals[ri] {
			if rows[ri][fi].Value != v || quoted != nil && rows[ri][fi].Quoted != quoted[ri][fi] {
				t.Fatalf("cell=%#v want %q", rows[ri][fi], v)
			}
		}
	}
}

func sameTable(a, b table.Table) bool {
	return signature(a) == signature(b)
}

func sameValues(a, b table.Table) bool {
	return valueSignature(a) == valueSignature(b)
}

func signature(t table.Table) string {
	var b strings.Builder
	for _, row := range append([][]cell.Cell{t.Header}, t.Records...) {
		for _, c := range row {
			fmt.Fprintf(&b, "%q,%t,%d:%d;", c.Value, c.Quoted, c.Start, c.End)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func valueSignature(t table.Table) string {
	var b strings.Builder
	for _, row := range append([][]cell.Cell{t.Header}, t.Records...) {
		for _, c := range row {
			fmt.Fprintf(&b, "%q,%t;", c.Value, c.Quoted)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func cellsValues(rows [][]cell.Cell) [][]string {
	out := make([][]string, len(rows))
	for i, r := range rows {
		for _, c := range r {
			out[i] = append(out[i], c.Value)
		}
	}
	return out
}
