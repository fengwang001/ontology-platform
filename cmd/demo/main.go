// Command demo 逐条验证属性级权限语义并打印 OK/FAIL。
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

var failures int

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Printf("[%s] %s\n", status, name)
}

func main() {
	check("骨架可运行", true)

	// acl：授权三态（授予/显式拒绝/撤销回默认）
	store := acl.NewStore()
	alice := acl.User("alice")
	store.Grant(alice, "name", acl.Read)
	allowed, exists := store.DirectAllowed(alice, "name", acl.Read)
	check("acl: Grant 后存在允许记录", allowed && exists)
	store.Revoke(alice, "name", acl.Read)
	_, exists = store.DirectAllowed(alice, "name", acl.Read)
	check("acl: Revoke 后记录被移除", !exists)
	store.Deny(alice, "secret", acl.Read)
	allowed, exists = store.DirectAllowed(alice, "secret", acl.Read)
	check("acl: Deny 后存在显式拒绝记录", !allowed && exists)

	// access：默认拒绝、组授权继承（含嵌套）、直接授权覆盖组授权
	store2 := acl.NewStore()
	eng := acl.Group("eng")
	engCore := acl.Group("eng-core")
	bob := acl.User("bob")
	carol := acl.User("carol")
	store2.AddMember(eng, engCore) // 嵌套：eng-core ∈ eng
	store2.AddMember(engCore, bob) // bob ∈ eng-core
	store2.Grant(eng, "title", acl.Read)
	ev := access.NewEvaluator(store2)
	check("access: 默认拒绝（无授权不可读）", !ev.Allowed(bob, "salary", acl.Read))
	check("access: 组授权经嵌套继承给成员", ev.Allowed(bob, "title", acl.Read))
	check("access: 无关系主体不继承", !ev.Allowed(carol, "title", acl.Read))
	store2.Deny(bob, "title", acl.Read)
	check("access: 直接拒绝覆盖组授权", !ev.Allowed(bob, "title", acl.Read))
	store2.Grant(bob, "title", acl.Read)
	check("access: 直接授予覆盖（优先于组）", ev.Allowed(bob, "title", acl.Read))
	store2.Revoke(eng, "title", acl.Read)
	check("access: 组授权撤销后仅直接授权生效", ev.Allowed(bob, "title", acl.Read))

	// project：读时投影移除不可见列（移除而非置零/掩码）
	store3 := acl.NewStore()
	dave := acl.User("dave")
	store3.Grant(dave, "name", acl.Read)
	store3.Grant(dave, "title", acl.Read)
	proj := project.NewProjector(access.NewEvaluator(store3))
	obj := project.Object{"name": "x", "title": "y", "secret": 42, "salary": 0}
	view := proj.Project(obj, dave)
	_, secretGone := view["secret"]
	_, salaryGone := view["salary"]
	check("project: 可见属性保留", view["name"] == "x" && view["title"] == "y")
	check("project: 不可见属性被移除（键消失）", !secretGone && !salaryGone)
	check("project: 原对象未被修改", obj["secret"] == 42)
	zeroObj := project.Object{"name": "", "title": "t"}
	zeroView := proj.Project(zeroObj, dave)
	v, namePresent := zeroView["name"]
	check("project: 零值与缺失可区分（零值保留）", namePresent && v == "")

	// api：写时强制 + 谓词探针防泄露
	store4 := acl.NewStore()
	erin := acl.User("erin")
	store4.Grant(erin, "name", acl.Read)
	store4.Grant(erin, "name", acl.Write)
	store4.Grant(erin, "title", acl.Read)
	store4.Grant(erin, "title", acl.Write)
	svc := api.NewService(store4)

	err := svc.Create(erin, "o1", map[string]any{"name": "n1", "secret": 1})
	check("api: 创建含无权限字段被拒", errors.Is(err, api.ErrWriteDenied))
	_, getErr := svc.Get(erin, "o1")
	check("api: 被拒的创建未留下对象", errors.Is(getErr, api.ErrNotFound))

	err = svc.Create(erin, "o1", map[string]any{"name": "n1", "title": "t1"})
	check("api: 有权限字段正常创建", err == nil)

	err = svc.Update(erin, "o1", map[string]any{"title": "t2", "secret": 9})
	check("api: 更新含无权限字段被拒", errors.Is(err, api.ErrWriteDenied))
	view1, _ := svc.Get(erin, "o1")
	check("api: 被拒的更新零静默丢弃（原值不变）", view1["title"] == "t1")

	err = svc.Update(erin, "o1", map[string]any{"title": "t2"})
	check("api: 有权限字段正常更新", err == nil)

	err = svc.Create(erin, "", map[string]any{"name": "x"})
	check("api: 参数校验（空对象 id）", errors.Is(err, api.ErrInvalidInput))

	notProbe := api.Predicate{Op: "not", Inner: &api.Predicate{Op: "gt", Column: "secret", Value: 10}}
	_, err = svc.Query(erin, notProbe)
	check("api: NOT(secret>10) 探针被拒", errors.Is(err, api.ErrPredicateDenied))
	nullProbe := api.Predicate{Op: "isnull", Column: "secret"}
	_, err = svc.Query(erin, nullProbe)
	check("api: secret IS NULL 探针被拒", errors.Is(err, api.ErrPredicateDenied))

	rows, err := svc.Query(erin, api.Predicate{Op: "eq", Column: "name", Value: "n1"})
	check("api: 可见列谓词正常查询", err == nil && len(rows) == 1)
	if len(rows) == 1 {
		_, leaked := rows[0]["secret"]
		check("api: 查询结果已投影（无 secret）", !leaked && rows[0]["name"] == "n1")
	}

	fmt.Printf("echo exit=%d\n", failures)
	if failures > 0 {
		os.Exit(1)
	}
}
