package table_test

import (
	"errors"
	"math/rand"
	"testing"

	"ontology/cell"
	"ontology/lexer"
	"ontology/par"
	"ontology/table"
	"ontology/writer"
)

var noLimit = lexer.Limits{}

func parseChunked(t *testing.T, in string, chunk int) (*table.Table, table.Stats, error) {
	t.Helper()
	p := table.NewParser(noLimit)
	for i := 0; i < len(in); i += chunk {
		end := i + chunk
		if end > len(in) {
			end = len(in)
		}
		_ = p.Feed([]byte(in[i:end]))
	}
	return p.Close()
}

func TestRecords(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want [][]cell.Cell
	}{
		{"simple", "a,b,c\n", [][]cell.Cell{{c("a", 0, 1), c("b", 2, 3), c("c", 4, 6)}}},
		{"no final lf", "x,y", [][]cell.Cell{{c("x", 0, 1), c("y", 2, 3)}}},
		{"crlf", "a,b\r\nc,d\r\n", [][]cell.Cell{
			{c("a", 0, 1), c("b", 2, 4)},
			{c("c", 5, 6), c("d", 7, 9)},
		}},
		{"quoted empty", "a,\"\"\n", [][]cell.Cell{{c("a", 0, 1), cq("", 2, 5)}}},
		{"bare empty", "a,\n", [][]cell.Cell{{c("a", 0, 1), c("", 2, 3)}}},
		{"leading blank line", "\na,b\n", [][]cell.Cell{{c("a", 1, 2), c("b", 3, 4)}}},
		{"double blank skipped", "a\n\nb\n", [][]cell.Cell{{c("a", 0, 2)}, {c("b", 3, 5)}}},
		{"empty quoted content preserves crlf", "\"a\r\nb,c\"", [][]cell.Cell{{cq("a\r\nb,c", 0, 8)}}},
		{"escaped quote", "\"a\"\"b\"", [][]cell.Cell{{cq("a\"b", 0, 6)}}},
		{"empty input", "", [][]cell.Cell{}},
		{"only newline", "\n", [][]cell.Cell{}},
		{"single quoted empty row", "\"\"\n", [][]cell.Cell{{cq("", 0, 3)}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tb, _, err := table.Parse([]byte(tc.in), noLimit)
			if err != nil {
				t.Fatalf("err %v", err)
			}
			assertRows(t, tb.Records(), tc.want)
		})
	}
}

func assertRows(t *testing.T, got, want [][]cell.Cell) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("rows %d want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if len(got[i]) != len(want[i]) {
			t.Fatalf("row %d cols %d want %d", i, len(got[i]), len(want[i]))
		}
		for j := range want[i] {
			if got[i][j] != want[i][j] {
				t.Fatalf("r%dc%d %+v want %+v", i, j, got[i][j], want[i][j])
			}
		}
	}
}

func c(v string, s, e int) cell.Cell { return cell.Cell{Value: v, Start: s, End: e} }
func cq(v string, s, e int) cell.Cell {
	return cell.Cell{Value: v, Quoted: true, Start: s, End: e}
}

func TestErrors(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		target error
		off    int
		rec    int
		field  int
	}{
		{"quote in bare", "ab\"c\n", lexer.ErrQuoteInBare, 2, 1, 1},
		{"chars after quote", "\"ab\"c\n", lexer.ErrCharsAfterQuote, 4, 1, 1},
		{"unclosed quote", "\"abc", lexer.ErrUnclosedQuote, 0, 1, 1},
		{"lone cr mid", "a\rb\n", lexer.ErrLoneCR, 1, 1, 1},
		{"lone cr eof", "ab\r", lexer.ErrLoneCR, 2, 1, 1},
		{"lone cr after quote", "\"a\"\rx", lexer.ErrLoneCR, 3, 1, 1},
		{"field count", "a,b\nc\n", table.ErrFieldCount, 6, 2, 1},
		{"cr inside quote ok", "\"a\rb\"\n", nil, 0, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := table.Parse([]byte(tc.in), noLimit)
			if tc.target == nil {
				if err != nil {
					t.Fatalf("want ok got %v", err)
				}
				return
			}
			e := table.AsError(err)
			if e == nil || !errors.Is(e, tc.target) {
				t.Fatalf("err %v want %v", err, tc.target)
			}
			if e.Offset != tc.off || e.Record != tc.rec || e.Field != tc.field {
				t.Fatalf("pos (%d,%d,%d) want (%d,%d,%d)", e.Offset, e.Record, e.Field, tc.off, tc.rec, tc.field)
			}
		})
	}
}

func TestLimits(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		lim    lexer.Limits
		target error
		kept   int
	}{
		{"field bytes bare", "ab,cd\n", lexer.Limits{MaxFieldBytes: 1}, lexer.ErrFieldTooLong, 0},
		{"field bytes quoted escape", "\"a\"\"\",x\n", lexer.Limits{MaxFieldBytes: 1}, lexer.ErrFieldTooLong, 0},
		{"fields per record", "a,b,c\n", lexer.Limits{MaxFieldsPerRecord: 2}, lexer.ErrTooManyFields, 0},
		{"records", "a\nb\nc\n", lexer.Limits{MaxRecords: 2}, table.ErrTooManyRecords, 2},
		{"kept prefix", "a\nb\nc\"x\n", lexer.Limits{}, lexer.ErrQuoteInBare, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tb, _, err := table.Parse([]byte(tc.in), tc.lim)
			if err == nil || !errors.Is(err, tc.target) {
				t.Fatalf("err %v want %v", err, tc.target)
			}
			if got := len(tb.Records()); got != tc.kept {
				t.Fatalf("kept %d want %d", got, tc.kept)
			}
			p := table.NewParser(tc.lim)
			e1 := p.Feed([]byte(tc.in))
			_, _, e2 := p.Close()
			if e1 != e2 && !errors.Is(e1, tc.target) {
				t.Fatalf("terminal state: %v %v", e1, e2)
			}
		})
	}
}

func TestChunkingIdentical(t *testing.T) {
	inputs := []string{
		"abc,def\r\nghi,\"j,k\"\r\n\"a\"\"b\",\"x\ry\"\n,,\"\"\nz",
		"\"split\r\nme\",\"\"," + "1\r\n\r\n",
		",\r\n,,\n\"unclosed",
	}
	for _, in := range inputs {
		ref, _, refErr := table.Parse([]byte(in), noLimit)
		for chunk := 1; chunk <= len(in); chunk++ {
			tb, n, err := parseChunked(t, in, chunk)
			if !sameOutcome(ref, tb, refErr, err) {
				t.Fatalf("chunk=%d in=%q refErr=%v err=%v", chunk, in, refErr, err)
			}
			if n.BytesProcessed != int64(len(in)) {
				t.Fatalf("chunk=%d byte count %d want %d", chunk, n.BytesProcessed, len(in))
			}
		}
		// 1 字节一段
		// 随机切分
		rng := rand.New(rand.NewSource(7))
		for iter := 0; iter < 30; iter++ {
			tb2, _, err2 := parseRandom(t, in, rng)
			if !sameOutcome(ref, tb2, refErr, err2) {
				t.Fatalf("random cut iter=%d mismatch %v", iter, err2)
			}
		}
	}
}

func parseRandom(t *testing.T, in string, rng *rand.Rand) (*table.Table, table.Stats, error) {
	t.Helper()
	p := table.NewParser(noLimit)
	for i := 0; i < len(in); {
		n := 1 + rng.Intn(4)
		end := i + n
		if end > len(in) {
			end = len(in)
		}
		_ = p.Feed([]byte(in[i:end]))
		i = end
	}
	return p.Close()
}

func sameOutcome(ref, got *table.Table, refErr, gotErr error) bool {
	if (refErr == nil) != (gotErr == nil) {
		return false
	}
	if refErr != nil {
		a, b := table.AsError(refErr), table.AsError(gotErr)
		return errors.Is(a, b.Err) && a.Offset == b.Offset && a.Record == b.Record && a.Field == b.Field
	}
	return ref.Equal(got)
}

func TestRoundTrip(t *testing.T) {
	cases := []string{
		"a,b,c\n", "a\r\n", "\"\",x\n", "\"a,b\",\"x\"\"\ny\"\n",
		"\"\"\n", "x\n\n", "\",\"\n\"line1\r\nline2\"\n",
	}
	for _, in := range cases {
		t1, _, err := table.Parse([]byte(in), noLimit)
		if err != nil {
			t.Fatalf("parse %q: %v", in, err)
		}
		out := writer.Write(t1)
		t2, _, err := table.Parse(out, noLimit)
		if err != nil || !t1.Equal(t2) {
			t.Fatalf("roundtrip %q -> %q err=%v", in, out, err)
		}
		// 幂等：Write(Parse(x)) 对规范输入逐字节稳定
		out2 := writer.Write(t2)
		if string(out) != string(out2) {
			t.Fatalf("canonical not idempotent: %q vs %q", out, out2)
		}
	}
	// 单列表空值必须写成 ""，否则空行会丢记录。
	tb, _, _ := table.Parse([]byte("\"\"\n"), noLimit)
	if got := string(writer.Write(tb)); got != "\"\"\n" {
		t.Fatalf("single empty cell write = %q", got)
	}
}

func TestTruncationAndInjection(t *testing.T) {
	base := "a,b\r\n\"x,y\",z\r\n\"q\"\"q\",n\n"
	for cut := 0; cut <= len(base); cut++ {
		tb, _, err := table.Parse([]byte(base[:cut]), noLimit)
		full, _, _ := table.Parse([]byte(base), noLimit)
		if err != nil && !errors.Is(err, lexer.ErrUnclosedQuote) &&
			!errors.Is(err, table.ErrFieldCount) {
			t.Fatalf("cut=%d unexpected err %v", cut, err)
		}
		fr := full.Records()
		gr := tb.Records()
		if len(gr) > len(fr) {
			t.Fatalf("cut=%d prefix longer", cut)
		}
		if err != nil && !errors.Is(err, lexer.ErrLoneCR) {
			for i := range gr {
				if len(gr[i]) != len(fr[i]) {
					t.Fatalf("cut=%d row %d width mismatch", cut, i)
				}
				for j := range gr[i] {
					if gr[i][j] != fr[i][j] {
						t.Fatalf("cut=%d not prefix at %d,%d", cut, i, j)
					}
				}
			}
		}
	}
	for cut := 0; cut <= len(base); cut++ {
		bad := base[:cut] + "\"" + base[cut:]
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic at cut=%d: %v", cut, r)
				}
			}()
			tb, _, err := table.Parse([]byte(bad), noLimit)
			if err == nil {
				return
			}
			e := table.AsError(err)
			ok := errors.Is(e, lexer.ErrQuoteInBare) || errors.Is(e, lexer.ErrCharsAfterQuote) ||
				errors.Is(e, lexer.ErrUnclosedQuote) || errors.Is(e, lexer.ErrLoneCR) ||
				errors.Is(e, table.ErrFieldCount)
			if !ok || e.Offset < cut {
				t.Fatalf("cut=%d bad err %+v", cut, e)
			}
			_ = tb
		}()
	}
}

var _ = par.Parse
