// 示例：一个带副作用模块、一个无副作用模块、一条通配重导出链与一条纯副作用导入。
package main

import (
	"fmt"

	"ontology/dce/analyzer"
	"ontology/dce/model"
	"ontology/dce/registry"
)

func main() {
	reg := registry.New()

	must(reg.Register(&model.Module{
		ID: "app",
		Decls: []model.Declaration{
			{Name: "main", SideEffect: true, Refs: []string{"u"}},
		},
		Imports: []model.Import{
			{Target: "util", Bindings: []model.ImportBinding{{Local: "u", Imported: "utilFn"}}},
			{Target: "polyfill"}, // 纯副作用导入
		},
		Exports: []model.Export{{Kind: model.ExportLocal, LocalName: "main"}},
	}))

	must(reg.Register(&model.Module{
		ID:         "util",
		SideEffect: model.SideEffectNo,
		Decls: []model.Declaration{
			{Name: "utilFn", Refs: []string{"helper"}},
			{Name: "helper"},
			{Name: "unused"},
		},
		Exports: []model.Export{
			{Kind: model.ExportLocal, LocalName: "utilFn"},
			{Kind: model.ExportLocal, LocalName: "helper"},
			{Kind: model.ExportLocal, LocalName: "unused"},
			{Kind: model.ExportStarReexport, Target: "sub"},
		},
	}))

	must(reg.Register(&model.Module{
		ID:         "sub",
		SideEffect: model.SideEffectNo,
		Decls:      []model.Declaration{{Name: "subFn"}},
		Exports:    []model.Export{{Kind: model.ExportLocal, LocalName: "subFn"}},
	}))

	must(reg.Register(&model.Module{
		ID:    "polyfill",
		Decls: []model.Declaration{{Name: "install", SideEffect: true}},
	}))

	sess := reg.NewSession([]string{"app"})
	res, err := sess.Solve(analyzer.WithLogger(analyzer.NewTextLogger(nil)))
	must(err)

	fmt.Println("included:", res.Included)
	for _, k := range res.Kept {
		fmt.Printf("kept %s/%s %s\n", k.Key.Module, k.Key.Decl, k.Reason)
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
