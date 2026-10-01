package ontology

import (
	"fmt"
	"testing"
)

func TestConstructorRejections(t *testing.T) {
	cases := []struct {
		name    string
		id      string
		peers   []string
		cap     int
		wantErr error
	}{
		{"cap zero", "r", []string{"p"}, 0, ErrInvalidCap},
		{"cap negative", "r", []string{"p"}, -3, ErrInvalidCap},
		{"empty self id", "", []string{"p"}, 2, ErrEmptyID},
		{"empty peer id", "r", []string{""}, 2, ErrEmptyID},
		{"no peers", "r", nil, 2, ErrNoPeers},
		{"self in peers", "r", []string{"r"}, 2, ErrSelfInPeers},
		{"duplicate peers", "r", []string{"p", "p"}, 2, ErrDuplicatePeer},
	}
	for _, tc := range cases {
		_, err := New(tc.id, tc.peers, tc.cap)
		judge(t, "New/"+tc.name,
			fmt.Sprintf("id=%q peers=%v cap=%d", tc.id, tc.peers, tc.cap),
			err, tc.wantErr, "构造参数非法必须以可区分原因整体拒绝")
	}

	r, err := New("r", []string{"p"}, 2)
	judge(t, "New/ok", `id="r" peers=[p] cap=2`, err, nil, "合法构造无错误")
	judge(t, "New/initial-state",
		fmt.Sprintf("S=%v c=%d |D|=%d", r.Snapshot(), r.Cursor(), r.BufferLen()),
		"map[] 0 0", "map[] 0 0", "初始 S 为空、c=0、D 为空")
}

func TestApplyInflationAndNoop(t *testing.T) {
	r, _ := New("r", []string{"p"}, 5)

	inflated, err := r.Apply("k", 5)
	judge(t, "Apply inflation", "Apply(k,5)",
		fmt.Sprintf("(%v,%v)", inflated, err), "(true,<nil>)",
		"v=5 > 缺省0：膨胀，占用序号 c=0，随后 c=1")
	judge(t, "Apply/state", "S[k]", r.Snapshot()["k"], uint64(5), "膨胀后 S[k]=5")
	judge(t, "Apply/cursor", "c", r.Cursor(), 1, "膨胀后下一序号为 1")

	equal, err := r.Apply("k", 5)
	judge(t, "Apply non-inflation equal", "Apply(k,5)",
		fmt.Sprintf("(%v,%v)", equal, err), "(false,<nil>)",
		"v 等于现值不是膨胀：返回 false 且不占序号")

	lower, err := r.Apply("k", 3)
	judge(t, "Apply non-inflation lower", "Apply(k,3)",
		fmt.Sprintf("(%v,%v)", lower, err), "(false,<nil>)",
		"v 小于现值不是膨胀：返回 false 且不占序号")
	judge(t, "Apply/cursor unchanged", "c", r.Cursor(), 1, "非膨胀不推进序号")

	missing, err := r.Apply("other", 0)
	judge(t, "Apply v=0 on missing", "Apply(other,0)",
		fmt.Sprintf("(%v,%v)", missing, err), "(false,<nil>)",
		"缺省值为 0：v=0 不大于缺省 0，不是膨胀")
}

func TestBufferFull(t *testing.T) {
	r, _ := New("r", []string{"p"}, 2)

	r.Apply("a", 1)
	r.Apply("b", 2)
	judge(t, "buffer reaches cap", "|D|", r.BufferLen(), 2, "两次膨胀后 |D|=Cap=2")

	ok, err := r.Apply("a", 1)
	judge(t, "full buffer non-inflation", "Apply(a,1)",
		fmt.Sprintf("(%v,%v)", ok, err), "(false,<nil>)",
		"非膨胀 Apply 在缓冲满时仍返回 false、不报错")

	ok, err = r.Apply("c", 3)
	judge(t, "full buffer inflation", "Apply(c,3)",
		fmt.Sprintf("(%v,%v)", ok, err),
		fmt.Sprintf("(false,%v)", ErrBufferFull),
		"膨胀且 |D|==Cap：以 ErrBufferFull 整体拒绝")
	judge(t, "state unchanged after reject", "S[c]", r.Snapshot()["c"], uint64(0),
		"被拒绝的操作不得改变状态")
	judge(t, "cursor unchanged after reject", "c", r.Cursor(), 2,
		"被拒绝的操作不占用序号")

	if err := r.Ack("p", 1); err != nil {
		t.Fatalf("ack: %v", err)
	}
	judge(t, "post-ack |D|", "|D| after Ack(p,1)", r.BufferLen(), 1,
		"回收序号 < minAck=1 后仅保留 D[1]")
	ok, err = r.Apply("c", 3)
	judge(t, "inflation after reclaim", "Apply(c,3)",
		fmt.Sprintf("(%v,%v)", ok, err), "(true,<nil>)",
		"回收腾出槽位后膨胀成功")
}

func TestDeltaToMergedInterval(t *testing.T) {
	r, _ := New("r", []string{"p", "q"}, 10)
	r.Apply("k", 5) // D[0] = {k:5}
	r.Apply("k", 9) // D[1] = {k:9}
	r.Apply("x", 2) // D[2] = {x:2}

	msg, err := r.DeltaTo("p")
	judge(t, "DeltaTo full", "DeltaTo(p)",
		fmt.Sprintf("(%d,%d,%v,%v)", msg.A, msg.B, msg.Group, err),
		"(0,3,map[k:9 x:2],<nil>)",
		"区间 [ack=0,c=3)，D[0..2] 逐键取最大：k=max(5,9)=9, x=2")

	msg.Group["k"] = 100
	again, _ := r.DeltaTo("p")
	judge(t, "DeltaTo no alias", "mutate returned group then DeltaTo again",
		again.Group["k"], uint64(9),
		"返回组与内部状态无别名：外部改写不影响副本")

	if err := r.Ack("p", 2); err != nil {
		t.Fatalf("ack: %v", err)
	}
	msg, _ = r.DeltaTo("p")
	judge(t, "DeltaTo tail", "after Ack(p,2): DeltaTo(p)",
		fmt.Sprintf("(%d,%d,%v)", msg.A, msg.B, msg.Group),
		"(2,3,map[x:2])",
		"新区间 [ack=2,c=3) 仅含 D[2]")

	if err := r.Ack("p", 3); err != nil {
		t.Fatalf("ack: %v", err)
	}
	msg, err = r.DeltaTo("p")
	judge(t, "DeltaTo empty", "after Ack(p,3): DeltaTo(p)",
		fmt.Sprintf("(%+v,%v)", msg, err),
		"({A:0 B:0 Group:map[]},<nil>)",
		"a==b 时返回空（零值消息）")

	_, err = r.DeltaTo("ghost")
	judge(t, "DeltaTo unknown peer", "DeltaTo(ghost)", err, ErrUnknownPeer,
		"未知对端必须可区分地拒绝")
}
