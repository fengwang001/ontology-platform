package pharmacy

import "testing"

func newEngine(t *testing.T, r int) *Engine {
	t.Helper()
	e, err := NewEngine(Config{R: r})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return e
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func expectErr(t *testing.T, err error, code ErrCode) {
	t.Helper()
	if CodeOf(err) != code {
		t.Fatalf("expect error %s, got %v", code, err)
	}
}

func mustDrug(t *testing.T, e *Engine, now int, id string) DrugState {
	t.Helper()
	d, err := e.QueryDrug(now, id)
	if err != nil {
		t.Fatalf("QueryDrug(%s): %v", id, err)
	}
	return d
}

func mustRx(t *testing.T, e *Engine, now int, id string) RxState {
	t.Helper()
	p, err := e.QueryPrescription(now, id)
	if err != nil {
		t.Fatalf("QueryPrescription(%s): %v", id, err)
	}
	return p
}

func setupDrug(t *testing.T, e *Engine, id string, box int, splittable bool, stock int) {
	t.Helper()
	must(t, e.RegisterDrug(0, id, box, splittable))
	if stock > 0 {
		must(t, e.Inbound(0, id, stock))
	}
}

func accept1(t *testing.T, e *Engine, now int, id, drug string, qty int, whole bool) error {
	t.Helper()
	return e.AcceptPrescription(now, RxInput{
		ID: id, Patient: "p-" + id, IssueTime: now, WholeOrder: whole,
		Lines: []LineInput{{DrugID: drug, Qty: qty}},
	})
}

// 取药窗口：恰到第 R 分钟仍可取，晚一分钟失效。
func TestPickupWindowBoundary(t *testing.T) {
	e := newEngine(t, 10)
	setupDrug(t, e, "A", 1, true, 5)
	must(t, accept1(t, e, 1, "rx1", "A", 5, false)) // 预留起始 1，失效时刻 12
	d := mustDrug(t, e, 11, "A")
	if d.Reserved != 5 || d.Available != 0 {
		t.Fatalf("第 R 分钟预留应仍有效: %+v", d)
	}
	must(t, e.Dispense(11, "rx1")) // 第 R 分钟取药成功
	p := mustRx(t, e, 11, "rx1")
	if p.Status != RxCompleted || p.Lines[0].Dispensed != 5 {
		t.Fatalf("取药后应完成: %+v", p)
	}

	e2 := newEngine(t, 10)
	setupDrug(t, e2, "A", 1, true, 5)
	must(t, accept1(t, e2, 1, "rx1", "A", 5, false))
	d = mustDrug(t, e2, 12, "A") // 第 R+1 分钟已失效
	if d.Reserved != 0 || d.Available != 5 {
		t.Fatalf("第 R+1 分钟预留应失效: %+v", d)
	}
	expectErr(t, e2.Dispense(12, "rx1"), ErrNoValidReservation)
}

// 处方有效期：恰等于有效期仍有效，其后过期。
func TestValidityBoundary(t *testing.T) {
	e := newEngine(t, 100000)
	setupDrug(t, e, "A", 1, true, 10)
	in := RxInput{ID: "rx1", Patient: "p", IssueTime: 0, Lines: []LineInput{{DrugID: "A", Qty: 10}}}
	must(t, e.AcceptPrescription(ValidityMinutes, in)) // 恰 72h，仍有效
	must(t, e.Dispense(ValidityMinutes, "rx1"))

	in2 := RxInput{ID: "rx2", Patient: "p", IssueTime: 0, Lines: []LineInput{{DrugID: "A", Qty: 1}}}
	expectErr(t, e.AcceptPrescription(ValidityMinutes+1, in2), ErrPrescriptionExpired)

	e2 := newEngine(t, 100000)
	setupDrug(t, e2, "A", 1, true, 10)
	must(t, e2.AcceptPrescription(0, RxInput{ID: "rx1", Patient: "p", IssueTime: 0, Lines: []LineInput{{DrugID: "A", Qty: 10}}}))
	p := mustRx(t, e2, ValidityMinutes+1, "rx1")
	if p.Status != RxExpired || p.Lines[0].Reserved != 0 || p.Lines[0].Status != LineVoid {
		t.Fatalf("有效期后处方应过期且预留释放: %+v", p)
	}
	d := mustDrug(t, e2, ValidityMinutes+1, "A")
	if d.Reserved != 0 || d.Available != 10 {
		t.Fatalf("过期后库存应回到可用量: %+v", d)
	}
}

// 不可拆零取整：向上取整（多出不算欠药）与向下取整。
func TestRoundingUpDownAndExcess(t *testing.T) {
	e := newEngine(t, 100)
	setupDrug(t, e, "A", 4, false, 8)
	must(t, accept1(t, e, 0, "up", "A", 5, false)) // 需 5，向上取整到 8
	p := mustRx(t, e, 0, "up")
	if p.Lines[0].Reserved != 8 || p.Lines[0].Backorder != 0 {
		t.Fatalf("向上取整应预留 8 且无欠药: %+v", p.Lines[0])
	}
	must(t, e.Dispense(1, "up"))
	p = mustRx(t, e, 1, "up")
	if p.Status != RxCompleted || p.Lines[0].Dispensed != 8 {
		t.Fatalf("取整多出部分发放后处方应完成: %+v", p)
	}

	e2 := newEngine(t, 100)
	setupDrug(t, e2, "A", 4, false, 6)
	must(t, accept1(t, e2, 0, "down", "A", 5, false)) // 需 5，可用 6 不足 8，向下取整到 4
	p = mustRx(t, e2, 0, "down")
	if p.Lines[0].Reserved != 4 || p.Lines[0].Backorder != 1 {
		t.Fatalf("向下取整应预留 4 欠 1: %+v", p.Lines[0])
	}

	e3 := newEngine(t, 100)
	setupDrug(t, e3, "A", 4, false, 3)
	must(t, accept1(t, e3, 0, "zero", "A", 5, false)) // 可用 3 不足一盒，预留 0
	p = mustRx(t, e3, 0, "zero")
	if p.Lines[0].Reserved != 0 || p.Lines[0].Backorder != 5 {
		t.Fatalf("不足一盒应预留 0 欠 5: %+v", p.Lines[0])
	}
}

// 整单发放：任一行不能满足则整单拒绝，且不产生任何预留与欠药。
func TestWholeOrderRejectNoSideEffects(t *testing.T) {
	e := newEngine(t, 100)
	setupDrug(t, e, "A", 1, true, 10)
	setupDrug(t, e, "B", 1, true, 0)
	err := e.AcceptPrescription(0, RxInput{
		ID: "w1", Patient: "p", IssueTime: 0, WholeOrder: true,
		Lines: []LineInput{{DrugID: "A", Qty: 5}, {DrugID: "B", Qty: 1}},
	})
	expectErr(t, err, ErrOutOfStock)
	if d := mustDrug(t, e, 0, "A"); d.Reserved != 0 || d.BackorderTotal != 0 {
		t.Fatalf("整单拒绝不得产生预留与欠药: %+v", d)
	}
	if _, err := e.QueryPrescription(0, "w1"); CodeOf(err) != ErrNotFound {
		t.Fatalf("整单拒绝不得留下处方: %v", err)
	}
	// 整单可满足时正常受理（不可拆零按向上取整判定）。
	setupDrug(t, e, "C", 4, false, 8)
	must(t, e.AcceptPrescription(1, RxInput{
		ID: "w2", Patient: "p", IssueTime: 1, WholeOrder: true,
		Lines: []LineInput{{DrugID: "A", Qty: 5}, {DrugID: "C", Qty: 5}},
	}))
	p := mustRx(t, e, 1, "w2")
	if p.Lines[1].Reserved != 8 || p.Lines[1].Backorder != 0 {
		t.Fatalf("整单不可拆零行应向上取整预留 8: %+v", p.Lines[1])
	}
}

// 连锁失效与再分配：A 的预留失效分给 B，B 的再失效分给 C。
func TestCascadeExpiryReallocation(t *testing.T) {
	e := newEngine(t, 5)
	setupDrug(t, e, "A", 1, true, 10)
	must(t, accept1(t, e, 0, "A1", "A", 10, false)) // 预留 10，失效时刻 6
	must(t, accept1(t, e, 1, "B1", "A", 10, false)) // 欠药 10
	must(t, accept1(t, e, 2, "C1", "A", 10, false)) // 欠药 10

	p := mustRx(t, e, 6, "B1") // t=6：A1 预留失效 → 分给 B1（新预留 6..11）
	if p.Lines[0].Reserved != 10 || p.Lines[0].Backorder != 0 {
		t.Fatalf("t=6 B1 应获得再分配: %+v", p.Lines[0])
	}
	if p = mustRx(t, e, 6, "C1"); p.Lines[0].Backorder != 10 {
		t.Fatalf("t=6 C1 应仍欠药: %+v", p.Lines[0])
	}
	p = mustRx(t, e, 12, "C1") // t=12：B1 预留失效 → 连锁分给 C1
	if p.Lines[0].Reserved != 10 || p.Lines[0].Backorder != 0 {
		t.Fatalf("t=12 C1 应获得连锁再分配: %+v", p.Lines[0])
	}
	d := mustDrug(t, e, 18, "A") // t=18：C1 预留失效，无欠药行，库存回到可用
	if d.Reserved != 0 || d.Available != 10 {
		t.Fatalf("t=18 预留应全部释放: %+v", d)
	}
}

// 欠药先到先得；同一时刻受理的按受理先后。
func TestBackorderFIFO(t *testing.T) {
	e := newEngine(t, 1000)
	setupDrug(t, e, "A", 1, true, 0)
	must(t, accept1(t, e, 0, "A1", "A", 6, false))
	must(t, accept1(t, e, 1, "B1", "A", 6, false))
	must(t, e.Inbound(2, "A", 6)) // 只够先到者
	if p := mustRx(t, e, 2, "A1"); p.Lines[0].Reserved != 6 {
		t.Fatalf("先到者应先得: %+v", p.Lines[0])
	}
	if p := mustRx(t, e, 2, "B1"); p.Lines[0].Backorder != 6 {
		t.Fatalf("后到者应继续欠药: %+v", p.Lines[0])
	}
	must(t, e.Inbound(3, "A", 6))
	if p := mustRx(t, e, 3, "B1"); p.Lines[0].Reserved != 6 || p.Lines[0].Backorder != 0 {
		t.Fatalf("到货后 B1 应获得分配: %+v", p.Lines[0])
	}
	// 同一 now 受理的两张处方，按受理先后分配。
	must(t, accept1(t, e, 4, "C1", "A", 3, false))
	must(t, accept1(t, e, 4, "D1", "A", 3, false))
	must(t, e.Inbound(5, "A", 3))
	if p := mustRx(t, e, 5, "C1"); p.Lines[0].Reserved != 3 {
		t.Fatalf("同时刻先受理者应先得: %+v", p.Lines[0])
	}
	if p := mustRx(t, e, 5, "D1"); p.Lines[0].Backorder != 3 {
		t.Fatalf("同时刻后受理者应继续欠药: %+v", p.Lines[0])
	}
}

// 取消释放：取消时刻库存回到可用量并分配给欠药行。
func TestCancelReleaseReallocation(t *testing.T) {
	e := newEngine(t, 1000)
	setupDrug(t, e, "A", 1, true, 10)
	must(t, accept1(t, e, 0, "A1", "A", 10, false))
	must(t, accept1(t, e, 1, "B1", "A", 10, false)) // 欠药 10
	must(t, e.CancelPrescription(2, "A1"))
	p := mustRx(t, e, 2, "B1")
	if p.Lines[0].Reserved != 10 || p.Lines[0].Backorder != 0 {
		t.Fatalf("取消释放后 B1 应获得分配: %+v", p.Lines[0])
	}
	if p = mustRx(t, e, 2, "A1"); p.Status != RxCancelled || p.Lines[0].Status != LineVoid {
		t.Fatalf("取消后处方与行状态应为取消/作废: %+v", p)
	}
	expectErr(t, e.CancelPrescription(3, "A1"), ErrStateMismatch) // 已取消不可再取消
}

// 过期释放：有效期结束下一分钟预留失效并分配给其他处方的欠药。
func TestExpiryReleaseReallocation(t *testing.T) {
	e := newEngine(t, 20000)
	setupDrug(t, e, "A", 1, true, 10)
	must(t, e.AcceptPrescription(0, RxInput{ID: "C1", Patient: "p", IssueTime: 0, Lines: []LineInput{{DrugID: "A", Qty: 10}}}))
	must(t, e.AcceptPrescription(1, RxInput{ID: "D1", Patient: "p", IssueTime: 1, Lines: []LineInput{{DrugID: "A", Qty: 10}}})) // 欠药 10
	p := mustRx(t, e, ValidityMinutes+1, "D1")                                                                                  // C1 于 4321 过期，释放分给 D1
	if p.Lines[0].Reserved != 10 || p.Lines[0].Backorder != 0 {
		t.Fatalf("过期释放后 D1 应获得分配: %+v", p.Lines[0])
	}
	if p = mustRx(t, e, ValidityMinutes+1, "C1"); p.Status != RxExpired || p.Lines[0].Status != LineVoid {
		t.Fatalf("过期后处方与行状态应为过期/作废: %+v", p)
	}
}

// 取药后欠药保持，直至被分配到预留并再次取药。
func TestBackorderPersistsAfterPickup(t *testing.T) {
	e := newEngine(t, 1000)
	setupDrug(t, e, "A", 1, true, 6)
	must(t, accept1(t, e, 0, "P1", "A", 10, false)) // 预留 6，欠药 4
	must(t, e.Dispense(1, "P1"))
	p := mustRx(t, e, 1, "P1")
	if p.Lines[0].Dispensed != 6 || p.Lines[0].Backorder != 4 || p.Status != RxActive {
		t.Fatalf("取药后欠药应保持: %+v", p.Lines[0])
	}
	expectErr(t, e.Dispense(2, "P1"), ErrNoValidReservation) // 无有效预留可取
	must(t, e.Inbound(3, "A", 4))                            // 到货分配给欠药行
	must(t, e.Dispense(4, "P1"))
	p = mustRx(t, e, 4, "P1")
	if p.Status != RxCompleted || p.Lines[0].Dispensed != 10 {
		t.Fatalf("再次取药后处方应完成: %+v", p)
	}
	expectErr(t, e.CancelPrescription(5, "P1"), ErrStateMismatch) // 已完成不可取消
}

// 错误优先级：参数非法 > 时钟回退 > 对象不存在 > 状态不符 > 处方过期 > 缺药 > 无有效预留。
func TestErrorPriority(t *testing.T) {
	e := newEngine(t, 100)
	setupDrug(t, e, "A", 1, true, 5)
	must(t, accept1(t, e, 10, "dup", "A", 5, false)) // 时钟推进到 10

	// 参数非法 优先于 时钟回退
	expectErr(t, e.Dispense(5, ""), ErrInvalidParam)
	// 时钟回退 优先于 对象不存在
	expectErr(t, e.Dispense(5, "nope"), ErrClockRollback)
	// 对象不存在 优先于 状态不符（受理：未知药品 + 重复处方号）
	err := e.AcceptPrescription(10, RxInput{ID: "dup", Patient: "p", IssueTime: 10, Lines: []LineInput{{DrugID: "nope", Qty: 1}}})
	expectErr(t, err, ErrNotFound)
	// 状态不符 优先于 处方过期（重复处方号 + 已过期开具时刻）
	err = e.AcceptPrescription(5000, RxInput{ID: "dup", Patient: "p", IssueTime: 0, Lines: []LineInput{{DrugID: "A", Qty: 1}}})
	expectErr(t, err, ErrStateMismatch)
	// 处方过期 优先于 缺药（整单 + 不足 + 已过期）
	err = e.AcceptPrescription(5000, RxInput{ID: "x1", Patient: "p", IssueTime: 0, WholeOrder: true, Lines: []LineInput{{DrugID: "A", Qty: 100}}})
	expectErr(t, err, ErrPrescriptionExpired)
	// 缺药（整单 + 不足）
	err = e.AcceptPrescription(5000, RxInput{ID: "x2", Patient: "p", IssueTime: 5000, WholeOrder: true, Lines: []LineInput{{DrugID: "A", Qty: 100}}})
	expectErr(t, err, ErrOutOfStock)
	// 无有效预留
	must(t, accept1(t, e, 5001, "x3", "A", 5, false))
	must(t, e.Dispense(5002, "x3"))
	expectErr(t, e.Dispense(5003, "x3"), ErrNoValidReservation)
	// 状态不符（取消已完成处方）
	expectErr(t, e.CancelPrescription(5004, "x3"), ErrStateMismatch)
}

// 被拒绝的操作不得改变任何状态与时钟（含不得泄漏事件推进效果）。
func TestRejectedOpAtomicity(t *testing.T) {
	e := newEngine(t, 45)
	setupDrug(t, e, "A", 1, true, 10)
	must(t, accept1(t, e, 0, "A1", "A", 10, false)) // 预留 10，失效时刻 46

	// t=50 的整单受理被拒（缺药）；其事件推进（t=46 失效）不得泄漏。
	err := e.AcceptPrescription(50, RxInput{ID: "B1", Patient: "p", IssueTime: 50, WholeOrder: true, Lines: []LineInput{{DrugID: "A", Qty: 20}}})
	expectErr(t, err, ErrOutOfStock)
	// 时钟未走：t=40 的操作仍被接受。
	d := mustDrug(t, e, 40, "A")
	if d.Reserved != 10 {
		t.Fatalf("被拒操作不得泄漏 t=46 的失效事件: %+v", d)
	}
	// t=46 之后失效正常体现。
	if d = mustDrug(t, e, 46, "A"); d.Reserved != 0 || d.Available != 10 {
		t.Fatalf("t=46 预留应失效并回到可用量: %+v", d)
	}
}
