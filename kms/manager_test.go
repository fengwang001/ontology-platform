package kms

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func mustErr(t *testing.T, err error, kind ErrKind) *Error {
	t.Helper()
	if err == nil {
		t.Fatalf("期望错误 %s，实际成功", kind)
	}
	ke, ok := err.(*Error)
	if !ok {
		t.Fatalf("错误类型不是 *Error：%v", err)
	}
	if ke.Kind != kind {
		t.Fatalf("期望错误类别 %s，实际 %s（%v）", kind, ke.Kind, ke)
	}
	t.Logf("拒绝符合预期：%v", ke)
	return ke
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("期望成功，实际错误：%v", err)
	}
}

func wantView(t *testing.T, m *Manager, id string, now int64, want KeyView) {
	t.Helper()
	got, err := m.Describe(id, now)
	mustOK(t, err)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Describe(%q,%d) 不一致：\n得到 %+v\n期望 %+v", id, now, got, want)
	}
}

func versAt(pairs ...int64) []VersionInfo {
	out := make([]VersionInfo, 0, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		out = append(out, VersionInfo{Number: pairs[i], CreatedAt: pairs[i+1]})
	}
	return out
}

func TestNewManagerConfig(t *testing.T) {
	valid := [][4]int64{{1, 1, 1, 1}, {64, 1, 1_000_000_000, 1_000_000}, {3, 100, 1000, 2}}
	for _, c := range valid {
		if _, err := NewManager(c[0], c[1], c[2], c[3]); err != nil {
			t.Fatalf("合法配置 %v 被拒绝：%v", c, err)
		}
	}
	invalid := [][4]int64{
		{0, 1, 1, 1}, {65, 1, 1, 1}, {-1, 1, 1, 1},
		{1, 0, 1, 1}, {1, 2, 1, 1}, {1, 1, 1_000_000_001, 1},
		{1, 1, 1, 0}, {1, 1, 1, 1_000_001},
	}
	for _, c := range invalid {
		_, err := NewManager(c[0], c[1], c[2], c[3])
		mustErr(t, err, ErrInvalidConfig)
	}
}

// 规格主示例：V=3、Wmin=100、Wmax=1000、Kmax=2。
func TestSpecExampleMain(t *testing.T) {
	m, err := NewManager(3, 100, 1000, 2)
	mustOK(t, err)

	mustOK(t, m.Create("k", 60, 0))
	wantView(t, m, "k", 0, KeyView{State: Enabled, Generation: 1,
		Versions: versAt(1, 0), NextRotation: 60})
	mustOK(t, m.Create("j", 0, 0))

	// Encrypt(k,215)：c=floor((215-60)/60)+1=3，补建 2、3、4 于 60、120、180，
	// 版本 1 被淘汰，d=240。
	cred, err := m.Encrypt("k", 215)
	mustOK(t, err)
	if cred != (Credential{ID: "k", Gen: 1, Version: 4}) {
		t.Fatalf("Encrypt 凭据错误：%+v", cred)
	}
	wantView(t, m, "k", 215, KeyView{State: Enabled, Generation: 1,
		Versions: versAt(2, 60, 3, 120, 4, 180), NextRotation: 240})

	// Disable(k,250)：先补建版本 5 于 240，保留 3、4、5，d=300，再置 Disabled。
	mustOK(t, m.Disable("k", 250))
	wantView(t, m, "k", 250, KeyView{State: Disabled, Generation: 1,
		Versions: versAt(3, 120, 4, 180, 5, 240), NextRotation: 300})

	// Enable(k,300)：d=300 不大于 300，d 改为 360，不补建版本 6。
	mustOK(t, m.Enable("k", 300))
	wantView(t, m, "k", 300, KeyView{State: Enabled, Generation: 1,
		Versions: versAt(3, 120, 4, 180, 5, 240), NextRotation: 360})

	// Decrypt((k,1,2),350)：d=360 不到期，版本 2 小于保留的最小版本 3。
	_, err = m.Decrypt(Credential{ID: "k", Gen: 1, Version: 2}, 350)
	mustErr(t, err, ErrVersionRetired)

	// ScheduleDeletion(k,100,400)：先补建版本 6 于 360，保留 4、5、6，
	// d=420，置 Pending 且 deleteAt=500。
	mustOK(t, m.ScheduleDeletion("k", 100, 400))
	wantView(t, m, "k", 400, KeyView{State: Pending, Generation: 1,
		Versions: versAt(4, 180, 5, 240, 6, 360), NextRotation: 420, DeleteAt: 500})

	// Decrypt((k,1,6),499) 报计划删除中。
	_, err = m.Decrypt(Credential{ID: "k", Gen: 1, Version: 6}, 499)
	ke := mustErr(t, err, ErrStateRejected)
	if ke.State != Pending {
		t.Fatalf("状态拒绝应带 Pending，实际 %s", ke.State)
	}

	// Create(x,0,499)：k 与 j 仍存活，Kmax=2，报超限。
	mustErr(t, m.Create("x", 0, 499), ErrCapacityExceeded)

	// Decrypt((k,1,6),500)：deleteAt=500 不大于 500，k 已删除，报密钥已删除。
	_, err = m.Decrypt(Credential{ID: "k", Gen: 1, Version: 6}, 500)
	mustErr(t, err, ErrKeyDeleted)

	// k 的名额已让出，Create(k,60,500) 成功，世代 2，版本重新从 1 起，d=560。
	mustOK(t, m.Create("k", 60, 500))
	wantView(t, m, "k", 500, KeyView{State: Enabled, Generation: 2,
		Versions: versAt(1, 500), NextRotation: 560})

	// 旧凭据 (k,1,6) 仍报密钥已删除；从未创建过的 id 报不存在。
	_, err = m.Decrypt(Credential{ID: "k", Gen: 1, Version: 6}, 500)
	mustErr(t, err, ErrKeyDeleted)
	_, err = m.Decrypt(Credential{ID: "ghost", Gen: 1, Version: 1}, 500)
	mustErr(t, err, ErrNotFound)
}

// 走到 Pending 后 CancelDeletion(k,499) 得 Disabled，Enable(k,600) 时
// d=420 不大于 600，d 改为 660。
func TestSpecExampleCancelPath(t *testing.T) {
	m, err := NewManager(3, 100, 1000, 2)
	mustOK(t, err)
	mustOK(t, m.Create("k", 60, 0))
	mustOK(t, m.Create("j", 0, 0))
	if _, err := m.Encrypt("k", 215); err != nil {
		t.Fatal(err)
	}
	mustOK(t, m.Disable("k", 250))
	mustOK(t, m.Enable("k", 300))
	mustOK(t, m.ScheduleDeletion("k", 100, 400))

	mustOK(t, m.CancelDeletion("k", 499))
	// 回到 Disabled 而不是 Enabled，d 保持 420。
	wantView(t, m, "k", 499, KeyView{State: Disabled, Generation: 1,
		Versions: versAt(4, 180, 5, 240, 6, 360), NextRotation: 420, DeleteAt: 500})

	mustOK(t, m.Enable("k", 600))
	wantView(t, m, "k", 600, KeyView{State: Enabled, Generation: 1,
		Versions: versAt(4, 180, 5, 240, 6, 360), NextRotation: 660, DeleteAt: 500})
}

// Enable(k,299)：d=300 大于 299，d 不变；后续 301 的补建建版本 6 于 300。
func TestSpecExampleEnableBeforeD(t *testing.T) {
	m, err := NewManager(3, 100, 1000, 2)
	mustOK(t, err)
	mustOK(t, m.Create("k", 60, 0))
	mustOK(t, m.Create("j", 0, 0))
	if _, err := m.Encrypt("k", 215); err != nil {
		t.Fatal(err)
	}
	mustOK(t, m.Disable("k", 250))
	mustOK(t, m.Enable("k", 299))
	wantView(t, m, "k", 299, KeyView{State: Enabled, Generation: 1,
		Versions: versAt(3, 120, 4, 180, 5, 240), NextRotation: 300})

	cred, err := m.Encrypt("k", 301)
	mustOK(t, err)
	if cred.Version != 6 {
		t.Fatalf("Encrypt 应返回版本 6，实际 %+v", cred)
	}
	wantView(t, m, "k", 301, KeyView{State: Enabled, Generation: 1,
		Versions: versAt(4, 180, 5, 240, 6, 300), NextRotation: 360})
}

// Encrypt(k,215) 之后：Decrypt((k,1,5),245) 成功（补建出版本 5，d=300，
// 5 即当前最大版本），Decrypt((k,1,6),245) 报版本不存在。
func TestSpecExampleDecryptVersions(t *testing.T) {
	m, err := NewManager(3, 100, 1000, 2)
	mustOK(t, err)
	mustOK(t, m.Create("k", 60, 0))
	mustOK(t, m.Create("j", 0, 0))
	if _, err := m.Encrypt("k", 215); err != nil {
		t.Fatal(err)
	}
	isMax, err := m.Decrypt(Credential{ID: "k", Gen: 1, Version: 5}, 245)
	mustOK(t, err)
	if !isMax {
		t.Fatal("版本 5 应为当前最大版本")
	}
	wantView(t, m, "k", 245, KeyView{State: Enabled, Generation: 1,
		Versions: versAt(3, 120, 4, 180, 5, 240), NextRotation: 300})
	_, err = m.Decrypt(Credential{ID: "k", Gen: 1, Version: 6}, 245)
	mustErr(t, err, ErrVersionNotFound)
}

// d 恰等于 now 即到期；c 的向下取整。
func TestCatchUpDueExactAndFloor(t *testing.T) {
	m, err := NewManager(5, 1, 1000, 10)
	mustOK(t, err)
	mustOK(t, m.Create("k", 60, 0))
	// now=59 未到期。
	cred, err := m.Encrypt("k", 59)
	mustOK(t, err)
	if cred.Version != 1 {
		t.Fatalf("now=59 不应补建，凭据 %+v", cred)
	}
	// now=60 恰等于 d，到期一次：c=(60-60)/60+1=1，版本 2 于 60，d=120。
	cred, err = m.Encrypt("k", 60)
	mustOK(t, err)
	if cred.Version != 2 {
		t.Fatalf("now=60 应补建一个版本，凭据 %+v", cred)
	}
	// now=179：c=(179-120)/60+1=1（向下取整），版本 3 于 120，d=180。
	cred, err = m.Encrypt("k", 179)
	mustOK(t, err)
	if cred.Version != 3 {
		t.Fatalf("now=179 应只补建一个版本，凭据 %+v", cred)
	}
	wantView(t, m, "k", 179, KeyView{State: Enabled, Generation: 1,
		Versions: versAt(1, 0, 2, 60, 3, 120), NextRotation: 180})
	// now=180 恰等于 d，再补一个版本 4 于 180。
	cred, err = m.Encrypt("k", 180)
	mustOK(t, err)
	if cred.Version != 4 {
		t.Fatalf("now=180 应补建版本 4，凭据 %+v", cred)
	}
}

// 版本号按完整 c 推进而只保留最后 V 个；创建时刻取计划时刻。
func TestCatchUpLargeC(t *testing.T) {
	m, err := NewManager(3, 1, 1_000_000_000, 10)
	mustOK(t, err)
	mustOK(t, m.Create("k", 60, 0))
	// c=(10000-60)/60+1=166，版本号推进到 1+166=167，只保留最后 3 个，
	// 创建时刻为计划时刻 9840、9900、9960，d=60+166*60=10020。
	cred, err := m.Encrypt("k", 10000)
	mustOK(t, err)
	if cred.Version != 167 {
		t.Fatalf("版本号应按完整 c 推进到 167，凭据 %+v", cred)
	}
	wantView(t, m, "k", 10000, KeyView{State: Enabled, Generation: 1,
		Versions: versAt(165, 9840, 166, 9900, 167, 9960), NextRotation: 10020})
	if m.materialized != 3 {
		t.Fatalf("本次补建应只物化 3 个版本，实际 %d", m.materialized)
	}
	// 版本 164 已淘汰，版本 168 不存在。
	if _, err := m.Decrypt(Credential{ID: "k", Gen: 1, Version: 164}, 10000); true {
		mustErr(t, err, ErrVersionRetired)
	}
	if _, err := m.Decrypt(Credential{ID: "k", Gen: 1, Version: 168}, 10000); true {
		mustErr(t, err, ErrVersionNotFound)
	}
}

// V=1：每次补建只保留最新一个版本。
func TestRetainOne(t *testing.T) {
	m, err := NewManager(1, 1, 1_000_000_000, 10)
	mustOK(t, err)
	mustOK(t, m.Create("k", 60, 0))
	// c=(1000-60)/60+1=16，版本号推进到 17，只保留 17（创建于 960），d=1020。
	cred, err := m.Encrypt("k", 1000)
	mustOK(t, err)
	if cred.Version != 17 {
		t.Fatalf("凭据 %+v", cred)
	}
	wantView(t, m, "k", 1000, KeyView{State: Enabled, Generation: 1,
		Versions: versAt(17, 960), NextRotation: 1020})
	if _, err := m.Decrypt(Credential{ID: "k", Gen: 1, Version: 16}, 1000); true {
		mustErr(t, err, ErrVersionRetired)
	}
	if m.materialized != 1 {
		t.Fatalf("V=1 时每次补建只物化 1 个版本，实际 %d", m.materialized)
	}
}

// Disabled 期间不补建；Enable 时 d 恰等于 now 与小 1 的两种结果。
func TestDisabledNoCatchUpAndEnableBoundary(t *testing.T) {
	// d 恰等于 now：d 改为 now+P。
	m, err := NewManager(3, 1, 1000, 10)
	mustOK(t, err)
	mustOK(t, m.Create("k", 60, 0))
	mustOK(t, m.Disable("k", 30)) // d=60 未到期，不补建
	// Disabled 期间经过 1000 秒不补建。
	wantView(t, m, "k", 1000, KeyView{State: Disabled, Generation: 1,
		Versions: versAt(1, 0), NextRotation: 60})
	mustOK(t, m.Enable("k", 1000)) // d=60 <= 1000，d 改为 1060，错过的轮换不补建
	wantView(t, m, "k", 1000, KeyView{State: Enabled, Generation: 1,
		Versions: versAt(1, 0), NextRotation: 1060})

	// d 比 now 大 1：d 不变。
	m2, err := NewManager(3, 1, 1000, 10)
	mustOK(t, err)
	mustOK(t, m2.Create("k", 60, 0))
	mustOK(t, m2.Disable("k", 30))
	mustOK(t, m2.Enable("k", 59)) // d=60 > 59，不变
	wantView(t, m2, "k", 59, KeyView{State: Enabled, Generation: 1,
		Versions: versAt(1, 0), NextRotation: 60})
	// 下一步在 60 到期补建版本 2 于 60。
	cred, err := m2.Encrypt("k", 60)
	mustOK(t, err)
	if cred.Version != 2 {
		t.Fatalf("凭据 %+v", cred)
	}
}

// Pending 与 Disabled 的 Decrypt 拒绝原因不同。
func TestPendingVsDisabledRejection(t *testing.T) {
	m, err := NewManager(3, 100, 1000, 10)
	mustOK(t, err)
	mustOK(t, m.Create("a", 0, 0))
	mustOK(t, m.Create("b", 0, 0))
	mustOK(t, m.Disable("a", 0))
	mustOK(t, m.ScheduleDeletion("b", 100, 0))

	_, err = m.Decrypt(Credential{ID: "a", Gen: 1, Version: 1}, 0)
	ke := mustErr(t, err, ErrStateRejected)
	if ke.State != Disabled {
		t.Fatalf("禁用中应带 Disabled，实际 %s", ke.State)
	}
	_, err = m.Decrypt(Credential{ID: "b", Gen: 1, Version: 1}, 0)
	ke = mustErr(t, err, ErrStateRejected)
	if ke.State != Pending {
		t.Fatalf("计划删除中应带 Pending，实际 %s", ke.State)
	}
	// Encrypt/ReEncrypt 同样区分。
	_, err = m.Encrypt("a", 0)
	if ke := mustErr(t, err, ErrStateRejected); ke.State != Disabled {
		t.Fatalf("实际 %s", ke.State)
	}
	_, _, err = m.ReEncrypt(Credential{ID: "b", Gen: 1, Version: 1}, 0)
	if ke := mustErr(t, err, ErrStateRejected); ke.State != Pending {
		t.Fatalf("实际 %s", ke.State)
	}
}

// deleteAt 恰等于 now 已删除而小 1 仍在；删除后名额立即让出。
func TestDeleteAtBoundaryAndSlot(t *testing.T) {
	m, err := NewManager(3, 100, 1000, 2)
	mustOK(t, err)
	mustOK(t, m.Create("k", 0, 0))
	mustOK(t, m.Create("j", 0, 0))
	mustOK(t, m.ScheduleDeletion("k", 100, 0)) // deleteAt=100

	// now=99 仍在：Create 第三个报超限；k 的 Describe 正常。
	mustErr(t, m.Create("x", 0, 99), ErrCapacityExceeded)
	wantView(t, m, "k", 99, KeyView{State: Pending, Generation: 1,
		Versions: versAt(1, 0), NextRotation: 0, DeleteAt: 100})

	// now=100 恰等于 deleteAt：已删除，名额立即让出。
	mustOK(t, m.Create("x", 0, 100))
	// k 对所有操作视同不存在。
	_, err = m.Describe("k", 100)
	mustErr(t, err, ErrNotFound)
	mustErr(t, m.Disable("k", 100), ErrNotFound)
	mustErr(t, m.CancelDeletion("k", 100), ErrNotFound)
}

// ScheduleDeletion 的 w 恰等于上下限。
func TestScheduleDeletionWindowBounds(t *testing.T) {
	m, err := NewManager(3, 100, 1000, 10)
	mustOK(t, err)
	mustOK(t, m.Create("a", 0, 0))
	mustOK(t, m.Create("b", 0, 0))
	mustOK(t, m.Create("c", 0, 0))
	mustErr(t, m.ScheduleDeletion("a", 99, 0), ErrInvalidParam)
	mustErr(t, m.ScheduleDeletion("a", 1001, 0), ErrInvalidParam)
	mustOK(t, m.ScheduleDeletion("a", 100, 0))  // 恰等于下限
	mustOK(t, m.ScheduleDeletion("b", 1000, 0)) // 恰等于上限
	// Disabled 密钥也可计划删除。
	mustOK(t, m.Disable("c", 0))
	mustOK(t, m.ScheduleDeletion("c", 500, 0))
	// Pending 不可重复计划删除，报状态冲突并带出当前状态。
	err = m.ScheduleDeletion("a", 200, 0)
	if ke := mustErr(t, err, ErrStateConflict); ke.State != Pending {
		t.Fatalf("应带出 Pending，实际 %s", ke.State)
	}
}

// ReEncrypt：版本落后则换新凭据并标记变化；已是当前版本则原样返回未变化。
func TestReEncrypt(t *testing.T) {
	m, err := NewManager(3, 1, 1000, 10)
	mustOK(t, err)
	mustOK(t, m.Create("k", 60, 0))
	cred, err := m.Encrypt("k", 0)
	mustOK(t, err) // (k,1,1)

	// 推进到 150：补建版本 2、3 于 60、120，保留 1、2、3，d=180。
	nc, changed, err := m.ReEncrypt(cred, 150)
	mustOK(t, err)
	if !changed || nc != (Credential{ID: "k", Gen: 1, Version: 3}) {
		t.Fatalf("ReEncrypt 应换新凭据 (k,1,3) 并标记变化，实际 %+v changed=%v", nc, changed)
	}
	// 旧凭据仍有效但不是最大版本。
	isMax, err := m.Decrypt(cred, 150)
	mustOK(t, err)
	if isMax {
		t.Fatal("旧凭据不应是最大版本")
	}
	// 本已是最大版本：原样返回，未变化。
	nc2, changed, err := m.ReEncrypt(nc, 150)
	mustOK(t, err)
	if changed || nc2 != nc {
		t.Fatalf("应原样返回并标记未变化，实际 %+v changed=%v", nc2, changed)
	}
}

// Describe 虚拟补建不改状态与时钟。
func TestDescribeNoMutation(t *testing.T) {
	m, err := NewManager(3, 1, 1_000_000_000, 10)
	mustOK(t, err)
	mustOK(t, m.Create("k", 60, 0))

	view, err := m.Describe("k", 1_000_000_000_000_000)
	mustOK(t, err)
	if len(view.Versions) != 3 || view.Versions[2].Number != 16666666666667 {
		t.Fatalf("虚拟补建视图错误：%+v", view.Versions)
	}
	if m.materialized != 0 {
		t.Fatalf("Describe 不应物化版本，实际 %d", m.materialized)
	}
	if m.maxNow != 0 {
		t.Fatalf("Describe 不应推进时钟，实际 %d", m.maxNow)
	}
	// 时钟未推进：now=500 的操作仍被接受。
	mustOK(t, m.Create("j", 0, 500))
	// 状态未变：真实补建前版本表仍只有版本 1。
	cred, err := m.Encrypt("k", 600)
	mustOK(t, err)
	if cred.Version != 11 { // c=(600-60)/60+1=10，最大版本号 1+10=11
		t.Fatalf("Describe 不应改变补建结果，凭据 %+v", cred)
	}
}

// 被拒绝的操作不改状态、不推进时钟、不触发补建与回收。
func TestRejectedOpNoSideEffects(t *testing.T) {
	m, err := NewManager(3, 100, 1000, 2)
	mustOK(t, err)
	mustOK(t, m.Create("k", 60, 0))
	mustOK(t, m.Create("j", 0, 0))
	mustOK(t, m.ScheduleDeletion("j", 100, 0)) // deleteAt=100

	// 状态冲突的拒绝（Enable 要求 Disabled）：now=1000 不生效。
	err = m.Enable("k", 1000)
	if ke := mustErr(t, err, ErrStateConflict); ke.State != Enabled {
		t.Fatalf("应带出 Enabled，实际 %s", ke.State)
	}
	// 时钟未推进：now=500 仍被接受。
	mustOK(t, m.Disable("k", 500))
	// k 未被补建：500 时补建从 d=60 起算。
	wantView(t, m, "k", 500, KeyView{State: Disabled, Generation: 1,
		Versions: versAt(7, 360, 8, 420, 9, 480), NextRotation: 540})
	// 版本拒绝不触发补建：Enabled 的 k 重新来。
	m2, err := NewManager(3, 100, 1000, 2)
	mustOK(t, err)
	mustOK(t, m2.Create("k", 60, 0))
	_, err = m2.Decrypt(Credential{ID: "k", Gen: 1, Version: 999}, 10000)
	mustErr(t, err, ErrVersionNotFound)
	if m2.materialized != 0 {
		t.Fatalf("版本拒绝不应物化版本，实际 %d", m2.materialized)
	}
	wantView(t, m2, "k", 10000, KeyView{State: Enabled, Generation: 1,
		Versions: versAt(165, 9840, 166, 9900, 167, 9960), NextRotation: 10020})
	if m2.maxNow != 0 {
		t.Fatalf("被拒绝的操作不应推进时钟，实际 %d", m2.maxNow)
	}
	// 参数非法的拒绝同样不推进时钟。
	mustErr(t, m2.Create("", 0, 99999), ErrInvalidParam)
	mustOK(t, m2.Create("j", 0, 100))
}

// 时钟回退：所有带 now 的操作共用一个全局时钟。
func TestClockRollback(t *testing.T) {
	m, err := NewManager(3, 100, 1000, 10)
	mustOK(t, err)
	mustOK(t, m.Create("k", 60, 100))
	mustErr(t, m.Create("j", 0, 99), ErrClockRollback)
	_, err = m.Encrypt("k", 99)
	mustErr(t, err, ErrClockRollback)
	_, err = m.Decrypt(Credential{ID: "k", Gen: 1, Version: 1}, 99)
	mustErr(t, err, ErrClockRollback)
	_, _, err = m.ReEncrypt(Credential{ID: "k", Gen: 1, Version: 1}, 99)
	mustErr(t, err, ErrClockRollback)
	mustErr(t, m.Disable("k", 99), ErrClockRollback)
	mustErr(t, m.Enable("k", 99), ErrClockRollback)
	mustErr(t, m.ScheduleDeletion("k", 100, 99), ErrClockRollback)
	mustErr(t, m.CancelDeletion("k", 99), ErrClockRollback)
	_, err = m.Describe("k", 99)
	mustErr(t, err, ErrClockRollback)
	// now 等于已接受的最大 now 不算回退。
	_, err = m.Encrypt("k", 100)
	mustOK(t, err)
}

// 参数非法校验。
func TestParamValidation(t *testing.T) {
	m, err := NewManager(3, 100, 1000, 10)
	mustOK(t, err)
	mustErr(t, m.Create("", 0, 0), ErrInvalidParam)
	mustErr(t, m.Create("k", 59, 0), ErrInvalidParam)
	mustErr(t, m.Create("k", 1_000_000_001, 0), ErrInvalidParam)
	mustErr(t, m.Create("k", 0, -1), ErrInvalidParam)
	mustErr(t, m.Create("k", 0, 1_000_000_000_000_001), ErrInvalidParam)
	mustOK(t, m.Create("k", 60, 0))
	mustOK(t, m.Create("j", 1_000_000_000, 0)) // P 上界合法

	_, err = m.Encrypt("", 0)
	mustErr(t, err, ErrInvalidParam)
	_, err = m.Decrypt(Credential{ID: "", Gen: 1, Version: 1}, 0)
	mustErr(t, err, ErrInvalidParam)
	_, err = m.Decrypt(Credential{ID: "k", Gen: 0, Version: 1}, 0)
	mustErr(t, err, ErrInvalidParam)
	_, err = m.Decrypt(Credential{ID: "k", Gen: 1, Version: 0}, 0)
	mustErr(t, err, ErrInvalidParam)
	// 参数非法先于时钟回退与其他类别。
	mustOK(t, m.Create("z", 0, 100))
	_, err = m.Decrypt(Credential{ID: "k", Gen: 0, Version: 1}, 50)
	mustErr(t, err, ErrInvalidParam)
}

// 不存在与密钥已删除的区分：世代超过已创建最大世代报不存在。
func TestNotFoundVsDeleted(t *testing.T) {
	m, err := NewManager(3, 100, 1000, 10)
	mustOK(t, err)
	mustOK(t, m.Create("k", 0, 0))
	// 世代 2 从未创建：不存在。
	_, err = m.Decrypt(Credential{ID: "k", Gen: 2, Version: 1}, 0)
	mustErr(t, err, ErrNotFound)
	// 删除并重建后，旧世代报密钥已删除。
	mustOK(t, m.ScheduleDeletion("k", 100, 0))
	mustOK(t, m.Create("k", 0, 100))
	_, err = m.Decrypt(Credential{ID: "k", Gen: 1, Version: 1}, 100)
	mustErr(t, err, ErrKeyDeleted)
	// 世代 3 仍未创建：不存在。
	_, err = m.Decrypt(Credential{ID: "k", Gen: 3, Version: 1}, 100)
	mustErr(t, err, ErrNotFound)
}

// 物化计数器：P=60、V=3，一次空闲 10^15 秒与空闲 240 秒（c>=V）物化数相同。
func TestMaterializedCounterBound(t *testing.T) {
	m1, _ := NewManager(3, 1, 1_000_000_000, 10)
	mustOK(t, m1.Create("k", 60, 0))
	if _, err := m1.Encrypt("k", 1_000_000_000_000_000); true {
		mustOK(t, err)
	}
	m2, _ := NewManager(3, 1, 1_000_000_000, 10)
	mustOK(t, m2.Create("k", 60, 0))
	if _, err := m2.Encrypt("k", 240); true { // c=(240-60)/60+1=4 >= V=3
		mustOK(t, err)
	}
	if m1.materialized != 3 || m2.materialized != 3 {
		t.Fatalf("每次补建物化数不应超过 V=3：m1=%d m2=%d", m1.materialized, m2.materialized)
	}
	// c<V 时物化 c 个。
	m3, _ := NewManager(3, 1, 1_000_000_000, 10)
	mustOK(t, m3.Create("k", 60, 0))
	if _, err := m3.Encrypt("k", 100); true { // c=1
		mustOK(t, err)
	}
	if m3.materialized != 1 {
		t.Fatalf("c=1 时应物化 1 个，实际 %d", m3.materialized)
	}
}

// 堆弹出计数器：每次回收弹出次数不超过到期删除数加作废条目数再加一。
func TestHeapPopsBound(t *testing.T) {
	m, err := NewManager(3, 100, 1000, 10)
	mustOK(t, err)
	for _, id := range []string{"a", "b", "c"} {
		mustOK(t, m.Create(id, 0, 0))
	}
	mustOK(t, m.ScheduleDeletion("a", 100, 0))
	mustOK(t, m.ScheduleDeletion("b", 100, 0))
	mustOK(t, m.ScheduleDeletion("c", 200, 0))
	mustOK(t, m.CancelDeletion("c", 50)) // c 的堆条目作废

	// now=150 的回收：弹出 a、b 两个到期条目，c 的作废条目 deleteAt=200 未到期。
	popsBefore := m.heapPops
	mustOK(t, m.Create("x", 0, 150))
	pops := m.heapPops - popsBefore
	if pops > 2 { // 到期 2 + 作废 0（+1 余量）
		t.Fatalf("本次回收弹出 %d 次，超过到期删除数 2", pops)
	}
	// now=250 的回收：弹出 c 的作废条目。
	popsBefore = m.heapPops
	mustOK(t, m.Create("y", 0, 250))
	pops = m.heapPops - popsBefore
	if pops > 1 { // 到期 0 + 作废 1
		t.Fatalf("本次回收弹出 %d 次，超过作废条目数 1", pops)
	}
}

// 并发调用：N 个 goroutine 同时 Create 同一 id，恰好一个成功；
// 并发混合操作不 panic、结果可串行化（配合 -race 运行）。
func TestConcurrency(t *testing.T) {
	m, err := NewManager(3, 1, 1000, 100)
	mustOK(t, err)
	const n = 32
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = m.Create("k", 60, 0)
		}(i)
	}
	wg.Wait()
	ok := 0
	for _, e := range errs {
		if e == nil {
			ok++
		} else if ke := e.(*Error); ke.Kind != ErrStateConflict {
			t.Fatalf("Create 冲突应报状态冲突，实际 %v", e)
		}
	}
	if ok != 1 {
		t.Fatalf("并发 Create 同一 id 应恰好一个成功，实际 %d", ok)
	}

	// 并发混合操作：每个 goroutine 独立 id 段，避免逻辑冲突干扰。
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			id := fmt.Sprintf("key-%d", g)
			for i := 0; i < 200; i++ {
				now := int64(i)
				if err := m.Create(id, 60, now); err != nil {
					if _, err := m.Encrypt(id, now); err == nil {
						continue
					}
				}
				cred, err := m.Encrypt(id, now)
				if err != nil {
					continue
				}
				_, _ = m.Decrypt(cred, now)
				_, _, _ = m.ReEncrypt(cred, now)
				_, _ = m.Describe(id, now)
			}
		}(g)
	}
	wg.Wait()
}

// 相同操作序列重放得到完全相同的结果。
func TestDeterminism(t *testing.T) {
	run := func() []interface{} {
		m, _ := NewManager(3, 100, 1000, 2)
		var out []interface{}
		out = append(out, m.Create("k", 60, 0))
		out = append(out, m.Create("j", 0, 0))
		cred, err := m.Encrypt("k", 215)
		out = append(out, cred, err)
		out = append(out, m.Disable("k", 250))
		out = append(out, m.Enable("k", 300))
		isMax, err := m.Decrypt(Credential{ID: "k", Gen: 1, Version: 2}, 350)
		out = append(out, isMax, err)
		out = append(out, m.ScheduleDeletion("k", 100, 400))
		_, err = m.Decrypt(Credential{ID: "k", Gen: 1, Version: 6}, 499)
		out = append(out, err)
		out = append(out, m.Create("x", 0, 499))
		_, err = m.Decrypt(Credential{ID: "k", Gen: 1, Version: 6}, 500)
		out = append(out, err)
		out = append(out, m.Create("k", 60, 500))
		view, err := m.Describe("k", 10000)
		out = append(out, view, err)
		return out
	}
	a, b := run(), run()
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("重放结果不一致：\n%v\n%v", a, b)
	}
}
