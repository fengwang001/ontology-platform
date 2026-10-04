package ssn

import (
	"reflect"
	"testing"
)

func mustNew(t *testing.T, K, H int) *Certifier {
	t.Helper()
	c, err := New(K, H)
	if err != nil {
		t.Fatalf("New(%d, %d) 失败: %v", K, H, err)
	}
	return c
}

func mustRead(t *testing.T, c *Certifier, tx, k int) int64 {
	t.Helper()
	v, err := c.Read(tx, k)
	if err != nil {
		t.Fatalf("Read(%d, %d) 意外拒绝: %v", tx, k, err)
	}
	return v
}

func mustWrite(t *testing.T, c *Certifier, tx, k int, v int64) {
	t.Helper()
	if err := c.Write(tx, k, v); err != nil {
		t.Fatalf("Write(%d, %d, %d) 意外拒绝: %v", tx, k, v, err)
	}
}

func mustCommit(t *testing.T, c *Certifier, tx int) int64 {
	t.Helper()
	n, err := c.Commit(tx)
	if err != nil {
		t.Fatalf("Commit(%d) 意外中止: %v", tx, err)
	}
	return n
}

func expectReason(t *testing.T, what string, err error, r Reason) {
	t.Helper()
	if !HasReason(err, r) {
		t.Fatalf("%s: 期望原因 %s，实际 %v", what, r, err)
	}
	t.Logf("%s -> 拒绝原因 %s（符合预期）", what, r)
}

func assertState(t *testing.T, c *Certifier, want State) {
	t.Helper()
	got := c.Dump()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("状态不一致:\n got=%+v\nwant=%+v", got, want)
	}
}

func vi(cs, ps, ss, val int64) VersionInfo {
	return VersionInfo{CS: cs, PS: ps, SS: ss, Value: val}
}

// checkInvariants 校验：每键版本链 cs 严格递增且不大于 n；任一版本
// ps 不大于 n；已被覆盖的版本其 ss 不大于覆盖者的 cs。
func checkInvariants(t *testing.T, s State) {
	t.Helper()
	for k, chain := range s.Chains {
		for i, v := range chain {
			if v.CS > s.N {
				t.Fatalf("键 %d 版本 %d: cs=%d 大于 n=%d", k, i, v.CS, s.N)
			}
			if v.PS > s.N {
				t.Fatalf("键 %d 版本 %d: ps=%d 大于 n=%d", k, i, v.PS, s.N)
			}
			if i > 0 && chain[i-1].CS >= v.CS {
				t.Fatalf("键 %d 版本链 cs 非严格递增: %d 后接 %d", k, chain[i-1].CS, v.CS)
			}
			if i+1 < len(chain) && v.SS > chain[i+1].CS {
				t.Fatalf("键 %d 版本 %d: ss=%d 大于覆盖者 cs=%d", k, i, v.SS, chain[i+1].CS)
			}
		}
	}
}

// TestWriteSkewExclusionWindow 复现题目示例：K=2 的写偏斜被排除窗口拒绝。
func TestWriteSkewExclusionWindow(t *testing.T) {
	c := mustNew(t, 2, 2)
	t1 := c.Begin()
	t2 := c.Begin()
	if t1 != 1 || t2 != 2 {
		t.Fatalf("事务号应从 1 起递增，得到 %d, %d", t1, t2)
	}
	mustRead(t, c, t1, 0)
	mustRead(t, c, t1, 1)
	mustWrite(t, c, t1, 0, 10)
	mustRead(t, c, t2, 0)
	mustRead(t, c, t2, 1)
	mustWrite(t, c, t2, 1, 20)

	// T1: η=max(读集 cs=0, 写集键0最新 cs=0/ps=0)=0，π=min(1, +∞)=1 > 0，通过。
	if got := mustCommit(t, c, t1); got != 1 {
		t.Fatalf("T1 提交号 = %d，期望 1", got)
	}
	t.Logf("T1 提交: η=0, π=1, c=1；键0旧版本 ss=1，键0/键1旧版本 ps=1")
	want := State{N: 1, Chains: [][]VersionInfo{
		{vi(0, 1, 1, 0), vi(1, 0, Inf, 10)},
		{vi(0, 1, Inf, 0)},
	}}
	assertState(t, c, want)

	// T2: π=min(2, 键0旧版本 ss=1)=1，η 含键1旧版本 ps=1 故 η=1，π<=η 中止。
	before := c.Dump()
	_, err := c.Commit(t2)
	expectReason(t, "T2 提交（写偏斜）", err, ReasonExclusionWindow)
	if got := c.Dump(); !reflect.DeepEqual(got, before) {
		t.Fatalf("SSN 中止后状态被改变:\n got=%+v\nwant=%+v", got, before)
	}
	t.Logf("T2 提交: η=1, π=1, π<=η 以串行安全网中止；n 仍为 1，水位逐字段不变")
}

// TestExclusionWindowBoundary 验证 π 恰等于 η 中止、π 比 η 大 1 通过。
func TestExclusionWindowBoundary(t *testing.T) {
	c := mustNew(t, 2, 2)
	t1 := c.Begin()
	mustRead(t, c, t1, 0)
	mustWrite(t, c, t1, 1, 5)
	if got := mustCommit(t, c, t1); got != 1 {
		t.Fatalf("T1 提交号 = %d，期望 1", got)
	}
	// 键0 v0: ps=1, ss=1；键1: v0{ss=1}, v1{cs=1}。

	t2 := c.Begin()       // s=1
	t3 := c.Begin()       // s=1
	mustRead(t, c, t2, 1) // 键1 v1{cs=1}
	mustWrite(t, c, t2, 0, 7)
	mustRead(t, c, t3, 0) // 键0 v0
	mustWrite(t, c, t3, 1, 9)

	// T2: η=max(键1 v1 cs=1, 键0最新 cs=0/ps=1)=1，π=min(2, 键1 v1 ss=+∞)=2，
	// π=η+1，恰好通过。
	if got := mustCommit(t, c, t2); got != 2 {
		t.Fatalf("T2 提交号 = %d，期望 2", got)
	}
	t.Logf("T2 提交: η=1, π=2, π=η+1 通过，c=2；键0 v0 ss=2，键1 v1 ps=2")

	// T3: η=max(键0 v0 cs=0, 键1最新 cs=1/ps=2)=2，π=min(3, 键0 v0 ss=2)=2，
	// π 恰等于 η，中止。
	before := c.Dump()
	_, err := c.Commit(t3)
	expectReason(t, "T3 提交（π==η）", err, ReasonExclusionWindow)
	if got := c.Dump(); !reflect.DeepEqual(got, before) {
		t.Fatalf("SSN 中止后状态被改变:\n got=%+v\nwant=%+v", got, before)
	}
	t.Logf("T3 提交: η=2, π=2, π==η 中止；n 仍为 2")
}

// TestReadOnlyCommitAdvancesN 验证只读事务同样走②到④：推进 n 并抬高 ps。
func TestReadOnlyCommitAdvancesN(t *testing.T) {
	c := mustNew(t, 2, 2)
	t1 := c.Begin()
	mustRead(t, c, t1, 0)
	if got := mustCommit(t, c, t1); got != 1 {
		t.Fatalf("只读 T1 提交号 = %d，期望 1", got)
	}
	// 空事务（读写集均空）也是只读事务，同样消耗提交号。
	t2 := c.Begin()
	if got := mustCommit(t, c, t2); got != 2 {
		t.Fatalf("空事务 T2 提交号 = %d，期望 2", got)
	}
	want := State{N: 2, Chains: [][]VersionInfo{
		{vi(0, 1, Inf, 0)}, // ps 抬为 1，链长不变，ss 仍为 +∞
		{vi(0, 0, Inf, 0)},
	}}
	assertState(t, c, want)
	t.Logf("只读事务推进 n 到 2，键0 v0 ps=1，无新版本产生")
}

// TestReadOnlyAnomaly 复现三事务只读异常：T1 读 y 写 y，T2 读 x、y 写 x，
// T3 在 T1 提交后只读 x、y；T3 提交后 T2 被串行安全网中止。
func TestReadOnlyAnomaly(t *testing.T) {
	const x, y = 0, 1
	c := mustNew(t, 2, 2)
	t1 := c.Begin() // s=0
	t2 := c.Begin() // s=0
	mustRead(t, c, t1, y)
	mustWrite(t, c, t1, y, 1)
	// T1: η=0, π=1, 通过；y v0: ps=1, ss=1。
	if got := mustCommit(t, c, t1); got != 1 {
		t.Fatalf("T1 提交号 = %d，期望 1", got)
	}

	t3 := c.Begin() // s=1，在 T1 提交后
	if v := mustRead(t, c, t3, x); v != 0 {
		t.Fatalf("T3 读 x = %d，期望 0", v)
	}
	if v := mustRead(t, c, t3, y); v != 1 {
		t.Fatalf("T3 读 y = %d，期望 1", v)
	}
	mustRead(t, c, t2, x)
	mustRead(t, c, t2, y) // s=0，读到 y v0
	mustWrite(t, c, t2, x, 9)

	// T3（只读）: η=max(x v0 cs=0, y v1 cs=1)=1，π=min(2, +∞)=2 > 1，通过。
	if got := mustCommit(t, c, t3); got != 2 {
		t.Fatalf("T3 提交号 = %d，期望 2", got)
	}
	t.Logf("T3 只读提交: η=1, π=2, c=2；x v0 ps=2, y v1 ps=2")

	// T2: η 含写集键 x 最新版本 ps=2 故 η=2，π=min(3, y v0 ss=1)=1，中止。
	before := c.Dump()
	_, err := c.Commit(t2)
	expectReason(t, "T2 提交（只读异常）", err, ReasonExclusionWindow)
	if got := c.Dump(); !reflect.DeepEqual(got, before) {
		t.Fatalf("SSN 中止后状态被改变:\n got=%+v\nwant=%+v", got, before)
	}
	want := State{N: 2, Chains: [][]VersionInfo{
		{vi(0, 2, Inf, 0)},
		{vi(0, 1, 1, 0), vi(1, 2, Inf, 1)},
	}}
	assertState(t, c, want)
	t.Logf("T2 提交: η=2, π=1, π<=η 中止；n 仍为 2")
}

// TestWriteSetPSInEta 验证写集键最新版本的 ps 参与 η：去掉它会漏检写偏斜。
func TestWriteSetPSInEta(t *testing.T) {
	// 主场景：键0 的 ps 被只读事务抬到 2，Tv 的 η=2，π=1，中止。
	c := mustNew(t, 3, 2)
	tv := c.Begin() // s=0
	tw := c.Begin() // s=0
	mustWrite(t, c, tw, 1, 1)
	if got := mustCommit(t, c, tw); got != 1 { // 键1 v0 ss=1
		t.Fatalf("Tw 提交号 = %d，期望 1", got)
	}
	tr := c.Begin() // s=1
	mustRead(t, c, tr, 0)
	if got := mustCommit(t, c, tr); got != 2 { // 键0 v0 ps=2
		t.Fatalf("Tr 提交号 = %d，期望 2", got)
	}
	mustRead(t, c, tv, 1) // 键1 v0，ss=1
	mustWrite(t, c, tv, 0, 5)
	// Tv: η=max(键1 v0 cs=0, 键0最新 cs=0/ps=2)=2，π=min(3, 键1 v0 ss=1)=1。
	_, err := c.Commit(tv)
	expectReason(t, "Tv 提交（写集 ps 参与 η）", err, ReasonExclusionWindow)
	if got := c.Dump().N; got != 2 {
		t.Fatalf("中止后 n = %d，期望 2", got)
	}
	t.Logf("Tv 提交: η=2（来自写集键0 ps=2）, π=1, π<=η 中止")

	// 对照：只读事务改读键2，键0 的 ps 保持 0，η=0，同一 Tv 得以提交。
	c2 := mustNew(t, 3, 2)
	cv := c2.Begin()
	cw := c2.Begin()
	mustWrite(t, c2, cw, 1, 1)
	mustCommit(t, c2, cw)
	cr := c2.Begin()
	mustRead(t, c2, cr, 2) // 不抬键0 的 ps
	mustCommit(t, c2, cr)
	mustRead(t, c2, cv, 1)
	mustWrite(t, c2, cv, 0, 5)
	if got := mustCommit(t, c2, cv); got != 3 {
		t.Fatalf("对照 Tv 提交号 = %d，期望 3（η=0, π=1 通过）", got)
	}
	t.Logf("对照: 写集键0 ps=0 时 η=0, π=1, 通过——证明写集 ps 是判中止的决定因素")
}

// TestSuccessorWatermarkIsPiNotC 验证被覆盖版本的 ss 置为 π 而非 c，
// 后继水位沿三事务链传递（ss 保持 1），最终使只读受害者被中止。
func TestSuccessorWatermarkIsPiNotC(t *testing.T) {
	c := mustNew(t, 3, 2)
	t1 := c.Begin() // s=0
	t2 := c.Begin() // s=0
	t3 := c.Begin() // s=0

	mustWrite(t, c, t1, 0, 1)
	if got := mustCommit(t, c, t1); got != 1 { // 键0 v0 ss=1
		t.Fatalf("T1 提交号 = %d，期望 1", got)
	}

	tv := c.Begin()       // s=1
	mustRead(t, c, tv, 0) // 键0 v1{cs=1}
	mustRead(t, c, tv, 2) // 键2 v0

	mustRead(t, c, t2, 0) // 键0 v0，ss=1
	mustWrite(t, c, t2, 1, 2)
	// T2: η=0，π=min(2, 键0 v0 ss=1)=1 > 0，通过；键1 v0 ss 置为 π=1 而非 c=2。
	if got := mustCommit(t, c, t2); got != 2 {
		t.Fatalf("T2 提交号 = %d，期望 2", got)
	}
	if ss := c.Dump().Chains[1][0].SS; ss != 1 {
		t.Fatalf("键1 v0 ss = %d，期望 1（π 而非 c=2）", ss)
	}
	t.Logf("T2 提交: π=1，键1 v0 ss=1（若误置为 c=2 则后续链会断）")

	mustRead(t, c, t3, 1) // 键1 v0，ss=1
	mustWrite(t, c, t3, 2, 3)
	// T3: η=0，π=min(3, 键1 v0 ss=1)=1 > 0，通过；键2 v0 ss=1。
	if got := mustCommit(t, c, t3); got != 3 {
		t.Fatalf("T3 提交号 = %d，期望 3", got)
	}
	if ss := c.Dump().Chains[2][0].SS; ss != 1 {
		t.Fatalf("键2 v0 ss = %d，期望 1（水位沿链传递）", ss)
	}
	t.Logf("T3 提交: π=1，键2 v0 ss=1（三事务链：1 -> 1 -> 1）")

	// Tv（只读）: η=max(键0 v1 cs=1, 键2 v0 cs=0)=1，π=min(4, 键2 v0 ss=1)=1，
	// π<=η 中止。若 ss 误置为 c，则键2 v0 ss=2，π=2 > η=1 会被漏检。
	_, err := c.Commit(tv)
	expectReason(t, "Tv 提交（ss=π 传递链）", err, ReasonExclusionWindow)
	if got := c.Dump().N; got != 3 {
		t.Fatalf("中止后 n = %d，期望 3", got)
	}
	t.Logf("Tv 提交: η=1, π=1, π<=η 中止——ss 置为 π 使后继水位传递")
}

// TestWriteWriteConflictPrecedence 验证写写冲突先于串行安全网上报。
func TestWriteWriteConflictPrecedence(t *testing.T) {
	c := mustNew(t, 2, 2)
	t1 := c.Begin() // s=0
	t2 := c.Begin() // s=0
	mustRead(t, c, t2, 0)
	mustWrite(t, c, t2, 0, 5)
	mustWrite(t, c, t1, 0, 1)
	if got := mustCommit(t, c, t1); got != 1 {
		t.Fatalf("T1 提交号 = %d，期望 1", got)
	}
	// T2 同时满足两种中止：写写冲突（键0最新 cs=1 > s=0），
	// 且 η=max(0, 键0最新 cs=1/ps=1)=1，π=min(2, 键0 v0 ss=1)=1 亦命中窗口。
	before := c.Dump()
	_, err := c.Commit(t2)
	expectReason(t, "T2 提交（写写冲突优先）", err, ReasonWriteWriteConflict)
	if got := c.Dump(); !reflect.DeepEqual(got, before) {
		t.Fatalf("写写冲突中止后状态被改变:\n got=%+v\nwant=%+v", got, before)
	}
	t.Logf("T2 提交: 写写冲突与 π<=η 同时成立，上报写写冲突；n 仍为 1")
}

// TestTrimmedVersionWatermark 验证被回收（截断）版本的水位仍参与认证：
// Tv 读集持有的键0 v0 已从链上丢弃，但其 ss=1 仍使 Tv 被中止。
func TestTrimmedVersionWatermark(t *testing.T) {
	c := mustNew(t, 2, 2) // H=2
	tv := c.Begin()       // s=0
	mustRead(t, c, tv, 0) // 持有键0 v0
	mustWrite(t, c, tv, 1, 7)

	ta := c.Begin() // s=0
	mustWrite(t, c, ta, 0, 1)
	if got := mustCommit(t, c, ta); got != 1 { // 键0 v0 ss=1
		t.Fatalf("Ta 提交号 = %d，期望 1", got)
	}
	tb := c.Begin() // s=1
	mustWrite(t, c, tb, 0, 2)
	if got := mustCommit(t, c, tb); got != 2 { // 链截为 [v1, v2]，v0 被回收
		t.Fatalf("Tb 提交号 = %d，期望 2", got)
	}
	if n := len(c.Dump().Chains[0]); n != 2 {
		t.Fatalf("键0 链长 = %d，期望 2（v0 已被回收）", n)
	}
	tc := c.Begin() // s=2
	mustRead(t, c, tc, 1)
	if got := mustCommit(t, c, tc); got != 3 { // 键1 v0 ps=3
		t.Fatalf("Tc 提交号 = %d，期望 3", got)
	}

	// Tv: η=max(键0 v0 cs=0, 键1最新 cs=0/ps=3)=3，
	// π=min(4, 键0 v0 ss=1)=1——v0 已回收但读集仍持有该对象。
	_, err := c.Commit(tv)
	expectReason(t, "Tv 提交（回收版本水位）", err, ReasonExclusionWindow)
	if got := c.Dump().N; got != 3 {
		t.Fatalf("中止后 n = %d，期望 3", got)
	}
	t.Logf("Tv 提交: η=3, π=1（来自已回收的键0 v0 ss=1）, π<=η 中止")
}

// TestSnapshotTooOld 复现题目示例：K=1, H=2，链被截为 cs=1,2 后，
// s=0 的事务读键0 以最旧版本 cs=1 > s=0 拒绝，且拒绝不改任何状态。
func TestSnapshotTooOld(t *testing.T) {
	c := mustNew(t, 1, 2)
	t1 := c.Begin() // s=0
	t4 := c.Begin() // s=0
	if v := mustRead(t, c, t4, 0); v != 0 {
		t.Fatalf("T4 读键0 = %d，期望 0", v)
	}

	t2 := c.Begin() // s=0
	mustWrite(t, c, t2, 0, 1)
	if got := mustCommit(t, c, t2); got != 1 {
		t.Fatalf("T2 提交号 = %d，期望 1", got)
	}
	t3 := c.Begin() // s=1
	mustWrite(t, c, t3, 0, 2)
	if got := mustCommit(t, c, t3); got != 2 {
		t.Fatalf("T3 提交号 = %d，期望 2", got)
	}
	// 链已截为 cs=1、2 两个版本。
	before := c.Dump()
	if got := len(before.Chains[0]); got != 2 {
		t.Fatalf("键0 链长 = %d，期望 2", got)
	}

	// T1（s=0）此时 Read(0)：最旧版本 cs=1 > s=0，快照过旧。
	_, err := c.Read(t1, 0)
	expectReason(t, "T1 读键0（快照过旧）", err, ReasonSnapshotTooOld)
	if got := c.Dump(); !reflect.DeepEqual(got, before) {
		t.Fatalf("快照过旧拒绝后状态被改变:\n got=%+v\nwant=%+v", got, before)
	}

	// 被拒绝的读不结束事务：T1 仍活跃，可写、可中止。
	mustWrite(t, c, t1, 0, 9)
	if v := mustRead(t, c, t1, 0); v != 9 {
		t.Fatalf("T1 读缓冲值 = %d，期望 9（写过则返回缓冲值，不判快照过旧）", v)
	}
	if err := c.Abort(t1); err != nil {
		t.Fatalf("T1 中止失败: %v", err)
	}

	// T4 此前已读过键0：即使 v0 已被回收，仍返回首次读到的值，不判快照过旧。
	if v := mustRead(t, c, t4, 0); v != 0 {
		t.Fatalf("T4 再读键0 = %d，期望 0（首次读到的值）", v)
	}
	t.Logf("快照过旧拒绝不改状态；缓冲值与首次读到的值均不触发快照检查")
}

// TestRejectionPrecedence 验证 Read/Write 的拒绝原因按序只报第一个，
// Commit 与 Abort 只检查前两项。
func TestRejectionPrecedence(t *testing.T) {
	c := mustNew(t, 2, 2)
	tx := c.Begin()

	// 事务号不存在优先于键越界。
	_, err := c.Read(999, -1)
	expectReason(t, "Read(未知事务, 坏键)", err, ReasonUnknownTx)
	_, err = c.Read(0, 5)
	expectReason(t, "Read(事务0, 坏键)", err, ReasonUnknownTx)
	expectReason(t, "Write(未知事务, 坏键)", c.Write(999, -1, 1), ReasonUnknownTx)
	_, err = c.Commit(999)
	expectReason(t, "Commit(未知事务)", err, ReasonUnknownTx)
	expectReason(t, "Abort(未知事务)", c.Abort(999), ReasonUnknownTx)

	// 活跃事务 + 键越界。
	_, err = c.Read(tx, -1)
	expectReason(t, "Read(活跃, 键-1)", err, ReasonKeyOutOfRange)
	_, err = c.Read(tx, 2)
	expectReason(t, "Read(活跃, 键2)", err, ReasonKeyOutOfRange)
	expectReason(t, "Write(活跃, 键7)", c.Write(tx, 7, 1), ReasonKeyOutOfRange)

	// 事务已结束优先于键越界；Commit/Abort 不检查键。
	if err := c.Abort(tx); err != nil {
		t.Fatalf("Abort(活跃) 失败: %v", err)
	}
	_, err = c.Read(tx, -1)
	expectReason(t, "Read(已中止, 坏键)", err, ReasonTxFinished)
	_, err = c.Read(tx, 0)
	expectReason(t, "Read(已中止)", err, ReasonTxFinished)
	expectReason(t, "Write(已中止)", c.Write(tx, 0, 1), ReasonTxFinished)
	_, err = c.Commit(tx)
	expectReason(t, "Commit(已中止)", err, ReasonTxFinished)
	expectReason(t, "Abort(已中止)", c.Abort(tx), ReasonTxFinished)

	// 已提交事务同样视为已结束。
	t2 := c.Begin()
	if _, err := c.Commit(t2); err != nil {
		t.Fatalf("Commit(活跃) 失败: %v", err)
	}
	_, err = c.Commit(t2)
	expectReason(t, "Commit(已提交)", err, ReasonTxFinished)
	expectReason(t, "Abort(已提交)", c.Abort(t2), ReasonTxFinished)
	_, err = c.Read(t2, 0)
	expectReason(t, "Read(已提交)", err, ReasonTxFinished)
}

// TestInvalidConfig 验证 K、H 越界以配置非法整体拒绝。
func TestInvalidConfig(t *testing.T) {
	bad := [][2]int{{0, 2}, {-1, 2}, {65, 2}, {100, 2}, {1, 1}, {1, 0}, {1, 9}, {2, -1}, {0, 0}, {65, 9}}
	for _, kh := range bad {
		c, err := New(kh[0], kh[1])
		if c != nil {
			t.Fatalf("New(%d, %d) 应整体拒绝，却返回认证器", kh[0], kh[1])
		}
		expectReason(t, "New 越界", err, ReasonInvalidConfig)
	}
	good := [][2]int{{1, 2}, {1, 8}, {64, 2}, {64, 8}, {32, 5}}
	for _, kh := range good {
		if _, err := New(kh[0], kh[1]); err != nil {
			t.Fatalf("New(%d, %d) 应成功: %v", kh[0], kh[1], err)
		}
	}
}

// TestReadYourWritesFirstReadWins 验证读己之写与首次读到的值优先。
func TestReadYourWritesFirstReadWins(t *testing.T) {
	c := mustNew(t, 1, 2)
	t1 := c.Begin()
	mustWrite(t, c, t1, 0, 42)
	if v := mustRead(t, c, t1, 0); v != 42 {
		t.Fatalf("写后读 = %d，期望缓冲值 42", v)
	}

	t2 := c.Begin()
	if v := mustRead(t, c, t2, 0); v != 0 {
		t.Fatalf("T2 首读 = %d，期望 0", v)
	}
	mustWrite(t, c, t2, 0, 7)
	if v := mustRead(t, c, t2, 0); v != 7 {
		t.Fatalf("T2 写后读 = %d，期望缓冲值 7", v)
	}

	if got := mustCommit(t, c, t1); got != 1 {
		t.Fatalf("T1 提交号 = %d，期望 1", got)
	}

	// 首次读到的值：t3 读到 42 后，t5 提交新值 100，t3 再读仍是 42。
	t3 := c.Begin() // s=1
	if v := mustRead(t, c, t3, 0); v != 42 {
		t.Fatalf("T3 首读 = %d，期望 42", v)
	}
	t5 := c.Begin()
	mustWrite(t, c, t5, 0, 100)
	if got := mustCommit(t, c, t5); got != 2 {
		t.Fatalf("T5 提交号 = %d，期望 2", got)
	}
	if v := mustRead(t, c, t3, 0); v != 42 {
		t.Fatalf("T3 再读 = %d，期望首次读到的 42", v)
	}
	t.Logf("读己之写返回缓冲值；重复读返回首次读到的值")
}

// TestCommitValidationAccessBound 验证 Commit 认证阶段访问的版本数
// （非导出计数器 validateAccesses）不超过读集与写集大小之和。
func TestCommitValidationAccessBound(t *testing.T) {
	c := mustNew(t, 2, 2)
	t1 := c.Begin()
	mustRead(t, c, t1, 0)
	mustRead(t, c, t1, 1)
	mustWrite(t, c, t1, 0, 1)
	mustWrite(t, c, t1, 1, 2)
	mustCommit(t, c, t1)
	if got, want := c.validateAccesses, 4; got > want {
		t.Fatalf("认证访问版本数 = %d，超过读集+写集 = %d", got, want)
	}

	t2 := c.Begin()
	mustRead(t, c, t2, 0)
	mustWrite(t, c, t2, 1, 5)
	mustCommit(t, c, t2)
	if got, want := c.validateAccesses, 2; got > want {
		t.Fatalf("认证访问版本数 = %d，超过读集+写集 = %d", got, want)
	}
	t.Logf("认证阶段版本访问数不超过读集与写集大小之和")
}
