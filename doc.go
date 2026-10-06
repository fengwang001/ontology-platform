// Package ontology 实现联合类型的流敏感窄化分析。
//
// 快速上手：
//
//	p := &ontology.Program{
//	    Decls: map[string]*ontology.Type{
//	        "x": ontology.TUnion(ontology.TNumber(), ontology.TString(), ontology.TNull()),
//	    },
//	    Stmts: []*ontology.Stmt{
//	        {ID: "if1", K: ontology.StmtIf,
//	            Cond: &ontology.Cond{K: ontology.CondIsType, Var: "x", Check: ontology.CheckNumber},
//	            Then: []*ontology.Stmt{{ID: "probe", K: ontology.StmtReturn}}},
//	    },
//	}
//	res, err := ontology.Analyze(context.Background(), p)
//	if err != nil {
//	    // err 的 Kind 区分四类失败，且整次分析不产生部分结果
//	    log.Fatal(err.Kind, err.StmtID, err.Detail)
//	}
//	pr, _ := res.Query("probe", "x") // 点 if1 真分支入口处 x 被窄化为 number
//
// 类型系统、窄化规则、错误优先级、不可变结果与查询复杂度的完整说明见
// DESIGN.md。
package ontology
