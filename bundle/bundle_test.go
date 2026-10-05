package bundle

import (
	"errors"
	"fmt"
	"testing"

	"ontology/flowtable"
	"ontology/match"
)

func setup(t *testing.T) (*flowtable.Table, *Manager) {
	t.Helper()
	ft, err := flowtable.New(4, true)
	if err != nil {
		t.Fatal(err)
	}
	return ft, NewManager(ft)
}

// TestCommitVisibilityAndRollback 批内前后可见；整批回滚不留痕。
func TestCommitVisibilityAndRollback(t *testing.T) {
	ft, mgr := setup(t)
	mA := match.Must(0x0A000000, 0xFF000000, 0, 0)
	seqA, _, err := ft.Add(mA, 10, false, 1, 1, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	// 顺序一：先 Delete A，再带检查 Add 相同匹配——可通过。
	id := mgr.Begin()
	if err := mgr.Append(id, NewDeleteMessage(mA, 10, true)); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Append(id, NewAddMessage(mA, 10, true, 2, 1, 0, 0)); err != nil {
		t.Fatal(err)
	}
	evs, err := mgr.Commit(id, 10)
	if err != nil {
		t.Fatalf("Delete 后再带检查 Add 应通过: %v", err)
	}
	if len(evs) != 1 || evs[0].Reason != flowtable.Delete || evs[0].Seq != seqA {
		t.Fatalf("事件=%v，期望 A 的 Delete 事件", evs)
	}
	if _, err := mgr.Commit(id, 20); !errors.Is(err, ErrBundleNotFound) {
		t.Fatalf("提交成功后批应销毁: %v", err)
	}
	// 顺序二（颠倒）：带检查 Add 与现存相同匹配冲突，整批拒绝、报下标 0 与重叠。
	id2 := mgr.Begin()
	if err := mgr.Append(id2, NewAddMessage(mA, 10, true, 3, 1, 0, 0)); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Append(id2, NewDeleteMessage(mA, 10, true)); err != nil {
		t.Fatal(err)
	}
	nowBefore := ft.Now()
	lenBefore := ft.Len()
	_, err = mgr.Commit(id2, 20)
	var rej *RejectError
	if !errors.As(err, &rej) || rej.Index != 0 || !errors.Is(err, flowtable.ErrOverlap) {
		t.Fatalf("应报下标 0 与重叠: err=%v", err)
	}
	// 回滚不留痕：时钟、表项数不变；再装新项的序号应紧接上一成功安装。
	if ft.Now() != nowBefore || ft.Len() != lenBefore {
		t.Fatalf("回滚留痕: Now=%d→%d Len=%d→%d", nowBefore, ft.Now(), lenBefore, ft.Len())
	}
	seq, _, err := ft.Add(match.Must(0x0B000000, 0xFF000000, 0, 0), 10, false, 9, 1, 0, 0, 30)
	if err != nil {
		t.Fatal(err)
	}
	if seq != seqA+2 { // seqA 被删后批内重装占了 seqA+1
		t.Fatalf("序号=%d，期望 %d（失败的批不占号）", seq, seqA+2)
	}
	// 批保持打开：改为先删后装再次提交成功。
	id3 := mgr.Begin()
	if err := mgr.Append(id3, NewDeleteMessage(mA, 10, true)); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Append(id3, NewAddMessage(mA, 10, true, 3, 1, 0, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Commit(id3, 40); err != nil {
		t.Fatalf("修正后应提交成功: %v", err)
	}
	t.Logf("颠倒次序拒绝: %v；回滚后序号连续、状态不变", rej)
}

// TestCommitAtomicExpiry 提交先落地到期，且失败时到期也不落地。
func TestCommitAtomicExpiry(t *testing.T) {
	ft, mgr := setup(t)
	mX := match.Must(0x01000000, 0xFF000000, 0, 0)
	mY := match.Must(0x02000000, 0xFF000000, 0, 0)
	sx, _, _ := ft.Add(mX, 1, false, 1, 1, 50, 0, 0) // t=50 Idle 到期
	if _, _, err := ft.Add(mY, 1, false, 2, 1, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	// 空批提交：只落地到期。
	id := mgr.Begin()
	evs, err := mgr.Commit(id, 60)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || evs[0].Seq != sx || evs[0].Reason != flowtable.Idle || evs[0].Time != 50 {
		t.Fatalf("空批应只落地到期事件: %v", evs)
	}
	// 失败批不落地到期：重装带 idle 的 X，再提交一个会失败的批。
	sx2, _, _ := ft.Add(mX, 1, false, 1, 1, 40, 0, 60) // t=100 Idle 到期
	id2 := mgr.Begin()
	if err := mgr.Append(id2, NewAddMessage(mY, 1, true, 3, 1, 0, 0)); err != nil { // 与 Y 重叠
		t.Fatal(err)
	}
	if _, err := mgr.Commit(id2, 200); !errors.Is(err, flowtable.ErrOverlap) {
		t.Fatalf("期望重叠拒绝: %v", err)
	}
	if ft.Len() != 2 || ft.Now() != 60 {
		t.Fatalf("失败批不应落地到期/推进时钟: Len=%d Now=%d", ft.Len(), ft.Now())
	}
	// 被接受的 Advance 才落地 X 的到期。
	evs, _ = ft.Advance(200)
	if len(evs) != 1 || evs[0].Seq != sx2 || evs[0].Reason != flowtable.Idle || evs[0].Time != 100 {
		t.Fatalf("到期事件=%v，期望 X(seq=%d,Idle,t=100)", evs, sx2)
	}
	t.Logf("空批落地到期 %v；失败批不落地", evs)
}

// TestBundleProtocol 批号不存在、Discard、条数上限与消息参数非法。
func TestBundleProtocol(t *testing.T) {
	ft, mgr := setup(t)
	m := match.Must(0, 0, 0, 0)
	if err := mgr.Append(999, NewAddMessage(m, 1, false, 1, 1, 0, 0)); !errors.Is(err, ErrBundleNotFound) {
		t.Errorf("Append 未知批: %v", err)
	}
	if err := mgr.Discard(999); !errors.Is(err, ErrBundleNotFound) {
		t.Errorf("Discard 未知批: %v", err)
	}
	if _, err := mgr.Commit(999, 0); !errors.Is(err, ErrBundleNotFound) {
		t.Errorf("Commit 未知批: %v", err)
	}
	// 消息参数非法。
	id := mgr.Begin()
	bad := match.Match{F: [2]match.Field{{Value: 1, Mask: 0}, {}}}
	if err := mgr.Append(id, NewAddMessage(bad, 1, false, 1, 1, 0, 0)); !errors.Is(err, flowtable.ErrInvalidParam) {
		t.Errorf("非法匹配: %v", err)
	}
	if err := mgr.Append(id, NewAddMessage(m, 1, false, 1, 1, 1_000_000_001, 0)); !errors.Is(err, flowtable.ErrInvalidParam) {
		t.Errorf("非法 idle: %v", err)
	}
	if err := mgr.Append(id, NewAddMessage(m, 65536, false, 1, 1, 0, 0)); !errors.Is(err, flowtable.ErrInvalidParam) {
		t.Errorf("非法 prio: %v", err)
	}
	if err := mgr.Append(id, Message{Kind: Kind(99), Match: m}); !errors.Is(err, flowtable.ErrInvalidParam) {
		t.Errorf("非法类型: %v", err)
	}
	// 至多 256 条。
	for i := 0; i < MaxMessages; i++ {
		if err := mgr.Append(id, NewModifyMessage(m, 0, false, uint32(i))); err != nil {
			t.Fatalf("第 %d 条: %v", i, err)
		}
	}
	if err := mgr.Append(id, NewModifyMessage(m, 0, false, 0)); !errors.Is(err, flowtable.ErrInvalidParam) {
		t.Errorf("第 257 条应报参数非法: %v", err)
	}
	// Discard 后批不存在。
	if err := mgr.Discard(id); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Discard(id); !errors.Is(err, ErrBundleNotFound) {
		t.Errorf("重复 Discard: %v", err)
	}
	if ft.Len() != 0 {
		t.Fatalf("未提交的批不应生效: Len=%d", ft.Len())
	}
}

// TestCommitRejectionOrder 批提交拒绝次序：参数非法 > 时钟回退 > 批不存在。
func TestCommitRejectionOrder(t *testing.T) {
	ft, mgr := setup(t)
	m := match.Must(0, 0, 0, 0)
	if _, _, err := ft.Add(m, 1, false, 1, 1, 0, 0, 100); err != nil {
		t.Fatal(err)
	}
	// 参数非法（now 超界）优先于时钟回退与批不存在。
	if _, err := mgr.Commit(999, 1_000_000_000_001); !errors.Is(err, flowtable.ErrInvalidParam) {
		t.Errorf("非法 now: %v", err)
	}
	// 时钟回退优先于批不存在。
	if _, err := mgr.Commit(999, 50); !errors.Is(err, flowtable.ErrClock) {
		t.Errorf("时钟回退: %v", err)
	}
	// 批不存在。
	if _, err := mgr.Commit(999, 200); !errors.Is(err, ErrBundleNotFound) {
		t.Errorf("批不存在: %v", err)
	}
	// 批内重叠与表满：表满批中报下标与原因。
	ft2, _ := flowtable.New(1, false)
	mgr2 := NewManager(ft2)
	mA := match.Must(0x0A000000, 0xFF000000, 0, 0)
	mB := match.Must(0x0B000000, 0xFF000000, 0, 0)
	mC := match.Must(0x0C000000, 0xFF000000, 0, 0)
	id := mgr2.Begin()
	if err := mgr2.Append(id, NewAddMessage(mA, 1, false, 1, 1, 0, 0)); err != nil {
		t.Fatal(err)
	}
	if err := mgr2.Append(id, NewAddMessage(mB, 1, false, 1, 1, 0, 0)); err != nil { // 表满
		t.Fatal(err)
	}
	if err := mgr2.Append(id, NewAddMessage(mC, 1, true, 1, 1, 0, 0)); err != nil {
		t.Fatal(err)
	}
	_, err := mgr2.Commit(id, 0)
	var rej *RejectError
	if !errors.As(err, &rej) || rej.Index != 1 || !errors.Is(err, flowtable.ErrTableFull) {
		t.Fatalf("应报下标 1 与表满: %v", err)
	}
	if ft2.Len() != 0 {
		t.Fatalf("整批回滚，表应为空: Len=%d", ft2.Len())
	}
	t.Logf("批内表满: %v", rej)
}

// TestCommitModifyDelete 批内 Modify/Delete 生效且后面消息可见。
func TestCommitModifyDelete(t *testing.T) {
	ft, mgr := setup(t)
	mA := match.Must(0x0A000000, 0xFF000000, 0, 0)
	mB := match.Must(0x0A010000, 0xFFFF0000, 0, 0)
	if _, _, err := ft.Add(mA, 10, false, 1, 1, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ft.Add(mB, 10, false, 2, 1, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	id := mgr.Begin()
	// 非严格 Modify(A) 作用于 A 与 B；随后严格 Delete B 应作用 1 项。
	if err := mgr.Append(id, NewModifyMessage(mA, 0, false, 77)); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Append(id, NewDeleteMessage(mB, 10, true)); err != nil {
		t.Fatal(err)
	}
	evs, err := mgr.Commit(id, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || evs[0].Reason != flowtable.Delete {
		t.Fatalf("事件=%v，期望 B 的 Delete", evs)
	}
	action, _, hit, _, _ := ft.Lookup(match.Packet{0x0A000000, 0}, 1, 6)
	if !hit || action != 77 {
		t.Fatalf("A 动作应为 77: action=%d hit=%v", action, hit)
	}
	if ft.Len() != 1 {
		t.Fatalf("B 应被删除: Len=%d", ft.Len())
	}
}

// Example 演示批量提交。
func Example() {
	ft, _ := flowtable.New(2, false)
	mgr := NewManager(ft)
	m := match.Must(0x0A000000, 0xFF000000, 0, 0)
	id := mgr.Begin()
	_ = mgr.Append(id, NewAddMessage(m, 10, false, 1, 1, 0, 0))
	_ = mgr.Append(id, NewModifyMessage(m, 10, true, 2))
	if _, err := mgr.Commit(id, 0); err != nil {
		fmt.Println(err)
	}
	action, _, hit, _, _ := ft.Lookup(match.Packet{0x0A000000, 0}, 1, 1)
	fmt.Println(action, hit)
	// Output: 2 true
}
