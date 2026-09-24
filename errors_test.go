package ontology

import (
	"errors"
	"testing"
)

// TestEmptyRingLocate 空环上 Locate 返回可判定的 ErrEmptyRing，而不是空串。
func TestEmptyRingLocate(t *testing.T) {
	r := New()
	owner, err := r.Locate("any-key")
	if !errors.Is(err, ErrEmptyRing) {
		t.Fatalf("空环 Locate 应返回 ErrEmptyRing，实际 err=%v", err)
	}
	if owner != "" {
		t.Fatalf("空环 Locate 出错时不应返回节点，实际 %q", owner)
	}
}

// TestInvalidVnodes vnodes<=0 添加节点时报 ErrInvalidVnodes。
func TestInvalidVnodes(t *testing.T) {
	r := New()
	for _, n := range []int{0, -1, -100} {
		if err := r.Add("node-x", n); !errors.Is(err, ErrInvalidVnodes) {
			t.Fatalf("Add(vnodes=%d) 应返回 ErrInvalidVnodes，实际 %v", n, err)
		}
	}
	if r.Size() != 0 {
		t.Fatalf("非法添加后环应为空，实际 Size=%d", r.Size())
	}
}

// TestDuplicateAdd 重复添加同一节点 ID 报 ErrNodeExists，
// 且环不被改变：重复添加前后所有 key 归属逐个不变。
func TestDuplicateAdd(t *testing.T) {
	keys := genKeys(numKeys)
	r := buildRing(t, nodeIDs(5), 100)
	before := locateAll(t, r, keys)
	sizeBefore := r.Size()

	if err := r.Add("node-03", 100); !errors.Is(err, ErrNodeExists) {
		t.Fatalf("重复添加应返回 ErrNodeExists，实际 %v", err)
	}
	// 用不同的 vnodes 重复添加同样报错。
	if err := r.Add("node-03", 7); !errors.Is(err, ErrNodeExists) {
		t.Fatalf("重复添加应返回 ErrNodeExists，实际 %v", err)
	}
	if r.Size() != sizeBefore {
		t.Fatalf("重复添加后 Size=%d，期望不变 %d", r.Size(), sizeBefore)
	}
	after := locateAll(t, r, keys)
	for i := range keys {
		if after[i] != before[i] {
			t.Fatalf("重复添加改变了 key %q 的归属: %q -> %q", keys[i], before[i], after[i])
		}
	}
}

// TestRemoveMissing 删除不存在的节点报 ErrNodeNotFound。
func TestRemoveMissing(t *testing.T) {
	r := buildRing(t, nodeIDs(3), 10)
	if err := r.Remove("node-99"); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("删除不存在节点应返回 ErrNodeNotFound，实际 %v", err)
	}
	// 空环上删除同样报 ErrNodeNotFound。
	if err := New().Remove("node-99"); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("空环删除应返回 ErrNodeNotFound，实际 %v", err)
	}
}

// TestErrorsAreDistinct 四类错误必须能用 errors.Is 互相区分。
func TestErrorsAreDistinct(t *testing.T) {
	all := []error{ErrEmptyRing, ErrInvalidVnodes, ErrNodeExists, ErrNodeNotFound}
	for i, a := range all {
		for j, b := range all {
			if i != j && errors.Is(a, b) {
				t.Fatalf("错误 %v 与 %v 不可区分", a, b)
			}
		}
	}
}
