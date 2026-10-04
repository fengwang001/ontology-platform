package bundle

import (
	"errors"
	"testing"

	"ontology/flowtable"
	"ontology/match"
)

func mustMatch(t *testing.T, v, m uint32) match.Match {
	t.Helper()
	mt, err := match.New(match.Field{Value: v, Mask: m}, match.Field{})
	if err != nil {
		t.Fatal(err)
	}
	return mt
}

func addOp(mt match.Match, prio uint16, check bool, action string, imp uint16) flowtable.Op {
	return flowtable.Op{Kind: flowtable.OpAdd, Add: flowtable.AddMsg{
		Match: mt, Prio: prio, CheckOverlap: check, Action: action, Importance: imp,
	}}
}

func deleteOp(mt match.Match, prio uint16, strict bool) flowtable.Op {
	return flowtable.Op{Kind: flowtable.OpDelete, Delete: flowtable.DeleteMsg{
		Match: mt, Prio: prio, Strict: strict,
	}}
}

func modifyOp(mt match.Match, prio uint16, strict bool, action string) flowtable.Op {
	return flowtable.Op{Kind: flowtable.OpModify, Modify: flowtable.ModifyMsg{
		Match: mt, Prio: prio, Strict: strict, Action: action,
	}}
}

// TestBundleVisibilityAndRollback：批内前后可见；颠倒次序整批拒绝且不留痕。
func TestBundleVisibilityAndRollback(t *testing.T) {
	ft, err := flowtable.New(4, false)
	if err != nil {
		t.Fatal(err)
	}
	mg := NewManager(ft)
	mtA := mustMatch(t, 0x0A000000, 0xFF000000)

	// 预置 A。
	b := mg.Begin()
	if err := mg.Append(b, addOp(mtA, 10, false, "A", 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := mg.Commit(b, 0); err != nil {
		t.Fatal(err)
	}

	// Delete A；带检查 Add 相同匹配：批内可见，可通过。
	b = mg.Begin()
	if err := mg.Append(b, deleteOp(mtA, 10, true)); err != nil {
		t.Fatal(err)
	}
	if err := mg.Append(b, addOp(mtA, 10, true, "A2", 1)); err != nil {
		t.Fatal(err)
	}
	ev, err := mg.Commit(b, 1)
	if err != nil {
		t.Fatalf("ordered bundle should commit: %v", err)
	}
	if ft.Len() != 1 || len(ev) != 1 || ev[0].Reason != flowtable.Delete {
		t.Fatalf("post commit len=%d ev=%+v", ft.Len(), ev)
	}

	// 颠倒次序：Add（重叠）在前，整批拒绝，下标 0、原因 Overlap。
	b = mg.Begin()
	if err := mg.Append(b, addOp(mtA, 10, true, "A3", 1)); err != nil {
		t.Fatal(err)
	}
	if err := mg.Append(b, deleteOp(mtA, 10, true)); err != nil {
		t.Fatal(err)
	}
	_, err = mg.Commit(b, 2)
	var ce *flowtable.CommitError
	if !errors.As(err, &ce) || ce.Index != 0 || !errors.Is(ce, flowtable.ErrOverlap) {
		t.Fatalf("want CommitError index=0 overlap, got %#v", err)
	}
	// 回滚不留痕：仍是上次提交的 A2，序号/事件不变。
	if ft.Len() != 1 {
		t.Fatalf("rollback len=%d want 1", ft.Len())
	}
	act, id, miss, _, _ := ft.Lookup(match.Pkt{F0: 0x0A000000}, 1, 3)
	if miss || act != "A2" || id != 2 {
		t.Fatalf("rollback changed state: act=%s id=%d miss=%v", act, id, miss)
	}

	// 批保持打开，可再次提交：改成先删后装，成功。
	if err := mg.Discard(b); err != nil {
		t.Fatal(err)
	}
	b = mg.Begin()
	if err := mg.Append(b, deleteOp(mtA, 10, true)); err != nil {
		t.Fatal(err)
	}
	if err := mg.Append(b, addOp(mtA, 10, false, "A4", 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := mg.Commit(b, 4); err != nil {
		t.Fatalf("recommit failed: %v", err)
	}
	if act, _, miss, _, _ := ft.Lookup(match.Pkt{F0: 0x0A000000}, 1, 5); miss || act != "A4" {
		t.Fatalf("final state act=%s miss=%v", act, miss)
	}
}

// TestBundleLimitsAndErrors：256 上限、非法消息、批号不存在、提交后销毁。
func TestBundleLimitsAndErrors(t *testing.T) {
	ft, _ := flowtable.New(300, false)
	mg := NewManager(ft)
	mt := mustMatch(t, 0x0A000000, 0xFF000000)

	if err := mg.Append(999, addOp(mt, 1, false, "x", 1)); !errors.Is(err, flowtable.ErrNoBundle) {
		t.Fatalf("append missing bundle want ErrNoBundle, got %v", err)
	}
	if _, err := mg.Commit(999, 0); !errors.Is(err, flowtable.ErrNoBundle) {
		t.Fatalf("commit missing bundle want ErrNoBundle, got %v", err)
	}
	if err := mg.Discard(999); !errors.Is(err, flowtable.ErrNoBundle) {
		t.Fatalf("discard missing bundle want ErrNoBundle, got %v", err)
	}

	b := mg.Begin()
	for i := 0; i < MaxMsgs; i++ {
		v := uint32(0x01000000 + i*0x01000000)
		op := addOp(mustMatch(t, v&0xFF000000, 0xFF000000), 1, false, "x", 1)
		if err := mg.Append(b, op); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	if err := mg.Append(b, addOp(mt, 1, false, "x", 1)); !errors.Is(err, flowtable.ErrInvalid) {
		t.Fatalf("257th append want ErrInvalid, got %v", err)
	}
	bad := match.Match{F0: match.Field{Value: 1, Mask: 0xFFFFFFFE}}
	if err := mg.Append(b, flowtable.Op{Kind: flowtable.OpAdd, Add: flowtable.AddMsg{Match: bad}}); !errors.Is(err, flowtable.ErrInvalid) {
		t.Fatalf("bad msg want ErrInvalid, got %v", err)
	}
	if _, err := mg.Commit(b, 0); err != nil {
		t.Fatalf("256-op commit: %v", err)
	}
	if err := mg.Discard(b); !errors.Is(err, flowtable.ErrNoBundle) {
		t.Fatalf("committed bundle must be destroyed, got %v", err)
	}
}

// TestBundleRejectedCommitNoExpiry：失败提交不落地到期、不推进时钟。
func TestBundleRejectedCommitNoExpiry(t *testing.T) {
	ft, _ := flowtable.New(2, false)
	mg := NewManager(ft)
	mtA := mustMatch(t, 0x0A000000, 0xFF000000)

	b0 := mg.Begin()
	if err := mg.Append(b0, flowtable.Op{Kind: flowtable.OpAdd, Add: flowtable.AddMsg{
		Match: mtA, Prio: 1, Idle: 5, Action: "A",
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := mg.Commit(b0, 0); err != nil {
		t.Fatal(err)
	}

	// t=4 命中把 idle 推到 9。
	if _, _, miss, _, err := ft.Lookup(match.Pkt{F0: 0x0A000001}, 1, 4); err != nil || miss {
		t.Fatalf("lookup t=4 miss=%v err=%v", miss, err)
	}
	// t=3 的批提交因时钟回退失败：不落地到期。
	b1 := mg.Begin()
	if err := mg.Append(b1, modifyOp(mtA, 1, true, "Z")); err != nil {
		t.Fatal(err)
	}
	if _, err := mg.Commit(b1, 3); !errors.Is(err, flowtable.ErrClockBk) {
		t.Fatalf("want clock back, got %v", err)
	}
	// 批仍在；t=9 提交先到期（Idle@9）再 Modify 作用 0 项，批提交成功但事件含到期。
	ev, err := mg.Commit(b1, 9)
	if err != nil {
		t.Fatalf("commit at 9: %v", err)
	}
	if len(ev) != 1 || ev[0].Reason != flowtable.Idle || ev[0].At != 9 {
		t.Fatalf("want single Idle@9 expiry event, got %+v", ev)
	}
}
