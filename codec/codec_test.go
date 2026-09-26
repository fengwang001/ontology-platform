package codec

import (
	"bytes"
	"math/rand"
	"strings"
	"testing"

	"ontology/ast"
)

// naiveEncode is an independent reference encoder (manual shifts).
func naiveEncode(n *ast.Expr) []byte {
	var buf bytes.Buffer
	put := func(v uint64, k int) {
		for i := 0; i < k; i++ {
			buf.WriteByte(byte(v >> (8 * i)))
		}
	}
	var walk func(e *ast.Expr)
	walk = func(e *ast.Expr) {
		switch e.Kind {
		case ast.IntLit:
			buf.WriteByte(0)
			put(uint64(e.I), 8)
		case ast.BoolLit:
			b := byte(0)
			if e.B {
				b = 1
			}
			buf.WriteByte(1)
			buf.WriteByte(b)
		case ast.Var:
			buf.WriteByte(2)
			put(uint64(len(e.Name)), 4)
			buf.WriteString(e.Name)
		case ast.Neg, ast.Not:
			buf.WriteByte(byte(e.Kind))
			walk(e.Child)
		case ast.BinOp:
			buf.WriteByte(5)
			buf.WriteByte(byte(e.Op))
			walk(e.L)
			walk(e.R)
		case ast.If:
			buf.WriteByte(6)
			walk(e.Cond)
			walk(e.Then)
			walk(e.Else)
		}
	}
	walk(n)
	return buf.Bytes()
}

func randExpr(r *rand.Rand, depth int) *ast.Expr {
	if depth > 0 {
		switch r.Intn(7) {
		case 0:
			return ast.NegOf(randExpr(r, depth-1))
		case 1:
			return ast.NotOf(randExpr(r, depth-1))
		case 2:
			return ast.Bin(ast.Op(r.Intn(12)), randExpr(r, depth-1), randExpr(r, depth-1))
		case 3:
			return ast.IfElse(randExpr(r, depth-1), randExpr(r, depth-1), randExpr(r, depth-1))
		}
	}
	names := []string{"", "a", "a\x00b", "\xff\xfe", strings.Repeat("z", r.Intn(20))}
	switch r.Intn(3) {
	case 0:
		return ast.Int(r.Int63() - 1<<62)
	case 1:
		return ast.Bool(r.Intn(2) == 0)
	default:
		return ast.Name(names[r.Intn(len(names))])
	}
}

func TestRoundTripNaive(t *testing.T) {
	cases := []*ast.Expr{ast.Bin(ast.OpAdd, ast.Int(258), ast.Name("ab")), ast.Int(4294967296),
		ast.IfElse(ast.Bool(true), ast.NegOf(ast.Int(-1)), ast.Name(""))}
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 200; i++ {
		cases = append(cases, randExpr(r, 4))
	}
	for i, n := range cases {
		enc := Encode(n)
		if want := naiveEncode(n); !bytes.Equal(enc, want) {
			t.Fatalf("case %d: encode differs from naive reference", i)
		}
		got, err := Decode(enc)
		if err != nil || !ast.Equal(got, n) {
			t.Fatalf("case %d: round-trip broken (err=%v)", i, err)
		}
	}
}

func TestByteWidths(t *testing.T) {
	cases := []struct {
		n    *ast.Expr
		want int
	}{
		{ast.Int(0), 9}, {ast.Int(4294967296), 9}, {ast.Bool(true), 2},
		{ast.Name(""), 5}, {ast.Name("ab"), 7}, {ast.NegOf(ast.Int(1)), 10},
		{ast.Bin(ast.OpAdd, ast.Int(258), ast.Name("ab")), 18},
	}
	for i, c := range cases {
		if got := len(Encode(c.n)); got != c.want {
			t.Fatalf("case %d: width %d, want %d", i, got, c.want)
		}
	}
}

func TestStringBytes(t *testing.T) {
	for _, name := range []string{"", "a\x00b", "\xff\xfe\x00", "normal", strings.Repeat("q", 300)} {
		got, err := Decode(Encode(ast.Name(name)))
		if err != nil || got.Name != name {
			t.Fatalf("name %q: got %q err=%v", name, got.Name, err)
		}
	}
}

func TestFaultInjection(t *testing.T) {
	names := []string{"truncated", "bad tag", "bool not 0/1", "var len overrun"}
	bs := [][]byte{Encode(ast.Int(1))[:4], {9}, {1, 2}, {2, 10, 0, 0, 0, 'a'}}
	wants := []error{ErrTruncated, ErrBadTag, ErrBadBool, ErrBadLen}
	for i := range names { // 每类命中各自不同的哨兵即证明互不相同
		n, err := Decode(bs[i])
		if n != nil || err != wants[i] {
			t.Fatalf("%s: got (%v, %v), want (nil, %v)", names[i], n, err, wants[i])
		}
	}
	if _, err := Decode(Encode(ast.Name("ok"))); err != nil {
		t.Fatalf("residue after rejections: %v", err)
	}
}

func TestBoundaryChecksConstant(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		d := &decoder{b: Encode(ast.Name(strings.Repeat("x", m)))}
		if _, err := d.expr(); err != nil || d.boundaryChecks > 8 {
			t.Fatalf("m=%d: err=%v boundaryChecks=%d", m, err, d.boundaryChecks)
		}
	}
}
