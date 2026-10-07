package validate_test

import (
	"context"
	"errors"
	"fmt"

	"ontology/ontology/validate"
)

// ObjectType 与 LinkType 演示同一套机制如何分别挂载到两类本体元素。
type ObjectType struct {
	Name       string
	Properties []string
}

type LinkType struct {
	Name      string
	SourceKey string
	TargetKey string
}

func Example_objectTypeAndLinkType() {
	ctx := context.Background()

	// 对象类型：结构组（短路）+ 语义组（不短路，汇总）。
	objects, err := validate.NewRegistry[*ObjectType](
		validate.GroupSpec{Name: "structural", Priority: 100, ShortCircuit: true},
		validate.GroupSpec{Name: "semantic", Priority: 10, ShortCircuit: false},
	)
	if err != nil {
		panic(err)
	}
	_ = objects.Register(validate.Hook[*ObjectType]{
		ID: "name-required", Group: "structural",
		Fn: func(_ context.Context, o *ObjectType) (validate.Outcome, error) {
			if o.Name == "" {
				return validate.Outcome{Decision: validate.Reject, Reason: "name is required"}, nil
			}
			return validate.Outcome{Decision: validate.Approve}, nil
		},
	})

	// 链接类型：引用完整性组；其中一个钩子以 error 表达自身故障（不可判定）。
	links, err := validate.NewRegistry[*LinkType](
		validate.GroupSpec{Name: "reference", Priority: 100, ShortCircuit: true},
	)
	if err != nil {
		panic(err)
	}
	_ = links.Register(validate.Hook[*LinkType]{
		ID: "endpoints-present", Group: "reference",
		Fn: func(_ context.Context, l *LinkType) (validate.Outcome, error) {
			if l.SourceKey == "" || l.TargetKey == "" {
				return validate.Outcome{Decision: validate.Reject, Reason: "missing endpoint"}, nil
			}
			return validate.Outcome{Decision: validate.Approve}, nil
		},
	})
	_ = links.Register(validate.Hook[*LinkType]{
		ID: "catalog-lookup", Group: "reference",
		Fn: func(context.Context, *LinkType) (validate.Outcome, error) {
			return validate.Outcome{}, errors.New("catalog service down")
		},
	})

	objRep, _ := objects.Validate(ctx, &ObjectType{Name: "Employee", Properties: []string{"id"}})
	fmt.Println("object:", objRep.FinalDecision, "ran:", objRep.RanHooks())

	badObj, _ := objects.Validate(ctx, &ObjectType{})
	fmt.Println("bad object:", badObj.FinalDecision, "skipped:", badObj.SkippedHooks())

	_, linkErr := links.Validate(ctx, &LinkType{Name: "manages", SourceKey: "a", TargetKey: "b"})
	var hee *validate.HookExecutionError
	if errors.As(linkErr, &hee) {
		fmt.Println("link error surfaced:", hee.HookID)
	}

	// Output:
	// object: 0 ran: 1
	// bad object: 1 skipped: 0
	// link error surfaced: catalog-lookup
}
