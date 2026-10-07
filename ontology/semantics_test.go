package ontology

import "testing"

// TestDeprecateAuditableButInert 废弃：线上失效且报标识符不存在，历史可审计。
func TestDeprecateAuditableButInert(t *testing.T) {
	g := NewGateway()
	mustType(t, g, "D",
		[]Attribute{
			{ID: "ssn", Name: "ssn", Kind: KindString, Required: true},
			{ID: "label", Name: "label", Kind: KindString},
		},
		PolicyRejectWhole)
	mustGrant(t, g, "D", "carol", "ssn", OpRead, 1, 0)
	mustGrant(t, g, "D", "carol", "ssn", OpWrite, 1, 0)
	mustGrant(t, g, "D", "carol", "label", OpRead, 1, 0)
	mustGrant(t, g, "D", "carol", "label", OpWrite, 1, 0)

	res, err := g.CreateObject(WriteRequest{
		ObjectID: "d1", TypeID: "D", Subject: "carol", SchemaVer: 1,
		Fields: map[string]any{"ssn": "111", "label": "L"},
	})
	if err != nil || !res.Applied {
		t.Fatalf("create: %v %+v", err, res)
	}

	mustEvolve(t, g, "D", []AttrChange{{Kind: ChangeDeprecate, AttrID: "ssn"}})

	if err := g.Grant("D", "carol", "ssn", OpRead, 2, 3); ErrorKindOf(err) != ErrAttrNotFound {
		t.Fatalf("grant on deprecated: want attr_not_found, got %v", err)
	}
	if err := g.Revoke("D", "carol", "ssn", OpWrite, 2, 2); ErrorKindOf(err) != ErrAttrNotFound {
		t.Fatalf("revoke on deprecated: want attr_not_found, got %v", err)
	}

	_, err = g.Write(WriteRequest{
		ObjectID: "d1", TypeID: "D", Subject: "carol", SchemaVer: 2,
		Fields: map[string]any{"ssn": "222"},
	})
	if ErrorKindOf(err) != ErrAttrNotFound {
		t.Fatalf("write deprecated: want attr_not_found, got %v", err)
	}

	rr, err := g.Read("d1", "D", "carol")
	if err != nil {
		t.Fatal(err)
	}
	if _, present := rr.Object.Fields["ssn"]; present {
		t.Fatal("deprecated field must be removed entirely, not zero-valued")
	}
	if !rr.PartialView || len(rr.RemovedAttrs) != 1 || rr.RemovedAttrs[0] != "ssn" {
		t.Fatalf("partial view marking wrong: %+v", rr)
	}

	rows := g.AuditPermissions("D", "carol", "ssn")
	if len(rows) != 2 {
		t.Fatalf("audit should retain 2 historical rows, got %d", len(rows))
	}
}

// TestRejectWholeChangesNothing 整条拒绝：任何拒绝路径不改对象/版本/权限。
func TestRejectWholeChangesNothing(t *testing.T) {
	g := NewGateway()
	mustType(t, g, "W",
		[]Attribute{{ID: "a", Name: "a", Kind: KindInt}, {ID: "b", Name: "b", Kind: KindInt}},
		PolicyRejectWhole)
	mustGrant(t, g, "W", "u", "a", OpWrite, 1, 0)

	res, err := g.CreateObject(WriteRequest{
		ObjectID: "w1", TypeID: "W", Subject: "u", SchemaVer: 1,
		Fields: map[string]any{"a": 1, "b": 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Applied || g.store.getObject("w1") != nil {
		t.Fatal("reject_whole must not create object when a field is denied")
	}

	res, err = g.CreateObject(WriteRequest{
		ObjectID: "w1", TypeID: "W", Subject: "u", SchemaVer: 1,
		Fields: map[string]any{"a": 10},
	})
	if err != nil || !res.Applied {
		t.Fatalf("setup create: %v %+v", err, res)
	}
	obj := g.store.getObject("w1")
	snapshotVer := obj.SchemaVer
	valA := obj.Fields["a"]

	res, err = g.Write(WriteRequest{
		ObjectID: "w1", TypeID: "W", Subject: "u", SchemaVer: 1,
		Fields: map[string]any{"a": 11, "b": 22},
	})
	if err != nil {
		t.Fatalf("denied write is reported via result, not error: %v", err)
	}
	if res.Applied || len(res.Denies) != 1 || res.Denies[0].Ref != "b" {
		t.Fatalf("expected one whole-rejection deny: %+v", res)
	}
	if obj.Fields["a"] != valA || obj.SchemaVer != snapshotVer {
		t.Fatal("object state changed despite whole rejection")
	}
	if _, ok := obj.Fields["b"]; ok {
		t.Fatal("denied field leaked into object")
	}
	if len(g.store.permLog) != 1 {
		t.Fatal("permission records must be untouched by rejected writes")
	}

	mustGrant(t, g, "W", "u", "b", OpWrite, 1, 0)
	if err := g.Revoke("W", "u", "b", OpWrite, 1, 0); err != nil {
		t.Fatal(err)
	}
	res, _ = g.Write(WriteRequest{
		ObjectID: "w1", TypeID: "W", Subject: "u", SchemaVer: 1,
		Fields: map[string]any{"b": 1},
	})
	if res.Applied || res.Denies[0].Kind != ErrRevoked {
		t.Fatalf("revoked deny should be classified revoked: %+v", res.Denies)
	}
	if obj.Fields["a"] != valA {
		t.Fatal("state changed on revoked write")
	}
}

// TestIgnoreFieldSkipsAndContinues 整体忽略：逐字段独立判定，跳过被拒字段。
func TestIgnoreFieldSkipsAndContinues(t *testing.T) {
	g := NewGateway()
	mustType(t, g, "I",
		[]Attribute{
			{ID: "a", Name: "a", Kind: KindInt},
			{ID: "b", Name: "b", Kind: KindInt},
			{ID: "c", Name: "c", Kind: KindInt},
		}, PolicyIgnoreField)
	mustGrant(t, g, "I", "u", "a", OpWrite, 1, 0)
	mustGrant(t, g, "I", "u", "c", OpWrite, 1, 0)
	res, err := g.CreateObject(WriteRequest{
		ObjectID: "i1", TypeID: "I", Subject: "u", SchemaVer: 1,
		Fields: map[string]any{"a": 1, "b": 2, "c": 3},
	})
	if err != nil || !res.Applied {
		t.Fatalf("create: %v %+v", err, res)
	}
	obj := g.store.getObject("i1")
	if obj.Fields["a"] != 1 || obj.Fields["c"] != 3 {
		t.Fatalf("allowed fields not written: %+v", obj.Fields)
	}
	if _, ok := obj.Fields["b"]; ok {
		t.Fatal("denied field must be skipped, never zero-written")
	}
	if len(res.WrittenFields) != 2 || !sameStrings(res.IgnoredFields, []string{"b"}) {
		t.Fatalf("result bookkeeping wrong: %+v", res)
	}
}

// TestReadProjectionAndPriority 读时整体移除、部分视图、拒绝优先级。
func TestReadProjectionAndPriority(t *testing.T) {
	g := NewGateway()
	mustType(t, g, "P",
		[]Attribute{
			{ID: "pub", Name: "pub", Kind: KindString, Required: true},
			{ID: "secret", Name: "secret", Kind: KindString},
		}, PolicyIgnoreField)
	mustGrant(t, g, "P", "u", "pub", OpRead, 1, 0)
	mustGrant(t, g, "P", "u", "pub", OpWrite, 1, 0)
	// 借助管理员主体把无读权限字段 secret 写入对象（类型级写授权）。
	mustGrant(t, g, "P", "admin", "*", OpWrite, 1, 0)
	_, err := g.CreateObject(WriteRequest{
		ObjectID: "o1", TypeID: "P", Subject: "admin", SchemaVer: 1,
		Fields: map[string]any{"pub": "hello", "secret": "s3cr3t"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// secret 无读权限 => 整体移除；pub 可读。
	rr, err := g.Read("o1", "P", "u")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rr.Object.Fields["secret"]; ok {
		t.Fatal("unreadable field must be removed, not masked/zero")
	}
	if rr.Object.Fields["pub"] != "hello" {
		t.Fatal("readable field missing")
	}
	if !rr.PartialView || !sameStrings(rr.RemovedAttrs, []string{"secret"}) {
		t.Fatalf("partial marking wrong: %+v", rr)
	}

	// 完全没有任何授权线索的主体 => 类型级无读权限。
	if _, err := g.Read("o1", "P", "stranger"); ErrorKindOf(err) != ErrTypeNoPermission {
		t.Fatalf("want type_no_permission, got %v", err)
	}
	// 对象不存在优先于类型级无权限。
	if _, err := g.Read("nope", "P", "stranger"); ErrorKindOf(err) != ErrObjectNotFound {
		t.Fatalf("want object_not_found precedence, got %v", err)
	}
	if _, err := g.Read("o1", "P", "stranger"); ErrorKindOf(err) != ErrTypeNoPermission {
		t.Fatalf("want type_no_permission, got %v", err)
	}
}

// TestWritePolicyConflict 同一类型不得混用两种写入策略。
func TestWritePolicyConflict(t *testing.T) {
	g := NewGateway()
	mustType(t, g, "C", []Attribute{{ID: "x", Name: "x", Kind: KindInt}}, PolicyRejectWhole)
	_, err := g.Evolve("C", nil, PolicyIgnoreField)
	if ErrorKindOf(err) != ErrWritePolicyConflict {
		t.Fatalf("want write_policy_conflict, got %v", err)
	}
	// 沿用相同策略演进是允许的。
	if _, err := g.Evolve("C", nil, PolicyRejectWhole); err != nil {
		t.Fatalf("same-policy evolve should pass: %v", err)
	}
}

// TestErrorKindsDistinct 六类错误彼此可区分。
func TestErrorKindsDistinct(t *testing.T) {
	g := NewGateway()
	if _, err := g.Read("ghost", "NoType", "u"); ErrorKindOf(err) != ErrObjectNotFound {
		t.Fatalf("missing object: %v", err)
	}
	if _, err := g.Write(WriteRequest{ObjectID: "ghost", TypeID: "NoType", Subject: "u"}); ErrorKindOf(err) != ErrObjectNotFound {
		t.Fatalf("missing object on write: %v", err)
	}

	// 先制造类型级无读权限：建类型与对象，但读取主体没有任何读授权。
	mustType(t, g, "K0", []Attribute{{ID: "z", Name: "z", Kind: KindInt}}, PolicyIgnoreField)
	mustGrant(t, g, "K0", "owner", "*", OpWrite, 1, 0)
	if _, err := g.CreateObject(WriteRequest{
		ObjectID: "k0", TypeID: "K0", Subject: "owner", SchemaVer: 1,
		Fields: map[string]any{"z": 1},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Read("k0", "K0", "noperm"); ErrorKindOf(err) != ErrTypeNoPermission {
		t.Fatalf("want type_no_permission, got %v", err)
	}

	mustType(t, g, "K", []Attribute{{ID: "a", Name: "a", Kind: KindInt}}, PolicyIgnoreField)
	_, err := g.CreateObject(WriteRequest{
		ObjectID: "k1", TypeID: "K", Subject: "u", SchemaVer: 9,
		Fields: map[string]any{"a": 1},
	})
	if ErrorKindOf(err) != ErrVersionExpired {
		t.Fatalf("stale schema: %v", err)
	}
	_, err = g.CreateObject(WriteRequest{
		ObjectID: "k1", TypeID: "K", Subject: "u", SchemaVer: 1,
		Fields: map[string]any{"ghost_attr": 1},
	})
	if ErrorKindOf(err) != ErrAttrNotFound {
		t.Fatalf("unknown attr: %v", err)
	}

	kinds := map[ErrorKind]bool{}
	for _, e := range g.DecisionLog() {
		if k, ok := e.Output["error_kind"].(string); ok && k != "" {
			kinds[ErrorKind(k)] = true
		}
	}
	for _, want := range []ErrorKind{
		ErrObjectNotFound, ErrVersionExpired, ErrAttrNotFound, ErrTypeNoPermission,
	} {
		if !kinds[want] {
			t.Fatalf("decision log missing evidence for error kind %q (got=%v)", want, kinds)
		}
	}
	// ErrWritePolicyConflict 与 ErrRevoked 的可区分性在专项测试中验证。
}
