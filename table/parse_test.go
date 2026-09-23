package table_test

import (
	"errors"
	"math/rand"
	"testing"

	"ontology/cell"
	"ontology/lexer"
	"ontology/table"
	"ontology/writer"
)

func mustParse(t *testing.T, in string, lim ...cell.Limits) *table.Table {
	t.Helper()
	var l cell.Limits
	if len(lim) > 0 {
		l = lim[0]
	}
	tb, err := table.Parse([]byte(in), l)
	if err != nil {
		t.Fatalf("Parse(%q): %v", in, err)
	}
	return tb
}

func TestDialect(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want [][]string
	}{
		{"simple", "a,b\nc,d\n", [][]string{{"a", "b"}, {"c", "d"}}},
		{"no-final-nl", "a,b\nc,d", [][]string{{"a", "b"}, {"c", "d"}}},
		{"comma-in-quote", "\"a,b\"\n", [][]string{{"a,b"}}},
		{"nl-in-quote", "\"a\nb\"\n", [][]string{{"a\nb"}}},
		{"crlf-in-quote", "\"a\r\nb\"\n", [][]string{{"a\r\nb"}}},
		{"crlf-record", "a\r\nb\r\n", [][]string{{"a"}, {"b"}}},
		{"escaped-quote", "\"a\"\"b\"\n", [][]string{{`a"b`}}},
		{"quoted-empty", "\"\"\n", [][]string{{""}}},
		{"empty-row", "\n", [][]string{{""}}},
		{"blank-between", "a\n\nb\n", [][]string{{"a"}, {""}, {"b"}}},
		{"empty-fields", ",\n", [][]string{{"", ""}}},
		{"empty-input", "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tb := mustParse(t, tc.in)
			got := tb.Records()
			if len(got) != len(tc.want) {
				t.Fatalf("records=%d want %d (%v)", len(got), len(tc.want), got)
			}
			for i, r := range got {
				for j, c := range r {
					if c.Value != tc.want[i][j] {
						t.Fatalf("[%d][%d]=%q want %q", i, j, c.Value, tc.want[i][j])
					}
				}
			}
		})
	}
}

func TestQuotedEmptyDistinction(t *testing.T) {
	tb := mustParse(t, ",\"\"\n")
	rec := tb.Records()[0]
	if rec[0].Quoted || !rec[1].Quoted {
		t.Fatalf("quoted flags wrong: %+v %+v", rec[0], rec[1])
	}
	if rec[0].Start != 0 || rec[0].End != 0 || rec[1].Start != 1 || rec[1].End != 3 {
		t.Fatalf("offsets wrong: %+v %+v", rec[0], rec[1])
	}
}

func TestSyntaxErrors(t *testing.T) {
	cases := []struct {
		name string
		in   string
		kind error
		off  int64
		rec  int
		fld  int
	}{
		{"bare-quote", "a\"b\n", lexer.ErrBareQuote, 1, 1, 1},
		{"after-close", "\"ab\"c\n", lexer.ErrQuoteClose, 4, 1, 1},
		{"unclosed", "\"ab\n", lexer.ErrUnclosedQuote, 0, 1, 1},
		{"bare-cr", "a\rb\n", lexer.ErrBareCR, 1, 1, 1},
		{"bare-cr-eof", "a\r", lexer.ErrBareCR, 1, 1, 1},
		{"cr-in-quote-ok", "\"a\rb\"\n", nil, 0, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := table.Parse([]byte(tc.in), cell.Limits{})
			if tc.kind == nil {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var te *table.Error
			if !errors.As(err, &te) || !errors.Is(err, tc.kind) {
				t.Fatalf("err=%v want %v", err, tc.kind)
			}
			if te.Offset != tc.off || te.Record != tc.rec || te.Field != tc.fld {
				t.Fatalf("pos=(%d,%d,%d) want (%d,%d,%d)", te.Offset, te.Record, te.Field, tc.off, tc.rec, tc.fld)
			}
		})
	}
}

func TestColumnCount(t *testing.T) {
	_, err := table.Parse([]byte("a,b\nc\n"), cell.Limits{})
	var te *table.Error
	if !errors.As(err, &te) || !errors.Is(err, table.ErrColumnCount) {
		t.Fatalf("err=%v", err)
	}
	if te.Record != 2 || te.Field != 1 {
		t.Fatalf("pos=(%d,%d)", te.Record, te.Field)
	}
}

func TestFeedChunking(t *testing.T) {
	base := "a,\"b\r\n\"\"x\"\",c\"\r\n\"y\",z\r\n,"
	ref, err := table.Parse([]byte(base), cell.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	run := func(chunks [][]byte) (*table.Table, error) {
		s := table.NewStream(cell.Limits{})
		for _, c := range chunks {
			if err := s.Feed(c); err != nil {
				return s.Table(), err
			}
		}
		err := s.Close()
		return s.Table(), err
	}
	eq := func(a, b *table.Table) bool {
		ra, rb := a.Records(), b.Records()
		if len(ra) != len(rb) {
			return false
		}
		for i := range ra {
			if len(ra[i]) != len(rb[i]) {
				return false
			}
			for j := range ra[i] {
				x, y := ra[i][j], rb[i][j]
				if x.Value != y.Value || x.Quoted != y.Quoted || x.Start != y.Start || x.End != y.End {
					return false
				}
			}
		}
		return true
	}
	for cut := 0; cut <= len(base); cut++ {
		got, err := run([][]byte{[]byte(base[:cut]), []byte(base[cut:])})
		if err != nil {
			t.Fatalf("cut=%d: %v", cut, err)
		}
		if !eq(got, ref) {
			t.Fatalf("cut=%d mismatch", cut)
		}
	}
	// 1 字节一段
	var chunks [][]byte
	for i := 0; i < len(base); i++ {
		chunks = append(chunks, []byte{base[i]})
	}
	got, err := run(chunks)
	if err != nil {
		t.Fatal(err)
	}
	if !eq(got, ref) {
		t.Fatal("1-byte chunks mismatch")
	}
	rnd := rand.New(rand.NewSource(1))
	for iter := 0; iter < 200; iter++ {
		var cs [][]byte
		for p := 0; p < len(base); {
			q := p + 1 + rnd.Intn(4)
			if q > len(base) {
				q = len(base)
			}
			cs = append(cs, []byte(base[p:q]))
			p = q
		}
		g, err := run(cs)
		if err != nil || !eq(g, ref) {
			t.Fatalf("random iter=%d err=%v", iter, err)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	ins := []string{
		"a,b\nc,d\n",
		"\"a,b\"\n\"x\r\ny\"\n\"q\"\"q\"\n",
		"\"\"\n\"\"\n",
		"a\n\nb\n",
		",\n,\n",
		"\n",
	}
	for _, in := range ins {
		tb := mustParse(t, in)
		out := writer.Write(tb.Records())
		markerStrict := false // 单列未引号空字段回写为 ""（见 DESIGN.md）
		tb2, err := table.Parse(out, cell.Limits{})
		if err != nil {
			t.Fatalf("reparse %q -> %q: %v", in, out, err)
		}
		r1, r2 := tb.Records(), tb2.Records()
		if len(r1) != len(r2) {
			t.Fatalf("rec count %q -> %q", in, out)
		}
		for i := range r1 {
			for j := range r1[i] {
				a, b := r1[i][j], r2[i][j]
				if a.Value != b.Value || (markerStrict && a.Quoted != b.Quoted) {
					t.Fatalf("marker mismatch %q -> %q: %+v %+v", in, out, a, b)
				}
			}
		}
		// 规范输入逐字节往返
		canon := map[string]bool{
			"a,b\nc,d\n":                        true,
			"\"a,b\"\n\"x\r\ny\"\n\"q\"\"q\"\n": true,
			"\"\"\n\"\"\n":                      true,
			",\n,\n":                            true,
		}
		if canon[in] && string(out) != in {
			t.Fatalf("canonical byte drift: %q -> %q", in, out)
		}
	}
}

func TestLimitsImmediate(t *testing.T) {
	cases := []struct {
		name string
		in   string
		lim  cell.Limits
		kind error
	}{
		{"field", "\"abcdef\"\n", cell.Limits{MaxFieldBytes: 3}, lexer.ErrFieldTooLong},
		{"escaped-counts-1", "\"a\"\"b\"\n", cell.Limits{MaxFieldBytes: 3}, nil},
		{"fields", "a,b,c\n", cell.Limits{MaxFields: 2}, lexer.ErrTooManyFields},
		{"records", "a\nb\nc\n", cell.Limits{MaxRecords: 2}, lexer.ErrTooManyRecs},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tb, err := table.Parse([]byte(tc.in), tc.lim)
			if tc.kind == nil {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if !errors.Is(err, tc.kind) {
				t.Fatalf("err=%v", err)
			}
			if tc.name == "records" && len(tb.Records()) != 2 {
				t.Fatalf("produced records=%d, want prefix kept", len(tb.Records()))
			}
			s := table.NewStream(tc.lim)
			if e := s.Feed([]byte(tc.in)); e == nil {
				_ = s.Close()
			}
			if err := s.Feed([]byte("x")); !errors.Is(err, lexer.ErrTerminal) {
				t.Fatalf("post-terminal Feed err=%v", err)
			}
		})
	}
}

func TestTruncatePrefix(t *testing.T) {
	base := "h1,h2\na,\"x\r\ny\"\nb,c\n"
	ref := mustParse(t, base)
	refR := ref.Records()
	for cut := 0; cut <= len(base); cut++ {
		tb, err := table.Parse([]byte(base[:cut]), cell.Limits{})
		if err != nil {
			ok := errors.Is(err, lexer.ErrUnclosedQuote) || errors.Is(err, lexer.ErrBareCR) ||
				(errors.Is(err, table.ErrColumnCount) && cut > 4) // 末记录不完整，仅在截断时允许
			if !ok {
				t.Fatalf("cut=%d unexpected err %v", cut, err)
			}
			if inQuote(base, cut) && !errors.Is(err, lexer.ErrUnclosedQuote) && !errors.Is(err, table.ErrColumnCount) {
				t.Fatalf("cut=%d in quote: %v", cut, err)
			}
		}
		// 已产出的完整记录必须是全量结果的前缀
		n := len(tb.Records())
		if n > len(refR) {
			t.Fatalf("cut=%d too many records", cut)
		}
		if n > 0 && cut < len(base) {
			last := tb.Records()[n-1]
			// 末尾记录仅当其后的行尾已被消费时才算完整
			consumed := last[len(last)-1].End
			if !(consumed < int64(cut) && (base[consumed] == '\n' || (consumed+1 < int64(len(base)) && base[consumed] == '\r'))) {
				n--
			}
		}
		for i := 0; i < n; i++ {
			for j := range tb.Records()[i] {
				if tb.Records()[i][j] != refR[i][j] {
					t.Fatalf("cut=%d prefix mismatch %d,%d", cut, i, j)
				}
			}
		}
	}
}

func inQuote(s string, cut int) bool {
	in := false
	for i := 0; i < cut; i++ {
		if s[i] == '"' {
			if in && i+1 < cut && s[i+1] == '"' {
				i++
				continue
			}
			in = !in
		}
	}
	return in
}

func TestQuoteInjectionNoPanic(t *testing.T) {
	base := "a,\"b,c\"\r\nd,\"e\"\"f\"\n"
	for at := 0; at <= len(base); at++ {
		in := base[:at] + "\"" + base[at:]
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("at=%d panic: %v", at, r)
				}
			}()
			tb, err := table.Parse([]byte(in), cell.Limits{})
			if err == nil {
				_ = tb
				return
			}
			kinds := []error{lexer.ErrBareQuote, lexer.ErrQuoteClose, lexer.ErrUnclosedQuote, lexer.ErrBareCR, table.ErrColumnCount, lexer.ErrTooManyFields}
			ok := false
			for _, k := range kinds {
				if errors.Is(err, k) {
					ok = true
				}
			}
			if !ok {
				t.Fatalf("at=%d unknown err %v", at, err)
			}
			var te *table.Error
			if errors.As(err, &te) && te.Offset < 0 {
				t.Fatalf("at=%d bad offset %d", at, te.Offset)
			}
			if errors.As(err, &te) && !errors.Is(err, lexer.ErrUnclosedQuote) && te.Offset < int64(at)-1 {
				t.Fatalf("at=%d offset %d too early", at, te.Offset)
			}
		}()
	}
}
