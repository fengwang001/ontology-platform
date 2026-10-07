# 联合仲裁子系统 — 最小用法

```go
package main

import (
	"fmt"

	"ontology/ontology"
)

func main() {
	a := ontology.NewArbiter()

	// 建模：Person.employer 与 Company.employee 为链接来源属性；
	// 一个人最多受雇于 1 家公司（起点基数 1），一家公司最多 2 名雇员（终点基数 2）。
	a.Apply(ontology.RegisterObjectType{Name: "Person", Attrs: []ontology.AttrSpec{
		{Name: "employer", Default: ontology.Visible},
	}})
	a.Apply(ontology.RegisterObjectType{Name: "Company", Attrs: []ontology.AttrSpec{
		{Name: "employee", Default: ontology.Visible},
	}})
	a.Apply(ontology.RegisterLinkType{Spec: ontology.LinkTypeSpec{
		Name: "employs",
		SrcType: "Person", SrcAttr: "employer",
		TgtType: "Company", TgtAttr: "employee",
		SrcMax: 1, TgtMax: 2,
	}})

	a.Apply(ontology.CreateInstance{ID: "p1", TypeName: "Person"})
	a.Apply(ontology.CreateInstance{ID: "c1", TypeName: "Company"})

	// 创建：参数 → 两端权限 → 起点基数 → 终点基数，按序短路。
	r := a.CreateLink(ontology.CreateLink{
		ID: "L1", TypeName: "employs", SrcID: "p1", TgtID: "c1", Operator: "alice",
	})
	fmt.Println(r.Err) // nil 表示成功；失败时 r.Err.Code 为归一化错误类别

	// 角色层级与覆盖：R1 包含 R2，alice 属于 R1；在 p1.employer 上
	// 给 R2 一个收紧覆盖（沿层级距离 2 生效，可放宽也可收紧）。
	a.Apply(ontology.AddRole{Role: "R1"})
	a.Apply(ontology.AddRole{Role: "R2"})
	a.Apply(ontology.IncludeRole{Child: "R1", Parent: "R2"})
	a.Apply(ontology.AssignRole{Operator: "alice", Role: "R1"})
	a.Apply(ontology.DeclareOverride{
		InstanceID: "p1", Attr: "employer",
		Kind: ontology.SubjectRole, Subject: "R2", Vis: ontology.Invisible,
	})

	// 收紧后链接对 alice 立即隐藏，但物理链接与基数占用不变。
	fmt.Println(a.VisibleLinks("alice")) // []
	fmt.Println(a.PhysicalLinks())       // [L1 ...]

	// 删除：不可见与不存在返回完全相同的 ERR_NOT_FOUND。
	d := a.DeleteLink(ontology.DeleteLink{ID: "L1", Operator: "alice"})
	fmt.Println(d.Err.Code) // ERR_NOT_FOUND（实际链接存在但对 alice 不可见）
}
```

所有命令都是纯数据结构（`ontology.Command` 接口的实现），可以序列化后发给单一串行仲裁点，
也可以直接交给 `ontology.Replay(cmds)` 原样重放。
