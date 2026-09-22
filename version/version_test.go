package version

import (
	"testing"

	"ontology/txid"
)

type testView struct {
	point  txid.ID
	active map[txid.ID]bool
}

func (v testView) Point() txid.ID           { return v.point }
func (v testView) IsActive(id txid.ID) bool { return v.active[id] }

func viewAt(point int, active ...int) testView {
	m := map[txid.ID]bool{}
	for _, a := range active {
		m[txid.ID(a)] = true
	}
	return testView{point: txid.ID(point), active: m}
}

func TestAppendKeepsOrder(t *testing.T) {
	c := NewChain()
	for _, id := range []int{5, 2, 9, 1} {
		c.Append(&Version{Commit: txid.ID(id), Value: []byte{byte(id)}})
	}
	vs := c.Committed()
	for i := 1; i < len(vs); i++ {
		if !vs[i-1].Commit.Before(vs[i].Commit) {
			t.Fatalf("链未按提交事务号升序")
		}
	}
}

func TestVisibleAtPicksNewestVisible(t *testing.T) {
	c := NewChain()
	c.Append(&Version{Commit: 2, Value: []byte("v2")})
	c.Append(&Version{Commit: 5, Value: []byte("v5")})
	c.Append(&Version{Commit: 8, Value: []byte("v8")})

	v, ok := c.VisibleAt(viewAt(6))
	if !ok || string(v.Value) != "v5" {
		t.Fatalf("point=6 应看到 v5，得到 %v %v", v, ok)
	}
	if _, ok := c.VisibleAt(viewAt(2)); ok {
		t.Fatalf("point=2 不应看到任何版本（左闭右开）")
	}
	v, ok = c.VisibleAt(viewAt(6, 5))
	if !ok || string(v.Value) != "v2" {
		t.Fatalf("活跃集合中的提交不可见，应回退到 v2")
	}
}

func TestRemoveProtectsNewest(t *testing.T) {
	c := NewChain()
	c.Append(&Version{Commit: 1})
	c.Append(&Version{Commit: 2})
	if c.Remove(2) {
		t.Fatalf("最新版本不得被 Remove 回收")
	}
	if !c.Remove(1) {
		t.Fatalf("旧版本应可回收")
	}
	if c.Len() != 1 {
		t.Fatalf("回收后链长应为 1")
	}
	if !c.RemoveUncommitted(2) {
		t.Fatalf("恢复路径应能强制移除最新版本")
	}
	if c.Len() != 0 {
		t.Fatalf("强制移除后链应为空")
	}
}
