package numalock

import (
	"bytes"
	"fmt"
	"testing"
)

// runScript replays calls, logging input/output/decision basis and checking
// invariants after every step.
func runScript(t *testing.T, l *Lock, calls []call) *bytes.Buffer {
	t.Helper()
	var log bytes.Buffer
	for i, c := range calls {
		r := applyReal(l, c)
		fmt.Fprintf(&log, "#%02d %-10s -> ok=%-5v granted=%-6v | %s\n",
			i, c, r.OK, fmt.Sprint(r.Granted), r.Reason)
		if err := checkInvariants(l, l.Snapshot()); err != nil {
			t.Fatalf("脚本第 %d 步 %s 后不变量破坏: %v\n%s", i, c, err, log.String())
		}
	}
	return &log
}

// 1. 读相位中到达的读者, 因有写者等待而排队
func TestScenarioReaderWaitsWhenWriterQueued(t *testing.T) {
	l, _ := New(2, 4, []int{0, 0, 1, 1}, 2)
	calls := []call{
		{"RLock", 0}, // 授予读
		{"WLock", 2}, // 写等待, G=[1]
		{"RLock", 1}, // 读相位但有写者等待 -> Qr
		{"RLock", 3}, // 同样 -> Qr
	}
	log := runScript(t, l, calls)
	s := l.Snapshot()
	if !eqInts(s.Qr, []int{1, 3}) {
		t.Fatalf("Qr=%v 期望 [1 3]\n%s", s.Qr, log.String())
	}
	if eqInts(s.R, []int{0}) == false {
		t.Fatalf("R=%v 期望 [0]", s.R)
	}
	// 写者全部撤销 -> 排队读者按序授予
	r := l.Cancel(2)
	mustGranted(t, r, []int{1, 3}, "撤销唯一写等待者")
	s = l.Snapshot()
	if !eqInts(sortedCopy(s.R), []int{0, 1, 3}) || len(s.Qr) != 0 || len(s.G) != 0 {
		t.Fatalf("撤销后 R=%v Qr=%v G=%v", s.R, s.Qr, s.G)
	}
	t.Logf("\n场景1 读者因写者等待而排队 + 撤销授予:\n%s", log.String())
}

// 2. 传递计数 p 恰等于 B 与小于 B
func TestScenarioHandoffBoundExact(t *testing.T) {
	// B=2: 节点0 三个写者, 另有节点1 写者占据 G
	l, _ := New(2, 5, []int{0, 0, 0, 0, 1}, 2)
	calls := []call{
		{"WLock", 0}, // w=0 gown=0 p=0
		{"WLock", 1}, // Qw[0]=[1]
		{"WLock", 2}, // Qw[0]=[1,2]
		{"WLock", 3}, // Qw[0]=[1,2,3]
		{"WLock", 4}, // Qw[1]=[4], G=[1]
	}
	log := runScript(t, l, calls)

	mustGranted(t, l.WUnlock(0), []int{1}, "p 0<2 本地传递")
	if s := l.Snapshot(); s.P != 1 {
		t.Fatalf("p=%d 期望 1", s.P)
	}
	mustGranted(t, l.WUnlock(1), []int{2}, "p 1<2 本地传递")
	s := l.Snapshot()
	if s.P != 2 || s.W != 2 {
		t.Fatalf("p=%d w=%d 期望 p=2,w=2", s.P, s.W)
	}
	// p 恰等于 B -> gown 重入 G 尾(在节点1之后), 起写节点1
	r := l.WUnlock(2)
	mustGranted(t, r, []int{4}, "p==B 跨节点")
	s = l.Snapshot()
	if !eqInts(s.G, []int{0}) || s.Gown != 1 || s.P != 0 {
		t.Fatalf("跨节点后 G=%v gown=%d p=%d", s.G, s.Gown, s.P)
	}
	if !eqInts(s.Qw[0], []int{3}) {
		t.Fatalf("Qw[0]=%v 期望 [3]", s.Qw[0])
	}
	t.Logf("\n场景2 p<B 传递与 p==B 阻止传递:\n%s", log.String())
}

// 3. G 空时传递不受 B 限制: 全部写者同一节点
func TestScenarioHandoffUnboundedWhenGEmpty(t *testing.T) {
	l, _ := New(1, 4, []int{0, 0, 0, 0}, 1)
	calls := []call{
		{"WLock", 0},
		{"WLock", 1},
		{"WLock", 2},
		{"WLock", 3},
	}
	log := runScript(t, l, calls)
	for i, want := range []int{1, 2, 3} {
		r := l.WUnlock(want - 1)
		mustGranted(t, r, []int{want}, fmt.Sprintf("G空第%d次传递", i+1))
		if s := l.Snapshot(); s.P != i+1 {
			t.Fatalf("G 空时 p=%d 期望 %d (可超过 B=1)", s.P, i+1)
		}
	}
	s := l.Snapshot()
	if s.P != 3 || s.W != 3 {
		t.Fatalf("p=%d w=%d", s.P, s.W)
	}
	r := l.WUnlock(3)
	if r.OK && len(r.Granted) != 0 {
		t.Fatalf("最后一个写者解锁应空闲, granted=%v", r.Granted)
	}
	t.Logf("\n场景3 G 空本地传递不受 B 限制:\n%s", log.String())
}

// 4. 写阶段结束时读者先于本地传递
func TestScenarioReadersBeforeLocalHandoff(t *testing.T) {
	l, _ := New(2, 4, []int{0, 0, 1, 1}, 4)
	calls := []call{
		{"WLock", 0}, // gown=0
		{"WLock", 1}, // Qw[0]=[1] 本地
		{"RLock", 2}, // Qr=[2]
		{"RLock", 3}, // Qr=[2,3]
	}
	log := runScript(t, l, calls)
	// 即便 p=0<B=4, Qr 非空也必须先授予读者
	r := l.WUnlock(0)
	mustGranted(t, r, []int{2, 3}, "读者先于本地传递")
	s := l.Snapshot()
	if s.Phase != PhaseRead || !eqInts(sortedCopy(s.R), []int{2, 3}) {
		t.Fatalf("应读相位 R=[2 3], got phase=%s R=%v", s.Phase, s.R)
	}
	if !eqInts(s.G, []int{0}) {
		t.Fatalf("gown 应重入 G: G=%v", s.G)
	}
	if !eqInts(s.Qw[0], []int{1}) {
		t.Fatalf("本地写者仍应排队 Qw[0]=%v", s.Qw[0])
	}
	t.Logf("\n场景4 读者先于本地传递:\n%s", log.String())
}

// 5. 写组重入 G 尾的位置
func TestScenarioGownRequeuePosition(t *testing.T) {
	// B=2; node0: 0,3,6,7; node1: 1,4; node2: 2,5
	l, _ := New(3, 8, []int{0, 1, 2, 0, 1, 2, 0, 0}, 2)
	calls := []call{
		{"WLock", 0},   // gown=0
		{"WLock", 1},   // G=[1]
		{"WLock", 2},   // G=[1,2]
		{"WLock", 3},   // Qw[0]=[3] gown 不入 G
		{"WLock", 4},   // 节点1 已在 G
		{"WLock", 5},   // 节点2 已在 G
		{"WLock", 6},   // Qw[0]=[3,6]
		{"WLock", 7},   // Qw[0]=[3,6,7]
		{"WUnlock", 0}, // p=0<2 本地传给 3, p=1
		{"WUnlock", 3}, // p=1<2 本地传给 6, p=2
	}
	log := runScript(t, l, calls)
	s := l.Snapshot()
	if s.W != 6 || s.P != 2 || !eqInts(s.G, []int{1, 2}) {
		t.Fatalf("两次本地传递后 w=%d p=%d G=%v", s.W, s.P, s.G)
	}
	if !eqInts(s.Qw[0], []int{7}) {
		t.Fatalf("Qw[0]=%v 期望 [7]", s.Qw[0])
	}
	// p==B 且 Qw[gown] 仍非空: gown(0) 追加到 G 尾 -> [1,2,0]; 起写节点1
	r := l.WUnlock(6)
	mustGranted(t, r, []int{1}, "起写节点1")
	s = l.Snapshot()
	if !eqInts(s.G, []int{2, 0}) || s.Gown != 1 {
		t.Fatalf("gown 重入位置错: G=%v gown=%d (期望 [2 0])", s.G, s.Gown)
	}
	if !eqInts(s.Qw[0], []int{7}) {
		t.Fatalf("Qw[0]=%v 期望 [7] 保留", s.Qw[0])
	}
	t.Logf("\n场景5 gown 重入 G 尾位置:\n%s", log.String())
}

// 6. 降级一并授予 Qr
func TestScenarioDowngradeGrantsQr(t *testing.T) {
	l, _ := New(2, 4, []int{0, 0, 1, 1}, 2)
	calls := []call{
		{"WLock", 0}, // gown=0
		{"WLock", 1}, // Qw[0]=[1]
		{"RLock", 2}, // Qr=[2]
		{"RLock", 3}, // Qr=[2,3]
	}
	log := runScript(t, l, calls)
	r := l.Downgrade(0)
	mustGranted(t, r, []int{0, 2, 3}, "降级授予 [t]+Qr")
	s := l.Snapshot()
	if s.Phase != PhaseRead || s.W != -1 || len(s.Qr) != 0 {
		t.Fatalf("降级后 phase=%s w=%d Qr=%v", s.Phase, s.W, s.Qr)
	}
	if !eqInts(sortedCopy(s.R), []int{0, 2, 3}) {
		t.Fatalf("R=%v 期望 {0,2,3}", s.R)
	}
	if !eqInts(s.G, []int{0}) {
		t.Fatalf("gown 应重入 G: G=%v", s.G)
	}
	// 降级者可以正常 RUnlock
	if r := l.RUnlock(0); !r.OK {
		t.Fatalf("降级者 RUnlock 失败: %s", r.Reason)
	}
	t.Logf("\n场景6 降级授予 Qr:\n%s", log.String())
}

// 7. 升级仅在唯一读者时成功, 且从 G 移出本节点
func TestScenarioUpgradeSoleReader(t *testing.T) {
	l, _ := New(2, 3, []int{0, 1, 0}, 2)
	calls := []call{
		{"WLock", 1},     // 节点1 持写
		{"WLock", 2},     // Qw[0]=[2] G=[0]
		{"Downgrade", 1}, // 节点1 降级, 无 Qr; G=[0]
	}
	log := runScript(t, l, calls)
	s := l.Snapshot()
	if !eqInts(s.G, []int{0}) || !eqInts(sortedCopy(s.R), []int{1}) {
		t.Fatalf("降级后 G=%v R=%v", s.G, s.R)
	}
	// 唯一读者升级: 其节点是 1, 不在 G; 验证移出 G 的情形需另构场景
	r := l.Upgrade(1)
	mustGranted(t, r, []int{1}, "唯一读者升级")
	s = l.Snapshot()
	if s.Phase != PhaseWrite || s.W != 1 || s.Gown != 1 {
		t.Fatalf("升级后 phase=%s w=%d gown=%d", s.Phase, s.W, s.Gown)
	}
	t.Logf("\n场景7a 唯一读者升级:\n%s", log.String())

	// 7b. 升级者节点在 G 中 -> 移出 G, Qw 保留为本地队列
	l2, _ := New(2, 3, []int{0, 1, 0}, 2)
	calls2 := []call{
		{"WLock", 1},     // gown=1
		{"WLock", 2},     // Qw[0]=[2] G=[0]
		{"WUnlock", 1},   // 起写节点0: w=2, gown=0, G=[]
		{"Downgrade", 2}, // 节点0 降级; Qw[0] 空 -> G 仍空
		{"WLock", 0},     // 同节点(=gown0)写等待: gown 不入 G ... 先制造 G 成员
	}
	_ = calls2
	// 直接精细构造: 降级后让另一线程以节点0入 Qw(不入G), 再让节点1入队使 G=[1]
	mustGranted(t, l2.WLock(1), []int{1}, "2: WLock 1")
	mustGranted(t, l2.WUnlock(1), nil, "2: 空闲")
	mustGranted(t, l2.RLock(0), []int{0}, "2: RLock 0 唯一读者")
	if r := l2.WLock(2); !r.OK { // Qw[0]=[2], 节点0入 G=[0]
		t.Fatalf("WLock 2: %s", r.Reason)
	}
	if r := l2.WLock(1); !r.OK { // Qw[1]=[1], G=[0,1]
		t.Fatalf("WLock 1: %s", r.Reason)
	}
	s2 := l2.Snapshot()
	if !eqInts(s2.G, []int{0, 1}) {
		t.Fatalf("G=%v 期望 [0 1]", s2.G)
	}
	r = l2.Upgrade(0)
	mustGranted(t, r, []int{0}, "2: 唯一读者升级")
	s2 = l2.Snapshot()
	if s2.Gown != 0 || !eqInts(s2.G, []int{1}) {
		t.Fatalf("升级后 gown=%d G=%v (节点0应移出 G)", s2.Gown, s2.G)
	}
	if !eqInts(s2.Qw[0], []int{2}) {
		t.Fatalf("Qw[0]=%v 应保留为本地队列 [2]", s2.Qw[0])
	}
	// 本地传递给 2, p=1
	r = l2.WUnlock(0)
	mustGranted(t, r, []int{2}, "2: 本地传给同节点 2")
	s2 = l2.Snapshot()
	if s2.Gown != 0 || s2.P != 1 {
		t.Fatalf("本地传递后 gown=%d p=%d", s2.Gown, s2.P)
	}
}

// 8. 撤销最后一个写等待者使排队读者被授予 (读相位中)
func TestScenarioCancelLastWriterGrantsReaders(t *testing.T) {
	l, _ := New(2, 4, []int{0, 0, 1, 1}, 2)
	calls := []call{
		{"RLock", 0}, // 读持有
		{"WLock", 2}, // 写等待 G=[1]
		{"RLock", 1}, // Qr=[1]
		{"RLock", 3}, // Qr=[1,3]
	}
	log := runScript(t, l, calls)
	// 先撤一个读等待不影响
	if r := l.Cancel(3); !r.OK || len(r.Granted) != 0 {
		t.Fatalf("撤销读等待 3: %+v", r)
	}
	// 撤掉唯一写等待节点 -> 读相位无写者 -> Qr 全体授予
	r := l.Cancel(2)
	mustGranted(t, r, []int{1}, "撤销最后写者授予剩余读者")
	s := l.Snapshot()
	if !eqInts(sortedCopy(s.R), []int{0, 1}) || len(s.Qr) != 0 || len(s.G) != 0 {
		t.Fatalf("撤销后 R=%v Qr=%v G=%v", s.R, s.Qr, s.G)
	}
	t.Logf("\n场景8 撤销最后写者授予排队读者:\n%s", log.String())
}

// 9. 撤销使节点移出 G, 之后该节点重新入 G 排在尾
func TestScenarioCancelNodeLeavesThenRequeuesAtTail(t *testing.T) {
	// 写相位中 gown=1; 节点0 是非 gown 的排队节点
	l, _ := New(3, 5, []int{0, 1, 0, 2, 0}, 2)
	calls := []call{
		{"WLock", 1}, // gown=1
		{"WLock", 2}, // Qw[0]=[2] G=[0]
		{"WLock", 3}, // Qw[2]=[3] G=[0,2]
		{"WLock", 4}, // Qw[0]=[2,4], 节点0 已在 G
	}
	log := runScript(t, l, calls)
	if s := l.Snapshot(); !eqInts(s.G, []int{0, 2}) ||
		!eqInts(s.Qw[0], []int{2, 4}) {
		t.Fatalf("排队状态 G=%v Qw[0]=%v", l.Snapshot().G, l.Snapshot().Qw[0])
	}
	// 撤掉节点0 全部写等待者(2,4) -> 节点0 移出 G
	if r := l.Cancel(2); !r.OK {
		t.Fatalf("cancel 2: %s", r.Reason)
	}
	if r := l.Cancel(4); !r.OK || !eqInts(l.Snapshot().G, []int{2}) {
		t.Fatalf("节点0 应移出 G: G=%v (%s)", l.Snapshot().G, r.Reason)
	}
	// 节点0 重新有写等待者 -> 追加到 G 尾 [2,0]
	if r := l.WLock(2); !r.OK {
		t.Fatalf("重新 WLock 2: %s", r.Reason)
	}
	s := l.Snapshot()
	if !eqInts(s.G, []int{2, 0}) || !eqInts(s.Qw[0], []int{2}) {
		t.Fatalf("重新入 G 应在尾: G=%v Qw[0]=%v", s.G, s.Qw[0])
	}
	t.Logf("\n场景9 节点移出 G 后重新排尾:\n%s", log.String())
}

// 10. 升级冲突 + 撤销非最后写者不授予读者
func TestScenarioUpgradeConflictAndNonLastCancel(t *testing.T) {
	l, _ := New(2, 4, []int{0, 0, 1, 1}, 2)
	calls := []call{
		{"WLock", 0},
		{"WLock", 2},     // G=[1]
		{"WUnlock", 0},   // 起写节点1: w=2
		{"Downgrade", 2}, // 节点1 降级(无本地等待) G=[]
		{"RLock", 1},     // 无写者等待, 随读批进入
		{"WLock", 3},     // G=[1] 写等待
		{"RLock", 0},     // Qr=[0]
	}
	log := runScript(t, l, calls)
	s := l.Snapshot()
	if !eqInts(sortedCopy(s.R), []int{1, 2}) || !eqInts(s.Qr, []int{0}) {
		t.Fatalf("R=%v Qr=%v", s.R, s.Qr)
	}
	// R={1,2}, 两者升级都应冲突
	mustReject(t, l.Upgrade(1), "多读者升级1")
	mustReject(t, l.Upgrade(2), "多读者升级2")
	// 读者1 离开后, 读者2 仍非唯一? 只剩 2 -> 但还有 Qr 等待者 0;
	// 升级要求 R 恰为 {t}, 与 Qr 无关: 2 可以升级吗?
	// 规则原文: R 恰为 {t} 即成功; Qr 中的 0 在升级后继续等待(写相位).
	if r := l.RUnlock(1); !r.OK {
		t.Fatalf("RUnlock 1: %s", r.Reason)
	}
	r := l.Upgrade(2)
	mustGranted(t, r, []int{2}, "唯一读者2 升级, Qr 继续等待")
	s = l.Snapshot()
	if s.Phase != PhaseWrite || !eqInts(s.Qr, []int{0}) || s.W != 2 {
		t.Fatalf("升级后 phase=%s Qr=%v w=%d", s.Phase, s.Qr, s.W)
	}
	t.Logf("\n场景10 升级冲突与唯一读者升级后 Qr 保留:\n%s", log.String())
}
