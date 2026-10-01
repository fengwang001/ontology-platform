package ontology

import (
	"fmt"
	"testing"
)

func TestReceiveBoundaryAndPrecedence(t *testing.T) {
	r, _ := New("r", []string{"p"}, 10)

	seen, err := r.Receive("p", 0, 2, DeltaGroup{"k": 7})
	judge(t, "Receive a==seen connects", "Receive(p,0,2,{k:7})",
		fmt.Sprintf("(%d,%v)", seen, err), "(2,<nil>)",
		"a=0 恰等于 seen=0 可接上：合并并令 seen=2")
	judge(t, "Receive merged", "S[k]", r.Snapshot()["k"], uint64(7), "group 合并进 S")

	seen, err = r.Receive("p", 0, 2, DeltaGroup{"k": 7})
	judge(t, "Receive b==seen stale", "Receive(p,0,2,...) repeated",
		fmt.Sprintf("(%d,%v)", seen, err), "(2,<nil>)",
		"b 恰等于 seen=2：陈旧重复，不合并、不改 seen，仍返回当前 seen")

	seen, err = r.Receive("p", 1, 2, DeltaGroup{"k": 99})
	judge(t, "Receive b<seen stale", "Receive(p,1,2,{k:99})",
		fmt.Sprintf("(%d,%v)", seen, err), "(2,<nil>)",
		"b<seen 陈旧重复：旧值 k=99 不得合并进 S")
	judge(t, "stale did not merge", "S[k]", r.Snapshot()["k"], uint64(7),
		"陈旧包内容不得污染状态")

	seen, err = r.Receive("p", 3, 4, DeltaGroup{"z": 1})
	judge(t, "Receive a==seen+1 gap", "Receive(p,3,4,...) with seen=2",
		fmt.Sprintf("(%d,%v)", seen, err),
		fmt.Sprintf("(2,%v)", ErrGap),
		"a 比 seen 大 1：缺序号 2 形成缺口，拒绝并返回当前 seen=2")
	judge(t, "gap did not merge", "S[z]", r.Snapshot()["z"], uint64(0),
		"缺口拒绝不得合并任何内容")

	seen, err = r.Receive("p", 1, 4, DeltaGroup{"k": 8, "z": 1})
	judge(t, "Receive overlap extends", "Receive(p,1,4,{k:8,z:1})",
		fmt.Sprintf("(%d,%v)", seen, err), "(4,<nil>)",
		"a<=seen<b：逐键最大合并（k 7->8）并推进 seen=4")
	judge(t, "overlap merged z", "S[z]", r.Snapshot()["z"], uint64(1),
		"新区段 z=1 合并成功")

	judge(t, "Receive creates no D", "c", r.Cursor(), 0,
		"远程合并进 S 不产生 D 条目、不推进本地 c")

	_, err = r.Receive("ghost", 5, 3, nil)
	judge(t, "precedence unknown first", "Receive(ghost,5,3,nil)",
		err, ErrUnknownPeer,
		"同时命中未知对端/区间非法/缺口时只报第一个：未知对端")
	_, err = r.Receive("p", 5, 3, nil)
	judge(t, "precedence invalid interval", "Receive(p,5,3,nil)",
		err, ErrInvalidInterval,
		"对端已知但 a>=b：报区间非法（先于缺口）")

	full, _ := New("r", []string{"p"}, 1)
	full.Apply("x", 1)
	seen, err = full.Receive("p", 0, 1, DeltaGroup{"y": 4})
	judge(t, "Receive ignores local cap", "full-buffer Receive",
		fmt.Sprintf("(%d,%v)", seen, err), "(1,<nil>)",
		"接收合并不占本地缓冲槽位，Cap 不影响 Receive")
}

func TestAckSemanticsAndReclaim(t *testing.T) {
	r, _ := New("r", []string{"p", "q"}, 10)
	r.Apply("a", 1)
	r.Apply("b", 2)
	r.Apply("c", 3)
	r.Apply("d", 4)

	err := r.Ack("p", 4)
	judge(t, "Ack n==c legal", "Ack(p,4) with c=4", err, nil,
		"n 恰等于 c 合法")
	err = r.Ack("p", 5)
	judge(t, "Ack n==c+1 rejected", "Ack(p,5)", err, ErrAckBeyondCursor,
		"n 比 c 大 1 必须拒绝")

	judge(t, "D[0] retained at min", "|D| with ack p=4 q=0", r.BufferLen(), 4,
		"minAck=0：仅删除序号 <0 的条目（没有），序号恰等于最小值的 D[0] 保留")

	err = r.Ack("p", 2)
	judge(t, "Ack rollback ignored", "Ack(p,2) after 4", err, nil,
		"较小的确认被忽略且不是错误")
	got, _ := r.AckOf("p")
	judge(t, "Ack stays high", "ack[p]", got, 4, "回退不改变 ack[p]")

	if err := r.Ack("q", 2); err != nil {
		t.Fatalf("ack q: %v", err)
	}
	judge(t, "reclaim at min=2", "|D| after Ack(q,2)", r.BufferLen(), 2,
		"minAck=min(4,2)=2：删除 D[0],D[1]，序号恰等于最小值的 D[2] 保留")

	// DeltaTo p is now empty; DeltaTo q sends [2,4).
	mp, _ := r.DeltaTo("p")
	judge(t, "DeltaTo p empty", "DeltaTo(p)", fmt.Sprintf("A=%d B=%d", mp.A, mp.B),
		"A=0 B=0", "p 已确认到 4：a==b 返回空（零值消息，无 group）")
	mq, _ := r.DeltaTo("q")
	judge(t, "DeltaTo q retained tail", "DeltaTo(q)",
		fmt.Sprintf("A=%d B=%d group=%v", mq.A, mq.B, mq.Group),
		"A=2 B=4 group=map[c:3 d:4]",
		"保留的 D[2],D[3] 仍可发送给确认较慢的 q")

	if err := r.Ack("q", 4); err != nil {
		t.Fatalf("ack q: %v", err)
	}
	judge(t, "all acked: D empty", "|D| after Ack(q,4)", r.BufferLen(), 0,
		"minAck=4：删除序号 <4 的全部条目，|D| == c-minAck = 0")

	if err := r.Ack("p", 1); err != nil {
		t.Fatalf("rollback after reclaim must stay a no-op: %v", err)
	}
	judge(t, "rollback never resurrects D", "|D|", r.BufferLen(), 0,
		"回退确认只被忽略，绝不复活已回收条目（ack 单调）")

	err = r.Ack("ghost", 1)
	judge(t, "Ack unknown peer", "Ack(ghost,1)", err, ErrUnknownPeer,
		"未知对端可区分拒绝")

	// A rejected ack changes nothing.
	before := r.BufferLen()
	_ = r.Ack("q", 99)
	judge(t, "rejected Ack is atomic", "|D| before/after Ack(q,99)",
		fmt.Sprintf("%d->%d", before, r.BufferLen()), "0->0",
		"n>c 的拒绝不得触发回收或任何状态变更")
}
