package loto

import "testing"

// 标准配置：
//
//	设备 D1 依赖隔离点 P1,P2
//	设备 D2 依赖隔离点 P2,P3 （与 D1 共享 P2）
//	设备 D3 依赖隔离点 P4
func testConfig() *Config {
	return &Config{DevicePoints: map[string][]string{
		"D1": {"P1", "P2"},
		"D2": {"P2", "P3"},
		"D3": {"P4"},
	}}
}

func newTestSystem(t *testing.T) *System {
	t.Helper()
	s, err := NewSystem(testConfig())
	if err != nil {
		t.Fatalf("NewSystem: %v", err)
	}
	people := map[string][]Role{
		"alice": {RoleApplicant},
		"bob":   {RoleApprover},
		"carol": {RoleApprover},
		"dave":  {RoleWorker},
		"erin":  {RoleWorker},
		"frank": {RoleSupervisor},
		"grace": {RoleSupervisor},
		"henry": {RoleApplicant, RoleWorker}, // 一人兼多角色
	}
	for id, roles := range people {
		if err := s.AddPerson(id, roles...); err != nil {
			t.Fatalf("AddPerson %s: %v", id, err)
		}
	}
	return s
}

func kindOf(t *testing.T, err error) ErrKind {
	t.Helper()
	if err == nil {
		t.Fatalf("期望错误，实际成功")
	}
	k, ok := KindOf(err)
	if !ok {
		t.Fatalf("非系统错误: %v", err)
	}
	return k
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("期望成功，实际: %v", err)
	}
}

func mustKind(t *testing.T, err error, want ErrKind) {
	t.Helper()
	if got := kindOf(t, err); got != want {
		t.Fatalf("错误类别: got %v, want %v (err=%v)", got, want, err)
	}
}

// applyAndApprove 快捷创建并批准一张票。
func applyAndApprove(t *testing.T, s *System, at int64, devices []string, wt WorkType, start, end int64) int {
	t.Helper()
	id, err := s.Apply(at, "alice", devices, wt, start, end)
	mustOK(t, err)
	mustOK(t, s.Approve(at, "bob", id))
	return id
}

// 时段恰好首尾相接不冲突；真正相交才冲突。
func TestConflictAdjacentIntervals(t *testing.T) {
	s := newTestSystem(t)
	applyAndApprove(t, s, 10, []string{"D1"}, WorkNormal, 100, 200)

	// [200,300) 与 [100,200) 首尾相接：不冲突
	id2, err := s.Apply(11, "alice", []string{"D1"}, WorkNormal, 200, 300)
	mustOK(t, err)
	mustOK(t, s.Approve(12, "bob", id2))

	// [199,201) 与两者都相交：冲突
	id3, err := s.Apply(13, "alice", []string{"D1"}, WorkNormal, 199, 201)
	mustOK(t, err)
	mustKind(t, s.Approve(14, "bob", id3), ErrConflict)

	// [300,400) 与第一张不相交但与第二张首尾相接：不冲突
	id4, err := s.Apply(15, "alice", []string{"D1"}, WorkNormal, 300, 400)
	mustOK(t, err)
	mustOK(t, s.Approve(16, "bob", id4))

	// 设备集合不相交，即使时段相交也不冲突
	id5, err := s.Apply(17, "alice", []string{"D3"}, WorkNormal, 100, 200)
	mustOK(t, err)
	mustOK(t, s.Approve(18, "bob", id5))
}

// 只读观察票可以共存；只读与普通票仍冲突。
func TestConflictReadOnlyCoexist(t *testing.T) {
	s := newTestSystem(t)
	applyAndApprove(t, s, 10, []string{"D1"}, WorkReadOnly, 100, 200)

	// 第二张只读观察：共存
	id2, err := s.Apply(11, "alice", []string{"D1"}, WorkReadOnly, 150, 250)
	mustOK(t, err)
	mustOK(t, s.Approve(12, "bob", id2))

	// 普通票与只读票相交：冲突
	id3, err := s.Apply(13, "alice", []string{"D1"}, WorkNormal, 160, 180)
	mustOK(t, err)
	mustKind(t, s.Approve(14, "bob", id3), ErrConflict)
}

// 高风险票须两个互不相同的批准人；重复批准不去重占名额。
func TestHighRiskTwoApprovers(t *testing.T) {
	s := newTestSystem(t)
	id, err := s.Apply(10, "alice", []string{"D1"}, WorkHighRisk, 100, 200)
	mustOK(t, err)

	// 批准人不得是申请人
	mustKind(t, s.Approve(11, "alice", id), ErrPermission)

	// 第一位批准人：尚未生效
	mustOK(t, s.Approve(12, "bob", id))
	if st, _ := s.PermitState(id); st != StateApplied {
		t.Fatalf("高风险票一人批准后不应生效, got %v", st)
	}

	// 同一批准人重复批准：条件不满足，不消耗名额
	mustKind(t, s.Approve(13, "bob", id), ErrPrecondition)
	if st, _ := s.PermitState(id); st != StateApplied {
		t.Fatalf("重复批准不应生效, got %v", st)
	}

	// 无批准人角色者
	mustKind(t, s.Approve(14, "dave", id), ErrPermission)

	// 第二位不同批准人：生效
	mustOK(t, s.Approve(15, "carol", id))
	if st, _ := s.PermitState(id); st != StateEffective && st != StateLocked {
		t.Fatalf("高风险票两人批准后应生效, got %v", st)
	}
}

// 被拒绝的批准不消耗批准名额。
func TestRejectedApprovalNotCounted(t *testing.T) {
	s := newTestSystem(t)
	// 占用 D1 [100,200)
	applyAndApprove(t, s, 10, []string{"D1"}, WorkNormal, 100, 200)

	// 高风险票与之时段、设备均相交
	id, err := s.Apply(11, "alice", []string{"D1"}, WorkHighRisk, 150, 250)
	mustOK(t, err)
	// bob 的批准尚不使票生效（需两人），不触发冲突判定
	mustOK(t, s.Approve(12, "bob", id))
	// carol 的批准会使票生效，触发冲突，被拒绝
	mustKind(t, s.Approve(13, "carol", id), ErrConflict)
	// carol 的名额未被消耗：冲突消除后（第一张票完工）再次批准应生效
	// 第一张票：零作业人员 -> 批准即已上锁 -> 验证 -> 开工 -> 完工
	mustOK(t, s.Verify(14, 1, "carol"))
	mustOK(t, s.Start(150, 1, "alice"))
	mustOK(t, s.Complete(160, 1, "alice"))
	mustOK(t, s.Approve(170, "carol", id))
	if st, _ := s.PermitState(id); st != StateEffective && st != StateLocked {
		t.Fatalf("冲突消除后批准应生效, got %v", st)
	}
}

// lockPermit 登记两名作业人员并完成全部上锁、验证、开工。
func lockVerifyStart(t *testing.T, s *System, id int, startAt int64) {
	t.Helper()
	mustOK(t, s.AddWorker(startAt, "alice", id, "dave"))
	mustOK(t, s.AddWorker(startAt, "alice", id, "erin"))
	p := s.permits[id]
	for _, w := range []string{"dave", "erin"} {
		for _, pt := range p.Points() {
			mustOK(t, s.Lock(startAt, id, w, pt))
		}
	}
	mustOK(t, s.Verify(startAt, id, "bob"))
	mustOK(t, s.Start(startAt, id, "alice"))
}

// 多票共享隔离点：最后一把锁被摘除隔离才解除。
func TestSharedPointLastLock(t *testing.T) {
	s := newTestSystem(t)
	// A: D1 (P1,P2) [100,200)；B: D2 (P2,P3) [100,200)，共享 P2，设备不相交故不冲突
	a := applyAndApprove(t, s, 10, []string{"D1"}, WorkNormal, 100, 200)
	b := applyAndApprove(t, s, 11, []string{"D2"}, WorkNormal, 100, 200)

	mustOK(t, s.AddWorker(12, "alice", a, "dave"))
	mustOK(t, s.AddWorker(12, "alice", b, "erin"))
	for _, pt := range []string{"P1", "P2"} {
		mustOK(t, s.Lock(13, a, "dave", pt))
	}
	for _, pt := range []string{"P2", "P3"} {
		mustOK(t, s.Lock(14, b, "erin", pt))
	}

	// 同一人在同一隔离点上对同一张票只能有一把锁
	mustKind(t, s.Lock(15, a, "dave", "P1"), ErrPrecondition)

	// 两票均开工
	mustOK(t, s.Verify(16, a, "bob"))
	mustOK(t, s.Start(100, a, "alice"))
	mustOK(t, s.Verify(101, b, "carol"))
	mustOK(t, s.Start(102, b, "alice"))

	// A 完工并摘除全部锁：P2 上仍有 B 的锁，D2 不可送电
	mustOK(t, s.Complete(110, a, "alice"))
	mustOK(t, s.Unlock(111, a, "dave", "P1"))
	mustOK(t, s.Unlock(112, a, "dave", "P2"))
	if d, _ := s.Energizable("D2"); d.OK {
		t.Fatalf("P2 上仍有 B 的锁，D2 不应可送电: %s", d.Reason)
	}
	if d, _ := s.Energizable("D1"); d.OK {
		t.Fatalf("D1 依赖的 P2 上仍有 B 的锁，D1 不应可送电: %s", d.Reason)
	}

	// B 完工并摘除最后一把锁后，隔离解除
	mustOK(t, s.Complete(120, b, "alice"))
	mustOK(t, s.Unlock(121, b, "erin", "P3"))
	if d, _ := s.Energizable("D2"); d.OK {
		t.Fatalf("P2 上仍有最后一把锁，D2 不应可送电: %s", d.Reason)
	}
	mustOK(t, s.Unlock(122, b, "erin", "P2"))
	if d, _ := s.Energizable("D2"); !d.OK {
		t.Fatalf("最后一把锁已摘除，D2 应可送电: %s", d.Reason)
	}
	if d, _ := s.Energizable("D1"); !d.OK {
		t.Fatalf("全部锁已摘除，D1 应可送电: %s", d.Reason)
	}
}

// 试运行：暂时解除本票锁，他票的锁仍保持隔离；恢复后须重新验证。
func TestTrialRun(t *testing.T) {
	s := newTestSystem(t)
	a := applyAndApprove(t, s, 10, []string{"D1"}, WorkNormal, 100, 300)
	b := applyAndApprove(t, s, 11, []string{"D2"}, WorkNormal, 100, 300)

	mustOK(t, s.AddWorker(12, "alice", a, "dave"))
	mustOK(t, s.AddWorker(12, "alice", b, "erin"))
	for _, pt := range []string{"P1", "P2"} {
		mustOK(t, s.Lock(13, a, "dave", pt))
	}
	for _, pt := range []string{"P2", "P3"} {
		mustOK(t, s.Lock(14, b, "erin", pt))
	}
	mustOK(t, s.Verify(15, a, "bob"))
	mustOK(t, s.Start(100, a, "alice"))

	// 有人在场时不可试运行
	mustOK(t, s.Enter(101, a, "dave"))
	mustKind(t, s.TrialBegin(102, a, "alice"), ErrPrecondition)
	mustOK(t, s.Leave(103, a, "dave"))

	// 非持票人不可申请试运行
	mustKind(t, s.TrialBegin(104, a, "frank"), ErrPermission)

	// 试运行开始：A 的锁暂时解除，但 B 在 P2 上的锁仍保持隔离
	mustOK(t, s.TrialBegin(105, a, "alice"))
	if d, _ := s.Energizable("D1"); d.OK {
		t.Fatalf("P2 上仍有 B 的锁，试运行期间 D1 不应可送电: %s", d.Reason)
	}

	// B 完工解锁后，A 处于试运行（不阻止送电），D1 可送电
	mustOK(t, s.Verify(106, b, "carol"))
	mustOK(t, s.Start(107, b, "alice"))
	mustOK(t, s.Complete(108, b, "alice"))
	mustOK(t, s.Unlock(109, b, "erin", "P2"))
	mustOK(t, s.Unlock(110, b, "erin", "P3"))
	if d, _ := s.Energizable("D1"); !d.OK {
		t.Fatalf("试运行期间本票锁已解除且他票锁已清，D1 应可送电: %s", d.Reason)
	}

	// 试运行结束，原持锁人重新上锁；他人不可代上
	mustOK(t, s.TrialEnd(111, a, "alice"))
	mustKind(t, s.AddWorker(112, "alice", a, "erin"), ErrState) // 恢复阶段不可登记新人
	mustKind(t, s.Lock(113, a, "erin", "P1"), ErrPermission)    // erin 不是该票作业人员
	mustOK(t, s.Lock(114, a, "dave", "P1"))                     // 原持锁人逐个恢复
	if st, _ := s.PermitState(a); st != StateTrialRestore {
		t.Fatalf("恢复未完成，状态应为试运行恢复中, got %v", st)
	}
	mustOK(t, s.Lock(115, a, "dave", "P2")) // 全部恢复 -> 已上锁
	if st, _ := s.PermitState(a); st != StateLocked {
		t.Fatalf("恢复完成应回到已上锁, got %v", st)
	}
	// 未重新验证不可开工
	mustKind(t, s.Start(116, a, "alice"), ErrState)
	mustOK(t, s.Verify(117, a, "bob"))
	mustOK(t, s.Start(118, a, "alice"))
}

// 逾期：到达终点未完成转逾期；逾期票的锁只能由两名主管强制摘除并记审计。
func TestOverdueForceUnlock(t *testing.T) {
	s := newTestSystem(t)
	a := applyAndApprove(t, s, 10, []string{"D1"}, WorkNormal, 100, 200)
	mustOK(t, s.AddWorker(11, "alice", a, "dave"))
	for _, pt := range []string{"P1", "P2"} {
		mustOK(t, s.Lock(12, a, "dave", pt))
	}
	mustOK(t, s.Verify(13, a, "bob"))
	mustOK(t, s.Start(100, a, "alice"))

	// 到达终点仍未完成：下一个被接受操作触发逾期
	mustOK(t, s.Enter(150, a, "dave"))
	mustOK(t, s.Leave(250, a, "dave")) // t=250 >= 终点 200，接受后票转逾期
	if st, _ := s.PermitState(a); st != StateOverdue {
		t.Fatalf("超过终点未完成应转逾期, got %v", st)
	}

	// 逾期票：正常摘锁不被允许（状态不允许）
	mustKind(t, s.Unlock(251, a, "dave", "P1"), ErrState)
	// 逾期票不可再进入现场
	mustKind(t, s.Enter(252, a, "dave"), ErrState)

	// 强制摘除的参数与权限校验
	mustKind(t, s.ForceUnlock(253, a, "frank", "grace", "dave", "P1", ""), ErrInvalidParam)  // 空理由
	mustKind(t, s.ForceUnlock(253, a, "bob", "grace", "dave", "P1", "x"), ErrPermission)     // 非主管
	mustKind(t, s.ForceUnlock(253, a, "frank", "bob", "dave", "P1", "x"), ErrPermission)     // 确认人非主管
	mustKind(t, s.ForceUnlock(253, a, "frank", "frank", "dave", "P1", "x"), ErrPermission)   // 同一主管
	mustKind(t, s.ForceUnlock(253, a, "frank", "grace", "erin", "P1", "x"), ErrPrecondition) // 该人无锁
	mustKind(t, s.ForceUnlock(253, a, "frank", "grace", "dave", "P9", "x"), ErrNotFound)     // 隔离点不存在

	// 合法强制摘除：记入审计
	mustOK(t, s.ForceUnlock(254, a, "frank", "grace", "dave", "P1", "人员离场未归"))
	if !s.permits[a].mustRelock["dave"] {
		t.Fatalf("被强制摘除锁的人应被标记须重新上锁")
	}
	// 被强制摘除锁的人在逾期票上重新上锁后，标记清除
	mustOK(t, s.Lock(254, a, "dave", "P1"))
	if s.permits[a].mustRelock["dave"] {
		t.Fatalf("重新完成全部上锁后应清除重新上锁标记")
	}
	// 再次强制摘除该锁，继续后续流程
	mustOK(t, s.ForceUnlock(254, a, "frank", "grace", "dave", "P1", "人员离场未归"))
	audit := s.Audit()
	last := audit[len(audit)-1]
	if last.Op != "ForceUnlock" || last.Actor != "frank" {
		t.Fatalf("强制摘除未记入审计: %+v", last)
	}
	found := false
	for _, e := range audit {
		if e.Op == "ForceUnlock" && e.Detail != "" {
			found = true
		}
	}
	if !found {
		t.Fatalf("审计中缺少强制摘除记录")
	}

	// 被强制摘除锁的人须重新上锁后才可再进入（当前票已逾期，此处验证标记语义）
	// 剩余一把锁继续强制摘除后，隔离解除
	mustOK(t, s.ForceUnlock(255, a, "grace", "frank", "dave", "P2", "继续清理"))
	if d, _ := s.Energizable("D1"); d.OK {
		t.Fatalf("逾期票仍占用，D1 不应可送电: %s", d.Reason)
	}
	// 逾期票全员离场后可完工收尾
	mustOK(t, s.Complete(256, a, "frank"))
	if d, _ := s.Energizable("D1"); !d.OK {
		t.Fatalf("锁已清空且票已完成，D1 应可送电: %s", d.Reason)
	}
}

// 送电判定的全部条件。
func TestEnergizeDecision(t *testing.T) {
	s := newTestSystem(t)
	// 初始：无锁无票，可送电
	if d, _ := s.Energizable("D3"); !d.OK {
		t.Fatalf("初始应可送电: %s", d.Reason)
	}
	// 不存在的设备
	_, err := s.Energizable("D9")
	mustKind(t, err, ErrNotFound)

	// 已生效（尚未上锁）即阻止送电
	a := applyAndApprove(t, s, 10, []string{"D3"}, WorkNormal, 100, 200)
	if d, _ := s.Energizable("D3"); d.OK {
		t.Fatalf("已生效票应阻止送电")
	}
	// 上锁后仍阻止（锁与票双重条件）
	mustOK(t, s.AddWorker(11, "alice", a, "dave"))
	mustOK(t, s.Lock(12, a, "dave", "P4"))
	if d, _ := s.Energizable("D3"); d.OK {
		t.Fatalf("已上锁应阻止送电")
	}
	// 已验证仍阻止
	mustOK(t, s.Verify(13, a, "bob"))
	if d, _ := s.Energizable("D3"); d.OK {
		t.Fatalf("已验证应阻止送电")
	}
	// 已开工：票本身不阻止，但锁仍在 -> 不可送电
	mustOK(t, s.Start(100, a, "alice"))
	if d, _ := s.Energizable("D3"); d.OK {
		t.Fatalf("开工期间锁仍在，不应可送电")
	}
	// 试运行：锁暂时解除且试运行票不阻止 -> 可送电
	mustOK(t, s.TrialBegin(101, a, "alice"))
	if d, _ := s.Energizable("D3"); !d.OK {
		t.Fatalf("试运行期间锁已解除，应可送电: %s", d.Reason)
	}
	// 恢复上锁并重新验证、开工、完工
	mustOK(t, s.TrialEnd(102, a, "alice"))
	mustOK(t, s.Lock(103, a, "dave", "P4"))
	mustOK(t, s.Verify(104, a, "bob"))
	mustOK(t, s.Start(105, a, "alice"))
	mustOK(t, s.Complete(106, a, "alice"))
	// 完工后锁未摘：仍不可送电
	if d, _ := s.Energizable("D3"); d.OK {
		t.Fatalf("完工后锁未摘，不应可送电")
	}
	mustOK(t, s.Unlock(107, a, "dave", "P4"))
	if d, _ := s.Energizable("D3"); !d.OK {
		t.Fatalf("锁已摘且票已完成，应可送电: %s", d.Reason)
	}

	// 逾期票阻止送电
	b := applyAndApprove(t, s, 108, []string{"D3"}, WorkNormal, 200, 300)
	mustOK(t, s.AddWorker(109, "alice", b, "dave"))
	mustOK(t, s.Lock(110, b, "dave", "P4"))
	mustOK(t, s.Verify(111, b, "bob"))
	mustOK(t, s.Start(200, b, "alice"))
	mustOK(t, s.Enter(201, b, "dave"))
	mustOK(t, s.Leave(250, b, "dave"))
	mustOK(t, s.ForceUnlock(301, b, "frank", "grace", "dave", "P4", "逾期清理"))
	if st, _ := s.PermitState(b); st != StateOverdue {
		t.Fatalf("应已逾期, got %v", st)
	}
	if d, _ := s.Energizable("D3"); d.OK {
		t.Fatalf("逾期票（无锁）仍应阻止送电")
	}
}

// 错误类别严格按优先级判定：参数非法 > 时刻回退 > 对象不存在 > 无权限 > 状态不允许 > 冲突 > 条件不满足。
func TestErrorPrecedence(t *testing.T) {
	s := newTestSystem(t)
	a := applyAndApprove(t, s, 100, []string{"D1"}, WorkNormal, 100, 200) // 时钟=100

	// 参数非法 优先于 时刻回退
	mustKind(t, s.Lock(50, a, "", "P1"), ErrInvalidParam)
	// 时刻回退 优先于 对象不存在
	mustKind(t, s.Lock(50, 999, "nobody", "PX"), ErrTimeRegression)
	// 对象不存在 优先于 无权限（permit 不存在）
	mustKind(t, s.Lock(100, 999, "dave", "P1"), ErrNotFound)
	// 对象不存在 优先于 无权限（person 不存在）
	mustKind(t, s.Lock(100, a, "nobody", "P1"), ErrNotFound)
	// 对象不存在 优先于 条件不满足（point 不存在）
	mustKind(t, s.Lock(100, a, "dave", "PX"), ErrNotFound)
	// 无权限 优先于 状态不允许（dave 未登记在票上，且票状态也不允许上锁）
	mustKind(t, s.Lock(100, a, "dave", "P1"), ErrPermission)
	// 状态不允许 优先于 条件不满足（票在已生效态但 henry 非作业人员 -> 权限；
	// 登记 dave 后票已生效，验证前对非隔离点集合内的点上锁 -> 条件不满足；
	// 而在已申请态的票上上锁 -> 状态不允许）
	b, err := s.Apply(101, "alice", []string{"D2"}, WorkNormal, 300, 400)
	mustOK(t, err)
	mustOK(t, s.AddWorker(102, "alice", b, "dave"))
	mustKind(t, s.Lock(103, b, "dave", "P2"), ErrState) // 已申请态不可上锁
	mustOK(t, s.Approve(104, "bob", b))
	mustKind(t, s.Lock(105, b, "dave", "P4"), ErrPrecondition) // P4 不属于该票隔离点集
	// 冲突 优先于 条件不满足：carol 重复批准前先撞冲突判定？
	// 构造：c 与 a 冲突，carol 的首次批准即生效 -> 冲突
	c, err := s.Apply(106, "alice", []string{"D1"}, WorkNormal, 150, 250)
	mustOK(t, err)
	mustKind(t, s.Approve(107, "carol", c), ErrConflict)
	// 验证人身份属条件不满足：票未上锁完成时验证 -> 状态不允许 优先
	mustKind(t, s.Verify(108, b, "bob"), ErrState)
}

// 被拒绝的操作不改变任何状态与时钟。
func TestRejectedOpNoSideEffect(t *testing.T) {
	s := newTestSystem(t)
	a := applyAndApprove(t, s, 100, []string{"D1"}, WorkHighRisk, 100, 200)
	// bob 的首次批准已在 applyAndApprove 中完成，时钟=100

	// 时刻回退的被拒绝操作不推进时钟
	mustKind(t, s.Approve(50, "carol", a), ErrTimeRegression)
	if s.Now() != 100 {
		t.Fatalf("被拒绝操作不应推进时钟, now=%d", s.Now())
	}
	// 其他类别被拒绝操作同样不推进时钟
	mustKind(t, s.Approve(150, "bob", a), ErrPrecondition) // 重复批准
	if s.Now() != 100 {
		t.Fatalf("被拒绝操作不应推进时钟, now=%d", s.Now())
	}
	// 时钟仍允许等于 100 的操作
	mustOK(t, s.Approve(100, "carol", a))
	if st, _ := s.PermitState(a); st != StateEffective && st != StateLocked {
		t.Fatalf("两位批准人后应生效, got %v", st)
	}
}
