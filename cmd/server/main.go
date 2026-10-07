// Command server 是嵌套动作事务与校验钩子子系统的演示入口：
// 注册对象类型、动作定义（含嵌套调用）与校验钩子，
// 依次演示成功提交、前置钩子失败回退与后置钩子聚合失败回退，
// 并打印钩子触发记录。
package main

import (
	"errors"
	"fmt"

	"ontology/engine"
	"ontology/errs"
	"ontology/hooks"
	"ontology/spec"
)

func main() {
	e := engine.New()
	e.RegisterType("document", spec.Schema{Fields: map[string]spec.FieldType{
		"title": spec.FieldString,
		"views": spec.FieldInt,
	}})
	e.RegisterType("comment", spec.Schema{Fields: map[string]spec.FieldType{
		"body": spec.FieldString,
	}})

	e.RegisterPreHook("document", "title-required", func(ctx hooks.Context) error {
		if ctx.Write.Op != spec.OpDelete {
			if title, ok := ctx.Write.Fields["title"].(string); ok && title == "" {
				return errors.New("document title must not be empty")
			}
		}
		return nil
	})
	e.RegisterPreHook("comment", "comment-cap", func(ctx hooks.Context) error {
		if ctx.Write.Op == spec.OpCreate && ctx.State.Count("comment") >= 2 {
			return fmt.Errorf("comment cap reached (%d)", ctx.State.Count("comment"))
		}
		return nil
	})
	e.RegisterPostHook("document", "at-least-one-doc", func(ctx hooks.Context) error {
		if ctx.State.Count("document") == 0 {
			return errors.New("at least one document must exist")
		}
		return nil
	})
	e.RegisterPostHook("comment", "comment-budget", func(ctx hooks.Context) error {
		if ctx.State.Count("comment") > 2 {
			return fmt.Errorf("too many comments: %d", ctx.State.Count("comment"))
		}
		return nil
	})

	must := func(def spec.ActionDef) {
		if err := e.RegisterAction(def); err != nil {
			panic(err)
		}
	}
	must(spec.ActionDef{Name: "add-comment", Body: []spec.Op{
		{Write: &spec.Write{Type: "comment", ID: "c-new", Op: spec.OpCreate,
			Fields: map[string]any{"body": "nested write"}}},
	}})
	must(spec.ActionDef{Name: "publish", Body: []spec.Op{
		{Write: &spec.Write{Type: "document", ID: "d-1", Op: spec.OpCreate,
			Fields: map[string]any{"title": "hello", "views": 0}}},
		{Call: "add-comment"},
		{Write: &spec.Write{Type: "document", ID: "d-1", Op: spec.OpUpdate,
			Fields: map[string]any{"views": 1}}},
	}})
	must(spec.ActionDef{Name: "bad-title", Body: []spec.Op{
		{Write: &spec.Write{Type: "document", ID: "d-2", Op: spec.OpCreate,
			Fields: map[string]any{"title": "", "views": 0}}},
	}})
	must(spec.ActionDef{Name: "delete-all", Body: []spec.Op{
		{Write: &spec.Write{Type: "document", ID: "d-1", Op: spec.OpDelete}},
	}})

	run := func(action string) {
		err := e.Execute(action)
		fmt.Printf("== Execute(%s)\n", action)
		if err == nil {
			fmt.Println("   结果: committed")
		} else {
			fmt.Printf("   结果: rejected (%v)\n   详情: %v\n", errs.KindOf(err), err)
		}
		fmt.Printf("   当前状态: documents=%d comments=%d\n\n",
			e.State().Count("document"), e.State().Count("comment"))
	}

	run("publish")
	run("bad-title")
	run("delete-all")

	fmt.Println("== 钩子触发记录 ==")
	for _, r := range e.HookLog() {
		fmt.Println("  " + r.String())
	}
}
