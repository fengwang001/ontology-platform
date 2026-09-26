// Package api 是对外门面：包装 codec 的编解码，并提供内置自检。
package api

import (
	"fmt"

	"ontology/ast"
	"ontology/codec"
)

// EncodeExpr 把 n 编码为字节串（语义同 codec.Encode）。
func EncodeExpr(n *ast.Expr) []byte { return codec.Encode(n) }

// DecodeExpr 从 b 解码一棵表达式树（语义同 codec.Decode）。
func DecodeExpr(b []byte) (*ast.Expr, error) { return codec.Decode(b) }

// RoundTrip 编码后立即解码，返回解码结果；等价性由调用方用 ast.Equal 判定。
func RoundTrip(n *ast.Expr) (*ast.Expr, error) { return codec.Decode(codec.Encode(n)) }

// 内置自检样本：覆盖全部 7 种节点与关键边界值。
func samples() []*ast.Expr {
	return []*ast.Expr{
		ast.BinOp("+", ast.IntLit(258), ast.Var("ab")),
		ast.IntLit(4294967296),
		ast.IntLit(-9223372036854775808),
		ast.Var(""),
		ast.Var("a\x00b"),
		ast.Var("\xff\xfe\x80"),
		ast.BoolLit(true),
		ast.Not(ast.BoolLit(false)),
		ast.Neg(ast.IntLit(-1)),
		ast.BinOp("||", ast.Var("x"), ast.BinOp("&&", ast.Var("y"), ast.Var("z"))),
		ast.If(ast.BinOp("<=", ast.Var("n"), ast.IntLit(10)),
			ast.Neg(ast.Var("n")), ast.BinOp("*", ast.Var("n"), ast.IntLit(2))),
	}
}

// SelfCheck 对内置 AST 核验四条不变量，全部通过返回 nil，否则返回首个失败原因。
// 不变量 4（失败不留痕）与边界 O(1) 的计数器断言在 codec 同包测试中钉住，
// 这里核验其对外可观察部分：拒绝返回 nil+error 且后续调用不受影响。
func SelfCheck() error {
	for i, n := range samples() {
		// 不变量 1+2：往返结构相等，且编码长度可预测（定宽字段+长度前缀）。
		got, err := RoundTrip(n)
		if err != nil {
			return fmt.Errorf("selfcheck sample %d: decode: %w", i, err)
		}
		if !ast.Equal(got, n) {
			return fmt.Errorf("selfcheck sample %d: round-trip not equal", i)
		}
		if len(EncodeExpr(n)) != encodedLen(n) {
			return fmt.Errorf("selfcheck sample %d: encoded length mismatch", i)
		}
	}
	// 不变量 3：含空字节/非 UTF-8 的名字逐字节保留。
	for _, s := range []string{"", "a\x00b", "\xff\xfe"} {
		got, err := RoundTrip(ast.Var(s))
		if err != nil || got.Name != s {
			return fmt.Errorf("selfcheck: raw bytes of %q not preserved", s)
		}
	}
	// 不变量 4：四类非法输入各自返回可判定错误且不产出 AST；拒绝后正常调用不受影响。
	bad := [][]byte{
		codec.Encode(ast.IntLit(7))[:5], // 截断
		{7},                             // 非法标签
		{1, 2},                          // BoolLit 非 0/1
		{2, 5, 0, 0, 0, 'a', 'b'},       // Var 长度超余量
	}
	for i, b := range bad {
		if n, err := DecodeExpr(b); err == nil || n != nil {
			return fmt.Errorf("selfcheck: bad input %d not rejected", i)
		}
	}
	if _, err := RoundTrip(samples()[0]); err != nil {
		return fmt.Errorf("selfcheck: state leaked after rejections: %w", err)
	}
	return nil
}

// encodedLen 按格式定义独立推算编码长度（不读 Encode 的实现）。
func encodedLen(n *ast.Expr) int {
	switch n.Kind {
	case ast.KIntLit:
		return 9
	case ast.KBoolLit:
		return 2
	case ast.KVar:
		return 5 + len(n.Name)
	case ast.KNeg, ast.KNot:
		return 1 + encodedLen(n.Children[0])
	case ast.KBinOp:
		return 2 + encodedLen(n.Children[0]) + encodedLen(n.Children[1])
	case ast.KIf:
		return 1 + encodedLen(n.Children[0]) + encodedLen(n.Children[1]) + encodedLen(n.Children[2])
	}
	return -1
}
