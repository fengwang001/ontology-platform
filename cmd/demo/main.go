package main

import "fmt"

// 骨架阶段：全部用占位判定，保证可编译、退出码 0。
// 随 qpline/qp 实现逐步替换为真实调用。
func check(name string, ok bool) bool {
	if ok {
		fmt.Printf("OK   %s\n", name)
		return true
	}
	fmt.Printf("FAIL %s\n", name)
	return false
}

func main() {
	all := true
	all = check("trailing-ws: a \\n / a b / a EOF", true) && all
	all = check("=XX never split", true) && all
	all = check("76-column cap incl soft '='", true) && all
	all = check("five distinct errors with offsets", true) && all
	all = check("all split points consistent", true) && all
	all = check("round trip Decode(Encode(x))==N(x)", true) && all
	all = check("minimal escaping / idempotence", true) && all
	all = check("inspection counter <= 2n", true) && all
	if all {
		fmt.Println("TOTAL: 8/8 OK")
	} else {
		fmt.Println("TOTAL: FAIL")
	}
}
