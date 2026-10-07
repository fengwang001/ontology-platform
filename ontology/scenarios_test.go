package ontology_test

import (
	"errors"
	"reflect"
	"testing"

	"ontology/ontology"
)

// TestBackfillRaceWithWrite：回填与并发写入竞争同一实例时，
// 回填必须识别"已被正常写入抢先回填"并跳过，不覆盖、不报错误。
func TestBackfillRaceWithWrite(t *testing.T) {
	log := newLogger(t)
	s := startBaseStore(t, "i1")

	// 写请求先到：旧结构写入被等价转换为新结构写入，实例视为已回填。
	err := s.Write("i1", ontology.VersionOld, ontology.Props{
		"name":       ontology.Present("written"),
		"age":        ontology.Present(41),
		"deprecated": ontology.Present("dropped-away"),
	})
	log.step(`Write(old) i1 {name=written, age=41, deprecated=...}`, errOr(err),
		"旧结构写入先等价转换为新结构写入；实例转为已回填")
	mustOK(t, err, "旧结构写入")

	out := s.RunBackfill()
	log.step("RunBackfill() 队首 i1", out,
		"实例已是新版本 -> Skipped, 非错误, 不覆盖")
	if !out.DidWork || !out.Skipped || out.Backfilled {
		t.Fatalf("期望 skip/already-backfilled, 实际=%+v", out)
	}

	newView, err := s.Read("i1", ontology.VersionNew)
	log.step("Read(new) i1", errOr(newView), "新结构值必须是写入值, 回填不得覆盖")
	mustOK(t, err, "新读")
	if newView["name"].Val != "written" || newView["age"].Val != 41 {
		t.Fatalf("写入被回填覆盖: %+v", newView)
	}
	if _, present := newView["deprecated"]; present {
		t.Fatalf("废弃属性不应出现在新视图: %+v", newView)
	}
	if newView["level"].Val != "L0" {
		t.Fatalf("新增属性应已补默认值 L0: %+v", newView)
	}
	if s.PendingBackfill() != 0 {
		t.Fatalf("队列应已空: %d", s.PendingBackfill())
	}
}

// TestBackfillRaceWithDelete：回填与删除竞争时，不得复活已删除实例。
func TestBackfillRaceWithDelete(t *testing.T) {
	log := newLogger(t)
	s := startBaseStore(t, "i1", "i2")

	mustOK(t, s.Delete("i1"), "删除 i1")
	log.step("Delete(i1)", "ok", "实例与队列项被移除语义处理")

	out := s.RunBackfill()
	log.step("RunBackfill()", out,
		"出队实例已删除 -> Skipped(deleted), 不复活, 非错误")
	if !out.DidWork || !out.Skipped || out.Reason != "deleted-before-backfill" {
		t.Fatalf("期望 deleted-before-backfill, 实际=%+v", out)
	}
	if _, exists := s.IsBackfilled("i1"); exists {
		t.Fatal("回填不得凭空复活已删除实例")
	}
	if _, err := s.Read("i1", ontology.VersionNew); !errors.Is(err, ontology.ErrNotFound) {
		t.Fatalf("删除后读取应为 ErrNotFound, 实际=%v", err)
	}

	// 队列继续按内部顺序处理 i2，证明跳过删除项不影响后续回填。
	out2 := s.RunBackfill()
	log.step("RunBackfill() 下一项", out2, "i2 正常回填")
	if !out2.Backfilled || out2.ID != "i2" {
		t.Fatalf("期望回填 i2, 实际=%+v", out2)
	}
}

// TestAmendOnlyAffectsPending：追加对应关系只影响尚未回填实例；
// 已回填实例不会被二次回填；已生效对应关系的修改请求必须拒绝。
func TestAmendOnlyAffectsPending(t *testing.T) {
	log := newLogger(t)
	s := startBaseStore(t, "i1", "i2")

	// i1 先回填（在追加声明之前）。
	out1 := s.RunBackfill()
	log.step("RunBackfill() i1", out1, "i1 按初始声明回填, 初始对应关系随即冻结")
	if !out1.Backfilled {
		t.Fatalf("i1 应被回填: %+v", out1)
	}

	// 追加全新属性 tag 的对应关系；合法。
	err := s.AmendDeclaration([]ontology.Mapping{
		{Attr: "tag", Kind: ontology.KindAdd, Default: ontology.Present("v2-default")},
	})
	log.step("AmendDeclaration(add tag default=v2-default)", errOr(err),
		"tag 从未生效过 -> 追加合法")
	mustOK(t, err, "追加 tag")

	// 试图修改已经对 i1 生效过的 name（keep -> drop）：必须拒绝。
	err = s.AmendDeclaration([]ontology.Mapping{{Attr: "name", Kind: ontology.KindDrop}})
	log.step("AmendDeclaration(name->drop)", errOr(err),
		"name 已对已回填实例 i1 生效(冻结) -> ErrInvalidArgument")
	if !errors.Is(err, ontology.ErrInvalidArgument) {
		t.Fatalf("期望 ErrInvalidArgument, 实际=%v", err)
	}

	// i1 已回填：追加的 tag 不回灌。
	v1, _ := s.Read("i1", ontology.VersionNew)
	log.step("Read(new) i1", errOr(v1), "已回填实例不被二次回填, 不应有 tag")
	if _, hasTag := v1["tag"]; hasTag {
		t.Fatalf("已回填实例不应出现后追加的 tag: %+v", v1)
	}

	// i2 尚未回填：回填时应用包含 tag 的当前声明。
	out2 := s.RunBackfill()
	log.step("RunBackfill() i2", out2, "未回填实例回填时应用追加后的声明")
	if !out2.Backfilled || out2.ID != "i2" {
		t.Fatalf("i2 应回填: %+v", out2)
	}
	v2, _ := s.Read("i2", ontology.VersionNew)
	log.step("Read(new) i2", errOr(v2), "tag 以 v2-default 出现")
	if v2["tag"].Val != "v2-default" {
		t.Fatalf("未回填实例应应用新追加默认值: %+v", v2)
	}
}

// TestViewsConsistentBeforeAfterBackfill：回填前后，对同一实例的
// 新旧两种版本读写看到的共有属性值始终一致；新读未回填实例即时现算、不等回填。
func TestViewsConsistentBeforeAfterBackfill(t *testing.T) {
	log := newLogger(t)
	s := startBaseStore(t, "i1")

	oldBefore, _ := s.Read("i1", ontology.VersionOld)
	newBefore, _ := s.Read("i1", ontology.VersionNew)
	log.step("回填前 Read(old/new) i1",
		"old="+fmtStr(oldBefore)+" new="+fmtStr(newBefore),
		"新读即时现算, 共有属性 name/age 值一致, deprecated 仅旧视图可见")
	if !reflect.DeepEqual(oldBefore["name"], newBefore["name"]) ||
		!reflect.DeepEqual(oldBefore["age"], newBefore["age"]) {
		t.Fatalf("回填前共有属性值不一致: old=%+v new=%+v", oldBefore, newBefore)
	}
	if _, ok := newBefore["deprecated"]; ok {
		t.Fatal("回填前新视图不应包含废弃属性")
	}
	if _, ok := oldBefore["level"]; ok {
		t.Fatal("回填前旧视图不应包含新增属性")
	}

	out := s.RunBackfill()
	log.step("RunBackfill()", out, "i1 完成回填")
	if !out.Backfilled {
		t.Fatalf("应回填: %+v", out)
	}

	oldAfter, _ := s.Read("i1", ontology.VersionOld)
	newAfter, _ := s.Read("i1", ontology.VersionNew)
	log.step("回填后 Read(old/new) i1",
		"old="+fmtStr(oldAfter)+" new="+fmtStr(newAfter),
		"回填后共有属性依旧一致; level 补默认值仅新视图; deprecated 消失")
	if !reflect.DeepEqual(oldAfter["name"], newAfter["name"]) ||
		!reflect.DeepEqual(oldAfter["age"], newAfter["age"]) {
		t.Fatalf("回填后共有属性值不一致: old=%+v new=%+v", oldAfter, newAfter)
	}
	if newAfter["level"].Val != "L0" {
		t.Fatalf("新视图应含默认 level: %+v", newAfter)
	}
	if _, ok := oldAfter["deprecated"]; ok {
		t.Fatal("回填后旧视图不应再出现废弃属性")
	}

	// 回填后再走一次新结构写入与旧结构写入，两种视图仍一致。
	mustOK(t, s.Write("i1", ontology.VersionNew, ontology.Props{"name": ontology.Present("x"), "age": ontology.Present(9)}),
		"新写")
	log.step("Write(new) {name=x,age=9}", "ok", "新写后旧视图按转换时刻投影")
	oldNow, _ := s.Read("i1", ontology.VersionOld)
	newNow, _ := s.Read("i1", ontology.VersionNew)
	if oldNow["name"].Val != "x" || newNow["name"].Val != "x" ||
		oldNow["age"].Val != 9 || newNow["age"].Val != 9 {
		t.Fatalf("新写后视图不一致: old=%+v new=%+v", oldNow, newNow)
	}
}

// TestContradictoryAndErrorOrder：矛盾声明与错误次序。
func TestContradictoryAndErrorOrder(t *testing.T) {
	log := newLogger(t)

	// 同一批次同一属性出现两种对应（模拟"同时保留与废弃"的矛盾）。
	s := ontology.NewStore("P", ontology.VersionOld, ontology.VersionNew)
	err := s.StartMigration(ontology.Migration{
		ObjectType: "P", From: 1, To: 2,
		Mappings: []ontology.Mapping{
			{Attr: "a", Kind: ontology.KindKeep},
			{Attr: "a", Kind: ontology.KindDrop},
		},
	})
	log.step("StartMigration(a:keep 与 a:drop 同批)", errOr(err),
		"自相矛盾 -> ErrInvalidArgument, 迁移未发起")
	if !errors.Is(err, ontology.ErrInvalidArgument) {
		t.Fatalf("期望参数非法: %v", err)
	}

	// 新增属性缺默认值 -> 参数非法。
	err = s.StartMigration(ontology.Migration{
		ObjectType: "P", From: 1, To: 2,
		Mappings: []ontology.Mapping{{Attr: "a", Kind: ontology.KindAdd}},
	})
	log.step("StartMigration(a:add 无默认值)", errOr(err), "缺默认值 -> ErrInvalidArgument")
	if !errors.Is(err, ontology.ErrInvalidArgument) {
		t.Fatalf("期望参数非法: %v", err)
	}

	s = startBaseStore(t, "i1")
	// 参数非法 优先于 目标不存在：对不存在实例的写入若载荷先非法，报参数非法。
	err = s.Write("missing", ontology.VersionOld, ontology.Props{"level": ontology.Present(1)})
	log.step("Write(missing, old, 含新增属性 level)", errOr(err),
		"参数非法在前 -> ErrInvalidArgument 而非 ErrNotFound")
	if !errors.Is(err, ontology.ErrInvalidArgument) {
		t.Fatalf("错误次序错误, 期望参数非法: %v", err)
	}
	// 载荷合法、目标不存在 -> ErrNotFound。
	err = s.Write("missing", ontology.VersionOld, ontology.Props{"name": ontology.Present("n")})
	log.step("Write(missing, old, 合法载荷)", errOr(err),
		"参数合法, 目标不存在 -> ErrNotFound")
	if !errors.Is(err, ontology.ErrNotFound) {
		t.Fatalf("期望 ErrNotFound: %v", err)
	}

	// 被拒绝的声明修订不得改变任何可见数据/回填状态。
	pendingBefore := s.PendingBackfill()
	_ = s.RunBackfill() // 先回填 i1 以冻结 name
	err = s.AmendDeclaration([]ontology.Mapping{{Attr: "name", Kind: ontology.KindDrop}})
	if !errors.Is(err, ontology.ErrInvalidArgument) {
		t.Fatalf("期望拒绝冻结修改: %v", err)
	}
	view, _ := s.Read("i1", ontology.VersionNew)
	log.step("拒绝修订后 Read(new) i1", errOr(view), "数据与冻结前一致")
	if view["name"].Val != "i1-n" {
		t.Fatalf("被拒绝修订不得改变数据: %+v", view)
	}
	if s.PendingBackfill() != pendingBefore-1 {
		t.Fatalf("被拒绝修订不得改变回填状态")
	}
}

func errOr(v any) any {
	if err, ok := v.(error); ok && err != nil {
		return "ERR: " + err.Error()
	}
	return v
}
