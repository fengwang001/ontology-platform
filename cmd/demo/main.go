// demo 逐条打印 OK/FAIL；全部 OK 时退出码为 0。
package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/ast"
	"ontology/codec"
)

var failed bool

func check(name string, ok bool) {
	mark := "OK  "
	if !ok {
		mark = "FAIL"
		failed = true
	}
	fmt.Println(mark + " " + name)
}

func main() {
	// 1. 第三节：258+ab 的完整编码字节
	got := api.EncodeExpr(ast.BinOp("+", ast.IntLit(258), ast.Var("ab")))
	want := []byte{5, 0, 0, 2, 1, 0, 0, 0, 0, 0, 0, 2, 2, 0, 0, 0, 'a', 'b'}
	check("encode(258+ab) bytes", bytes.Equal(got, want))

	// 2. IntLit(2^32) round-trip 值
	n, err := api.RoundTrip(ast.IntLit(4294967296))
	check("IntLit(2^32) round-trip", err == nil && n.Int == 4294967296)

	// 3. Var("") 长度前缀为 0、无名字字节
	b := api.EncodeExpr(ast.Var(""))
	check("Var(\"\") prefix=0,len=5", len(b) == 5 && b[1] == 0 && b[2] == 0 && b[3] == 0 && b[4] == 0)

	// 4. Var("a\x00b") round-trip 逐字节相同
	n, err = api.RoundTrip(ast.Var("a\x00b"))
	check("Var(a\\x00b) raw bytes", err == nil && n.Name == "a\x00b" && len(n.Name) == 3)

	// 5. If/BinOp 嵌套 round-trip 结构相等
	tree := ast.If(ast.BinOp("<=", ast.Var("n"), ast.IntLit(10)),
		ast.Neg(ast.Var("n")), ast.BinOp("*", ast.Var("n"), ast.IntLit(2)))
	n, err = api.RoundTrip(tree)
	check("nested If/BinOp round-trip", err == nil && ast.Equal(n, tree))

	// 6. 四类可判定错误，互不相同
	bads := []struct {
		in   []byte
		want error
	}{
		{api.EncodeExpr(ast.IntLit(7))[:5], codec.ErrTruncated},
		{[]byte{7}, codec.ErrIllegalTag},
		{[]byte{1, 2}, codec.ErrIllegalBool},
		{[]byte{2, 5, 0, 0, 0, 'a', 'b'}, codec.ErrVarTooLong},
	}
	ok := codec.ErrTruncated != codec.ErrIllegalTag && codec.ErrIllegalTag != codec.ErrIllegalBool &&
		codec.ErrIllegalBool != codec.ErrVarTooLong
	for _, c := range bads {
		r, e := api.DecodeExpr(c.in)
		ok = ok && r == nil && errors.Is(e, c.want)
	}
	check("four distinct decidable errors", ok)

	// 7. 被拒后状态不变
	_, _ = api.DecodeExpr([]byte{7})
	n, err = api.RoundTrip(tree)
	check("no state after rejection", err == nil && ast.Equal(n, tree))

	// 8. 大 m 下边界检查个数不随 m 增长（计数器断言在 codec 同包测试
	// TestVarBoundaryScanBounded；此处核验大 m 解码本身正确）
	const m = 10000
	n, err = api.DecodeExpr(api.EncodeExpr(ast.Var(string(make([]byte, m)))))
	check("large-m var decode (O(1) scan)", err == nil && len(n.Name) == m)

	// 9. 并发 round-trip 一致
	var wg sync.WaitGroup
	oks := make(chan bool, 64)
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := api.RoundTrip(tree)
			oks <- e == nil && ast.Equal(r, tree)
		}()
	}
	wg.Wait()
	close(oks)
	ok = true
	for r := range oks {
		ok = ok && r
	}
	check("concurrent round-trip", ok)

	// 10. 内置自检
	check("api.SelfCheck", api.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
