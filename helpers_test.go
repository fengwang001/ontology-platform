package ontology

import "testing"

// mustCompare 比对两份快照，失败即终止测试。
func mustCompare(t *testing.T, oldS, newS *Snapshot) *Result {
	t.Helper()
	res, err := Compare(oldS, newS)
	if err != nil {
		t.Fatalf("Compare failed: %v", err)
	}
	return res
}

// findObjectDiff 按对象键查找差异。
func findObjectDiff(d *InstanceDiff, key string) *ObjectDiff {
	for i := range d.Objects {
		if d.Objects[i].Key == key {
			return &d.Objects[i]
		}
	}
	return nil
}

// findLinkDiff 按链接键查找差异。
func findLinkDiff(d *InstanceDiff, key string) *LinkDiff {
	for i := range d.Links {
		if d.Links[i].Key == key {
			return &d.Links[i]
		}
	}
	return nil
}

// findPropChange 按属性 ID 查找取值差异。
func findPropChange(changes []PropertyChange, propID string) *PropertyChange {
	for i := range changes {
		if changes[i].PropID == propID {
			return &changes[i]
		}
	}
	return nil
}

// personBuilder 构造一个含 person 类型（pk/name/age）的快照构建器。
func personBuilder() *Builder {
	b := NewBuilder("L", 1)
	b.AddObjectType("person", "Person",
		Property{ID: "pk", Key: "id", Type: TypeInt},
		Property{ID: "p-name", Key: "name", Type: TypeString},
		Property{ID: "p-age", Key: "age", Type: TypeInt},
	)
	return b
}

func objKey(typeID string, pk Value) string {
	return ObjectRef{TypeID: typeID, PK: pk}.key()
}

func linkKey(typeID string, src, dst ObjectRef) string {
	return Link{TypeID: typeID, Source: src, Target: dst}.key()
}
