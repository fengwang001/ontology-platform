package ontology

import "testing"

// TestRenameKeepsIdentifier 覆盖弹性重命名前后新旧标识符/名字的行为差异：
// 标识符延续 => 原权限对新名字继续生效；旧名字引用 => 属性标识符不存在。
func TestRenameKeepsIdentifier(t *testing.T) {
	g := NewGateway()
	mustType(t, g, "Person",
		[]Attribute{{ID: "a_email", Name: "email", Kind: KindString}},
		PolicyIgnoreField)
	mustGrant(t, g, "Person", "alice", "a_email", OpRead, 1, 0)
	mustGrant(t, g, "Person", "alice", "a_email", OpWrite, 1, 0)

	res, err := g.CreateObject(WriteRequest{
		ObjectID: "p1", TypeID: "Person", Subject: "alice", SchemaVer: 1,
		Fields: map[string]any{"email": "x@example.com"},
	})
	if err != nil || !res.Applied {
		t.Fatalf("create: %v %+v", err, res)
	}

	// v2：标识符 a_email 保留，名字 email -> contact_email。
	mustEvolve(t, g, "Person", []AttrChange{
		{Kind: ChangeRename, AttrID: "a_email", NewName: "contact_email"},
	})

	// 版本过期：按 v1 写必须整体拒绝且不改状态。
	beforeVal := g.store.getObject("p1").Fields["a_email"]
	_, err = g.Write(WriteRequest{
		ObjectID: "p1", TypeID: "Person", Subject: "alice", SchemaVer: 1,
		Fields: map[string]any{"contact_email": "stale@example.com"},
	})
	if ErrorKindOf(err) != ErrVersionExpired {
		t.Fatalf("want version_expired, got %v", err)
	}
	if g.store.getObject("p1").Fields["a_email"] != beforeVal {
		t.Fatal("stale write changed state")
	}

	// 新名字 + 标识符延续的权限 => 写成功，字段仍按稳定标识符落盘。
	wres, err := g.Write(WriteRequest{
		ObjectID: "p1", TypeID: "Person", Subject: "alice", SchemaVer: 2,
		Fields: map[string]any{"contact_email": "new@example.com"},
	})
	if err != nil || !wres.Applied || len(wres.WrittenFields) != 1 {
		t.Fatalf("write by new name: %v %+v", err, wres)
	}
	if wres.WrittenFields[0] != "a_email" {
		t.Fatalf("field should be keyed by stable id, got %v", wres.WrittenFields)
	}

	// 旧名字引用 => 属性标识符不存在（不是权限错误）。
	_, err = g.Write(WriteRequest{
		ObjectID: "p1", TypeID: "Person", Subject: "alice", SchemaVer: 2,
		Fields: map[string]any{"email": "old@example.com"},
	})
	if ErrorKindOf(err) != ErrAttrNotFound {
		t.Fatalf("want attr_not_found for old name, got %v", err)
	}

	// 读：标识符延续的权限仍有效，按标识符投影。
	rr, err := g.Read("p1", "Person", "alice")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if rr.Object.Fields["a_email"] != "new@example.com" {
		t.Fatalf("renamed attr not readable via stable id: %+v", rr.Object.Fields)
	}
}

// TestRevocationOverUnion 覆盖多条授权取并集、吊销挖洞、吊销优先的边界。
func TestRevocationOverUnion(t *testing.T) {
	g := NewGateway()
	mustType(t, g, "T", []Attribute{{ID: "f", Name: "f", Kind: KindInt}}, PolicyRejectWhole)
	mustGrant(t, g, "T", "bob", "f", OpWrite, 1, 2)
	mustGrant(t, g, "T", "bob", "f", OpWrite, 5, 0)
	if err := g.Revoke("T", "bob", "f", OpWrite, 2, 5); err != nil {
		t.Fatal(err)
	}

	want := map[int]bool{1: true, 2: false, 3: false, 4: false, 5: false}
	for v := 1; v <= 5; v++ {
		for currentVer(t, g, "T") < v {
			mustEvolve(t, g, "T", nil)
		}
		view, err := g.PermissionView("T", "bob", v)
		if err != nil {
			t.Fatal(err)
		}
		if got := view[OpWrite]["f"]; got != want[v] {
			t.Fatalf("v%d: want allowed=%v got %v", v, want[v], got)
		}
	}

	// v6 起开放授权重新生效（吊销区间止于 5）。
	mustEvolve(t, g, "T", nil)
	view, _ := g.PermissionView("T", "bob", 6)
	if !view[OpWrite]["f"] {
		t.Fatal("open-ended grant must resume after revocation window")
	}
}
