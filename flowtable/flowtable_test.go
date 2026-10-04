package flowtable

import (
	"errors"
	"testing"

	"ontology/match"
)

func mkMatch(t *testing.T, v0, m0, v1, m1 uint32) match.Match {
	t.Helper()
	mt, err := match.New(match.Field{Value: v0, Mask: m0}, match.Field{Value: v1, Mask: m1})
	if err != nil {
		t.Fatalf("bad match: %v", err)
	}
	return mt
}

func matchA(t *testing.T) match.Match {
	return mkMatch(t, 0x0A000000, 0xFF000000, 0, 0)
}

func matchB(t *testing.T) match.Match {
	return mkMatch(t, 0x0A010000, 0xFFFF0000, 0, 0)
}

func addMsg(mt match.Match, prio uint16, check bool, action string, imp uint16) AddMsg {
	return AddMsg{Match: mt, Prio: prio, CheckOverlap: check, Action: action, Importance: imp}
}

// TestSpecExample 复现题目给出的 C=2 驱逐示例。
func TestSpecExample(t *testing.T) {
	ft, err := New(2, true)
	if err != nil {
		t.Fatal(err)
	}
	r, ev, err := ft.Add(addMsg(matchA(t), 10, false, "A", 1), 0)
	if err != nil || r.ID != 1 || len(ev) != 0 {
		t.Fatalf("add A: %+v ev=%v err=%v", r, ev, err)
	}
	_, _, err = ft.Add(addMsg(matchB(t), 10, true, "B", 1), 1)
	if !errors.Is(err, ErrOverlap) {
		t.Fatalf("B with check want overlap, got %v", err)
	}
	r, _, err = ft.Add(addMsg(matchB(t), 10, false, "B", 1), 1)
	if err != nil || r.ID != 2 {
		t.Fatalf("B without check: %+v err=%v", r, err)
	}
	act, id, miss, _, err := ft.Lookup(match.Pkt{F0: 0x0A010203}, 10, 2)
	if err != nil || miss || act != "A" || id != 1 {
		t.Fatalf("lookup 0x0A010203 act=%s id=%d miss=%v err=%v", act, id, miss, err)
	}
	_, id, miss, _, err = ft.Lookup(match.Pkt{F0: 0x0A020000}, 10, 3)
	if err != nil || miss || id != 1 {
		t.Fatalf("lookup 0x0A020000 id=%d miss=%v", id, miss)
	}
	d := mkMatch(t, 0xCCCC0000, 0xFFFF0000, 0, 0)
	_, _, err = ft.Add(addMsg(d, 20, false, "D", 1), 4)
	if !errors.Is(err, ErrFull) {
		t.Fatalf("importance equal want full, got %v", err)
	}
	if ft.Len() != 2 {
		t.Fatalf("len=%d want 2", ft.Len())
	}
	r, ev, err = ft.Add(addMsg(d, 20, false, "D", 2), 5)
	if err != nil || r.ID != 3 || len(ev) != 1 || ev[0].ID != 1 || ev[0].Reason != Evict || ev[0].At != 5 {
		t.Fatalf("evict add: %+v ev=%v err=%v", r, ev, err)
	}
	if ev[0].Packets != 2 || ev[0].Bytes != 20 {
		t.Fatalf("evicted counters=%+v want 2 packets/20 bytes", ev[0])
	}
	if ft.Len() != 2 {
		t.Fatalf("len after evict=%d want 2", ft.Len())
	}
}

// TestOverlapThenReplace：带检查相同匹配报重叠，不带则原地替换且不占名额。
func TestOverlapThenReplace(t *testing.T) {
	ft, _ := New(2, false)
	mt := matchA(t)
	r, _, _ := ft.Add(addMsg(mt, 10, false, "old", 1), 0)
	if r.ID != 1 {
		t.Fatalf("id=%d", r.ID)
	}
	_, _, err := ft.Add(addMsg(mt, 10, true, "new", 1), 1)
	if !errors.Is(err, ErrOverlap) {
		t.Fatalf("checked same match want overlap, got %v", err)
	}
	_, _, _, _, _ = ft.Lookup(match.Pkt{F0: 0x0A000000}, 100, 2)
	r, ev, err := ft.Add(addMsg(mt, 10, false, "new", 1), 3)
	if err != nil || r.ID != 2 || len(ev) != 0 {
		t.Fatalf("replace: %+v ev=%v err=%v", r, ev, err)
	}
	if ft.Len() != 1 {
		t.Fatalf("replace must not take a slot, len=%d", ft.Len())
	}
	act, id, miss, _, _ := ft.Lookup(match.Pkt{F0: 0x0A000000}, 5, 4)
	if miss || act != "new" || id != 2 {
		t.Fatalf("post replace lookup act=%s id=%d miss=%v", act, id, miss)
	}
	other := mkMatch(t, 0x0B000000, 0xFF000000, 0, 0)
	r, _, _ = ft.Add(addMsg(other, 10, false, "C", 1), 5)
	if r.ID != 3 {
		t.Fatalf("next id=%d want 3", r.ID)
	}
}

// TestTableFullNoEvict：不允许驱逐时满表报表满。
func TestTableFullNoEvict(t *testing.T) {
	ft, _ := New(1, false)
	_, _, _ = ft.Add(addMsg(matchA(t), 10, false, "A", 9), 0)
	other := mkMatch(t, 0x0B000000, 0xFF000000, 0, 0)
	_, _, err := ft.Add(addMsg(other, 10, false, "B", 9), 1)
	if !errors.Is(err, ErrFull) {
		t.Fatalf("want full, got %v", err)
	}
}

// TestSamePrioOrder：同 prio 多命中取安装序号最小者。
func TestSamePrioOrder(t *testing.T) {
	ft, _ := New(4, true)
	wide := mkMatch(t, 0, 0, 0, 0)
	mid := matchB(t)
	_, _, _ = ft.Add(addMsg(mid, 10, false, "B", 1), 0)
	_, _, _ = ft.Add(addMsg(wide, 10, false, "W", 1), 1)
	_, _, _ = ft.Add(addMsg(matchA(t), 10, false, "A", 1), 2)
	act, id, miss, _, _ := ft.Lookup(match.Pkt{F0: 0x0A010203}, 1, 3)
	if miss || id != 1 || act != "B" {
		t.Fatalf("want earliest id=1 B, got id=%d act=%s", id, act)
	}
	_, _, _ = ft.Add(addMsg(wide, 20, false, "H", 1), 4)
	act, id, _, _, _ = ft.Lookup(match.Pkt{F0: 0x0A010203}, 1, 5)
	if id == 0 || act != "H" {
		t.Fatalf("want high prio H, got id=%d act=%s", id, act)
	}
}

// TestTimeoutIdleHard：idle/hard 恰等到期，同刻原因记 Hard。
func TestTimeoutIdleHard(t *testing.T) {
	cases := []struct {
		name       string
		idle, hard int64
		lookupAt   int64
		expireAt   int64
		wantReason Reason
		wantHit    bool
	}{
		{"idle exact", 30, 0, -1, 30, Idle, false},
		{"idle refreshed", 30, 0, 29, 59, Idle, true},
		{"hard exact", 0, 50, 10, 50, Hard, false},
		{"idle hard tie is hard", 70, 70, -1, 70, Hard, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ft, _ := New(2, false)
			m := AddMsg{Match: matchA(t), Prio: 10, Action: "A", Importance: 1, Idle: c.idle, Hard: c.hard}
			_, _, _ = ft.Add(m, 0)
			if c.lookupAt >= 0 {
				_, id, miss, _, err := ft.Lookup(match.Pkt{F0: 0x0A000001}, 1, c.lookupAt)
				if err != nil {
					t.Fatal(err)
				}
				if c.wantHit && (miss || id != 1) {
					t.Fatalf("refresh lookup miss=%v id=%d", miss, id)
				}
			}
			_, _, miss, ev, err := ft.Lookup(match.Pkt{F0: 0x0A000001}, 1, c.expireAt)
			if err != nil {
				t.Fatal(err)
			}
			if !miss {
				t.Fatal("expired entry must not match")
			}
			if len(ev) != 1 || ev[0].Reason != c.wantReason || ev[0].At != c.expireAt || ev[0].ID != 1 {
				t.Fatalf("event=%+v want reason=%s at=%d", ev, c.wantReason, c.expireAt)
			}
			if ft.Len() != 0 {
				t.Fatal("expired entry must be removed")
			}
		})
	}
}

// TestModifyDeleteScope：严格/非严格作用范围。
func TestModifyDeleteScope(t *testing.T) {
	ft, _ := New(4, false)
	_, _, _ = ft.Add(addMsg(matchA(t), 10, false, "A", 1), 0)
	_, _, _ = ft.Add(addMsg(matchB(t), 20, false, "B", 1), 1)
	n, _, err := ft.Modify(ModifyMsg{Match: matchA(t), Strict: false, Action: "X"}, 2)
	if err != nil || n != 2 {
		t.Fatalf("non-strict modify A n=%d err=%v", n, err)
	}
	n, _, _ = ft.Modify(ModifyMsg{Match: matchB(t), Strict: false, Action: "Y"}, 3)
	if n != 1 {
		t.Fatalf("non-strict modify B n=%d want 1", n)
	}
	if act, _, miss, _, _ := ft.Lookup(match.Pkt{F0: 0x0A010000}, 1, 4); miss || act != "Y" {
		t.Fatalf("B action=%s", act)
	}
	n, ev, _ := ft.Delete(DeleteMsg{Match: matchA(t), Prio: 11, Strict: true}, 5)
	if n != 0 || len(ev) != 0 {
		t.Fatalf("strict delete wrong prio n=%d ev=%v", n, ev)
	}
	n, ev, _ = ft.Delete(DeleteMsg{Match: matchA(t), Strict: false}, 6)
	if n != 2 || len(ev) != 2 || ev[0].ID != 1 || ev[1].ID != 2 ||
		ev[0].Reason != Delete || ev[0].At != 6 {
		t.Fatalf("non-strict delete n=%d ev=%+v", n, ev)
	}
}

// TestRejectionOrder：参数非法 > 时钟回退 > 重叠 > 表满；被拒不落地到期。
func TestRejectionOrder(t *testing.T) {
	ft, _ := New(1, false)
	_, err := New(0, false)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("capacity 0 want invalid, got %v", err)
	}
	badMatch := match.Match{F0: match.Field{Value: 1, Mask: 0xFFFFFFFE}}
	_, _, err = ft.Add(addMsg(badMatch, 10, false, "X", 1), -5)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("want invalid, got %v", err)
	}
	_, _, _ = ft.Add(addMsg(matchA(t), 10, false, "A", 1), 10)
	_, _, err = ft.Add(addMsg(matchA(t), 10, true, "A2", 1), 9)
	if !errors.Is(err, ErrClockBk) {
		t.Fatalf("want clock back, got %v", err)
	}
	_, _, err = ft.Add(addMsg(matchA(t), 10, true, "A2", 1), 11)
	if !errors.Is(err, ErrOverlap) {
		t.Fatalf("want overlap, got %v", err)
	}
	other := mkMatch(t, 0x0B000000, 0xFF000000, 0, 0)
	_, _, err = ft.Add(addMsg(other, 10, false, "C", 1), 12)
	if !errors.Is(err, ErrFull) {
		t.Fatalf("want full, got %v", err)
	}
	// 被拒操作不落地到期、不推进时钟：
	// t=4 命中把 idle 到期推到 9；t=3 的回退 Modify 拒绝，不落地到期；
	// t=9 的 Lookup 先以 Idle 移除（事件时刻 9），故不命中。
	ft3, _ := New(1, false)
	_, _, _ = ft3.Add(AddMsg{Match: matchA(t), Prio: 1, Idle: 5}, 0)
	_, _, _, _, err = ft3.Lookup(match.Pkt{F0: 0x0A000000}, 1, 4)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = ft3.Modify(ModifyMsg{Match: matchA(t), Strict: true, Action: "z"}, 3)
	if !errors.Is(err, ErrClockBk) {
		t.Fatalf("want clock back, got %v", err)
	}
	var ev []Event
	var miss bool
	_, _, miss, ev, _ = ft3.Lookup(match.Pkt{F0: 0x0A000000}, 1, 9)
	if !miss {
		t.Fatal("lookup at exact expiry must miss after removal")
	}
	if len(ev) != 1 || ev[0].Reason != Idle || ev[0].At != 9 {
		t.Fatalf("expiry at 9 want Idle, got %+v", ev)
	}
}

// TestExaminedBound：低优先级表项数与 examined 无关。
func TestExaminedBound(t *testing.T) {
	run := func(lowCount int) int {
		ft, _ := New(lowCount+10, false)
		hit := addMsg(matchB(t), 10, false, "HIT", 1)
		_, _, _ = ft.Add(hit, 0)
		for i := 0; i < lowCount; i++ {
			v := uint32(0x10000000 + uint32(i)*0x01000000)
			mt := mkMatch(t, v, 0xFF000000, 0, 0)
			_, _, err := ft.Add(addMsg(mt, 1, false, "low", 1), int64(i+1))
			if err != nil {
				t.Fatalf("add low %d: %v", i, err)
			}
		}
		_, _, miss, _, err := ft.Lookup(match.Pkt{F0: 0x0A010203}, 1, int64(lowCount+10))
		if err != nil || miss {
			t.Fatalf("lookup miss=%v err=%v", miss, err)
		}
		return ft.Examined()
	}
	e100 := run(100)
	e10000 := run(10000)
	if e100 != 1 || e10000 != 1 {
		t.Fatalf("examined 100-low=%d 10000-low=%d, both must be 1", e100, e10000)
	}
}
