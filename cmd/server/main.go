// 演示入口：构造类型层级，执行若干写入与规则变更，打印判定日志。
package main

import (
	"fmt"

	"ontology/ontology"
)

func main() {
	c := ontology.NewCoordinator()

	must(c.CreateType("asset", "", map[string]ontology.Rule{
		"name":   {AllowRoles: []string{"*"}},
		"secret": {AllowRoles: []string{"admin"}},
	}, []string{"name"}))
	must(c.CreateType("server", "asset", nil, nil))
	must(c.CreateType("dbserver", "server", nil, nil))
	// server 显式放宽 secret：所有人可写，此后 asset 的变更不再影响它。
	must(c.SetRule("server", "secret", ontology.Rule{AllowRoles: []string{"*"}}))

	must(c.CreateObject("db-1", "dbserver", map[string]any{"name": "db-1", "secret": "s3cr3t"}))

	// 正常写入：继承 server 的覆盖规则，guest 可写 secret。
	v, err := c.Write("db-1", 1, map[string]any{"secret": "rotated"}, "guest")
	report("write secret (inherit override)", v, err)

	// 版本过期 + 权限不足同时成立：必须先报版本冲突。
	must(c.SetRule("server", "secret", ontology.Rule{}))
	v, err = c.Write("db-1", 1, map[string]any{"secret": "x"}, "guest")
	report("stale version + denied", v, err)

	// 版本正确但权限不足：报权限拒绝。
	v, err = c.Write("db-1", 2, map[string]any{"secret": "x"}, "guest")
	report("permission denied", v, err)

	// 重组到根类型下：未覆盖的 name 仍继承 asset，secret 因覆盖历史不受影响。
	must(c.Reparent("dbserver", "asset"))
	v, err = c.Write("db-1", 2, map[string]any{"name": "db-1b"}, "guest")
	report("write after reparent", v, err)

	fmt.Println("\n== decision log ==")
	for i, d := range c.Log().Entries() {
		fmt.Printf("[%02d] op=%-12s input=%-40s output=%-22s basis=%s\n",
			i, d.Op, d.Input, d.Output, d.Basis)
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func report(label string, version int64, err error) {
	if err != nil {
		fmt.Printf("%-34s -> rejected: %v\n", label, err)
		return
	}
	fmt.Printf("%-34s -> ok, new version=%d\n", label, version)
}
