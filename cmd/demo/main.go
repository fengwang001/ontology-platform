package main

import (
	"bytes"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/ast"
	"ontology/codec"
)

var failed bool

func check(name string, ok bool, detail string) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s %s\n", status, name, detail)
}

func decodeErr(b []byte) error {
	_, err := codec.Decode(b)
	return err
}

func main() {
	nested := ast.IfElse(ast.Bin(ast.OpLe, ast.Name("x"), ast.Int(7)),
		ast.NotOf(ast.Bool(true)), ast.NegOf(ast.Int(-3)))

	// 第三节：258+ab 的完整编码字节
	enc := codec.Encode(ast.Bin(ast.OpAdd, ast.Int(258), ast.Name("ab")))
	want := []byte{5, 0, 0, 2, 1, 0, 0, 0, 0, 0, 0, 2, 2, 0, 0, 0, 'a', 'b'}
	check("enc 258+ab", bytes.Equal(enc, want), fmt.Sprintf("%x", enc))

	// (甲) IntLit(2^32)：9 字节，round-trip 值不变
	big, err := api.DecodeExpr(api.EncodeExpr(ast.Int(4294967296)))
	check("IntLit(2^32)", err == nil && big.I == 4294967296 &&
		len(api.EncodeExpr(ast.Int(4294967296))) == 9, fmt.Sprintf("v=%d", big.I))

	// (乙)(丙) 空串长度前缀为 0；含 \x00 名字逐字节保留
	empty := api.EncodeExpr(ast.Name(""))
	z, zerr := api.DecodeExpr(api.EncodeExpr(ast.Name("a\x00b")))
	check("strings bytewise", len(empty) == 5 && bytes.Equal(empty[1:5], []byte{0, 0, 0, 0}) &&
		zerr == nil && z.Name == "a\x00b", "")

	// If/BinOp 嵌套 round-trip 结构相等
	rt, err := api.RoundTrip(nested)
	check("nested roundtrip", err == nil && ast.Equal(rt, nested), "")

	// 四类可判定错误，互不相同
	bad := []error{
		decodeErr(api.EncodeExpr(ast.Int(1))[:3]), // 截断
		decodeErr([]byte{9}),                      // 非法标签
		decodeErr([]byte{1, 2}),                   // BoolLit 非 0/1
		decodeErr([]byte{2, 5, 0, 0, 0, 'a'}),     // 长度超余量
	}
	check("4 sentinel errors", bad[0] == codec.ErrTruncated && bad[1] == codec.ErrBadTag &&
		bad[2] == codec.ErrBadBool && bad[3] == codec.ErrBadLen, "")

	// 被拒后状态不变 + api 自检（四条不变量）
	again, aerr := api.DecodeExpr(enc)
	check("no residue+SelfCheck", aerr == nil &&
		ast.Equal(again, ast.Bin(ast.OpAdd, ast.Int(258), ast.Name("ab"))) && api.SelfCheck() == nil, "")

	// 大 m：边界由 4 字节前缀直接定位（含 \x00 的名字也逐字节保留），
	// 边界检查计数器由 codec.TestBoundaryChecksConstant 钉住
	largeOK := true
	for _, m := range []int{100, 1000, 10000} {
		name := "a" + string(bytes.Repeat([]byte{0, 'b'}, m/2))
		got, err := api.RoundTrip(ast.Name(name))
		largeOK = largeOK && err == nil && got.Name == name
	}
	check("large m O(1) boundary", largeOK, "")

	// 并发 round-trip 一致
	batch := []*ast.Expr{nested, ast.Bin(ast.OpAdd, ast.Int(258), ast.Name("ab")), ast.Name("a\x00b")}
	var wg sync.WaitGroup
	ok := true
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				for _, n := range batch {
					rt, err := api.RoundTrip(n)
					if err != nil || !ast.Equal(rt, n) {
						ok = false
					}
				}
			}
		}()
	}
	wg.Wait()
	check("concurrent roundtrip", ok, "")

	if failed {
		os.Exit(1)
	}
}
