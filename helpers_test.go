package ontology_test

import (
	"fmt"
	"testing"

	"ontology"
	"ontology/naive"
)

// 测试统一使用的对象类型：
//
//	主键 id；数值属性 amount(int)；两个分组属性 region/category(string)。
//
// 上挂两个聚合视图，使一次提交天然可能同时影响两个视图。
const (
	typeOrder      = "Order"
	viewByRegion   = "sum_by_region"
	viewByCategory = "sum_by_category"
)

func orderSchema() ontology.ObjectTypeSchema {
	return ontology.ObjectTypeSchema{
		Name:     typeOrder,
		KeyField: "id",
		Attrs: map[string]ontology.ValueType{
			"amount":   ontology.TypeInt,
			"region":   ontology.TypeString,
			"category": ontology.TypeString,
		},
	}
}

func newKernel(t testing.TB) *ontology.Kernel {
	t.Helper()
	k := ontology.NewKernel()
	k.RegisterType(orderSchema())
	if err := k.RegisterView(ontology.AggregateDef{
		Name: viewByRegion, SourceType: typeOrder, GroupBy: "region", ValueField: "amount",
	}); err != nil {
		t.Fatalf("register view: %v", err)
	}
	if err := k.RegisterView(ontology.AggregateDef{
		Name: viewByCategory, SourceType: typeOrder, GroupBy: "category", ValueField: "amount",
	}); err != nil {
		t.Fatalf("register view: %v", err)
	}
	return k
}

func newNaive(t testing.TB) *naive.Model {
	t.Helper()
	m := naive.New()
	m.RegisterType(orderSchema())
	if err := m.RegisterView(ontology.AggregateDef{
		Name: viewByRegion, SourceType: typeOrder, GroupBy: "region", ValueField: "amount",
	}); err != nil {
		t.Fatalf("naive register: %v", err)
	}
	if err := m.RegisterView(ontology.AggregateDef{
		Name: viewByCategory, SourceType: typeOrder, GroupBy: "category", ValueField: "amount",
	}); err != nil {
		t.Fatalf("naive register: %v", err)
	}
	return m
}

func attrs(amount float64, region, category string) map[string]ontology.Value {
	return map[string]ontology.Value{
		"amount":   {Num: amount, Type: ontology.TypeInt},
		"region":   {Str: region, Type: ontology.TypeString},
		"category": {Str: category, Type: ontology.TypeString},
	}
}

// kindOf 归一化两边错误的类别，便于按「错误类别」而非错误文案对照。
func kindOf(err error) ontology.ErrorKind {
	if err == nil {
		return ""
	}
	if e, ok := err.(*ontology.Error); ok {
		return e.Kind
	}
	return "unexpected_error"
}

// trace 打印每次操作的输入、实际输出与判定依据，同时写入测试日志，
// 满足「测试过程中的每次操作须打印输入、实际输出与据以判定的依据」。
func trace(t testing.TB, opno int, input, actual, basis string) {
	t.Helper()
	line := fmt.Sprintf("[op %03d] IN:  %s\n         OUT: %s\n         判定依据: %s", opno, input, actual, basis)
	fmt.Println(line)
	t.Log(line)
}

func errLabel(err error) string {
	if err == nil {
		return "<nil>"
	}
	return string(kindOf(err)) + "(" + err.Error() + ")"
}
