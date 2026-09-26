package codec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math/rand"
	"testing"

	"ontology/ast"
)

// naiveEncode 是独立朴素参照：手写逐字节构造，不复用实现内部任何函数。
func naiveEncode(e *ast.Expr) []byte {
	kids := make([][]byte, len(e.Children))
	for i, c := range e.Children {
		kids[i] = naiveEncode(c)
	}
	switch e.Kind {
	case ast.KIntLit:
		var v [8]byte
		binary.LittleEndian.PutUint64(v[:], uint64(e.Int))
		return append([]byte{0}, v[:]...)
	case ast.KBoolLit:
		if e.Bool {
			return []byte{1, 1}
		}
		return []byte{1, 0}
	case ast.KVar:
		b := []byte{2, 0, 0, 0, 0}
		binary.LittleEndian.PutUint32(b[1:], uint32(len(e.Name)))
		return append(b, e.Name...)
	case ast.KNeg, ast.KNot:
		return append([]byte{byte(e.Kind)}, kids[0]...)
	case ast.KBinOp:
		return append(append([]byte{5, opCode[e.Op]}, kids[0]...), kids[1]...)
	case ast.KIf:
		return append(append(append([]byte{6}, kids[0]...), kids[1]...), kids[2]...)
	}
	panic("bad kind")
}
func TestGolden258PlusAB(t *testing.T) {
	got := Encode(ast.BinOp("+", ast.IntLit(258), ast.Var("ab")))
	want := []byte{5, 0, 0, 2, 1, 0, 0, 0, 0, 0, 0, 2, 2, 0, 0, 0, 'a', 'b'}
	if !bytes.Equal(got, want) {
		t.Fatalf("got=% x\nwant=% x", got, want)
	}
}

func TestRoundTripNaiveReference(t *testing.T) {
	ops := []string{"+", "-", "*", "/", "<", ">", "<=", ">=", "==", "!=", "&&", "||"}
	names := []string{"", "a", "ab", "x\x00y", "\xff\xfe", "变量", "a\x00"}
	rng := rand.New(rand.NewSource(258))
	leaves := []func() *ast.Expr{
		func() *ast.Expr { return ast.IntLit(rng.Int63n(1 << 40)) },
		func() *ast.Expr { return ast.BoolLit(rng.Intn(2) == 0) },
		func() *ast.Expr { return ast.Var(names[rng.Intn(len(names))]) }}
	var gen func(int) *ast.Expr
	gen = func(d int) *ast.Expr {
		if d == 0 {
			return leaves[rng.Intn(3)]()
		}
		l, r := gen(d-1), gen(d-1)
		switch rng.Intn(5) {
		case 0:
			return ast.Neg(l)
		case 1:
			return ast.Not(l)
		case 2:
			return ast.BinOp(ops[rng.Intn(12)], l, r)
		}
		return ast.If(l, r, gen(d-1))
	}
	for i := 0; i < 2000; i++ {
		n := gen(4)
		if b := Encode(n); !bytes.Equal(b, naiveEncode(n)) {
			t.Fatalf("case %d: Encode=% x naive=% x", i, b, naiveEncode(n))
		}
		if got, err := Decode(Encode(n)); err != nil || !ast.Equal(got, n) {
			t.Fatalf("case %d: round-trip err=%v", i, err)
		}
	}
}
func TestEndiannessAndWidth(t *testing.T) {
	cases := []struct {
		e *ast.Expr
		n int
	}{{ast.IntLit(4294967296), 9}, {ast.IntLit(-1), 9},
		{ast.Var(""), 5}, {ast.Var("a\x00b"), 8}, {ast.BoolLit(true), 2}}
	for i, c := range cases {
		b := Encode(c.e)
		got, err := Decode(b)
		if len(b) != c.n || err != nil || !ast.Equal(got, c.e) {
			t.Errorf("case %d: len=%d want %d err=%v", i, len(b), c.n, err)
		}
	}
	if v := Encode(ast.IntLit(4294967296)); v[5] != 1 { // 小端：2^32 落在载荷第 4 字节
		t.Errorf("2^32 little-endian wrong: % x", v[1:])
	}
}
func TestVarRawBytes(t *testing.T) {
	for _, s := range []string{"", "a\x00b", "\x00", "\xff\xfe\x80", "a\x00\x00b\xff"} {
		got, err := Decode(Encode(ast.Var(s)))
		if err != nil || got.Name != s || len(got.Name) != len(s) {
			t.Fatalf("%q -> %q err=%v", s, got.Name, err)
		}
	}
}
func TestVarBoundaryScanBounded(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 4096, 10000} {
		d := &decoder{b: Encode(ast.Var(string(make([]byte, m))))}
		if d.decode() == nil || d.boundaryScans != 4 {
			t.Fatalf("m=%d scans=%d", m, d.boundaryScans)
		}
	}
}
func TestDecodeSentinelErrors(t *testing.T) {
	root := ast.BinOp("+", ast.IntLit(1), ast.Var("v"))
	cases := []struct {
		name string
		in   []byte
		want error
	}{{"truncated", Encode(ast.IntLit(7))[:5], ErrTruncated},
		{"illegal tag", []byte{7}, ErrIllegalTag},
		{"bad bool", []byte{1, 2}, ErrIllegalBool},
		{"var too long", []byte{2, 5, 0, 0, 0, 'a', 'b'}, ErrVarTooLong},
		{"trailing", append(Encode(root), 0), ErrTrailing}}
	for _, c := range cases {
		if n, err := Decode(c.in); !errors.Is(err, c.want) || n != nil {
			t.Errorf("%s: n=%v err=%v want %v", c.name, n, err, c.want)
		}
	}
	if ErrTruncated == ErrIllegalTag || ErrIllegalTag == ErrIllegalBool || ErrIllegalBool == ErrVarTooLong {
		t.Fatal("sentinels not distinct")
	}
}
func TestRejectionLeavesNoState(t *testing.T) {
	root := ast.BinOp("+", ast.IntLit(1), ast.Var("v"))
	bad := [][]byte{Encode(ast.IntLit(7))[:5], {7}, {1, 2}, {2, 5, 0, 0, 0, 'a', 'b'}}
	for i := 0; i < 3; i++ {
		for _, b := range bad {
			if _, err := Decode(b); err == nil {
				t.Fatal("expected rejection")
			}
		}
		if got, err := Decode(Encode(root)); err != nil || !ast.Equal(got, root) {
			t.Fatalf("round %d: %v", i, err)
		}
	}
}
