// Package codec 按固定的小端长度前缀格式把 ast.Expr 编码为字节串并解码，仅依赖 ast。
package codec

import (
	"encoding/binary"
	"errors"

	"ontology/ast"
)

// 可判定的哨兵错误，互不相同，调用方可用 errors.Is 精确判别。
var (
	ErrTruncated   = errors.New("codec: truncated input")
	ErrIllegalTag  = errors.New("codec: illegal node tag")
	ErrIllegalBool = errors.New("codec: bool byte must be 0 or 1")
	ErrVarTooLong  = errors.New("codec: var length prefix exceeds remaining bytes")
	ErrIllegalOp   = errors.New("codec: illegal binop code")
	ErrTrailing    = errors.New("codec: trailing bytes after root node")
)

// 运算符枚举码，取值逐字遵照格式规定，不得改动。
var opCode = map[string]byte{"+": 0, "-": 1, "*": 2, "/": 3, "<": 4, ">": 5,
	"<=": 6, ">=": 7, "==": 8, "!=": 9, "&&": 10, "||": 11}
var codeOp = [12]string{"+", "-", "*", "/", "<", ">", "<=", ">=", "==", "!=", "&&", "||"}

// Encode 编码 n。合法 AST 只能由 ast 包构造函数产生；nil、未知 Kind、
// 未知 op 属于调用方编程错误，直接 panic（题目签名无 error）。
func Encode(n *ast.Expr) []byte {
	if n == nil {
		panic("codec: cannot encode nil node")
	}
	out := []byte{}
	var enc func(*ast.Expr)
	enc = func(e *ast.Expr) {
		switch e.Kind {
		case ast.KIntLit:
			var b [8]byte
			binary.LittleEndian.PutUint64(b[:], uint64(e.Int))
			out = append(append(out, 0), b[:]...)
		case ast.KBoolLit:
			if e.Bool {
				out = append(out, 1, 1)
			} else {
				out = append(out, 1, 0)
			}
		case ast.KVar:
			var b [4]byte
			binary.LittleEndian.PutUint32(b[:], uint32(len(e.Name)))
			out = append(append(append(out, 2), b[:]...), e.Name...) // 原始字节
		case ast.KNeg, ast.KNot:
			out = append(out, byte(e.Kind))
			enc(e.Children[0])
		case ast.KBinOp:
			c, ok := opCode[e.Op]
			if !ok {
				panic("codec: unknown binop " + e.Op)
			}
			out = append(out, 5, c)
			enc(e.Children[0])
			enc(e.Children[1])
		case ast.KIf:
			out = append(out, 6)
			enc(e.Children[0])
			enc(e.Children[1])
			enc(e.Children[2])
		default:
			panic("codec: unknown node kind")
		}
	}
	enc(n)
	return out
}

// decErr 是解码失败的内部信号，Decode 边界上 recover 成 error 返回。
type decErr struct{ err error }

// decoder 每次 Decode 新建；无包级可变状态，故失败不留痕、并发互不干扰。
type decoder struct {
	b             []byte
	pos           int
	boundaryScans int // 非导出：为定位字段边界检查过的字节数，仅同包测试可读
}

// must 顺序取下一个 n 字节，余量不足即 panic(decErr{ErrTruncated})；
// boundary 时把 n 计入边界检查字节数。
func (d *decoder) must(n int, boundary bool) []byte {
	if d.pos+n > len(d.b) {
		panic(decErr{ErrTruncated})
	}
	if boundary {
		d.boundaryScans += n
	}
	s := d.b[d.pos : d.pos+n]
	d.pos += n
	return s
}

func (d *decoder) decode() *ast.Expr {
	t := d.must(1, false)
	switch ast.Kind(t[0]) {
	case ast.KIntLit:
		return ast.IntLit(int64(binary.LittleEndian.Uint64(d.must(8, false))))
	case ast.KBoolLit:
		if s := d.must(1, false); s[0] > 1 {
			panic(decErr{ErrIllegalBool})
		} else {
			return ast.BoolLit(s[0] == 1)
		}
	case ast.KVar:
		m := int(binary.LittleEndian.Uint32(d.must(4, true))) // 只查 4 字节前缀定位边界
		if m > len(d.b)-d.pos {                               // 声明超余量，区别于普通截断
			panic(decErr{ErrVarTooLong})
		}
		name := string(d.b[d.pos : d.pos+m]) // 按原始字节复制，不扫描
		d.pos += m
		return ast.Var(name)
	case ast.KNeg:
		return ast.Neg(d.decode())
	case ast.KNot:
		return ast.Not(d.decode())
	case ast.KBinOp:
		s := d.must(1, false)
		if s[0] > 11 {
			panic(decErr{ErrIllegalOp})
		}
		l, r := d.decode(), d.decode()
		return ast.BinOp(codeOp[s[0]], l, r)
	case ast.KIf:
		return ast.If(d.decode(), d.decode(), d.decode())
	}
	panic(decErr{ErrIllegalTag})
}

// Decode 从 b 解码出恰好一棵树；任何非法情况都整体失败、不返回 AST。
func Decode(b []byte) (n *ast.Expr, err error) {
	d := &decoder{b: b}
	defer func() {
		if r := recover(); r != nil {
			if de, ok := r.(decErr); ok {
				n, err = nil, de.err
				return
			}
			panic(r)
		}
		if d.pos != len(b) {
			n, err = nil, ErrTrailing
		}
	}()
	return d.decode(), nil
}
