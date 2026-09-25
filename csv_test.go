package csv_test

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"ontology/cell"
	"ontology/lexer"
	"ontology/par"
	"ontology/table"
	"ontology/writer"
)

func cells(vals ...string) []cell.Cell {
	out := make([]cell.Cell, len(vals))
	for i, v := range vals {
		out[i] = cell.Cell{Value: v}
	}
	return out
}

// 逐段喂入，切分点由 cuts 指定。
func feedCuts(t *testing.T, in string, cuts []int) (table.Table, error) {
	t.Helper()
	b := table.New(table.Limits{})
	prev := 0
	for _, c := range cuts {
		if err := b.Feed([]byte(in[prev:c])); err != nil {
			return b.Table(), err
		}
		prev = c
	}
	if err := b.Feed([]byte(in[prev:])); err != nil {
		return b.Table(), err
	}
	return b.Table(), b.Close()
}

func sameTable(a, b table.Table) bool {
	same := func(x, y []cell.Cell) bool {
		if len(x) != len(y) {
			return false
		}
		for i := range x {
			if !x[i].Equal(y[i]) {
				return false
			}
		}
		return true
	}
	if !same(a.Header, b.Header) || len(a.Rows) != len(b.Rows) {
		return false
	}
	for i := range a.Rows {
		if !same(a.Rows[i], b.Rows[i]) {
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
		{"basic", "a,b\nc,d\n", [][]string{{"a", "b"}, {"c", "d"}}},
		{"comma in quote", "a,\"x,y\"\n", [][]string{{"a", "x,y"}}},
		{"lf in quote", "\"a\nb\"\n", [][]string{{"a\nb"}}},
		{"crlf in quote preserved", "\"a\r\nb\"\n", [][]string{{"a\r\nb"}}},
		{"crlf record end", "a\r\nb\r\n", [][]string{{"a"}, {"b"}}},
		{"escaped quote", `"a""b"` + "\n", [][]string{{"a\"b"}}},
		{"no trailing newline", "a,b", [][]string{{"a", "b"}}},
		{"empty unquoted", "a,,b\n", [][]string{{"a", "", "b"}}},
		{"empty quoted", "a,\"\",b\n", [][]string{{"a", "", "b"}}},
		{"blank lines skipped", "\na,b\n\nc,d\n\n", [][]string{{"a", "b"}, {"c", "d"}}},
		{"single col quoted empty", "\"\"\n", [][]string{{""}}},
		{"single col empty line skipped", "\n\n", [][]string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tab, err := table.Parse([]byte(tc.in), table.Limits{})
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			all := append([][]string{}, vals(tab.Header))
			for _, r := range tab.Rows {
				all = append(all, vals(r))
			}
			if fmt.Sprint(all) != fmt.Sprint(tc.want) {
				t.Fatalf("got %v want %v", all, tc.want)
			}
			// 引号标记区分：未引号空 vs 加引号空。
			if tc.name == "empty quoted" && !tab.Header[1].Quoted {
				t.Fatal("quoted empty not marked")
			}
			if tc.name == "empty unquoted" && tab.Header[1].Quoted {
				t.Fatal("unquoted empty wrongly marked")
			}
		})
	}
}

func vals(cs []cell.Cell) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Value
	}
	return out
}

func TestQuotedEmptyPreservedRoundtrip(t *testing.T) {
	in := "a,\"\",b\n"
	tab, err := table.Parse([]byte(in), table.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	out := writer.Write(tab)
	if string(out) != in {
		t.Fatalf("roundtrip %q != %q", out, in)
	}
}

func TestSyntaxErrors(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want error
		off  int
		rec  int
		fld  int
	}{
		{"bare quote", "ab\"c\n", lexer.ErrBareQuote, 2, 1, 1},
		{"after close", "\"ab\"c\n", lexer.ErrQuoteAfterClose, 4, 1, 1},
		{"unclosed", "\"abc\n", lexer.ErrUnclosedQuote, 6, 1, 1},
		{"ragged", "a,b\nc\n", table.ErrRagged, 4, 2, 1},
		{"orphan cr unquoted", "ab\rc", lexer.ErrOrphanCR, 3, 1, 1},
		{"orphan cr eof", "ab\r", lexer.ErrOrphanCR, 2, 1, 1},
		{"orphan cr blank line", "a,b\n\rc", lexer.ErrOrphanCR, 5, 2, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := table.Parse([]byte(tc.in), table.Limits{})
			if err == nil || !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
			var pe *lexer.PosError
			if !errors.As(err, &pe) {
				t.Fatal("not PosError")
			}
			if pe.Offset != tc.off || pe.Record != tc.rec || pe.Field != tc.fld {
				t.Fatalf("pos=%+v want off=%d rec=%d fld=%d", pe, tc.off, tc.rec, tc.fld)
			}
		})
	}
}

func TestCRInsideQuoteIsContent(t *testing.T) {
	tab, err := table.Parse([]byte("\"a\rb\"\n"), table.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if tab.Header[0].Value != "a\rb" {
		t.Fatalf("got %q", tab.Header[0].Value)
	}
}
