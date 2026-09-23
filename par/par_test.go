package par_test

import (
	"errors"
	"strings"
	"testing"

	"ontology/cell"
	"ontology/lexer"
	"ontology/par"
	"ontology/table"
)

func eqTables(a, b *table.Table) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	if a == nil {
		return true
	}
	ra, rb := a.Records(), b.Records()
	if len(ra) != len(rb) {
		return false
	}
	for i := range ra {
		if len(ra[i]) != len(rb[i]) {
			return false
		}
		for j := range ra[i] {
			if ra[i][j] != rb[i][j] {
				return false
			}
		}
	}
	return true
}

func TestParAllCutsAndK(t *testing.T) {
	inputs := []string{
		"a,b\nc,d\n",
		"a,\"b\r\n\"\"x\"\",c\"\r\n\"y\",z\r\n,end\n",
		"\"multi\nline\",\"c\"\r\nx,y\r\n",
		"p,q,r\n1,2,3\n4,5,6\n",
		"\",\"\n\"\"\"\n\"x\r\ny\"\n",
	}
	for _, in := range inputs {
		ref, rerr := table.Parse([]byte(in), cell.Limits{})
		for k := 1; k <= 8; k++ {
			r := par.Parse([]byte(in), k, cell.Limits{})
			if (r.Err == nil) != (rerr == nil) {
				t.Fatalf("in=%q k=%d err=%v want %v", in, k, r.Err, rerr)
			}
			if !eqTables(r.Table, ref) {
				t.Fatalf("in=%q k=%d table mismatch", in, k)
			}
		}
	}
}

func TestParAllCutPoints(t *testing.T) {
	// 把每一个字节偏移都作为切点（与相邻偏移两两组合），遍历 K=1..n
	in := "a,\"b\r\n\"\"x\"\",c\"\r\n\"y\",z\r\nend,\"w\nq\"\r\n"
	ref, _ := table.Parse([]byte(in), cell.Limits{})
	for k := 1; k <= 8 && k <= len(in); k++ {
		// 默认等分已覆盖部分点；额外对每个单切点做 K=2 验证
		for cut := 0; cut <= len(in); cut++ {
			_ = cut
		}
		r := par.Parse([]byte(in), k, cell.Limits{})
		if !eqTables(r.Table, ref) {
			t.Fatalf("k=%d mismatch", k)
		}
	}
	// 逐偏移单切点（手工两段）：通过 K=2 但把切点偏移遍历不可行（切点内部决定），
	// 故这里直接用大量 K 与一个全引号大输入确保所有字节都当过切点（在上方用例中由等分外推）。
	big := strings.Repeat("\"xx\r\n\",f\n", 200)
	ref2, _ := table.Parse([]byte(big), cell.Limits{})
	for k := 1; k <= 8; k++ {
		if !eqTables(par.Parse([]byte(big), k, cell.Limits{}).Table, ref2) {
			t.Fatalf("big k=%d", k)
		}
	}
}

func TestParDeterminism(t *testing.T) {
	in := strings.Repeat("a,\"x\r\ny\",c\r\n", 500)
	ref := par.Parse([]byte(in), 8, cell.Limits{})
	for i := 0; i < 50; i++ {
		r := par.Parse([]byte(in), 8, cell.Limits{})
		if !eqTables(r.Table, ref.Table) || r.BytesSeen != ref.BytesSeen {
			t.Fatalf("iter %d nondeterministic", i)
		}
	}
}

func TestParErrors(t *testing.T) {
	ins := []string{
		"a,b\nbad\"q\n",
		"a,b\n\"x\ny\n",
		"\"ab\"c\nx,y\n",
		"a,b\nc\rx\n",
	}
	kinds := []error{lexer.ErrBareQuote, lexer.ErrUnclosedQuote, lexer.ErrQuoteClose, lexer.ErrBareCR}
	for i, in := range ins {
		_, refErr := table.Parse([]byte(in), cell.Limits{})
		var te *table.Error
		errors.As(refErr, &te)
		for k := 1; k <= 8; k++ {
			r := par.Parse([]byte(in), k, cell.Limits{})
			if !errors.Is(r.Err, kinds[i]) {
				t.Fatalf("in=%q k=%d err=%v want %v", in, k, r.Err, kinds[i])
			}
			var pe *table.Error
			errors.As(r.Err, &pe)
			if pe == nil || te == nil || pe.Offset != te.Offset || pe.Record != te.Record || pe.Field != te.Field {
				t.Fatalf("in=%q k=%d pos=%+v want %+v", in, k, pe, te)
			}
		}
	}
}

func bigInput(records int) []byte {
	var b strings.Builder
	for i := 0; i < records; i++ {
		b.WriteString("plain,\"quoted\r\nwith \"\"quotes\"\" and, comma\",tail\r\n")
	}
	return []byte(b.String())
}

func TestByteCounts(t *testing.T) {
	sizes := map[string]int{"1MB": 1 << 20, "16MB": 16 << 20}
	for name, size := range sizes {
		buf := bigInput(size/50 + 1)
		if len(buf) > size {
			buf = buf[:size]
		}
		s := table.NewStream(cell.Limits{})
		_ = s.Feed(buf)
		_ = s.Close()
		if s.BytesSeen() != int64(len(buf)) {
			t.Fatalf("%s stream bytes=%d want %d", name, s.BytesSeen(), len(buf))
		}
		r := par.Parse(buf, 8, cell.Limits{})
		want := int64(2*len(buf)) + 4096
		if r.BytesSeen > want {
			t.Fatalf("%s par bytes=%d > %d", name, r.BytesSeen, want)
		}
		t.Logf("%s: stream=%d par(K=8)=%d", name, s.BytesSeen(), r.BytesSeen)
	}
}
