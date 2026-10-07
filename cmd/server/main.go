// Command server 演示跨对象类型权限传播引擎的基本用法。
package main

import (
	"fmt"

	"ontology/ontology"
)

func main() {
	store := ontology.NewStore()
	store.AddObjectType(ontology.ObjectType{ID: "Employee", Properties: []string{"name", "dept"}})
	store.AddObjectType(ontology.ObjectType{ID: "Project", Properties: []string{"title"}})
	store.AddLinkType(ontology.LinkType{ID: "memberOf", From: "Employee", To: "Project"})
	store.PutInstance(&ontology.Instance{ID: "e1", Type: "Employee", Props: map[string]string{"name": "alice"}, Version: 1})
	store.PutInstance(&ontology.Instance{ID: "e2", Type: "Employee", Props: map[string]string{"name": "bob"}, Version: 1})
	store.PutInstance(&ontology.Instance{ID: "p1", Type: "Project", Props: map[string]string{"title": "apollo"}, Version: 1})
	store.AddLink(ontology.Link{Type: "memberOf", From: "e1", To: "p1"})
	store.AddLink(ontology.Link{Type: "memberOf", From: "e2", To: "p1"})

	auth := ontology.AuthorizerFunc{
		VisibleFn: func(s ontology.SubjectID, id ontology.InstanceID) bool {
			return !(s == "mallory" && id == "e2") // e2 对 mallory 不可见
		},
		AuthorizeFn: func(s ontology.SubjectID, id ontology.InstanceID, depth int) bool {
			return s != "mallory"
		},
	}
	eng := ontology.NewEngine(store, auth)
	eng.RegisterAction(ontology.ActionDecl{
		ID:         "reassign",
		AllowedOps: map[ontology.ObjectTypeID][]ontology.OpKind{"Employee": {ontology.OpModify}},
		Cascade:    ontology.CascadeRule{MaxDepth: 2},
		Invisible:  ontology.InvisibleSkip,
		Merge:      ontology.MergeAll,
	})

	run := func(subject ontology.SubjectID) {
		allowed, err := eng.Execute(subject, ontology.Invocation{
			Action: "reassign",
			Ops: []ontology.DirectOp{{
				Kind: ontology.OpModify, Type: "Employee", Target: "e1",
				Props: map[string]string{"dept": "infra"},
			}},
		})
		if err != nil {
			fmt.Printf("subject=%s allowed=%v err=%v\n", subject, allowed, err)
			return
		}
		fmt.Printf("subject=%s allowed=%v\n", subject, allowed)
	}
	run("alice")
	run("mallory") // 合并规则拒绝：mallory 无任何层级被授权

	for _, rec := range eng.Log() {
		fmt.Printf("log#%d subject=%s allowed=%v err=%v checks=%d path=%v skipped=%v\n",
			rec.Seq, rec.Subject, rec.Allowed, rec.Err, rec.CheckCount, rec.Path, rec.Skipped)
	}
	snap := store.Snapshot()
	for _, id := range []ontology.InstanceID{"e1", "e2", "p1"} {
		in := snap.Instances[id]
		fmt.Printf("instance %s version=%d mark=%d props=%v\n", id, in.Version, in.CascadeMark, in.Props)
	}
}
