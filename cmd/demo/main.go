package main

import (
	"fmt"
	"ontology/api"
	"ontology/lex"
	"ontology/parse"
	"strings"
	"sync"
)

func line(name string, ok bool) {
	if ok {
		fmt.Println(name + ": OK")
	} else {
		fmt.Println(name + ": FAIL")
	}
}

// postorder 收集 AST 后序遍历每个节点的求值，演示第三节的七步构建。
func postorder(n *parse.Node, out *[]int64) {
	if n == nil {
		return
	}
	postorder(n.Left, out)
	postorder(n.Right, out)
	v, _ := api.Eval(n)
	*out = append(*out, v)
}

func main() {
	// 1. 第三节：8/2/2-3 后序七步每步节点的值
	n, _ := api.Parse("8/2/2-3")
	var seq []int64
	postorder(n, &seq)
	want := []int64{8, 2, 4, 2, 2, 3, -1}
	stepOK := len(seq) == 7
	for i := range want {
		stepOK = stepOK && seq[i] == want[i]
	}
	line(fmt.Sprintf("seven-step %v", seq), stepOK)

	// 2. 六个基准表达式
	exprs := []string{"8/2/2-3", "2+3*4", "-7/2", "8-3-2", "- -3", "(1+2)*3"}
	wants := []int64{-1, 14, -3, 3, 3, 9}
	vals := make([]int64, len(exprs))
	evalOK := true
	for i, s := range exprs {
		v, err := api.ParseAndEval(s)
		vals[i], evalOK = v, evalOK && err == nil && v == wants[i]
	}
	line(fmt.Sprintf("evals %v", vals), evalOK)

	// 3-6. 四类互不相同的可判定错误
	_, e1 := api.ParseAndEval("1@2")
	_, e2 := api.ParseAndEval("(1+2")
	_, e3 := api.ParseAndEval("1/0")
	_, e4a := api.ParseAndEval("")
	_, e4b := api.ParseAndEval("1 2")
	line("err illegal", e1 == lex.ErrIllegalChar)
	line("err paren", e2 == parse.ErrParen)
	line("err divzero", e3 == api.ErrDivZero)
	line("err empty/trailing", e4a == parse.ErrEmpty && e4b == parse.ErrTrailing)

	// 7. 被拒后不留痕：再求正常值仍稳定
	v, _ := api.ParseAndEval("(1+2)*3")
	line("state unchanged after reject", v == 9 && api.SelfCheck() == nil)

	// 8. 大 m 下前瞻缓冲峰值 <= 1
	big := strings.Repeat("1+", 5000) + "1"
	line("ll1 peak<=1 (m=5001)", parse.VerifyLL1(big))

	// 9. 并发求值逐值一致
	const ng = 64
	var wg sync.WaitGroup
	results := make([][]int64, ng)
	conOK := true
	for g := 0; g < ng; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			r := make([]int64, len(exprs))
			for i, s := range exprs {
				r[i], _ = api.ParseAndEval(s)
			}
			results[g] = r
		}(g)
	}
	wg.Wait()
	for g := 1; g < ng; g++ {
		for i := range wants {
			conOK = conOK && results[g][i] == results[0][i]
		}
	}
	line("concurrent consistent", conOK)
}
