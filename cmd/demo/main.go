// Command demo 逐条打印属性级访问控制语义的 OK/FAIL 判定。
package main

import (
	"errors"
	"fmt"
	"os"

	"ontology/access"
	"ontology/acl"
	"ontology/api"
	"ontology/project"
)

var failed bool

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s\n", status, name)
}

func main() {
	check("骨架可运行", true)

	pol := acl.NewPolicy()
	alice := acl.User("alice")
	pol.Grant(alice, "name", acl.Read)
	check("acl: 授予后持有直接授权", pol.Direct(alice, "name", acl.Read))
	pol.Revoke(alice, "name", acl.Read)
	check("acl: 撤销后授权收回", !pol.Direct(alice, "name", acl.Read))

	bob := acl.User("bob")
	devs := acl.Group("devs")
	leads := acl.Group("leads")
	pol.AddMember(devs, bob)
	pol.AddMember(leads, devs)

	ev := access.NewEvaluator(pol)
	check("access: 默认拒绝（无授权不可读）", !ev.Allowed(bob, "name", acl.Read))
	check("access: 默认拒绝（无授权不可写）", !ev.Allowed(bob, "name", acl.Write))

	pol.Grant(devs, "email", acl.Read)
	pol.Grant(leads, "salary", acl.Read)
	check("access: 组授权被成员继承", ev.Allowed(bob, "email", acl.Read))
	check("access: 嵌套组授权被继承", ev.Allowed(bob, "salary", acl.Read))
	check("access: 组授权不越界（email 不可写）", !ev.Allowed(bob, "email", acl.Write))

	pol.Grant(bob, "email", acl.Write)
	check("access: 主体直接授权优先于组（并集生效）", ev.Allowed(bob, "email", acl.Write))
	pol.Revoke(bob, "email", acl.Write)
	pol.Revoke(devs, "email", acl.Read)
	check("access: 组授权撤销后继承失效", !ev.Allowed(bob, "email", acl.Read))

	pol.Grant(bob, "name", acl.Read)
	pol.Grant(bob, "age", acl.Read)
	proj := project.NewProjector(ev, bob)
	obj := project.Object{"name": "Bob", "age": 0, "secret": "x"}
	view := proj.Apply(obj)
	_, secretGone := view["secret"]
	check("project: 不可见属性被移除", !secretGone && len(view) == 2)
	ageVal, ageOK := view["age"]
	check("project: 零值与缺失可区分", ageOK && ageVal == 0)
	check("project: 移除计数器记录被删属性", proj.Removed() == 1)
	again := proj.Apply(obj)
	check("project: 投影确定性（重复投影一致）",
		fmt.Sprint(view) == fmt.Sprint(again) && len(again) == 2)
	check("project: 输入对象不被修改", len(obj) == 3)

	sv := proj.ApplyWithSchema(obj, []string{"name", "secret"})
	check("project: 必填列被移除时降级为部分可见视图",
		sv.Partial && len(sv.Missing) == 1 && sv.Missing[0] == "secret")

	// api：写时强制与查询组装。
	apol := acl.NewPolicy()
	carol := acl.User("carol")
	staff := acl.Group("staff")
	apol.AddMember(staff, carol)
	apol.Grant(staff, "name", acl.Read)
	apol.Grant(staff, "name", acl.Write)
	apol.Grant(carol, "age", acl.Read)
	apol.Grant(carol, "age", acl.Write)
	svc := api.NewService(access.NewEvaluator(apol))

	err := svc.Create(carol, "o1", map[string]any{"name": "c", "secret": 42})
	var dfe *api.DeniedFieldsError
	check("api: 创建含无权限字段被拒并指名",
		errors.Is(err, api.ErrWriteDenied) && errors.As(err, &dfe) &&
			len(dfe.Fields) == 1 && dfe.Fields[0] == "secret")
	check("api: 拒绝的创建零物化", svc.Materialized() == 0)

	check("api: 参数校验（空 id）",
		errors.Is(svc.Create(carol, "", map[string]any{"name": "x"}), api.ErrInvalidArgument))

	mustOK := svc.Create(carol, "o1", map[string]any{"name": "c", "age": 30}) == nil
	check("api: 有权限字段正常创建（全量物化）", mustOK && svc.Materialized() == 2)

	err = svc.Update(carol, "o1", map[string]any{"age": 31, "secret": 1})
	check("api: 更新含无权限字段被拒并指名",
		errors.Is(err, api.ErrWriteDenied) && errors.As(err, &dfe) &&
			len(dfe.Fields) == 1 && dfe.Fields[0] == "secret")
	rows, qerr := svc.Query(carol, api.Cmp{Column: "age", Op: "=", Value: 31})
	check("api: 拒绝的更新零静默丢弃（age 未被部分写入）",
		qerr == nil && len(rows) == 0 && svc.Materialized() == 2)

	check("api: 有权限字段正常更新", svc.Update(carol, "o1", map[string]any{"age": 32}) == nil)

	_, err = svc.Query(carol, api.Not{Inner: api.Cmp{Column: "secret", Op: ">", Value: 10}})
	var pce *api.PredicateColumnError
	check("api: NOT(secret>10) 探针整条被拒",
		errors.Is(err, api.ErrPredicateDenied) && errors.As(err, &pce) && pce.Column == "secret")
	_, err = svc.Query(carol, api.IsNull{Column: "secret"})
	check("api: secret IS NULL 探针整条被拒",
		errors.Is(err, api.ErrPredicateDenied) && errors.As(err, &pce) && pce.Column == "secret")

	rows, qerr = svc.Query(carol, api.Cmp{Column: "age", Op: ">", Value: 10})
	check("api: 有权限谓词正常查询", qerr == nil && len(rows) == 1 && rows[0]["age"] == 32)

	if failed {
		os.Exit(1)
	}
}
