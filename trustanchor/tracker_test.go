package trustanchor

import (
	"fmt"
	"testing"
)

func it(id string) KeyItem { return KeyItem{ID: []byte(id)} }

func itr(id string) KeyItem { return KeyItem{ID: []byte(id), Revoked: true} }

func sig(ids ...string) [][]byte {
	out := make([][]byte, 0, len(ids))
	for _, id := range ids {
		out = append(out, []byte(id))
	}
	return out
}

func mustNew(t *testing.T, cfg Config) *Tracker {
	t.Helper()
	tr, err := NewTracker(cfg)
	if err != nil {
		t.Fatalf("NewTracker: %v", err)
	}
	return tr
}

func wantErr(t *testing.T, err error, kind ErrorKind) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望错误 %v，实际为 nil", kind)
	}
	k, ok := KindOf(err)
	if !ok {
		t.Fatalf("错误类型不是 *Error: %v", err)
	}
	if k != kind {
		t.Fatalf("期望错误类别 %v，实际 %v (%v)", kind, k, err)
	}
}

func wantOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("期望接受，实际拒绝: %v", err)
	}
}

func wantState(t *testing.T, tr *Tracker, id string, st State, since, at int64, cnt int) {
	t.Helper()
	got := tr.State([]byte(id))
	want := KeyStatus{State: st, Since: since, At: at, Cnt: cnt}
	if got != want {
		t.Fatalf("State(%s) = %+v，期望 %+v", id, got, want)
	}
}

// 题目主示例：H=30、R=100、M=1、Q=1，初始锚 {k1, k2}。
func TestSpecExampleWalkthrough(t *testing.T) {
	cfg := Config{AddHold: 30, Retain: 100, MaxTracked: 10, MinAppear: 1, Quorum: 1, Anchors: sig("k1", "k2")}
	tr := mustNew(t, cfg)

	wantOK(t, tr.Observe([]KeyItem{it("k1"), it("k2"), it("k3")}, sig("k1"), 10))
	wantState(t, tr, "k3", StateAddPend, 10, 0, 1)

	wantOK(t, tr.Observe([]KeyItem{it("k1"), it("k2"), it("k3")}, sig("k2"), 39))
	wantState(t, tr, "k3", StateAddPend, 10, 0, 2) // since 不刷新，39 < 10+30 不晋升

	wantOK(t, tr.Observe([]KeyItem{it("k1"), it("k2"), it("k3")}, sig("k1"), 40))
	wantState(t, tr, "k3", StateValid, 0, 0, 0) // 40 >= 10+30 晋升
}

// ADDPEND 缺席一次计时丢失并重新开始。
func TestSpecExampleAbsenceRestart(t *testing.T) {
	cfg := Config{AddHold: 30, Retain: 100, MaxTracked: 10, MinAppear: 1, Quorum: 1, Anchors: sig("k1", "k2")}
	tr := mustNew(t, cfg)

	wantOK(t, tr.Observe([]KeyItem{it("k1"), it("k2"), it("k3")}, sig("k1"), 10))
	wantState(t, tr, "k3", StateAddPend, 10, 0, 1)

	wantOK(t, tr.Observe([]KeyItem{it("k1"), it("k2")}, sig("k1"), 20))
	wantState(t, tr, "k3", StateUntracked, 0, 0, 0) // 缺席变未跟踪

	wantOK(t, tr.Observe([]KeyItem{it("k1"), it("k2"), it("k3")}, sig("k1"), 35))
	wantState(t, tr, "k3", StateAddPend, 35, 0, 1) // 重新计时

	wantOK(t, tr.Observe([]KeyItem{it("k1"), it("k2"), it("k3")}, sig("k1"), 64))
	wantState(t, tr, "k3", StateAddPend, 35, 0, 2) // 64 < 65 不晋升

	wantOK(t, tr.Observe([]KeyItem{it("k1"), it("k2"), it("k3")}, sig("k1"), 65))
	wantState(t, tr, "k3", StateValid, 0, 0, 0) // 65 >= 35+30 晋升
}

// 自签吊销：签名者按观测开始前状态判定。
func TestSelfSignedRevocation(t *testing.T) {
	cfg := Config{AddHold: 30, Retain: 100, MaxTracked: 10, MinAppear: 1, Quorum: 1, Anchors: sig("k1", "k2")}
	tr := mustNew(t, cfg)
	wantOK(t, tr.Observe([]KeyItem{it("k1"), it("k2"), it("k3")}, sig("k1"), 10))
	wantOK(t, tr.Observe([]KeyItem{it("k1"), it("k2"), it("k3")}, sig("k1"), 40)) // k3 VALID

	wantOK(t, tr.Observe([]KeyItem{it("k1"), itr("k2"), it("k3")}, sig("k2"), 50))
	wantState(t, tr, "k2", StateRevoked, 0, 50, 0) // 自签吊销，at=50
	wantState(t, tr, "k1", StateValid, 0, 0, 0)
	wantState(t, tr, "k3", StateValid, 0, 0, 0)
}

// 锁死：H>0 时新密钥当次不能晋升，报锁死且整体不提交；H=0 时通过。
func TestLockupAndZeroHold(t *testing.T) {
	base := Config{AddHold: 30, Retain: 100, MaxTracked: 10, MinAppear: 1, Quorum: 1, Anchors: sig("k1", "k2")}
	tr := mustNew(t, base)
	wantOK(t, tr.Observe([]KeyItem{it("k1"), it("k2"), it("k3")}, sig("k1"), 10))
	wantOK(t, tr.Observe([]KeyItem{it("k1"), it("k2"), it("k3")}, sig("k1"), 40))
	wantOK(t, tr.Observe([]KeyItem{it("k1"), itr("k2"), it("k3")}, sig("k2"), 50)) // k2 REVOKED

	// k1、k3 被吊销，k4 新见未晋升（60 < 60+30），VALID 为空 → 锁死，不提交。
	wantErr(t, tr.Observe([]KeyItem{itr("k1"), itr("k3"), it("k4")}, sig("k3"), 60), ErrLockup)
	wantState(t, tr, "k1", StateValid, 0, 0, 0)
	wantState(t, tr, "k3", StateValid, 0, 0, 0)
	wantState(t, tr, "k4", StateUntracked, 0, 0, 0)

	// H=0 的另一实例：k4 当次晋升，同一调用被接受。
	cfg0 := base
	cfg0.AddHold = 0
	tr0 := mustNew(t, cfg0)
	wantOK(t, tr0.Observe([]KeyItem{it("k1"), it("k2"), it("k3")}, sig("k1"), 10))
	wantOK(t, tr0.Observe([]KeyItem{it("k1"), itr("k2"), it("k3")}, sig("k2"), 50))
	wantOK(t, tr0.Observe([]KeyItem{itr("k1"), itr("k3"), it("k4")}, sig("k3"), 60))
	wantState(t, tr0, "k1", StateRevoked, 0, 60, 0)
	wantState(t, tr0, "k3", StateRevoked, 0, 60, 0)
	wantState(t, tr0, "k4", StateValid, 0, 0, 0)
}

// REVOKED 恰等于 at+R 移入 BANNED；BANNED 标识再出现被忽略。
func TestRevokedBanAndBannedIgnored(t *testing.T) {
	cfg := Config{AddHold: 30, Retain: 100, MaxTracked: 10, MinAppear: 1, Quorum: 1, Anchors: sig("k1", "k2")}
	tr := mustNew(t, cfg)
	wantOK(t, tr.Observe([]KeyItem{it("k1"), it("k2"), it("k3")}, sig("k1"), 10))
	wantOK(t, tr.Observe([]KeyItem{it("k1"), it("k2"), it("k3")}, sig("k1"), 40))
	wantOK(t, tr.Observe([]KeyItem{it("k1"), itr("k2"), it("k3")}, sig("k2"), 50))

	wantOK(t, tr.Observe([]KeyItem{it("k1"), it("k3")}, sig("k1"), 149))
	wantState(t, tr, "k2", StateRevoked, 0, 50, 0) // 缺席不变，149 < 150

	wantOK(t, tr.Observe([]KeyItem{it("k1"), it("k3")}, sig("k1"), 150))
	wantState(t, tr, "k2", StateBanned, 0, 0, 0) // 150 >= 50+100 封禁

	wantOK(t, tr.Observe([]KeyItem{it("k1"), it("k2"), it("k3")}, sig("k1"), 160))
	wantState(t, tr, "k2", StateBanned, 0, 0, 0) // 非吊销位忽略

	wantOK(t, tr.Observe([]KeyItem{it("k1"), itr("k2"), it("k3")}, sig("k1"), 170))
	wantState(t, tr, "k2", StateBanned, 0, 0, 0) // 吊销位同样忽略
}

// MISSING 再出现变回 VALID 且 since 清除；恰等于 since+R 变未跟踪。
func TestMissingRecoveryAndExpiry(t *testing.T) {
	cfg := Config{AddHold: 30, Retain: 100, MaxTracked: 10, MinAppear: 1, Quorum: 1, Anchors: sig("k1", "k2")}
	tr := mustNew(t, cfg)
	wantOK(t, tr.Observe([]KeyItem{it("k1"), it("k2"), it("k3")}, sig("k1"), 10))
	wantOK(t, tr.Observe([]KeyItem{it("k1"), it("k2"), it("k3")}, sig("k1"), 40)) // 全部 VALID

	wantOK(t, tr.Observe([]KeyItem{it("k1"), it("k3")}, sig("k1"), 70))
	wantState(t, tr, "k2", StateMissing, 70, 0, 0) // 缺席变 MISSING

	wantOK(t, tr.Observe([]KeyItem{it("k1"), it("k2"), it("k3")}, sig("k1"), 80))
	wantState(t, tr, "k2", StateValid, 0, 0, 0) // 再出现变回 VALID

	wantOK(t, tr.Observe([]KeyItem{it("k1"), it("k3")}, sig("k1"), 90))
	wantState(t, tr, "k2", StateMissing, 90, 0, 0)
	wantOK(t, tr.Observe([]KeyItem{it("k1"), it("k3")}, sig("k1"), 189))
	wantState(t, tr, "k2", StateMissing, 90, 0, 0) // 189 < 190
	wantOK(t, tr.Observe([]KeyItem{it("k1"), it("k3")}, sig("k1"), 190))
	wantState(t, tr, "k2", StateUntracked, 0, 0, 0) // 190 >= 90+100 变未跟踪，不进 BANNED
}

// MISSING 状态的密钥作签名者不可信。
func TestMissingSignerUntrusted(t *testing.T) {
	cfg := Config{AddHold: 30, Retain: 100, MaxTracked: 10, MinAppear: 1, Quorum: 1, Anchors: sig("k1", "k2")}
	tr := mustNew(t, cfg)
	wantOK(t, tr.Observe([]KeyItem{it("k1")}, sig("k1"), 70)) // k2 缺席变 MISSING
	wantState(t, tr, "k2", StateMissing, 70, 0, 0)
	wantErr(t, tr.Observe([]KeyItem{it("k1"), it("k2")}, sig("k2"), 80), ErrUntrustedSigners)
	wantState(t, tr, "k2", StateMissing, 70, 0, 0) // 拒绝不改状态
}

// M=3：cnt 恰等于 M 且保持期已过才晋升；保持期已过但 cnt 不足不晋升。
func TestMinAppearBoundary(t *testing.T) {
	cfg := Config{AddHold: 30, Retain: 100, MaxTracked: 10, MinAppear: 3, Quorum: 1, Anchors: sig("k1", "k2")}

	tr := mustNew(t, cfg)
	wantOK(t, tr.Observe([]KeyItem{it("k1"), it("k2"), it("k3")}, sig("k1"), 10))
	wantOK(t, tr.Observe([]KeyItem{it("k1"), it("k2"), it("k3")}, sig("k1"), 20))
	wantState(t, tr, "k3", StateAddPend, 10, 0, 2)
	wantOK(t, tr.Observe([]KeyItem{it("k1"), it("k2"), it("k3")}, sig("k1"), 45))
	wantState(t, tr, "k3", StateValid, 0, 0, 0) // cnt=3 且 45 >= 10+30

	// 保持期已过但 cnt=2 < 3，仍为 ADDPEND。
	tr2 := mustNew(t, cfg)
	wantOK(t, tr2.Observe([]KeyItem{it("k1"), it("k2"), it("k3")}, sig("k1"), 10))
	wantOK(t, tr2.Observe([]KeyItem{it("k1"), it("k2"), it("k3")}, sig("k1"), 45))
	wantState(t, tr2, "k3", StateAddPend, 10, 0, 2)
	wantOK(t, tr2.Observe([]KeyItem{it("k1"), it("k2"), it("k3")}, sig("k1"), 50))
	wantState(t, tr2, "k3", StateValid, 0, 0, 0) // 下一次出现 cnt=3 晋升
}

// 缺席与带吊销位使 cnt 清零重来。
func TestAppearCountReset(t *testing.T) {
	cfg := Config{AddHold: 0, Retain: 100, MaxTracked: 10, MinAppear: 3, Quorum: 1, Anchors: sig("k1")}

	tr := mustNew(t, cfg)
	wantOK(t, tr.Observe([]KeyItem{it("k1"), it("k2")}, sig("k1"), 10))
	wantOK(t, tr.Observe([]KeyItem{it("k1"), it("k2")}, sig("k1"), 20))
	wantState(t, tr, "k2", StateAddPend, 10, 0, 2)
	wantOK(t, tr.Observe([]KeyItem{it("k1")}, sig("k1"), 30)) // 缺席
	wantState(t, tr, "k2", StateUntracked, 0, 0, 0)
	wantOK(t, tr.Observe([]KeyItem{it("k1"), it("k2")}, sig("k1"), 40))
	wantState(t, tr, "k2", StateAddPend, 40, 0, 1) // cnt 从 1 重来

	// 带吊销位直接变未跟踪。
	wantOK(t, tr.Observe([]KeyItem{it("k1"), itr("k2")}, sig("k1"), 50))
	wantState(t, tr, "k2", StateUntracked, 0, 0, 0)
	wantOK(t, tr.Observe([]KeyItem{it("k1"), it("k2")}, sig("k1"), 60))
	wantState(t, tr, "k2", StateAddPend, 60, 0, 1)
}

// ADDPEND 带吊销位直接变未跟踪；未知密钥带吊销位被忽略。
func TestRevokedBitOnAddPendAndUnknown(t *testing.T) {
	cfg := Config{AddHold: 30, Retain: 100, MaxTracked: 10, MinAppear: 1, Quorum: 1, Anchors: sig("k1")}
	tr := mustNew(t, cfg)
	wantOK(t, tr.Observe([]KeyItem{it("k1"), it("k2")}, sig("k1"), 10))
	wantState(t, tr, "k2", StateAddPend, 10, 0, 1)
	wantOK(t, tr.Observe([]KeyItem{it("k1"), itr("k2"), itr("k9")}, sig("k1"), 20))
	wantState(t, tr, "k2", StateUntracked, 0, 0, 0) // ADDPEND + 吊销位 → 未跟踪
	wantState(t, tr, "k9", StateUntracked, 0, 0, 0) // 未知 + 吊销位 → 忽略
}

// REVOKED 再出现：吊销位不刷新 at，非吊销位仍为 REVOKED。
func TestRevokedNoRefreshNoResurrect(t *testing.T) {
	cfg := Config{AddHold: 0, Retain: 100, MaxTracked: 10, MinAppear: 1, Quorum: 1, Anchors: sig("k1", "k2")}
	tr := mustNew(t, cfg)
	wantOK(t, tr.Observe([]KeyItem{it("k1"), itr("k2")}, sig("k1"), 50))
	wantState(t, tr, "k2", StateRevoked, 0, 50, 0)

	wantOK(t, tr.Observe([]KeyItem{it("k1"), itr("k2")}, sig("k1"), 80))
	wantState(t, tr, "k2", StateRevoked, 0, 50, 0) // at 不刷新

	wantOK(t, tr.Observe([]KeyItem{it("k1"), it("k2")}, sig("k1"), 90))
	wantState(t, tr, "k2", StateRevoked, 0, 50, 0) // 不会重新被信任
}

// Q=2：去重后 VALID 签名者恰够通过，少 1、重复与未知签名者不凑数。
func TestQuorumDedup(t *testing.T) {
	cfg := Config{AddHold: 0, Retain: 100, MaxTracked: 10, MinAppear: 1, Quorum: 2, Anchors: sig("k1", "k2", "k3")}
	tr := mustNew(t, cfg)

	wantErr(t, tr.Observe([]KeyItem{it("k1"), it("k2"), it("k3")}, sig("k1"), 10), ErrUntrustedSigners)
	wantErr(t, tr.Observe([]KeyItem{it("k1"), it("k2"), it("k3")}, sig("k1", "k1"), 10), ErrUntrustedSigners)
	wantErr(t, tr.Observe([]KeyItem{it("k1"), it("k2"), it("k3")}, sig("k1", "x"), 10), ErrUntrustedSigners)
	wantErr(t, tr.Observe([]KeyItem{it("k1"), it("k2"), it("k3")}, nil, 10), ErrUntrustedSigners)
	wantOK(t, tr.Observe([]KeyItem{it("k1"), it("k2"), it("k3")}, sig("k1", "k2", "x"), 10))
	wantOK(t, tr.Observe([]KeyItem{it("k1"), it("k2"), it("k3")}, sig("k1", "k2", "k2"), 20))
}

// VALID 恰为 Q 通过，Q-1 报锁死且整体不提交。
func TestValidExactlyQuorum(t *testing.T) {
	cfg := Config{AddHold: 0, Retain: 100, MaxTracked: 10, MinAppear: 1, Quorum: 2, Anchors: sig("k1", "k2", "k3")}
	tr := mustNew(t, cfg)

	wantOK(t, tr.Observe([]KeyItem{it("k1"), itr("k2"), it("k3")}, sig("k1", "k2"), 20))
	wantState(t, tr, "k2", StateRevoked, 0, 20, 0)
	wantState(t, tr, "k1", StateValid, 0, 0, 0)
	wantState(t, tr, "k3", StateValid, 0, 0, 0) // VALID={k1,k3}，恰为 Q=2

	wantErr(t, tr.Observe([]KeyItem{it("k1"), itr("k3")}, sig("k1", "k3"), 30), ErrLockup)
	wantState(t, tr, "k3", StateValid, 0, 0, 0) // 不提交
	wantState(t, tr, "k1", StateValid, 0, 0, 0)
}

// 锁死与超限同时成立时报锁死。
func TestLockupBeforeOverflow(t *testing.T) {
	cfg := Config{AddHold: 30, Retain: 100, MaxTracked: 1, MinAppear: 1, Quorum: 1, Anchors: sig("k1")}
	tr := mustNew(t, cfg)
	// k1 被吊销（VALID=0 < Q=1，锁死），k2 新见（跟踪数 2 > Kmax=1，超限）。
	wantErr(t, tr.Observe([]KeyItem{itr("k1"), it("k2")}, sig("k1"), 10), ErrLockup)
	wantState(t, tr, "k1", StateValid, 0, 0, 0)
	wantState(t, tr, "k2", StateUntracked, 0, 0, 0)
}

// 超限按处理后的跟踪数判定；MISSING 过期释放名额则不超限。
func TestOverflowPostProcessing(t *testing.T) {
	cfg := Config{AddHold: 30, Retain: 10, MaxTracked: 2, MinAppear: 1, Quorum: 1, Anchors: sig("k1", "k2")}
	tr := mustNew(t, cfg)

	// 直接超 Kmax：k3 新见后跟踪数 3 > 2。
	wantErr(t, tr.Observe([]KeyItem{it("k1"), it("k2"), it("k3")}, sig("k1"), 5), ErrOverflow)
	wantState(t, tr, "k3", StateUntracked, 0, 0, 0)

	// k2 缺席变 MISSING(since=10)，到 now=20 恰过期释放名额，k3 加入后不超限。
	wantOK(t, tr.Observe([]KeyItem{it("k1")}, sig("k1"), 10))
	wantState(t, tr, "k2", StateMissing, 10, 0, 0)
	wantOK(t, tr.Observe([]KeyItem{it("k1"), it("k3")}, sig("k1"), 20))
	wantState(t, tr, "k2", StateUntracked, 0, 0, 0) // 20 >= 10+10 过期
	wantState(t, tr, "k3", StateAddPend, 20, 0, 1)
}

// 被拒绝的观测不改变状态、BANNED 集合与时钟。
func TestRejectionAtomicity(t *testing.T) {
	cfg := Config{AddHold: 30, Retain: 100, MaxTracked: 10, MinAppear: 1, Quorum: 1, Anchors: sig("k1", "k2")}
	tr := mustNew(t, cfg)
	wantOK(t, tr.Observe([]KeyItem{it("k1"), it("k2"), it("k3")}, sig("k1"), 10))
	wantState(t, tr, "k3", StateAddPend, 10, 0, 1)

	// 锁死拒绝：吊销 k1、k2，k3 未晋升（20 < 10+30）。
	wantErr(t, tr.Observe([]KeyItem{itr("k1"), itr("k2"), it("k3")}, sig("k1"), 20), ErrLockup)
	wantState(t, tr, "k1", StateValid, 0, 0, 0)
	wantState(t, tr, "k2", StateValid, 0, 0, 0)
	wantState(t, tr, "k3", StateAddPend, 10, 0, 1)

	// 时钟未前进：now=40 的正常观测仍被接受，且 k3 晋升（40 >= 10+30）。
	wantOK(t, tr.Observe([]KeyItem{it("k1"), it("k2"), it("k3")}, sig("k2"), 40))
	wantState(t, tr, "k3", StateValid, 0, 0, 0)

	// 参数非法拒绝也不推进时钟：now=45 仍被接受（maxNow 停在 40 而非 50）。
	wantErr(t, tr.Observe(nil, sig("k1"), 50), ErrInvalidArgument)
	wantOK(t, tr.Observe([]KeyItem{it("k1"), it("k2")}, sig("k1"), 45))
}

// 时钟回退先于签名者检查。
func TestClockRollbackBeforeSigners(t *testing.T) {
	cfg := Config{AddHold: 0, Retain: 100, MaxTracked: 10, MinAppear: 1, Quorum: 1, Anchors: sig("k1")}
	tr := mustNew(t, cfg)
	wantOK(t, tr.Observe([]KeyItem{it("k1")}, sig("k1"), 100))
	// 签名者完全不可信，但 now 回退优先报时钟回退。
	wantErr(t, tr.Observe([]KeyItem{it("k1")}, sig("x"), 50), ErrClockRollback)
	// 回退被拒绝不推进时钟：now=100 仍接受。
	wantOK(t, tr.Observe([]KeyItem{it("k1")}, sig("k1"), 100))
}

// 构造参数非法整体拒绝；边界合法值接受。
func TestConfigValidation(t *testing.T) {
	base := Config{AddHold: 30, Retain: 100, MaxTracked: 3, MinAppear: 1, Quorum: 1, Anchors: sig("k1", "k2")}
	bad := []Config{
		{AddHold: -1, Retain: 100, MaxTracked: 3, MinAppear: 1, Quorum: 1, Anchors: sig("k1")},
		{AddHold: MaxHold + 1, Retain: 100, MaxTracked: 3, MinAppear: 1, Quorum: 1, Anchors: sig("k1")},
		{AddHold: 0, Retain: 0, MaxTracked: 3, MinAppear: 1, Quorum: 1, Anchors: sig("k1")},
		{AddHold: 0, Retain: MaxHold + 1, MaxTracked: 3, MinAppear: 1, Quorum: 1, Anchors: sig("k1")},
		{AddHold: 0, Retain: 1, MaxTracked: 0, MinAppear: 1, Quorum: 1, Anchors: sig("k1")},
		{AddHold: 0, Retain: 1, MaxTracked: 1001, MinAppear: 1, Quorum: 1, Anchors: sig("k1")},
		{AddHold: 0, Retain: 1, MaxTracked: 3, MinAppear: 0, Quorum: 1, Anchors: sig("k1")},
		{AddHold: 0, Retain: 1, MaxTracked: 3, MinAppear: 101, Quorum: 1, Anchors: sig("k1")},
		{AddHold: 0, Retain: 1, MaxTracked: 3, MinAppear: 1, Quorum: 0, Anchors: sig("k1")},
		{AddHold: 0, Retain: 1, MaxTracked: 3, MinAppear: 1, Quorum: 4, Anchors: sig("k1")},
		{AddHold: 0, Retain: 1, MaxTracked: 3, MinAppear: 1, Quorum: 2, Anchors: sig("k1")},                  // 锚个数 < Q
		{AddHold: 0, Retain: 1, MaxTracked: 2, MinAppear: 1, Quorum: 1, Anchors: sig("k1", "k2", "k3")},      // 锚个数 > Kmax
		{AddHold: 0, Retain: 1, MaxTracked: 3, MinAppear: 1, Quorum: 1, Anchors: sig("k1", "k1")},            // 重复
		{AddHold: 0, Retain: 1, MaxTracked: 3, MinAppear: 1, Quorum: 1, Anchors: [][]byte{[]byte("k1"), {}}}, // 空标识
	}
	for i, cfg := range bad {
		if _, err := NewTracker(cfg); err == nil {
			t.Fatalf("坏配置 %d 未被拒绝", i)
		} else if k, _ := KindOf(err); k != ErrInvalidConfig {
			t.Fatalf("坏配置 %d 错误类别 = %v，期望 %v", i, k, ErrInvalidConfig)
		}
	}
	good := []Config{
		base,
		{AddHold: 0, Retain: 1, MaxTracked: 1, MinAppear: 1, Quorum: 1, Anchors: sig("k1")},
		{AddHold: MaxHold, Retain: MaxHold, MaxTracked: MaxTracked, MinAppear: MaxAppear, Quorum: 1, Anchors: sig("k1")},
	}
	for i, cfg := range good {
		if _, err := NewTracker(cfg); err != nil {
			t.Fatalf("好配置 %d 被拒绝: %v", i, err)
		}
	}
}

// Observe 参数非法是第一关。
func TestObserveArgValidation(t *testing.T) {
	cfg := Config{AddHold: 0, Retain: 1, MaxTracked: 4, MinAppear: 1, Quorum: 1, Anchors: sig("k1")}
	tr := mustNew(t, cfg)

	wantErr(t, tr.Observe(nil, sig("k1"), 0), ErrInvalidArgument)                         // 空密钥集
	wantErr(t, tr.Observe(make([]KeyItem, 65), sig("k1"), 0), ErrInvalidArgument)         // 超 64 项
	wantErr(t, tr.Observe([]KeyItem{{ID: nil}}, sig("k1"), 0), ErrInvalidArgument)        // 空标识
	wantErr(t, tr.Observe([]KeyItem{it("a"), it("a")}, sig("k1"), 0), ErrInvalidArgument) // 重复标识
	wantErr(t, tr.Observe([]KeyItem{it("a")}, [][]byte{{}}, 0), ErrInvalidArgument)       // 空签名者
	wantErr(t, tr.Observe([]KeyItem{it("a")}, sig("k1"), -1), ErrInvalidArgument)         // now 越界
	wantErr(t, tr.Observe([]KeyItem{it("a")}, sig("k1"), MaxNow+1), ErrInvalidArgument)

	// 非法参数不推进时钟：now=0 仍接受。
	wantOK(t, tr.Observe([]KeyItem{it("k1")}, sig("k1"), 0))
}

// 非导出计数器：每次 Observe 处理的密钥项数不超过密钥集项数加被跟踪数（不含 BANNED）。
func TestScanCountBound(t *testing.T) {
	cfg := Config{AddHold: 0, Retain: 2, MaxTracked: 10, MinAppear: 1, Quorum: 1, Anchors: sig("k1", "k2")}
	tr := mustNew(t, cfg)

	observes := []struct {
		set     []KeyItem
		signers [][]byte
		now     int64
	}{
		{[]KeyItem{it("k1"), it("k2"), it("k3")}, sig("k1"), 0},
		{[]KeyItem{it("k1"), itr("k2")}, sig("k1"), 1},          // k2 REVOKED at=1，k3 缺席变 MISSING
		{[]KeyItem{it("k1")}, sig("k1"), 3},                     // k2 移入 BANNED，k3 过期变未跟踪
		{[]KeyItem{it("k1"), it("k2"), it("k4")}, sig("k1"), 4}, // k2 被忽略，不计入跟踪数
	}
	for i, o := range observes {
		trackedBefore := len(tr.keys)
		if err := tr.Observe(o.set, o.signers, o.now); err != nil {
			t.Fatalf("观测 %d 被拒绝: %v", i, err)
		}
		if tr.scanCount > tr.scanBound {
			t.Fatalf("观测 %d：scanCount=%d 超过界 %d", i, tr.scanCount, tr.scanBound)
		}
		if want := len(o.set) + trackedBefore; tr.scanBound != want {
			t.Fatalf("观测 %d：scanBound=%d，期望 %d", i, tr.scanBound, want)
		}
	}
	// BANNED 集合大小不影响计数界：最后一次观测的界 = 2 项 + 1 个被跟踪（k1）。
	if tr.scanBound != 3+1 {
		t.Fatalf("BANNED 不应计入跟踪数界，scanBound=%d，期望 4", tr.scanBound)
	}
	wantState(t, tr, "k2", StateBanned, 0, 0, 0)
}

// BANNED 集合增大不影响处理计数界。
func TestScanCountBannedExcluded(t *testing.T) {
	cfg := Config{AddHold: 0, Retain: 1, MaxTracked: 100, MinAppear: 1, Quorum: 1, Anchors: sig("k0")}
	tr := mustNew(t, cfg)
	var now int64
	for i := 0; i < 30; i++ {
		k := fmt.Sprintf("ban%02d", i)
		wantOK(t, tr.Observe([]KeyItem{it("k0"), it(k)}, sig("k0"), now))
		now++
		wantOK(t, tr.Observe([]KeyItem{it("k0"), itr(k)}, sig("k0"), now))
		now++
		wantOK(t, tr.Observe([]KeyItem{it("k0")}, sig("k0"), now)) // 过期封禁
		now++
	}
	if len(tr.banned) != 30 {
		t.Fatalf("BANNED 个数 = %d，期望 30", len(tr.banned))
	}
	wantOK(t, tr.Observe([]KeyItem{it("k0"), it("zz")}, sig("k0"), now))
	if tr.scanBound != 2+1 {
		t.Fatalf("scanBound=%d，期望 3（BANNED 不计入）", tr.scanBound)
	}
	if tr.scanCount > tr.scanBound {
		t.Fatalf("scanCount=%d 超过界 %d", tr.scanCount, tr.scanBound)
	}
}

// 并发调用等价于某个串行顺序（配合 -race 运行）。
func TestConcurrency(t *testing.T) {
	cfg := Config{AddHold: 0, Retain: 100, MaxTracked: 64, MinAppear: 1, Quorum: 1, Anchors: sig("k1")}
	tr := mustNew(t, cfg)
	done := make(chan struct{})
	go func() {
		for i := int64(0); i < 200; i++ {
			_ = tr.Observe([]KeyItem{it("k1"), it("k2")}, sig("k1"), i)
		}
		close(done)
	}()
	for i := 0; i < 200; i++ {
		_ = tr.State([]byte("k1"))
		_ = tr.State([]byte("k2"))
	}
	<-done
	if got := tr.State([]byte("k2")); got.State != StateValid {
		t.Fatalf("k2 状态 = %v，期望 VALID", got.State)
	}
}
