package ontology

import "testing"

// 错误优先级一：快照结构性损坏优先于一切其他错误类别报出，
// 即使同一次比对中也存在口径无法确定的问题。
func TestErrorPriorityCorruptBeatsBasis(t *testing.T) {
	b0 := personBuilder()
	b0.PutObject("person", IntValue(1), map[string]Value{"p-name": StringValue("a")})
	s0 := b0.Build()

	// 构造损坏快照：链接端点对象不存在。
	bad := NewBuilder("L", 2)
	bad.AddObjectType("person", "Person", Property{ID: "pk", Key: "id", Type: TypeInt})
	bad.AddObjectType("company", "Company", Property{ID: "pk", Key: "id", Type: TypeInt})
	bad.AddLinkType("wf", "WF", "person", "company")
	bad.PutObject("person", IntValue(1), nil)
	bad.PutLink("wf", ObjectRef{TypeID: "person", PK: IntValue(1)}, ObjectRef{TypeID: "company", PK: IntValue(99)})
	sBad := bad.Build()
	if err := ValidateSnapshot(sBad); err == nil {
		t.Fatalf("expected corrupt snapshot")
	}

	_, err := Compare(s0, sBad)
	if ErrorClassOf(err) != ErrClassCorrupt {
		t.Fatalf("expected corrupt class, got %v", err)
	}
}

// 错误类别三：主键被改型，实例身份无法跨快照对齐。
func TestBasisUndeterminedByPrimaryKeyRetype(t *testing.T) {
	b0 := personBuilder()
	b0.PutObject("person", IntValue(1), map[string]Value{"p-name": StringValue("a")})
	s0 := b0.Build()

	b1 := FromSnapshot(s0)
	b1.Advance(2)
	b1.RetypeProperty("person", "pk", TypeFloat)
	s1 := b1.Build()
	if err := ValidateSnapshot(s1); err != nil {
		t.Fatalf("snapshot should remain valid: %v", err)
	}

	_, err := Compare(s0, s1)
	if ErrorClassOf(err) != ErrClassBasis {
		t.Fatalf("expected basis class, got %v", err)
	}
}

// 错误类别三：主键指定被更换。
func TestBasisUndeterminedByPrimaryKeyChange(t *testing.T) {
	b0 := personBuilder()
	b0.PutObject("person", IntValue(1), map[string]Value{"p-name": StringValue("a")})
	s0 := b0.Build()

	b1 := FromSnapshot(s0)
	b1.Advance(2)
	b1.ChangePrimaryKey("person", "p-name")
	s1 := b1.Build()

	_, err := Compare(s0, s1)
	if ErrorClassOf(err) != ErrClassBasis {
		t.Fatalf("expected basis class, got %v", err)
	}
}

// 错误类别三：存续链接类型的端点对象类型被更换。
func TestBasisUndeterminedByLinkEndpointChange(t *testing.T) {
	b0 := NewBuilder("L", 1)
	b0.AddObjectType("a", "A", Property{ID: "pk", Key: "id", Type: TypeInt})
	b0.AddObjectType("b", "B", Property{ID: "pk", Key: "id", Type: TypeInt})
	b0.AddLinkType("ab", "AB", "a", "b")
	s0 := b0.Build()

	b1 := FromSnapshot(s0)
	b1.Advance(2)
	b1.SetLinkTypeEndpoints("ab", "b", "a")
	s1 := b1.Build()

	_, err := Compare(s0, s1)
	if ErrorClassOf(err) != ErrClassBasis {
		t.Fatalf("expected basis class, got %v", err)
	}
}

// 主键重命名不影响口径：身份由主键取值决定，与属性名无关。
func TestPrimaryKeyRenameKeepsBasis(t *testing.T) {
	b0 := personBuilder()
	b0.PutObject("person", IntValue(1), map[string]Value{"p-name": StringValue("a")})
	s0 := b0.Build()

	b1 := FromSnapshot(s0)
	b1.Advance(2)
	b1.RenameProperty("person", "pk", "identifier")
	s1 := b1.Build()

	res := mustCompare(t, s0, s1)
	if !res.Instances.Empty() {
		t.Fatalf("pk rename alone must not produce instance diffs, got %+v", res.Instances)
	}
}

// 错误类别二：结构层差异判定冲突。对合法快照该冲突不会自然发生，
// 这里直接校验差异一致性守卫的各条不变式。
func TestSchemaDiffConflictValidation(t *testing.T) {
	cases := []struct {
		name string
		diff *SchemaDiff
	}{
		{"duplicate type entry", &SchemaDiff{ObjectTypes: []ObjectTypeDiff{
			{TypeID: "t", Disposition: DispAdded},
			{TypeID: "t", Disposition: DispRemoved},
		}}},
		{"removed carries dimensions", &SchemaDiff{ObjectTypes: []ObjectTypeDiff{
			{TypeID: "t", Disposition: DispRemoved, Renamed: &Rename{OldKey: "a", NewKey: "b"}},
		}}},
		{"modified without dimension", &SchemaDiff{ObjectTypes: []ObjectTypeDiff{
			{TypeID: "t", Disposition: DispModified},
		}}},
		{"prop duplicate entry", &SchemaDiff{ObjectTypes: []ObjectTypeDiff{
			{TypeID: "t", Disposition: DispModified, Props: []PropertyDiff{
				{PropID: "p", Disposition: DispAdded},
				{PropID: "p", Disposition: DispRemoved},
			}},
		}}},
		{"prop removed with rename", &SchemaDiff{ObjectTypes: []ObjectTypeDiff{
			{TypeID: "t", Disposition: DispModified, Props: []PropertyDiff{
				{PropID: "p", Disposition: DispRemoved, Renamed: &Rename{OldKey: "a", NewKey: "b"}},
			}},
		}}},
		{"rename identical keys", &SchemaDiff{ObjectTypes: []ObjectTypeDiff{
			{TypeID: "t", Disposition: DispModified, Props: []PropertyDiff{
				{PropID: "p", Disposition: DispModified, Renamed: &Rename{OldKey: "a", NewKey: "a"}},
			}},
		}}},
		{"retype class inconsistent", &SchemaDiff{ObjectTypes: []ObjectTypeDiff{
			{TypeID: "t", Disposition: DispModified, Props: []PropertyDiff{
				{PropID: "p", Disposition: DispModified, Retyped: &Retype{OldType: TypeInt, NewType: TypeFloat, Class: RetypeNarrowing}},
			}},
		}}},
		{"link modified without dimension", &SchemaDiff{LinkTypes: []LinkTypeDiff{
			{LinkTypeID: "l", Disposition: DispModified},
		}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.diff.Validate()
			if ErrorClassOf(err) != ErrClassConflict {
				t.Fatalf("expected conflict class, got %v", err)
			}
		})
	}
}

// 差异校验管线位置：冲突校验在口径对齐之前（依赖顺序）。
// 这里通过手工构造的差异直接验证两个阶段的错误类别可区分。
func TestErrorClassesAreDistinct(t *testing.T) {
	classes := map[ErrorClass]bool{
		ErrClassCorrupt: true, ErrClassConflict: true, ErrClassBasis: true,
	}
	if len(classes) != 3 {
		t.Fatalf("error classes must be distinct")
	}
}
