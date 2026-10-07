// 演示：构建引擎、登记可见性与脱敏策略、执行呈现并输出调用日志。
package main

import (
	"fmt"
	"os"
	"sort"

	"ontology/ontology"
)

func main() {
	e := ontology.NewEngine(false)
	e.SetLogger(ontology.NewJSONLogger(os.Stdout))
	e.SetSchema(map[string]ontology.AttrType{
		"name":   {Kind: ontology.KindString, MaxLen: 64},
		"ssn":    {Kind: ontology.KindString, MaxLen: 32},
		"salary": {Kind: ontology.KindInt, HasRange: true, Min: 0, Max: 1 << 40},
	})
	e.Apply(ontology.PolicyBatch{
		RegisterVisibility: []ontology.VisibilityPolicy{
			{ID: "v-name", Subject: "*", Attr: "name", Effect: ontology.Allow},
			{ID: "v-ssn", Subject: "*", Attr: "ssn", Effect: ontology.Allow},
			{ID: "v-salary", Subject: "*", Attr: "salary", Effect: ontology.Allow,
				Cond: &ontology.Condition{Attr: "name", Op: ontology.OpNeq,
					Value: ontology.StringValue("")}},
		},
		RegisterMasking: []ontology.MaskingPolicy{
			{ID: "m-ssn", Subject: "*", Attr: "ssn", Strength: 2,
				Rule: ontology.MaskingRule{Kind: ontology.RuleRedact}},
			{ID: "m-salary", Subject: "*", Attr: "salary", Strength: 1,
				Rule: ontology.MaskingRule{Kind: ontology.RuleHash}},
		},
	})

	inst := ontology.Instance{ID: "emp-1", Attrs: map[string]ontology.Value{
		"name":   ontology.StringValue("alice"),
		"ssn":    ontology.StringValue("123-45-6789"),
		"salary": ontology.IntValue(100000),
	}}
	res := e.Present("bob", inst)

	attrs := make([]string, 0, len(res.Values))
	for a := range res.Values {
		attrs = append(attrs, a)
	}
	sort.Strings(attrs)
	fmt.Fprintln(os.Stderr, "== 呈现结果 ==")
	for _, a := range attrs {
		fmt.Fprintf(os.Stderr, "%s = %v\n", a, res.Values[a].V)
	}
	for _, err := range res.Errors {
		fmt.Fprintln(os.Stderr, "ERR:", err)
	}
	fmt.Fprintf(os.Stderr, "考察策略数: %d\n", res.Examined)
}
