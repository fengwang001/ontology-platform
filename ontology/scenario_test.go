package ontology

import "testing"

// 可延迟即时模式：一条语句内两行互换键成功；
// 非可延迟约束在同语句互换时于第一个 op 即失败并整条回滚。
func TestSwapKeysWithinStatement(t *testing.T) {
	def, err := NewTable(true, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := def.Begin(); err != nil {
		t.Fatal(err)
	}
	expectReason(t, def.Apply([]Op{
		Insert("a", StringKey("x")),
		Insert("b", StringKey("y")),
	}), ReasonOK)
	expectReason(t, def.Apply([]Op{
		Update("a", StringKey("y")),
		Update("b", StringKey("x")),
	}), ReasonOK)
	expectReason(t, def.Commit(), ReasonOK)
	if got, ok := def.Get("a"); !ok || got.Value != "y" {
		t.Fatalf("a = %+v,%v want y", got, ok)
	}
	if got, ok := def.Get("b"); !ok || got.Value != "x" {
		t.Fatalf("b = %+v,%v want x", got, ok)
	}

	non, err := NewTable(false, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := non.Begin(); err != nil {
		t.Fatal(err)
	}
	if err := non.Apply([]Op{
		Insert("a", StringKey("x")),
		Insert("b", StringKey("y")),
	}); err != nil {
		t.Fatal(err)
	}
	err = non.Apply([]Op{
		Update("a", StringKey("y")),
		Update("b", StringKey("x")),
	})
	expectReason(t, err, ReasonUniqueViolation)
	if e := err.(*Error); e.OpIndex != 0 || e.ViolatedKey != "y" {
		t.Fatalf("expected violation at op 0 on key y, got idx=%d key=%q", e.OpIndex, e.ViolatedKey)
	}
	// 整条语句撤销：a、b 仍是原键
	if got, _ := non.Get("a"); got.Value != "x" {
		t.Fatalf("a should roll back to x, got %q", got.Value)
	}
	if got, _ := non.Get("b"); got.Value != "y" {
		t.Fatalf("b should stay y, got %q", got.Value)
	}
}

// 两条语句分别改键：延迟模式提交成功；即时模式第一条语句末即失败。
func TestTwoStatementsDeferredVsImmediate(t *testing.T) {
	run := func(deferrable, initiallyDeferred bool) error {
		tbl, err := NewTable(deferrable, initiallyDeferred)
		if err != nil {
			t.Fatal(err)
		}
		if err := tbl.Begin(); err != nil {
			t.Fatal(err)
		}
		if err := tbl.Apply([]Op{
			Insert("a", StringKey("x")),
			Insert("b", StringKey("y")),
		}); err != nil {
			t.Fatal(err)
		}
		if err := tbl.Apply([]Op{Update("a", StringKey("y"))}); err != nil {
			return err
		}
		if err := tbl.Apply([]Op{Update("b", StringKey("x"))}); err != nil {
			return err
		}
		return tbl.Commit()
	}

	if err := run(true, true); err != nil {
		t.Fatalf("deferred chain should commit, got %v", err)
	}

	err := run(true, false)
	expectReason(t, err, ReasonUniqueViolation)
	if e := err.(*Error); e.OpIndex != -1 || e.ViolatedKey != "y" {
		t.Fatalf("expected end-of-statement violation key y, got idx=%d key=%q", e.OpIndex, e.ViolatedKey)
	}
}

// 延迟模式留下重复：Commit 失败并整体回滚，已提交状态不变。
func TestDeferredCommitViolationRollsBack(t *testing.T) {
	tbl, err := NewTable(true, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := tbl.Begin(); err != nil {
		t.Fatal(err)
	}
	if err := tbl.Apply([]Op{
		Insert("a", StringKey("x")),
		Insert("b", StringKey("x")),
	}); err != nil {
		t.Fatalf("deferred statement must not check: %v", err)
	}
	err = tbl.Commit()
	expectReason(t, err, ReasonUniqueViolation)
	if e := err.(*Error); e.ViolatedKey != "x" {
		t.Fatalf("violated key = %q want x", e.ViolatedKey)
	}
	// 事务已回滚：无事务、已提交状态为空
	if err := tbl.Commit(); err == nil {
		t.Fatal("Commit after rollback should report no transaction")
	} else if e := err.(*Error); e.Reason != ReasonNoTransaction {
		t.Fatalf("want no transaction, got %v", err)
	}
	if len(tbl.Keys()) != 0 {
		t.Fatalf("committed state must be unchanged, got %v", tbl.Keys())
	}
}

// 切到 IMMEDIATE 遇重复被拒且仍为延迟模式；修复后再切成功。
func TestSetModeRejectThenRepair(t *testing.T) {
	tbl, err := NewTable(true, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := tbl.Begin(); err != nil {
		t.Fatal(err)
	}
	if err := tbl.Apply([]Op{
		Insert("a", StringKey("k")),
		Insert("b", StringKey("k")),
	}); err != nil {
		t.Fatal(err)
	}
	err = tbl.SetMode(IMMEDIATE)
	expectReason(t, err, ReasonUniqueViolation)
	if e := err.(*Error); e.ViolatedKey != "k" {
		t.Fatalf("violated key = %q want k", e.ViolatedKey)
	}
	// 模式不变：重复语句仍不检查
	if err := tbl.Apply([]Op{Insert("c", StringKey("k"))}); err != nil {
		t.Fatalf("mode must remain DEFERRED, got %v", err)
	}
	// 修复：删掉两行重复，只留一行
	if err := tbl.Apply([]Op{Delete("b"), Delete("c")}); err != nil {
		t.Fatal(err)
	}
	if err := tbl.SetMode(IMMEDIATE); err != nil {
		t.Fatalf("SetMode after repair should succeed, got %v", err)
	}
	// 即时模式下再制造重复应在语句末被拒
	err = tbl.Apply([]Op{Insert("b", StringKey("k"))})
	expectReason(t, err, ReasonUniqueViolation)
}

// 多个 NULL 共存；空串与 NULL 有别；空串本身仍唯一。
func TestNullsAndEmptyString(t *testing.T) {
	tbl, err := NewTable(false, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := tbl.Begin(); err != nil {
		t.Fatal(err)
	}
	if err := tbl.Apply([]Op{
		Insert("a", NullKey()),
		Insert("b", NullKey()),
		Insert("c", NullKey()),
		Insert("d", StringKey("")),
	}); err != nil {
		t.Fatalf("multiple NULLs and one empty string should coexist: %v", err)
	}
	// 第二个空串违例
	err = tbl.Apply([]Op{Insert("e", StringKey(""))})
	expectReason(t, err, ReasonUniqueViolation)
	if e := err.(*Error); e.ViolatedKey != "" {
		t.Fatalf("empty-string violation key should be empty, got %q", e.ViolatedKey)
	}
	// NULL 更新为另一 NULL 始终合法
	if err := tbl.Apply([]Op{Update("a", NullKey())}); err != nil {
		t.Fatal(err)
	}
	if _, ok := tbl.Get("missing"); ok {
		t.Fatal("missing row should report not found")
	}
}

// 语句失败时，本语句内此前已成功的 op 一并撤销，事务仍有效。
func TestFailedStatementRollsBackAllOps(t *testing.T) {
	tbl, err := NewTable(false, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := tbl.Begin(); err != nil {
		t.Fatal(err)
	}
	if err := tbl.Apply([]Op{Insert("a", StringKey("1"))}); err != nil {
		t.Fatal(err)
	}
	err = tbl.Apply([]Op{
		Insert("b", StringKey("2")),
		Update("a", StringKey("2")),
		Insert("c", StringKey("3")),
	})
	expectReason(t, err, ReasonUniqueViolation)
	if e := err.(*Error); e.OpIndex != 1 {
		t.Fatalf("want failure at op 1, got %d", e.OpIndex)
	}
	if _, ok := tbl.Get("b"); ok {
		t.Fatal("b from failed statement must be rolled back")
	}
	if _, ok := tbl.Get("c"); ok {
		t.Fatal("c from failed statement must be rolled back")
	}
	if got, _ := tbl.Get("a"); got.Value != "1" {
		t.Fatalf("a must be restored to 1, got %q", got.Value)
	}
	// 事务仍有效，可继续操作并提交
	if err := tbl.Apply([]Op{Insert("b", StringKey("2"))}); err != nil {
		t.Fatalf("transaction should remain usable: %v", err)
	}
	if err := tbl.Commit(); err != nil {
		t.Fatal(err)
	}
}

// 多个违例键取字节序最小者：语句末检查、SetMode、Commit 三处一致。
func TestMinViolatedKey(t *testing.T) {
	tbl, _ := NewTable(true, false)
	_ = tbl.Begin()
	_ = tbl.Apply([]Op{
		Insert("a", StringKey("banana")),
		Insert("b", StringKey("apple")),
	})
	err := tbl.Apply([]Op{
		Update("a", StringKey("dup1")),
		Insert("c", StringKey("dup1")),
		Update("b", StringKey("dup0")),
		Insert("d", StringKey("dup0")),
	})
	expectReason(t, err, ReasonUniqueViolation)
	if e := err.(*Error); e.ViolatedKey != "dup0" {
		t.Fatalf("end-of-statement min key = %q want dup0", e.ViolatedKey)
	}

	// 延迟模式：SetMode 与 Commit 同样取最小键
	tbl2, _ := NewTable(true, true)
	_ = tbl2.Begin()
	_ = tbl2.Apply([]Op{
		Insert("a", StringKey("z")),
		Insert("b", StringKey("z")),
		Insert("c", StringKey("a")),
		Insert("d", StringKey("a")),
	})
	err = tbl2.SetMode(IMMEDIATE)
	expectReason(t, err, ReasonUniqueViolation)
	if e := err.(*Error); e.ViolatedKey != "a" {
		t.Fatalf("SetMode min key = %q want a", e.ViolatedKey)
	}
	err = tbl2.Commit()
	expectReason(t, err, ReasonUniqueViolation)
	if e := err.(*Error); e.ViolatedKey != "a" {
		t.Fatalf("Commit min key = %q want a", e.ViolatedKey)
	}
}

// 构造参数、无事务、模式非法、不可延迟等可区分原因与报错顺序。
func TestDistinguishableErrors(t *testing.T) {
	_, err := NewTable(false, true)
	expectReason(t, err, ReasonInitiallyDeferredRequiresDeferrable)

	tbl, _ := NewTable(false, false)
	expectReason(t, tbl.Apply(nil), ReasonNoTransaction)
	expectReason(t, tbl.Commit(), ReasonNoTransaction)
	expectReason(t, tbl.Rollback(), ReasonNoTransaction)
	// SetMode 报错顺序：无事务 > 模式非法 > 不可延迟
	expectReason(t, tbl.SetMode(Mode(99)), ReasonNoTransaction)
	_ = tbl.Begin()
	expectReason(t, tbl.Begin(), ReasonTransactionActive)
	expectReason(t, tbl.SetMode(Mode(99)), ReasonInvalidMode)
	expectReason(t, tbl.SetMode(DEFERRED), ReasonNotDeferrable)
	expectReason(t, tbl.SetMode(IMMEDIATE), ReasonNotDeferrable)

	// Apply 报错次序：空行号 > 已存在/不存在 > 即时违例
	err = tbl.Apply([]Op{Insert("", StringKey("x"))})
	expectReason(t, err, ReasonEmptyRow)
	if e := err.(*Error); e.OpIndex != 0 {
		t.Fatalf("op index = %d want 0", e.OpIndex)
	}
	_ = tbl.Apply([]Op{Insert("a", StringKey("x"))})
	err = tbl.Apply([]Op{Insert("a", StringKey("y"))})
	expectReason(t, err, ReasonRowExists)
	err = tbl.Apply([]Op{Update("nope", StringKey("y"))})
	expectReason(t, err, ReasonRowNotFound)
	err = tbl.Apply([]Op{Delete("nope")})
	expectReason(t, err, ReasonRowNotFound)
}

// 被触碰行已删除后不再参与延迟检查，提交成功。
func TestDeletedTouchedRowExcluded(t *testing.T) {
	tbl, _ := NewTable(true, true)
	_ = tbl.Begin()
	_ = tbl.Apply([]Op{
		Insert("a", StringKey("x")),
		Insert("b", StringKey("x")),
		Delete("b"),
	})
	if err := tbl.Commit(); err != nil {
		t.Fatalf("deleted dup row should be excluded, got %v", err)
	}
	keys := tbl.Keys()
	if len(keys) != 1 || keys[0].Row != "a" || keys[0].Key.Value != "x" {
		t.Fatalf("unexpected committed state: %v", keys)
	}
}
