package main

import (
	"fmt"

	"ontology"
)

// 这是一个进程内演示（无网络依赖）：完整走一遍
// 建类型 -> 建链接类型 -> 实例 -> 授权 -> 创建/收紧/删除 的仲裁链路。
func main() {
	p := ontology.NewPlatform()

	p.DefineObjectType("Doc", []ontology.PropertySpec{
		{Name: "docRef", LinkSource: true, DefaultVis: ontology.Invisible},
	})
	p.DefineObjectType("Person", []ontology.PropertySpec{
		{Name: "personRef", LinkSource: true, DefaultVis: ontology.Invisible},
	})
	must(p.DefineLinkType(ontology.LinkTypeSpec{
		Name:      "authored",
		Source:    ontology.EndpointSpec{ObjectType: "Doc", Property: "docRef"},
		Target:    ontology.EndpointSpec{ObjectType: "Person", Property: "personRef"},
		SourceMax: 1,
		TargetMax: 1,
	}))
	must(p.CreateInstance("d1", "Doc"))
	must(p.CreateInstance("d2", "Doc"))
	must(p.CreateInstance("p1", "Person"))
	must(p.CreateInstance("p2", "Person"))

	link := ontology.Link{LinkType: "authored", SourceInstance: "d1", TargetInstance: "p1"}

	report("无权限创建（默认不可见）", p.CreateLink("alice", "authored", "d1", "p1"))

	// alice 通过 junior -> senior 角色层级间接获得可见性。
	p.AddActorRole("alice", "junior")
	p.AddRoleContains("junior", "senior")
	p.GrantRole("senior", "d1", "docRef", ontology.Visible)
	p.GrantActor("alice", "p1", "personRef", ontology.Visible)
	report("经角色继承 + 直接覆盖后创建", p.CreateLink("alice", "authored", "d1", "p1"))

	// 基数已满：bob 有权限时得到基数错误；无权限的 carol 得到权限错误。
	p.GrantActor("bob", "d1", "docRef", ontology.Visible)
	p.GrantActor("bob", "p1", "personRef", ontology.Visible)
	p.GrantActor("bob", "d2", "docRef", ontology.Visible)
	p.GrantActor("bob", "p2", "personRef", ontology.Visible)
	report("有权限但起点基数超限", p.CreateLink("bob", "authored", "d1", "p2"))
	report("无权限优先于基数超限", p.CreateLink("carol", "authored", "d2", "p1"))

	// 收紧 alice：链接隐藏但物理仍在，bob 仍可见，基数不释放。
	p.GrantRole("senior", "d1", "docRef", ontology.Invisible)
	fmt.Printf("收紧后 alice 可见=%v, bob 可见=%v, 物理存在=%v, 起点占用=%d\n",
		p.LinkVisible("alice", link), p.LinkVisible("bob", link),
		p.LinkExists(link), p.SourceCount("authored", "d1"))

	// 删除：不可见与不存在返回同一错误类别；可见者删除成功。
	report("alice 删除不可见链接(=not_found)", p.DeleteLink("alice", "authored", "d1", "p1"))
	report("删除确实不存在的链接(=not_found)", p.DeleteLink("bob", "authored", "d2", "p2"))
	report("bob 正常删除", p.DeleteLink("bob", "authored", "d1", "p1"))
}

func report(stage string, err *ontology.DecisionError) {
	if err == nil {
		fmt.Printf("%-32s => ok\n", stage)
		return
	}
	fmt.Printf("%-32s => %s (%s)\n", stage, err.Class, err.Message)
}

func must(err *ontology.DecisionError) {
	if err != nil {
		panic(err)
	}
}
