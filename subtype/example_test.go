package subtype_test

import (
	"fmt"

	"ontology/subtype"
)

// Example 演示登记递归命名类型并判定子类型关系。
func Example() {
	r := subtype.NewRegistry()
	_ = r.Register("A", subtype.Obj(subtype.RO("next", subtype.Ref{Name: "A"})))
	_ = r.Register("B", subtype.Obj(subtype.RO("next", subtype.Ref{Name: "B"})))

	ok, _ := r.Check(subtype.Ref{Name: "A"}, subtype.Ref{Name: "B"})
	fmt.Println(ok)
	ok, _ = r.Check(subtype.Int{}, subtype.Float{})
	fmt.Println(ok)
	ok, _ = r.Check(subtype.Float{}, subtype.Int{})
	fmt.Println(ok)
	// Output:
	// true
	// true
	// false
}
