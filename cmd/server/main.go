// Command server 演示对象类型注册、全图引用扫描与原子重命名。
package main

import (
	"fmt"
	"log/slog"
	"os"

	"ontology/ontology"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	reg := ontology.NewRegistry(logger)

	must(reg.AddType(&ontology.ObjectType{
		Name:       "Customer",
		Properties: []ontology.Property{{Name: "name", Type: ontology.PrimitiveString}},
	}))
	must(reg.AddType(&ontology.ObjectType{
		Name: "Order",
		Properties: []ontology.Property{
			{Name: "id", Type: ontology.PrimitiveString},
			{Name: "customer", Type: ontology.PrimitiveObjectRef, RefType: "Customer"},
		},
		Links: []ontology.LinkType{{Name: "placed_by", SourceType: "Order", TargetType: "Customer"}},
		Actions: []ontology.Action{{
			Name:        "escalate",
			Params:      []ontology.ActionParam{{Name: "customer", TypeRef: "Customer"}},
			ReturnRefs:  []string{"Customer"},
			PropertyRef: []ontology.PropertyRef{{TypeName: "Customer", PropertyName: "name"}},
		}},
	}))

	fmt.Println("== 重命名前 Customer 的引用位置 ==")
	for _, ref := range reg.References("Customer") {
		fmt.Printf("  %s %s %s\n", ref.HolderType, ref.Kind, ref.Location)
	}

	must(reg.Rename("Customer", "Client"))

	if _, err := reg.Resolve("Customer"); err != nil {
		fmt.Println("== 旧名已失效 ==", err)
	}
	if err := reg.Rename("Order", "Client"); err != nil {
		fmt.Println("== 重名被拒绝 ==", err)
	}
	must(reg.Validate())
	fmt.Println("== 校验通过：全图无悬空引用 ==")
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}
