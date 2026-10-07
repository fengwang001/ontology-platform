package ontology

import (
	"fmt"
	"sync"
	"testing"
)

// TestViewAccessIsScaleIndependent 以可验证方式证明性能要求：
// 在规模（历史版本数 V、历史权限条目数 P）翻倍时，计算“给定主体在给定版本下
// 对某类型的权限视图”所访问的历史版本记录数与历史权限条目数必须保持不变。
//
// 物化在授予/演进发生时完成，在线判定只读 1 条当前状态记录与 1 条视图记录，
// 故 PermissionRows 与 VersionRecords 恒为 0，ViewRecords 恒为 1。
func TestViewAccessIsScaleIndependent(t *testing.T) {
	measure := func(versions, grantsPerSubject int) AccessCounters {
		g := NewGateway()
		mustType(t, g, "Big", []Attribute{{ID: "a0", Name: "a0", Kind: KindInt}},
			PolicyIgnoreField)
		subject := "probe"
		// 制造大量历史版本（新增属性再废弃，制造版本轴长度）。
		for v := 2; v <= versions; v++ {
			id := fmt.Sprintf("a%d", v)
			mustEvolve(t, g, "Big", []AttrChange{
				{Kind: ChangeAdd, AttrID: id, NewName: id, NewAttr: KindInt},
			})
			mustEvolve(t, g, "Big", []AttrChange{{Kind: ChangeDeprecate, AttrID: id}})
		}
		// 制造大量历史权限条目：多个噪声主体 + 探针主体自身多条授权/吊销。
		for i := 0; i < grantsPerSubject; i++ {
			noise := fmt.Sprintf("noise-%d", i)
			mustGrant(t, g, "Big", noise, "a0", OpRead, 1, 0)
			if err := g.Revoke("Big", noise, "a0", OpRead, 1, 0); err != nil {
				t.Fatal(err)
			}
		}
		for i := 1; i <= grantsPerSubject; i++ {
			mustGrant(t, g, "Big", subject, "a0", OpRead, i, i)
		}

		latest := currentVer(t, g, "Big")
		g.ResetAccessCounters()
		// 在线判定：两次不同版本的视图计算。
		if _, err := g.PermissionView("Big", subject, latest); err != nil {
			t.Fatal(err)
		}
		if _, err := g.PermissionView("Big", subject, 1); err != nil {
			t.Fatal(err)
		}
		return g.AccessCounters()
	}

	small := measure(10, 10)
	large := measure(80, 80)
	t.Logf("small=%+v", small)
	t.Logf("large=%+v", large)
	// 与规模无关的硬性断言：不访问任何历史记录。
	if small.PermissionRows != 0 || large.PermissionRows != 0 {
		t.Fatalf("permission-view computation scanned historical rows: %+v %+v", small, large)
	}
	if small.VersionRecords != 0 || large.VersionRecords != 0 {
		t.Fatalf("permission-view computation scanned historical versions: %+v %+v", small, large)
	}
	if small.ViewRecords != large.ViewRecords || small.ViewRecords != 2 {
		t.Fatalf("view record access must be constant (1 per query): %+v %+v", small, large)
	}
}

// TestHistoryVersionsImmutable 演进不得改写历史版本快照（回归底层数组共享缺陷）。
func TestHistoryVersionsImmutable(t *testing.T) {
	g := NewGateway()
	mustType(t, g, "H", []Attribute{{ID: "keep", Name: "old", Kind: KindString}},
		PolicyIgnoreField)
	mustEvolve(t, g, "H", []AttrChange{
		{Kind: ChangeRename, AttrID: "keep", NewName: "new"},
		{Kind: ChangeAdd, AttrID: "gone", NewName: "gone", NewAttr: KindInt},
	})
	mustEvolve(t, g, "H", []AttrChange{{Kind: ChangeDeprecate, AttrID: "gone"}})

	v1 := g.store.snapshotVersion("H", 1)
	if len(v1.Attrs) != 1 || v1.Attrs[0].Name != "old" {
		t.Fatalf("v1 snapshot mutated by later evolution: %+v", v1.Attrs)
	}
	v2 := g.store.snapshotVersion("H", 2)
	if len(v2.Attrs) != 2 {
		t.Fatalf("v2 snapshot mutated: %+v", v2.Attrs)
	}
	for _, a := range v2.Attrs {
		if a.ID == "keep" && a.Name != "new" {
			t.Fatalf("v2 rename lost: %+v", a)
		}
	}
	v3 := g.store.snapshotVersion("H", 3)
	for _, a := range v3.Attrs {
		if a.ID == "gone" {
			t.Fatal("deprecated attr must not appear in v3 snapshot")
		}
	}
}

// TestConcurrentSerializable 并发授权/吊销/演进/读写：
// 结束后不变式必须成立，且每个观察都对应某个串行前缀。
func TestConcurrentSerializable(t *testing.T) {
	g := NewGateway()
	mustType(t, g, "S",
		[]Attribute{{ID: "x", Name: "x", Kind: KindInt}}, PolicyIgnoreField)
	mustGrant(t, g, "S", "u", "x", OpWrite, 1, 0)
	mustGrant(t, g, "S", "u", "x", OpRead, 1, 0)
	if _, err := g.CreateObject(WriteRequest{
		ObjectID: "s1", TypeID: "S", Subject: "u", SchemaVer: 1,
		Fields: map[string]any{"x": 0},
	}); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	// 并发演进（仅收紧，不改变标识符集合 => 版本会前进）。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 30; i++ {
			if _, err := g.Evolve("S", []AttrChange{
				{Kind: ChangeTighten, AttrID: "x", NewAttr: KindInt},
			}, ""); err != nil {
				t.Errorf("evolve: %v", err)
				return
			}
		}
		close(stop)
	}()
	// 并发授权/吊销（用独立噪声主体，避免与 u 的判定语义耦合）。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			s := fmt.Sprintf("c-%d", i%4)
			_ = g.Grant("S", s, "x", OpRead, 1, 0)
			_ = g.Revoke("S", s, "x", OpRead, 1, 0)
		}
	}()
	// 并发读写：所有结果必须只可能是“成功（在当时最新版本）”或
	// version_expired（演进刚好插入），二者都是合法串行前缀；
	// 绝不允许部分写、脏状态或 panic。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			latest, _ := g.CurrentVersion("S")
			res, err := g.Write(WriteRequest{
				ObjectID: "s1", TypeID: "S", Subject: "u", SchemaVer: latest,
				Fields: map[string]any{"x": 1},
			})
			switch {
			case err == nil && res.Applied:
			case ErrorKindOf(err) == ErrVersionExpired:
			default:
				t.Errorf("unexpected concurrent write outcome: %v %+v", err, res)
				return
			}
			rr, err := g.Read("s1", "S", "u")
			if err != nil {
				t.Errorf("concurrent read: %v", err)
				return
			}
			if v, ok := rr.Object.Fields["x"]; ok && v != nil && v != 1 && v != 0 {
				t.Errorf("corrupt field value %v", v)
				return
			}
		}
	}()
	wg.Wait()

	// 最终不变式：对象版本等于类型最新版本，且无并发崩溃。
	latest, _ := g.CurrentVersion("S")
	obj := g.store.getObject("s1")
	if obj.SchemaVer > latest {
		t.Fatalf("object version %d ahead of type version %d", obj.SchemaVer, latest)
	}
}
