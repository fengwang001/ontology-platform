// Package codec implements the fixed AST wire format: 1-byte tags,
// little-endian fixed-width ints, 4-byte length-prefixed strings.
package codec

import (
	"encoding/binary"
	"errors"

	"ontology/ast"
)

// Decidable sentinel errors; each rejected input maps to exactly one.
var (
	ErrTruncated = errors.New("codec: truncated input")
	ErrBadTag    = errors.New("codec: invalid node tag")
	ErrBadBool   = errors.New("codec: bool literal byte not 0/1")
	ErrBadLen    = errors.New("codec: var length prefix exceeds remaining input")
	ErrBadOp     = errors.New("codec: invalid operator code")
)

// Encode serializes n into the wire format.
func Encode(n *ast.Expr) []byte { return encode(make([]byte, 0, 32), n) }

func encode(buf []byte, n *ast.Expr) []byte {
	switch n.Kind {
	case ast.IntLit:
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], uint64(n.I))
		return append(append(buf, byte(ast.IntLit)), b[:]...)
	case ast.BoolLit:
		v := byte(0)
		if n.B {
			v = 1
		}
		return append(buf, byte(ast.BoolLit), v)
	case ast.Var:
		var b [4]byte
		binary.LittleEndian.PutUint32(b[:], uint32(len(n.Name)))
		buf = append(append(buf, byte(ast.Var)), b[:]...)
		return append(buf, n.Name...) // raw bytes, no escaping
	case ast.Neg, ast.Not:
		return encode(append(buf, byte(n.Kind)), n.Child)
	case ast.BinOp:
		buf = append(buf, byte(ast.BinOp), byte(n.Op))
		return encode(encode(buf, n.L), n.R)
	case ast.If:
		buf = append(buf, byte(ast.If))
		return encode(encode(encode(buf, n.Cond), n.Then), n.Else)
	}
	return buf
}

// decoder is per-call state: no globals, safe for concurrent use.
type decoder struct {
	b   []byte
	pos int
	// boundaryChecks counts bytes examined to locate a field boundary;
	// non-exported, unreachable from the public API.
	boundaryChecks int
}

// Decode parses one expression from b; rejection returns nil + sentinel.
func Decode(b []byte) (*ast.Expr, error) { return (&decoder{b: b}).expr() }

func (d *decoder) take(n int) ([]byte, error) {
	if len(d.b)-d.pos < n {
		return nil, ErrTruncated
	}
	s := d.b[d.pos : d.pos+n]
	d.pos += n
	return s, nil
}

func (d *decoder) kids(n int) ([]*ast.Expr, error) {
	k := make([]*ast.Expr, n)
	for i := range k {
		c, err := d.expr()
		if err != nil {
			return nil, err
		}
		k[i] = c
	}
	return k, nil
}

func (d *decoder) expr() (*ast.Expr, error) {
	tag, err := d.take(1)
	if err != nil {
		return nil, err
	}
	switch ast.Kind(tag[0]) {
	case ast.IntLit:
		s, err := d.take(8)
		if err != nil {
			return nil, err
		}
		return ast.Int(int64(binary.LittleEndian.Uint64(s))), nil
	case ast.BoolLit:
		s, err := d.take(1)
		if err != nil {
			return nil, err
		}
		if s[0] > 1 {
			return nil, ErrBadBool
		}
		return ast.Bool(s[0] == 1), nil
	case ast.Var:
		s, err := d.take(4)
		if err != nil {
			return nil, err
		}
		d.boundaryChecks += 4 // boundary from the 4-byte prefix alone; never scans the name
		m := binary.LittleEndian.Uint32(s)
		if uint64(len(d.b)-d.pos) < uint64(m) {
			return nil, ErrBadLen
		}
		name, _ := d.take(int(m))
		return ast.Name(string(name)), nil
	case ast.Neg, ast.Not:
		k, err := d.kids(1)
		if err != nil {
			return nil, err
		}
		if tag[0] == byte(ast.Neg) {
			return ast.NegOf(k[0]), nil
		}
		return ast.NotOf(k[0]), nil
	case ast.BinOp:
		op, err := d.take(1)
		if err != nil {
			return nil, err
		}
		if ast.Op(op[0]) > ast.OpOr {
			return nil, ErrBadOp
		}
		k, err := d.kids(2)
		if err != nil {
			return nil, err
		}
		return ast.Bin(ast.Op(op[0]), k[0], k[1]), nil
	case ast.If:
		k, err := d.kids(3)
		if err != nil {
			return nil, err
		}
		return ast.IfElse(k[0], k[1], k[2]), nil
	}
	return nil, ErrBadTag
}
