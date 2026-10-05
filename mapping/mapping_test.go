package mapping_test

import (
	"errors"
	"reflect"
	"testing"

	"ontology/mapping"
)

func TestNewValidatesFmax(t *testing.T) {
	for _, fmax := range []int{0, -1, 100001} {
		if _, err := mapping.New(fmax); !errors.Is(err, mapping.ErrInvalidArgument) {
			t.Fatalf("fmax=%d 应报参数非法: %v", fmax, err)
		}
	}
	if _, err := mapping.New(1); err != nil {
		t.Fatal(err)
	}
	if _, err := mapping.New(100000); err != nil {
		t.Fatal(err)
	}
}

func TestChildOrAddAndRollback(t *testing.T) {
	m, err := mapping.New(2)
	if err != nil {
		t.Fatal(err)
	}
	root := m.Root()
	a, created, err := m.ChildOrAdd(root, "a", mapping.Object)
	if err != nil || !created {
		t.Fatalf("建 a: created=%v err=%v", created, err)
	}
	if _, created, _ := m.ChildOrAdd(root, "a", mapping.Long); created {
		t.Fatal("重复 ChildOrAdd 不应新建")
	}
	if _, _, err := m.ChildOrAdd(a, "b", mapping.Long); err != nil {
		t.Fatal(err)
	}
	// 恰等上限后，大 1 报字段超限且不改状态。
	if _, _, err := m.ChildOrAdd(root, "c", mapping.Long); !errors.Is(err, mapping.ErrFieldLimit) {
		t.Fatalf("超限应报 ErrFieldLimit: %v", err)
	}
	if m.Count() != 2 {
		t.Fatalf("超限不应改计数: %d", m.Count())
	}
	// 回滚删除后名额释放。
	m.RemoveChild(a, "b")
	if m.Count() != 1 {
		t.Fatalf("回滚后计数应为 1: %d", m.Count())
	}
	if _, _, err := m.ChildOrAdd(root, "c", mapping.Long); err != nil {
		t.Fatalf("回滚后应能再建: %v", err)
	}
}

func TestFieldsAndMV(t *testing.T) {
	m, _ := mapping.New(10)
	root := m.Root()
	a, _, _ := m.ChildOrAdd(root, "a", mapping.Object)
	m.ChildOrAdd(a, "b", mapping.Long)
	m.ChildOrAdd(root, "c", mapping.Keyword)
	want := map[string]mapping.Type{"a": mapping.Object, "a.b": mapping.Long, "c": mapping.Keyword}
	if !reflect.DeepEqual(m.Fields(), want) {
		t.Fatalf("Fields 应为 %v，实际 %v", want, m.Fields())
	}
	if m.MV() != 0 {
		t.Fatalf("mv 初始应为 0: %d", m.MV())
	}
	m.BumpMV()
	m.BumpMV()
	if m.MV() != 2 {
		t.Fatalf("mv 应为 2: %d", m.MV())
	}
}

func TestTypeValid(t *testing.T) {
	if !mapping.Long.Valid() || !mapping.Object.Valid() || mapping.Type(5).Valid() || mapping.Type(-1).Valid() {
		t.Fatal("Type.Valid 判定错误")
	}
	if !mapping.True.Valid() || !mapping.Strict.Valid() || mapping.Dynamic(3).Valid() {
		t.Fatal("Dynamic.Valid 判定错误")
	}
}
