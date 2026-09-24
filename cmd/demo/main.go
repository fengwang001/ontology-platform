// Command demo 逐条演示转发链可信前缀裁剪器的判定。
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
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("[%s] %s: %s\n", status, name, detail)
}

func trimmer() *trim.Trimmer {
	t, err := trim.New(16, 8, []string{"10.0.0.0/8"})
	if err != nil {
		panic(err)
	}
	return t
}

func clientOf(t *trim.Trimmer, chain, fwd string) string {
	r, err := t.Trim("192.0.2.1", chain, fwd)
	if err != nil || !r.HasClient {
		return ""
	}
	return r.Client.String()
}

func main() {
	// 1. 链式：裁掉可信的 10.0.0.0/8 两跳，客户端是 1.2.3.4。
	c1 := clientOf(trimmer(), "1.2.3.4, 10.0.0.1, 10.0.0.2", "")
	check("chain-basic", c1 == "1.2.3.4", "client="+c1)

	// 2. 伪造输入：攻击者塞入的 9.9.9.9 不得成为结果。
	c2 := clientOf(trimmer(), "9.9.9.9, 1.2.3.4, 10.0.0.1", "")
	check("forged-far-hop", c2 == "1.2.3.4", "client="+c2)

	// 3. 全部可信：客户端是最远端那一跳。
	c3 := clientOf(trimmer(), "10.0.0.3, 10.0.0.1", "")
	check("all-trusted-farthest", c3 == "10.0.0.3", "client="+c3)

	// 4. 近端 for=unknown：裁剪在该跳（第 2 跳）终止，地址缺失。
	r4, err4 := trimmer().Trim("192.0.2.1", "", "for=10.0.0.1, for=unknown")
	ok4 := err4 == nil && !r4.HasClient && r4.StoppedAt == 2
	check("unknown-stops-trim", ok4, fmt.Sprintf("stoppedAt=%d hasClient=%v", r4.StoppedAt, r4.HasClient))

	// 5. IPv4 映射的 IPv6 地址被 IPv4 网段判定包含（端口已剥离）。
	mapped, err1 := netmatch.ParseAddr("[::ffff:10.0.0.1]:443")
	v4net, err2 := netmatch.ParsePrefix("10.0.0.0/8")
	ok5 := err1 == nil && err2 == nil && netmatch.Contains(v4net, mapped)
	check("mapped-v4-in-v4-cidr", ok5, "[::ffff:10.0.0.1]:443 ∈ 10.0.0.0/8")

	// 6. 键值式：引号内的分号、逗号不被切断，只取 for 项。
	hops6 := hoplex.ParseForwarded(`for="1.2.3.4";proto=https, for=10.0.0.1`)
	ok6 := len(hops6) == 2 && hops6[0].Addr == "1.2.3.4" && hops6[1].Addr == "10.0.0.1"
	check("kv-quoted-parse", ok6, fmt.Sprintf("hops=%v", hops6))

	// 7. 两种头部同时出现：右对齐合并（近端对齐，同址去重，冲突时键值式更近）。
	c7 := clientOf(trimmer(), "1.2.3.4, 10.0.0.1", "for=10.0.0.1")
	check("both-headers-merged", c7 == "1.2.3.4", "order=right-aligned-zip client="+c7)

	// 8. 跳数超上限：报错且没有任何部分结果。
	lim, _ := trim.New(1, 8, []string{"10.0.0.0/8"})
	r8, err8 := lim.Trim("192.0.2.1", "1.2.3.4, 10.0.0.1", "")
	ok8 := errors.Is(err8, trim.ErrTooManyHops) && r8 == trim.Result{}
	check("hop-limit-fails", ok8, fmt.Sprintf("err=%v result=%+v", err8, r8))

	if failed {
		os.Exit(1)
	}
	fmt.Println("ALL OK")
}
