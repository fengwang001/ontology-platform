package ontology

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func sameSummary(a, b Summary) bool {
	return reflect.DeepEqual(a, b)
}

func requireCode(t *testing.T, err error, code ErrorCode) {
	t.Helper()
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != code {
		t.Fatalf("want error code %s, got %v", code, err)
	}
}

func leafBody(nested Nested, args []Type) (Type, error) {
	return InstanceOf("Leaf", args...), nil
}

func boxBody(nested Nested, args []Type) (Type, error) {
	inner, err := nested.Instantiate("Leaf", args...)
	if err != nil {
		return Type{}, err
	}
	return Struct(Field{Name: "inner", Type: inner}), nil
}

func TestCanonicalEquivalenceHits(t *testing.T) {
	var logs bytes.Buffer
	registry := NewRegistry(Config{Logger: &logs})
	if err := registry.RegisterDefinition(Definition{Name: "Leaf", Params: []Param{{Name: "t", Default: Ptr(Named("string"))}}, Body: leafBody}); err != nil {
		t.Fatal(err)
	}
	aliased := Alias("text", Named("string"))
	first, err := registry.Instantiate("Leaf", aliased)
	if err != nil {
		t.Fatal(err)
	}
	if first.Hit {
		t.Fatal("first request must create")
	}
	second, err := registry.Instantiate("Leaf", Named("string"))
	if err != nil {
		t.Fatal(err)
	}
	if !second.Hit || second.Instance.ID != first.Instance.ID {
		t.Fatalf("alias must hit first instance: first=%d second=%v hit=%t", first.Instance.ID, second.Instance, second.Hit)
	}
	structA := Struct(Field{Name: "x", Type: Named("int")}, Field{Name: "y", Type: Named("string")})
	structB := Struct(Field{Name: "x", Type: Alias("i32", Named("int"))}, Field{Name: "y", Type: Named("string")})
	a, err := registry.Instantiate("Leaf", structA)
	if err != nil {
		t.Fatal(err)
	}
	b, err := registry.Instantiate("Leaf", structB)
	if err != nil || !b.Hit || b.Instance.ID != a.Instance.ID {
		t.Fatalf("canonical struct should hit: %v %v", b, err)
	}
	reordered := Struct(Field{Name: "y", Type: Named("string")}, Field{Name: "x", Type: Named("int")})
	reorderedResult, err := registry.Instantiate("Leaf", reordered)
	if err != nil || reorderedResult.Hit {
		t.Fatalf("reordered fields must create a separate instance: %+v %v", reorderedResult, err)
	}
	defaulted, err := registry.Instantiate("Leaf")
	if err != nil || !defaulted.Hit || defaulted.Instance.ID != first.Instance.ID {
		t.Fatalf("default argument must hit alias-equivalent instance: %+v %v", defaulted, err)
	}
	if summary := registry.Summary(); summary.ActiveInstances != 3 || summary.CumulativeHits != 3 || summary.CumulativeCreates != 3 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	if !strings.Contains(logs.String(), "decision=hit") || !strings.Contains(logs.String(), "decision=created") {
		t.Fatalf("logs must show both inputs and decision basis: %s", logs.String())
	}
}

func TestNestedDepthFailureRollsBackEntireTree(t *testing.T) {
	registry := NewRegistry(Config{MaxDepth: 2})
	mustRegister := func(def Definition) {
		t.Helper()
		if err := registry.RegisterDefinition(def); err != nil {
			t.Fatal(err)
		}
	}
	mustRegister(Definition{Name: "Leaf", Params: []Param{{Name: "t"}}, Body: leafBody})
	mustRegister(Definition{Name: "Box", Params: []Param{{Name: "t"}}, Body: boxBody})
	mustRegister(Definition{
		Name:   "Crate",
		Params: []Param{{Name: "t"}},
		Body: func(nested Nested, args []Type) (Type, error) {
			inner, err := nested.Instantiate("Box", args...)
			if err != nil {
				return Type{}, err
			}
			deep, err := nested.Instantiate("Crate", args...)
			if err != nil {
				return Type{}, err
			}
			return Struct(Field{Name: "box", Type: inner}, Field{Name: "crate", Type: deep}), nil
		},
	})
	reused, err := registry.Instantiate("Leaf", Named("string"))
	if err != nil {
		t.Fatal(err)
	}
	before := registry.Summary()
	_, err = registry.Instantiate("Crate", Named("string"))
	requireCode(t, err, ErrDepth)
	after := registry.Summary()
	if !sameSummary(after, before) {
		t.Fatalf("rejected depth request changed state: before=%+v after=%+v", before, after)
	}
	reusedAgain, err := registry.Instantiate("Leaf", Named("string"))
	if err != nil || !reusedAgain.Hit || reusedAgain.Instance.ID != reused.Instance.ID {
		t.Fatalf("reused instance must survive rollback: %+v %v", reusedAgain, err)
	}
}

func TestUpdatePropagatesStalenessAndCleanupRules(t *testing.T) {
	registry := NewRegistry(Config{})
	defs := []Definition{
		{Name: "Leaf", Params: []Param{{Name: "t"}}, Body: leafBody},
		{Name: "Box", Params: []Param{{Name: "t"}}, Body: boxBody},
		{
			Name:   "Outer",
			Params: []Param{{Name: "t"}},
			Body: func(nested Nested, args []Type) (Type, error) {
				inner, err := nested.Instantiate("Box", args...)
				if err != nil {
					return Type{}, err
				}
				return Struct(Field{Name: "outer", Type: inner}), nil
			},
		},
	}
	for _, def := range defs {
		if err := registry.RegisterDefinition(def); err != nil {
			t.Fatal(err)
		}
	}
	outer, err := registry.Instantiate("Outer", Named("int"))
	if err != nil {
		t.Fatal(err)
	}
	var leafID, boxID, outerID uint64
	outerInstance := outer.Instance
	if len(outerInstance.Dependencies) != 1 {
		t.Fatalf("outer should directly depend on box: %+v", outerInstance)
	}
	outerID = outerInstance.ID
	boxKey := outerInstance.Dependencies[0]
	_ = boxKey
	ids := []uint64{}
	for id := range registry.instances {
		ids = append(ids, id)
	}
	if len(ids) != 3 {
		t.Fatalf("want nested leaf, box, outer, got %d", len(ids))
	}
	for id, node := range registry.instances {
		switch node.def {
		case "Leaf":
			leafID = id
		case "Box":
			boxID = id
		case "Outer":
			outerID = id
		}
	}
	if err := registry.UpdateDefinition(defs[0]); err != nil {
		t.Fatal(err)
	}
	for _, id := range []uint64{leafID, boxID, outerID} {
		instance, ok := registry.Get(id)
		if !ok || !instance.Stale || instance.StaleReason.UpdatedDef != "Leaf" {
			t.Fatalf("instance %d should be stale from Leaf update: %+v", id, instance)
		}
	}
	leaf, _ := registry.Get(leafID)
	box, _ := registry.Get(boxID)
	outerCopy, _ := registry.Get(outerID)
	if leaf.StaleReason.Hop != 0 || box.StaleReason.Hop != 1 || outerCopy.StaleReason.Hop != 2 {
		t.Fatalf("hops should follow dependency reverse edges: leaf=%d box=%d outer=%d", leaf.StaleReason.Hop, box.StaleReason.Hop, outerCopy.StaleReason.Hop)
	}
	if err := registry.Cleanup(boxID); err != nil {
		t.Fatalf("a stale dependent must not block cleanup: %v", err)
	}
	if err := registry.Cleanup(outerID); err != nil {
		t.Fatalf("outer should be cleanable: %v", err)
	}
	if err := registry.Cleanup(leafID); err != nil {
		t.Fatalf("leaf should be cleanable last: %v", err)
	}
	if summary := registry.Summary(); summary.ActiveInstances != 0 || summary.StaleInstances != 0 || summary.InstancesByDef["Leaf"] != 0 {
		t.Fatalf("cleanup left instances: %+v", summary)
	}
}

func TestQuotaBoundaryAndRejectionOrder(t *testing.T) {
	registry := NewRegistry(Config{MaxTotal: 2, MaxPerDef: map[string]int{"Leaf": 1}, MaxDepth: 10})
	if err := registry.RegisterDefinition(Definition{Name: "Leaf", Params: []Param{{Name: "t"}}, Body: leafBody}); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Instantiate("Leaf", Named("a")); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Instantiate("Leaf", Named("a")); err != nil {
		t.Fatalf("equal-boundary hit must not consume quota: %v", err)
	}
	_, err := registry.Instantiate("Leaf", Named("b"))
	requireCode(t, err, ErrQuota)
	before := registry.Summary()
	_, err = registry.Instantiate("Missing", Named("b"), Named("extra"))
	requireCode(t, err, ErrUndefined)
	_, err = registry.Instantiate("Leaf", Named("b"), Named("extra"))
	requireCode(t, err, ErrParameters)
	constrained := Definition{
		Name: "Constrained",
		Params: []Param{
			{Name: "a", Constraints: []Constraint{{Kind: ConstraintOneOf, Allowed: []Type{Named("int")}}}},
		},
		Body: leafBody,
	}
	if err := registry.RegisterDefinition(constrained); err != nil {
		t.Fatal(err)
	}
	_, err = registry.Instantiate("Constrained", Named("nope"), Named("extra"))
	requireCode(t, err, ErrParameters)
	_, err = registry.Instantiate("Constrained", Named("nope"))
	requireCode(t, err, ErrConstraint)
	after := registry.Summary()
	if after.ActiveInstances != before.ActiveInstances || after.StaleInstances != before.StaleInstances || after.CumulativeHits != before.CumulativeHits || after.CumulativeCreates != before.CumulativeCreates {
		t.Fatalf("rejections changed state: before=%+v after=%+v", before, after)
	}
}

func TestConcurrentEquivalentRequestsCreateOnce(t *testing.T) {
	registry := NewRegistry(Config{})
	if err := registry.RegisterDefinition(Definition{Name: "Leaf", Params: []Param{{Name: "t"}}, Body: leafBody}); err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	results := make(chan uint64, 32)
	for i := 0; i < 32; i++ {
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			result, err := registry.Instantiate("Leaf", Alias("text", Named("string")))
			if err != nil {
				t.Errorf("concurrent request failed: %v", err)
				return
			}
			results <- result.Instance.ID
		}(i)
	}
	wait.Wait()
	close(results)
	first := uint64(0)
	count := 0
	for id := range results {
		if first == 0 {
			first = id
		}
		if id != first {
			t.Fatalf("concurrent equivalent requests returned different ids: %d vs %d", first, id)
		}
		count++
	}
	summary := registry.Summary()
	if summary.ActiveInstances != 1 || summary.CumulativeCreates != 1 || summary.CumulativeHits != uint64(count-1) {
		t.Fatalf("unexpected concurrent summary: %+v count=%d", summary, count)
	}
}

func TestSelfInstanceArgumentCreatesOneSelfDependentInstance(t *testing.T) {
	registry := NewRegistry(Config{MaxDepth: 6})
	selfRef := Definition{
		Name:   "Rec",
		Params: []Param{{Name: "t"}},
		Body: func(nested Nested, args []Type) (Type, error) {
			marker, err := nested.Instantiate("Marker", args...)
			if err != nil {
				return Type{}, err
			}
			if sameType(args[0], Named("start")) {
				if _, err := nested.Instantiate("Rec", InstanceOf("Rec", Named("start"))); err != nil {
					return Type{}, err
				}
			}
			return Struct(Field{Name: "marker", Type: marker}), nil
		},
	}
	marker := Definition{Name: "Marker", Params: []Param{{Name: "t"}}, Body: leafBody}
	if err := registry.RegisterDefinition(marker); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterDefinition(selfRef); err != nil {
		t.Fatal(err)
	}
	result, err := registry.Instantiate("Rec", Named("start"))
	if err != nil {
		t.Fatalf("Rec<Rec<T>> must be allowed: %v", err)
	}
	if result.Hit {
		t.Fatal("first self-argument request must create")
	}
	again, err := registry.Instantiate("Rec", Alias("s", Named("start")))
	if err != nil || !again.Hit || again.Instance.ID != result.Instance.ID {
		t.Fatalf("outer request must hit same instance: %+v %v", again, err)
	}
	if summary := registry.Summary(); summary.ActiveInstances != 4 {
		t.Fatalf("want outer/inner Rec and two Markers, got %+v", summary)
	}
}
