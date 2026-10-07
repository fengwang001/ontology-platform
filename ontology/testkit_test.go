package ontology

import (
	"fmt"
	"strings"
	"testing"
)

// testType 构建测试用对象类型：
//   - 字段 v（int64）、name（string，可选）
//   - 聚合 cnt（条数）、sum_v（v 之和）
//
// 钩子行为通过策略函数注入，保证真实实现与朴素模型共用同一套钩子。
const testTypeName = "Widget"

func testRegistry(pre PreHook, post PostHook) *Registry {
	ot := &ObjectType{
		Name: testTypeName,
		Fields: map[string]FieldSchema{
			"v":    {Type: TypeInt},
			"name": {Type: TypeString},
		},
		Aggregates: []AggregateDef{
			{Name: "cnt", Field: "v", Method: AggCount},
			{Name: "sum_v", Field: "v", Method: AggSum},
		},
		Pre:  pre,
		Post: post,
	}
	return NewRegistry().Register(ot)
}

func rec(pk string, v int64) Record {
	return Record{PK: pk, Fields: map[string]Value{"v": v}}
}

func recNamed(pk string, v int64, name string) Record {
	return Record{PK: pk, Fields: map[string]Value{"v": v, "name": name}}
}

// 打印辅助：满足「打印输入、实际输出与判定依据」。
func dump(t *testing.T, title, input string, got, want any, basis string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("\n===== " + title + " =====\n")
	b.WriteString("INPUT : " + input + "\n")
	b.WriteString(fmt.Sprintf("ACTUAL: %+v\n", got))
	b.WriteString(fmt.Sprintf("EXPECT: %+v\n", want))
	b.WriteString("BASIS : " + basis + "\n")
	t.Log(b.String())
}

func recsToStrings(rs []Record) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = fmt.Sprintf("#%d %s v=%v", i, r.PK, r.Fields["v"])
	}
	return out
}

func resultsSummary(rs []RecordResult) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		if r.Err != nil {
			out[i] = fmt.Sprintf("#%d %s %s %s/%s",
				i, r.PK, r.Status, r.Err.Kind, r.Err.Code)
		} else {
			out[i] = fmt.Sprintf("#%d %s %s", i, r.PK, r.Status)
		}
	}
	return out
}
