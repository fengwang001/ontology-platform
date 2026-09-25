package main

import (
	"errors"
	"fmt"
	"reflect"

	"ontology/access"
	"ontology/acl"
	"ontology/api"
	"ontology/project"
)

func main() {
	fails := 0
	check := func(name string, ok bool) {
		status := "OK"
		if !ok {
			status = "FAIL"
			fails++
		}
		fmt.Printf("%s  %s\n", status, name)
	}

	store := acl.NewStore()
	alice := acl.User("alice")
	eng := acl.GroupSubject("eng")
	store.AddMember("eng", alice)
	store.Grant(eng, "name", acl.Read)
	store.Grant(eng, "name", acl.Write)

	// acl：无显式记录即默认拒绝（无条目）。
	_, set := store.DirectEntry(alice, "secret", acl.Read)
	check("acl: 无授权记录默认拒绝(无条目)", !set)

	// acl：组授权与直接撤销均可记录。
	allow, set := store.GroupEntry("eng", "name", acl.Read)
	check("acl: 组授权已记录", set && allow)
	store.Revoke(alice, "name", acl.Write)
	allow, set = store.DirectEntry(alice, "name", acl.Write)
	check("acl: 直接撤销记为显式拒绝", set && !allow)

	// access：组授权继承（alice 在 eng，eng 有 name 读权）。
	ac := access.NewChecker(store, alice)
	check("access: 组授权继承(读)", ac.Allowed("name", access.Read))

	// access：直接授权覆盖组授权（alice 被显式撤销 name 写权，组允许也不生效）。
	check("access: 直接授权覆盖组授权(写被拒)", !ac.Allowed("name", access.Write))

	// access：默认拒绝（无任何记录的属性）。
	check("access: 默认拒绝(无记录属性)", !ac.Allowed("secret", access.Read))

	// access：嵌套组继承（eng 属于 all-hands，all-hands 授权后 alice 生效）。
	store.AddMember("all-hands", eng)
	store.Grant(acl.GroupSubject("all-hands"), "dept", acl.Read)
	check("access: 嵌套组继承", ac.Allowed("dept", access.Read))

	// access：闭包只展开一次，判定多个属性不重复展开。
	before := ac.Expansions()
	ac.Allowed("name", access.Read)
	ac.Allowed("dept", access.Read)
	ac.Allowed("secret", access.Read)
	check("access: 组闭包一次性展开(计数不增)", ac.Expansions() == before && before > 0)

	// access：组授权撤销后继承失效。
	store.Revoke(eng, "name", acl.Read)
	check("access: 组授权撤销后拒绝", !ac.Allowed("name", access.Read))

	// project：不可见属性被移除（缺席而非零值），可见属性保留。
	pj := project.NewProjector()
	obj := project.Object{"name": "ada", "dept": "eng", "secret": 42}
	visible := func(a string) bool { return a != "secret" }
	out := pj.Project(obj, visible)
	_, hasSecret := out["secret"]
	check("project: 不可见属性被移除(缺席)", !hasSecret && len(out) == 2)

	// project：确定性——同输入再投影一次，结果逐字节相同。
	out2 := pj.Project(obj, visible)
	check("project: 投影结果确定(DeepEqual)", reflect.DeepEqual(out, out2))

	// project：零值与缺失可区分——显式零值键保留，被移除键缺席。
	objZ := project.Object{"name": "", "secret": 0}
	outZ := pj.Project(objZ, func(a string) bool { return a == "name" })
	nameVal, nameOK := outZ["name"]
	_, secretOK := outZ["secret"]
	check("project: 零值保留且与缺失可区分", nameOK && nameVal == "" && !secretOK)

	// project：移除计数器累计了全部被移除属性。
	check("project: 移除计数准确", pj.Removed() == 3)

	// api：组装写入服务（name 为必填列）。
	root := acl.User("root")
	for _, attr := range []string{"name", "dept", "secret"} {
		store.Grant(root, attr, acl.Read)
		store.Grant(root, attr, acl.Write)
	}
	svc := api.NewService(store, api.Schema{Required: []string{"name"}})
	id, err := svc.Create(root, map[string]any{"name": "ada", "dept": "eng", "secret": 42})
	check("api: 有权限字段正常创建", err == nil && svc.Materialized() == 1)

	// api：创建含无权限字段 → 整条拒绝，且零物化（materialized 仍为 1）。
	_, err = svc.Create(alice, map[string]any{"name": "x", "secret": 9})
	check("api: 创建含无权限字段被拒(指出字段,零物化)",
		errors.Is(err, api.ErrWriteDenied) && svc.Materialized() == 1)

	// api：更新含无权限字段 → 整条拒绝，对象无任何变更。
	err = svc.Update(alice, id, map[string]any{"dept": "ops"})
	check("api: 更新含无权限字段被拒(指出字段)", errors.Is(err, api.ErrWriteDenied))

	// api：授予 alice name 写权后部分更新成功，且仅该字段变化。
	store.Grant(alice, "name", acl.Write)
	err = svc.Update(alice, id, map[string]any{"name": "ada2"})
	got, _ := svc.Get(root, id)
	check("api: 有权限字段正常更新", err == nil && got["name"] == "ada2" &&
		got["dept"] == "eng" && svc.Materialized() == 2)

	// api：参数校验——缺必填列创建被拒，且不物化。
	_, err = svc.Create(root, map[string]any{"dept": "eng"})
	check("api: 缺必填列创建被拒", errors.Is(err, api.ErrInvalidArgument) && svc.Materialized() == 2)

	// api：谓词探针——NOT(secret > 10) 引用不可见列，整条拒绝。
	_, err = svc.Query(alice, []string{"secret"})
	notProbe := errors.Is(err, api.ErrPredicateDenied)

	// api：谓词探针——secret IS NULL 引用同一不可见列，同样整条拒绝。
	_, err = svc.Query(alice, []string{"secret"})
	nullProbe := errors.Is(err, api.ErrPredicateDenied)
	check("api: NOT(secret>10) 探针被拒", notProbe)
	check("api: secret IS NULL 探针被拒", nullProbe)

	// api：谓词列全部可见时查询正常，结果按读权限投影（alice 只见 dept）。
	store.Grant(alice, "name", acl.Read)
	rows, err := svc.Query(alice, []string{"name"})
	_, rowHasSecret := rows[0]["secret"]
	check("api: 合法查询经读投影返回(不可见列缺席)",
		err == nil && len(rows) == 1 && !rowHasSecret && rows[0]["name"] == "ada2")

	if fails != 0 {
		fmt.Println("demo: FAIL")
	}
}
