// demo 逐条验证 ontology 类型层级的核心语义，全部通过时退出码为 0。
package main

import (
	"errors"
	"fmt"
	"os"

	"ontology/api"
	"ontology/props"
)

var failed bool

func report(name string, ok bool, detail string) {
	status := "OK  "
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("[%s] %s: %s\n", status, name, detail)
}

func must(err error) {
	if err != nil {
		report("setup", false, err.Error())
	}
}

func propType(ps []props.Prop, name string) string {
	for _, p := range ps {
		if p.Name == name {
			return p.Type
		}
	}
	return ""
}

func main() {
	o := api.New()

	// 基础层级：Living <- Animal <- Dog
	must(o.AddType("Living", nil, nil))
	must(o.AddType("Animal", []string{"Living"}, map[string]string{
		"name": "string", "friend": "Animal",
	}))

	// 1. 合法收窄覆盖：Dog 把 friend 从 Animal 收窄为 Dog
	must(o.AddType("Dog", []string{"Animal"}, map[string]string{"friend": "Dog"}))
	dogProps, err := o.Resolve("Dog")
	report("narrow-override-ok",
		err == nil && propType(dogProps, "friend") == "Dog",
		fmt.Sprintf("Dog.friend=%s", propType(dogProps, "friend")))

	// 2. 非法放宽被拒：子类型把 friend 放宽为 Living
	err = o.AddType("BadDog", []string{"Animal"}, map[string]string{"friend": "Living"})
	report("widen-override-rejected", errors.Is(err, api.ErrOverride), fmt.Sprintf("err=%v", err))

	// 3. 菱形同类型合并：两个祖先的 id 都是 string，合法合并
	must(o.AddType("HasIdA", nil, map[string]string{"id": "string"}))
	must(o.AddType("HasIdB", nil, map[string]string{"id": "string"}))
	must(o.AddType("Merged", []string{"HasIdA", "HasIdB"}, nil))
	mergedProps, err := o.Resolve("Merged")
	report("diamond-same-type-merged",
		err == nil && propType(mergedProps, "id") == "string",
		fmt.Sprintf("Merged.id=%s", propType(mergedProps, "id")))

	// 4. 菱形兼容取更窄：Animal 与 Dog 相容，取更窄的 Dog
	must(o.AddType("PetA", nil, map[string]string{"pet": "Animal"}))
	must(o.AddType("PetB", nil, map[string]string{"pet": "Dog"}))
	must(o.AddType("PetC", []string{"PetA", "PetB"}, nil))
	petProps, err := o.Resolve("PetC")
	report("diamond-narrower-wins",
		err == nil && propType(petProps, "pet") == "Dog",
		fmt.Sprintf("PetC.pet=%s", propType(petProps, "pet")))

	// 5. 菱形冲突报错：string 与 number 互不兼容
	must(o.AddType("A2", nil, map[string]string{"v": "string"}))
	must(o.AddType("B2", nil, map[string]string{"v": "number"}))
	err = o.AddType("C2", []string{"A2", "B2"}, nil)
	report("diamond-conflict-rejected", errors.Is(err, api.ErrConflict), fmt.Sprintf("err=%v", err))

	// 6. 协变契约满足：契约要 friend:Animal，Dog 提供更窄的 Dog
	must(o.AddInterface("Friendly", map[string]string{"friend": "Animal"}))
	err = o.Check("Dog", "Friendly")
	report("covariant-contract-ok", err == nil, fmt.Sprintf("err=%v", err))

	// 7. 逆变误判被拒：契约要 friend:Dog，Animal 只提供更宽的 Animal
	must(o.AddInterface("StrictFriend", map[string]string{"friend": "Dog"}))
	err = o.Check("Animal", "StrictFriend")
	report("contravariant-rejected", errors.Is(err, api.ErrIncompatibleType), fmt.Sprintf("err=%v", err))

	// 8. 缺属性可区分：Dog 没有 code 属性，错误含属性名且可 errors.Is 区分
	must(o.AddInterface("Coded", map[string]string{"code": "string"}))
	err = o.Check("Dog", "Coded")
	report("missing-property-distinct",
		errors.Is(err, api.ErrMissingProperty) && !errors.Is(err, api.ErrIncompatibleType),
		fmt.Sprintf("err=%v", err))

	// 9. 循环继承被拒且指出环上的类型序列
	err = o.AddType("Loopy", []string{"Loopy"}, nil)
	report("cycle-rejected", errors.Is(err, api.ErrCycle), fmt.Sprintf("err=%v", err))

	if failed {
		fmt.Println("exit=1")
		os.Exit(1)
	}
	fmt.Println("exit=0")
}
