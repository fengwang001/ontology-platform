// Command demo 用一个最小可运行场景演示派生索引一致性子系统：
//
//	Dept.code  <-(memberOf)- Manager.deptCode
//	Manager.deptCode <-(reportsTo)- Employee.mgrDept（两级传递）
//
// 运行：go run ./cmd/demo
package main

import (
	"fmt"
	"os"

	"ontology/ontology"
)

func main() {
	s := ontology.NewSchema()
	must(s.RegisterObjectType("Dept", []ontology.PropertyName{"code"}))
	must(s.RegisterObjectType("Manager", []ontology.PropertyName{"deptCode"}))
	must(s.RegisterObjectType("Employee", []ontology.PropertyName{"mgrDept"}))
	must(s.RegisterLinkType(ontology.LinkType{Name: "memberOf", SrcType: "Manager", DstType: "Dept"}))
	must(s.RegisterLinkType(ontology.LinkType{Name: "reportsTo", SrcType: "Employee", DstType: "Manager"}))
	must(s.RegisterDerivedIndex(ontology.DerivedIndex{
		SrcType: "Manager", PropName: "deptCode", Link: "memberOf", DstProp: "code"}))
	must(s.RegisterDerivedIndex(ontology.DerivedIndex{
		SrcType: "Employee", PropName: "mgrDept", Link: "reportsTo", DstProp: "deptCode"}))

	st := ontology.NewStore(s)
	st.Journal().SetOutput(os.Stdout)

	must(st.CreateInstance("d1", "Dept", map[ontology.PropertyName]string{"code": "D1"}))
	must(st.CreateInstance("m1", "Manager", nil))
	must(st.CreateInstance("e1", "Employee", nil))

	fmt.Println("== 建立链接，派生索引立即物化（含两级传递） ==")
	_, _ = st.AddLink("memberOf", "m1", "d1")
	_, _ = st.AddLink("reportsTo", "e1", "m1")
	printQuery(st, "Employee", "mgrDept", "D1")

	fmt.Println("== 源属性变化，同一单元内穿过两级传播 ==")
	_, _ = st.SetAttribute("d1", "code", ontology.PropertyValue{Val: "D9", Has: true})
	printQuery(st, "Employee", "mgrDept", "D9")

	fmt.Println("== 第二条 memberOf 边使唯一性失效，立即不可索引 ==")
	must(st.CreateInstance("d2", "Dept", map[ontology.PropertyName]string{"code": "D2"}))
	_, _ = st.AddLink("memberOf", "m1", "d2")
	stt, _ := st.Resolve("m1", "deptCode")
	fmt.Printf("m1.deptCode status = %s (0=indexable,1=missing,2=not-unique,3=no-value)\n",
		statusName(stt.Status))
}

func printQuery(st *ontology.Store, t, p, v string) {
	rows, err := st.QueryIndex(ontology.ObjectTypeName(t), ontology.PropertyName(p), v)
	must(err)
	fmt.Printf("query %s.%s=%s -> %d row(s)", t, p, v, len(rows))
	for _, r := range rows {
		fmt.Printf(" %s", r.ID)
	}
	fmt.Println()
}

func statusName(s ontology.IndexStatus) string {
	switch s {
	case ontology.StateIndexable:
		return "indexable"
	case ontology.StateMissingSource:
		return "missing-source"
	case ontology.StateNotUnique:
		return "not-unique"
	case ontology.StateNoValue:
		return "no-value"
	default:
		return "unknown"
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
