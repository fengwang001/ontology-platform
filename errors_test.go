package ontology

import (
	"errors"
	"testing"
)

func TestLocateOnEmptyRing(t *testing.T) {
	r := New()
	owner, err := r.Locate("anything")
	if !errors.Is(err, ErrEmptyRing) {
		t.Fatalf("err = %v, want ErrEmptyRing", err)
	}
	if owner != "" {
		t.Fatalf("owner = %q, want empty on error", owner)
	}
}

func TestAddWithNonPositiveVnodes(t *testing.T) {
	r := New()
	for _, v := range []int{0, -1, -100} {
		if err := r.Add("n", v); !errors.Is(err, ErrInvalidVnodes) {
			t.Fatalf("Add(vnodes=%d) err = %v, want ErrInvalidVnodes", v, err)
		}
	}
	if r.NumNodes() != 0 {
		t.Fatal("failed adds must not change the ring")
	}
}

// 重复添加同一节点 ID 必须报 ErrNodeExists，且环不得改变：
// 失败前后所有 key 的归属逐个不变。
func TestDuplicateAddFailsAndKeepsRing(t *testing.T) {
	keys := GenerateKeys(10000)
	r := buildRing(t, 5, 100)
	before := ownersOf(t, r, keys)
	pointsBefore := len(r.points)
	err := r.Add(nodeID(2), 100)
	if !errors.Is(err, ErrNodeExists) {
		t.Fatalf("err = %v, want ErrNodeExists", err)
	}
	if len(r.points) != pointsBefore {
		t.Fatalf("point count changed: %d -> %d", pointsBefore, len(r.points))
	}
	after := ownersOf(t, r, keys)
	for i := range keys {
		if after[i] != before[i] {
			t.Fatalf("key %q owner changed %s -> %s after failed add", keys[i], before[i], after[i])
		}
	}
}

func TestRemoveMissingNode(t *testing.T) {
	r := buildRing(t, 3, 10)
	if err := r.Remove("ghost"); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("err = %v, want ErrNodeNotFound", err)
	}
	if r.NumNodes() != 3 {
		t.Fatal("failed remove must not change the ring")
	}
}

// 四类错误必须可用 errors.Is 两两区分。
func TestErrorsAreDistinguishable(t *testing.T) {
	all := []error{ErrEmptyRing, ErrInvalidVnodes, ErrNodeExists, ErrNodeNotFound}
	for i, a := range all {
		for j, b := range all {
			if i != j && errors.Is(a, b) {
				t.Fatalf("error %v matches %v", a, b)
			}
		}
	}
}
