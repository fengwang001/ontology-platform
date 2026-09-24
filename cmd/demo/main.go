package main

import (
	"errors"
	"fmt"
	"os"

	"ontology/hoplex"
	"ontology/netmatch"
	"ontology/trim"
)

var failed bool

func check(name string, ok bool, detail string) {
	status := "OK  "
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s: %s\n", status, name, detail)
}

func mustTrimmer(maxHops int, cidrs ...string) *trim.Trimmer {
	t, err := trim.New(maxHops, 8, cidrs)
	if err != nil {
		fmt.Println("FAIL trimmer build:", err)
		os.Exit(1)
	}
	return t
}

func main() {
	t1 := mustTrimmer(8, "10.0.0.0/8")
	r1, e1 := t1.Client("", "1.2.3.4, 10.0.0.1, 10.0.0.2", "")
	check("chain trim", e1 == nil && r1.HasAddr && r1.Addr.String() == "1.2.3.4",
		"链式 1.2.3.4, 10.0.0.1, 10.0.0.2 => 客户端 1.2.3.4")

	r2, e2 := t1.Client("", "9.9.9.9, 1.2.3.4, 10.0.0.1", "")
	check("forged prefix ignored", e2 == nil && r2.Addr.String() == "1.2.3.4",
		"伪造输入 9.9.9.9, 1.2.3.4, 10.0.0.1 => 客户端 1.2.3.4（不是 9.9.9.9）")

	r3, e3 := t1.Client("", "10.0.0.1, 10.0.0.2", "")
	check("all trusted", e3 == nil && r3.Addr.String() == "10.0.0.1" && r3.Stop == 0,
		"全部可信 => 客户端是最远端 10.0.0.1")

	r4, e4 := t1.Client("", "", "for=1.2.3.4, for=unknown")
	check("missing hop stops", e4 == nil && !r4.HasAddr && r4.Stop == 1,
		"近端 for=unknown => 裁剪终止于第 2 跳（0 基索引 1），地址缺失")

	p5, _ := netmatch.ParseCIDR("10.0.0.0/8")
	a5, e5 := netmatch.ParseAddr("[::ffff:10.0.0.1]:443")
	check("mapped-v6 in v4 cidr", e5 == nil && netmatch.Contains(p5, a5),
		"[::ffff:10.0.0.1]:443 被 10.0.0.0/8 判定为可信")

	kv := hoplex.ParseForwarded(`for="1.2.3.4";proto=https, for=10.0.0.1`)
	ok6 := len(kv) == 2 && kv[0].Addr == "1.2.3.4" && kv[1].Addr == "10.0.0.1"
	check("forwarded kv parse", ok6,
		`for="1.2.3.4";proto=https, for=10.0.0.1 => 2 跳，引号内未被切断`)

	r7, e7 := t1.Client("", "8.8.8.8", "for=10.0.0.1")
	check("both headers merged", e7 == nil && r7.Addr.String() == "8.8.8.8",
		"两种头部同时出现 => 合并次序：链式（较远）+ 键值式（较近），客户端 8.8.8.8")

	t8 := mustTrimmer(1, "10.0.0.0/8")
	r8, e8 := t8.Client("", "1.2.3.4, 10.0.0.1", "")
	check("hop limit exceeded", errors.Is(e8, trim.ErrTooManyHops) && !r8.HasAddr,
		"跳数超上限 => 报超出跳数上限，无部分结果")

	if failed {
		os.Exit(1)
	}
	fmt.Println("ALL OK")
}
