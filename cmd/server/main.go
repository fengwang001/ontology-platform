// server 是访问控制模块的演示程序：构建一个小型本体，
// 执行若干访问判定并以 JSON 日志输出每次判定的输入、输出与裁决依据。
package main

import (
	"fmt"
	"log/slog"
	"os"

	"ontology/ontology"
)

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "声明失败:", err)
		os.Exit(1)
	}
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	e := ontology.New(logger)

	// 对象类型网络：文档 --所属--> 项目 --所属--> 部门。
	for _, ot := range []string{"Document", "Project", "Department"} {
		check(e.AddObjectType(ot))
	}
	check(e.AddLinkType("belongsTo"))
	check(e.AddLinkEdge(ontology.LinkEdge{LinkType: "belongsTo", From: "Document", To: "Project"}))
	check(e.AddLinkEdge(ontology.LinkEdge{LinkType: "belongsTo", From: "Project", To: "Department"}))

	// 标签挂在部门上，沿 belongsTo 逆向上游传播到项目与文档。
	check(e.AddTag("Confidential"))
	check(e.AttachTag(ontology.Attachment{ObjectType: "Department", Tag: "Confidential"}))
	check(e.AddPropagation(ontology.Propagation{Tag: "Confidential", LinkType: "belongsTo", Direction: ontology.Upstream}))

	// 角色层级：实习生 <- 员工 <- 部门主管。
	check(e.AddRole("intern"))
	check(e.AddRole("employee", "intern"))
	check(e.AddRole("manager", "employee"))
	check(e.AddGrant(ontology.Grant{Role: "manager", Tag: "Confidential", Effect: ontology.Allow}))
	check(e.AddGrant(ontology.Grant{Role: "intern", Tag: "Confidential", Effect: ontology.Deny}))

	check(e.AddSubject("alice", "manager"))
	check(e.AddSubject("bob", "employee"))
	check(e.AddInstance("doc-1", "Document"))

	fmt.Fprintf(os.Stderr, "alice(manager) -> doc-1: %+v\n", e.Decide("alice", "doc-1").Allowed)
	fmt.Fprintf(os.Stderr, "bob(employee)  -> doc-1: %+v\n", e.Decide("bob", "doc-1").Allowed)
}
