package kanban

import "testing"

func TestDependencyGating(t *testing.T) {
	b := mustBoard(t, baseCfg(3, 50, []int{0, 5, 0}))
	mustOK(t, ignoreErr(b.AddCard("a", "alice", 1))) // 前置
	mustOK(t, ignoreErr(b.AddCard("b", "bob", 2)))   // 后继
	if err := b.AddDep("u", "b", "a", 1, 3); err != nil {
		t.Fatal(err)
	}
	// 重复声明
	assertCode(t, ErrDependency, b.AddDep("u", "b", "a", 2, 4))
	// 自依赖
	assertCode(t, ErrDependency, b.AddDep("u", "a", "a", 1, 4))
	// b 的前置未完成，不能离开待办
	_, err := b.Move("u", "b", 1, 2, false, 5)
	assertCode(t, ErrDependency, err)
	// a 完成后 b 可走
	mustOK(t, ignoreErr(b.Move("u", "a", 1, 1, false, 6)))
	mustOK(t, ignoreErr(b.Move("u", "a", 2, 2, false, 7)))
	mustOK(t, ignoreErr(b.Move("u", "b", 1, 2, false, 8)))
	// b 已离开待办，不能再加未完成前置
	mustOK(t, ignoreErr(b.AddCard("c", "carol", 9)))
	assertCode(t, ErrDependency, b.AddDep("u", "b", "c", 3, 10))
	// 前置不存在
	assertCode(t, ErrCardNotFound, b.AddDep("u", "b", "ghost", 3, 10))
	// 版本冲突先于依赖类错误（重复声明 + 错版本）
	assertCode(t, ErrVersionConflict, b.AddDep("u", "b", "a", 99, 10))
	// c 完成后再给 b 加前置合法（已离开待办但前置已完成）
	mustOK(t, ignoreErr(b.Move("u", "c", 1, 1, false, 11)))
	mustOK(t, ignoreErr(b.Move("u", "c", 2, 2, false, 12)))
	if err := b.AddDep("u", "b", "c", 3, 13); err != nil {
		t.Fatalf("done prereq for active card should be allowed: %v", err)
	}
	// RemoveDep：不存在、版本冲突优先
	if err := b.RemoveDep("u", "b", "a", 4, 14); err != nil {
		t.Fatal(err)
	}
	assertCode(t, ErrDependency, b.RemoveDep("u", "b", "a", 5, 15))
}

func TestCycleDetection(t *testing.T) {
	// a -> b -> c（箭头表示前置关系 prerequisite -> card）
	b := mustBoard(t, baseCfg(3, 50, []int{0, 5, 0}))
	for _, id := range []string{"a", "b", "c"} {
		mustOK(t, ignoreErr(b.AddCard(id, "x", 1)))
	}
	if err := b.AddDep("u", "b", "a", 1, 2); err != nil {
		t.Fatal(err)
	}
	if err := b.AddDep("u", "c", "b", 1, 3); err != nil {
		t.Fatal(err)
	}
	// 新增 a 的前置 c => c -> ... -> a -> ... -> c 成环
	assertCode(t, ErrDependency, b.AddDep("u", "a", "c", 1, 4))
	// 成环操作不改变任何状态：a 版本仍为 2（AddCard v1, AddDep 不作用在 a）
	ca, _ := b.GetCard("a")
	if ca.Version != 1 {
		t.Fatalf("rejected cycle add changed version: %d", ca.Version)
	}
	// 直接两环：给 a 加前置 b，b 已在 a 的后继链上
	assertCode(t, ErrDependency, b.AddDep("u", "a", "b", 1, 5))
}

func TestReopenBlockedBySuccessor(t *testing.T) {
	b := mustBoard(t, baseCfg(3, 50, []int{0, 5, 0}))
	mustOK(t, ignoreErr(b.AddCard("a", "alice", 1)))
	mustOK(t, ignoreErr(b.AddCard("b", "bob", 2)))
	if err := b.AddDep("u", "b", "a", 1, 3); err != nil {
		t.Fatal(err)
	}
	// 两张卡都完成
	mustOK(t, ignoreErr(b.Move("u", "a", 1, 1, false, 4)))
	mustOK(t, ignoreErr(b.Move("u", "a", 2, 2, false, 5)))
	cb, _ := b.GetCard("b")
	mustOK(t, ignoreErr(b.Move("u", "b", 1, cb.Version, false, 6)))
	cb, _ = b.GetCard("b")
	mustOK(t, ignoreErr(b.Move("u", "b", 2, cb.Version, false, 7)))
	// 后继 b 已完成，a 不能 reopen
	ca, _ := b.GetCard("a")
	_, err := b.Reopen("u", "a", ca.Version, 8)
	assertCode(t, ErrDependency, err)
	// 把 b reopen 再移回待办（完成列不能直接左移，须先 Reopen；向左不检查依赖），a 即可 reopen
	cb, _ = b.GetCard("b")
	rb := cardOf(b.Reopen("u", "b", cb.Version, 9))
	mustOK(t, ignoreErr(b.Move("u", "b", 0, rb.Version, false, 10)))
	ca, _ = b.GetCard("a")
	ra, err := b.Reopen("u", "a", ca.Version, 11)
	if err != nil {
		t.Fatalf("reopen should succeed once successor back in todo: %v", err)
	}
	if ra.Column != 1 || ra.Version != ca.Version+1 {
		t.Fatalf("reopen result wrong: %+v", ra)
	}
	// 非完成列卡片不能 reopen
	assertCode(t, ErrIllegalFlow, ignoreErr(b.Reopen("u", "a", ra.Version, 12)))
	// reopen 同样受列上限约束
	b2 := mustBoard(t, baseCfg(3, 50, []int{0, 1, 0}))
	mustOK(t, ignoreErr(b2.AddCard("x", "alice", 1)))
	mustOK(t, ignoreErr(b2.AddCard("y", "bob", 2)))
	mustOK(t, ignoreErr(b2.Move("u", "x", 1, 1, false, 3)))
	mustOK(t, ignoreErr(b2.Move("u", "x", 2, 2, false, 4)))
	mustOK(t, ignoreErr(b2.Move("u", "y", 1, 1, false, 5)))
	cx, _ := b2.GetCard("x")
	_, err = b2.Reopen("u", "x", cx.Version, 6)
	assertCode(t, ErrColumnFull, err)
}

func TestRejectOrderingAdjacentPairs(t *testing.T) {
	// 逐对相邻类别验证“只报第一个”。
	b := mustBoard(t, baseCfg(3, 50, []int{0, 5, 0}))
	mustOK(t, ignoreErr(b.AddCard("a", "alice", 10)))

	// 参数非法 > 时钟回退（坏 now + 回退）
	_, err := b.AddCard("z1", "alice", -1)
	assertCode(t, ErrInvalidArgument, err)
	// 时钟回退 > 卡片不存在
	_, err = b.Move("u", "ghost", 1, 1, false, 1)
	assertCode(t, ErrClockRollback, err)
	// 卡片不存在 > 版本冲突
	_, err = b.Move("u", "ghost", 1, 99, false, 11)
	assertCode(t, ErrCardNotFound, err)
	// 版本冲突 > 流转不合法（坏版本 + 越列 + 已在原列 一并构造）
	_, err = b.Move("u", "a", 0, 99, false, 11)
	assertCode(t, ErrVersionConflict, err)
	// 流转不合法 > 依赖未完成（已在原列，同时挂未完成前置）
	mustOK(t, ignoreErr(b.AddCard("p", "pat", 12)))
	mustOK(t, b.AddDep("u", "a", "p", 1, 13))
	_, err = b.Move("u", "a", 0, 2, false, 14)
	assertCode(t, ErrIllegalFlow, err)
	// 依赖未完成 > 加急已占用（a 有未完成前置；先制造一张加急卡）
	mustOK(t, ignoreErr(b.AddCard("e", "eve", 15)))
	mustOK(t, ignoreErr(b.Move("u", "e", 1, 1, true, 16)))
	_, err = b.Move("u", "a", 1, 2, true, 17)
	assertCode(t, ErrDependency, err)
	// 加急已占用 > 列上限：无依赖的卡 + 加急申请，列已满
	mustOK(t, ignoreErr(b.AddCard("q", "quinn", 18)))
	mustOK(t, b.SetColumnLimit(1, 1, 19)) // e 占用 col1，恰满
	_, err = b.Move("u", "q", 1, 1, true, 20)
	assertCode(t, ErrExpediteBusy, err)
	// 列上限已满 > 负责人上限：把负责人 G 降到 1，列上限也设为 1；
	// 新卡普通移入，应先报列上限。
	b2 := mustBoard(t, baseCfg(3, 1, []int{0, 1, 0}))
	mustOK(t, ignoreErr(b2.AddCard("h", "hank", 1)))
	mustOK(t, ignoreErr(b2.Move("u", "h", 1, 1, false, 2)))
	mustOK(t, ignoreErr(b2.AddCard("i", "hank", 3)))
	_, err = b2.Move("u", "i", 1, 1, false, 4)
	assertCode(t, ErrColumnFull, err)
	// 负责人上限 > （无下一类）：列不限 G=1，第二张同负责人
	b3 := mustBoard(t, baseCfg(3, 1, []int{0, 0, 0}))
	mustOK(t, ignoreErr(b3.AddCard("h", "hank", 1)))
	mustOK(t, ignoreErr(b3.Move("u", "h", 1, 1, false, 2)))
	mustOK(t, ignoreErr(b3.AddCard("i", "hank", 3)))
	_, err = b3.Move("u", "i", 1, 1, false, 4)
	assertCode(t, ErrOwnerFull, err)
}

func TestChangeOwner(t *testing.T) {
	b := mustBoard(t, baseCfg(3, 1, []int{0, 0, 0}))
	mustOK(t, ignoreErr(b.AddCard("a", "alice", 1)))
	mustOK(t, ignoreErr(b.Move("u", "a", 1, 1, false, 2)))
	// 新负责人已满
	mustOK(t, ignoreErr(b.AddCard("b", "bob", 3)))
	mustOK(t, ignoreErr(b.Move("u", "b", 1, 1, false, 4)))
	_, err := b.ChangeOwner("u", "a", "bob", 2, 5)
	assertCode(t, ErrOwnerFull, err)
	// 待办列改负责人不受 G 约束
	mustOK(t, ignoreErr(b.AddCard("c", "carol", 6)))
	c, err := b.ChangeOwner("u", "c", "bob", 1, 7)
	if err != nil || c.Owner != "bob" || c.Version != 2 {
		t.Fatalf("change owner in todo: %+v %v", c, err)
	}
	// 空负责人参数非法；相同负责人流转不合法
	assertCode(t, ErrInvalidArgument, func() error { _, e := b.ChangeOwner("u", "c", "", 2, 8); return e }())
	_, err = b.ChangeOwner("u", "c", "bob", 2, 8)
	assertCode(t, ErrIllegalFlow, err)
}

func TestInvariantsAfterRandomishSequence(t *testing.T) {
	b := mustBoard(t, baseCfg(4, 2, []int{0, 2, 2, 0}))
	ids := []string{"a", "b", "c", "d"}
	for _, id := range ids {
		mustOK(t, ignoreErr(b.AddCard(id, "u1", 1)))
	}
	seq := []func() error{
		func() error { _, e := b.Move("u", "a", 1, 1, false, 2); return e },
		func() error { _, e := b.Move("u", "b", 1, 1, false, 3); return e },
		func() error { _, e := b.Move("u", "c", 1, 1, false, 4); return e }, // 列/G
		func() error { _, e := b.Move("u", "a", 2, 2, false, 5); return e },
		func() error { _, e := b.Move("u", "c", 1, 1, false, 6); return e },
		func() error { return b.SetColumnLimit(1, 1, 7) },
		func() error { _, e := b.Move("u", "d", 1, 1, false, 8); return e },
		func() error { _, e := b.Move("u", "a", 1, 3, false, 9); return e },
		func() error { _, e := b.Move("u", "a", 0, 4, false, 10); return e },
	}
	for _, f := range seq {
		_ = f()
		if err := b.CheckInvariants(); err != nil {
			t.Fatalf("invariants broken: %v", err)
		}
	}
}
