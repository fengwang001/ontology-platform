package delta

import (
	"errors"
	"testing"
)

func mustReplica(t *testing.T, id string, peers []string, cap int) *Replica {
	t.Helper()
	r, err := NewReplica(id, peers, cap)
	if err != nil {
		t.Fatalf("NewReplica(%q, %v, %d) unexpected error: %v", id, peers, cap, err)
	}
	return r
}

func TestConstructorValidation(t *testing.T) {
	cases := []struct {
		name  string
		id    string
		peers []string
		cap   int
		want  error
	}{
		{"cap zero", "A", []string{"B"}, 0, ErrInvalidCap},
		{"cap negative", "A", []string{"B"}, -3, ErrInvalidCap},
		{"empty id", "", []string{"B"}, 1, ErrEmptyReplicaID},
		{"nil peers", "A", nil, 1, ErrEmptyPeerSet},
		{"empty peers", "A", []string{}, 1, ErrEmptyPeerSet},
		{"empty peer id", "A", []string{"B", ""}, 1, ErrEmptyPeerID},
		{"duplicate peer", "A", []string{"B", "B"}, 1, ErrDuplicatePeer},
		{"peer is self", "A", []string{"A", "B"}, 1, ErrPeerIsSelf},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewReplica(tc.id, tc.peers, tc.cap)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			t.Logf("输入 id=%q peers=%v cap=%d -> 输出 err=%v (判定依据: 构造参数非法须整体拒绝)", tc.id, tc.peers, tc.cap, err)
		})
	}
}

func TestApplyInflationAndSequence(t *testing.T) {
	r := mustReplica(t, "A", []string{"B"}, 8)

	ok, err := r.Apply("x", 5)
	if !ok || err != nil {
		t.Fatalf("first apply: ok=%v err=%v", ok, err)
	}
	t.Logf("输入 Apply(x,5) -> 输出 ok=%v (判定依据: 5 > 缺省 0, 膨胀, 占序号 0)", ok)

	ok, err = r.Apply("x", 5)
	if ok || err != nil {
		t.Fatalf("equal apply: ok=%v err=%v", ok, err)
	}
	if got := r.Counter(); got != 1 {
		t.Fatalf("equal apply consumed a sequence number, c=%d", got)
	}
	t.Logf("输入 Apply(x,5) 现值=5 -> 输出 ok=%v c=%d (判定依据: v 等于现值不膨胀, 不占序号)", ok, r.Counter())

	ok, err = r.Apply("x", 3)
	if ok || err != nil {
		t.Fatalf("smaller apply: ok=%v err=%v", ok, err)
	}
	if got := r.Counter(); got != 1 {
		t.Fatalf("smaller apply consumed a sequence number, c=%d", got)
	}
	t.Logf("输入 Apply(x,3) 现值=5 -> 输出 ok=%v c=%d (判定依据: 3 < 5 不膨胀)", ok, r.Counter())

	ok, err = r.Apply("x", 9)
	if !ok || err != nil || r.Counter() != 2 {
		t.Fatalf("inflating apply: ok=%v err=%v c=%d", ok, err, r.Counter())
	}
	t.Logf("输入 Apply(x,9) 现值=5 -> 输出 ok=%v c=%d (判定依据: 9 > 5 膨胀, 占序号 1)", ok, r.Counter())
}

func TestBufferFullSemantics(t *testing.T) {
	r := mustReplica(t, "A", []string{"B"}, 2)
	if _, err := r.Apply("x", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Apply("y", 1); err != nil {
		t.Fatal(err)
	}

	ok, err := r.Apply("x", 1)
	if ok || err != nil {
		t.Fatalf("non-inflating apply on full buffer: ok=%v err=%v", ok, err)
	}
	t.Logf("输入 Apply(x,1) 缓冲满 现值=1 -> 输出 ok=%v err=%v (判定依据: 非膨胀即使缓冲已满也成功返回 false)", ok, err)

	ok, err = r.Apply("z", 1)
	if ok || !errors.Is(err, ErrBufferFull) {
		t.Fatalf("inflating apply on full buffer: ok=%v err=%v", ok, err)
	}
	if got := r.Counter(); got != 2 {
		t.Fatalf("rejected apply mutated counter, c=%d", got)
	}
	if got := r.Snapshot()["z"]; got != 0 {
		t.Fatalf("rejected apply mutated state, S[z]=%d", got)
	}
	t.Logf("输入 Apply(z,1) 缓冲满 -> 输出 ok=%v err=%v c=%d (判定依据: 膨胀且 |D|==Cap 须拒绝且不改状态)", ok, err, r.Counter())
}

func TestDeltaToIntervalAndAliasing(t *testing.T) {
	r := mustReplica(t, "A", []string{"B"}, 8)

	a, b, g, err := r.DeltaTo("B")
	if err != nil || a != 0 || b != 0 || g != nil {
		t.Fatalf("empty delta: a=%d b=%d g=%v err=%v", a, b, g, err)
	}
	t.Logf("输入 DeltaTo(B) ack=0 c=0 -> 输出 a=%d b=%d group=%v (判定依据: a==b 返回空)", a, b, g)

	if _, err := r.Apply("x", 5); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Apply("y", 7); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Apply("x", 9); err != nil {
		t.Fatal(err)
	}

	a, b, g, err = r.DeltaTo("B")
	if err != nil || a != 0 || b != 3 {
		t.Fatalf("delta interval: a=%d b=%d err=%v", a, b, err)
	}
	if g["x"] != 9 || g["y"] != 7 || len(g) != 2 {
		t.Fatalf("delta group not per-key max: %v", g)
	}
	t.Logf("输入 DeltaTo(B) ack=0 c=3 -> 输出 a=%d b=%d group=%v (判定依据: D[0..2] 逐键取最大)", a, b, g)

	g["x"] = 1000
	_, _, g2, err := r.DeltaTo("B")
	if err != nil {
		t.Fatal(err)
	}
	if g2["x"] != 9 {
		t.Fatalf("returned group aliases internal state: %v", g2)
	}
	t.Logf("输入 篡改返回 group[x]=1000 后再次 DeltaTo -> 输出 group=%v (判定依据: 返回值不得与内部状态别名)", g2)

	if _, _, _, err := r.DeltaTo("Z"); !errors.Is(err, ErrUnknownPeer) {
		t.Fatalf("unknown peer: err=%v", err)
	}
}

func TestReceiveBoundaries(t *testing.T) {
	src := mustReplica(t, "B", []string{"A"}, 8)
	dst := mustReplica(t, "A", []string{"B"}, 8)

	if _, err := src.Apply("x", 5); err != nil {
		t.Fatal(err)
	}
	if _, err := src.Apply("x", 9); err != nil {
		t.Fatal(err)
	}

	// a == seen (0) attaches exactly: accepted.
	seen, err := dst.Receive("B", 0, 1, map[string]uint64{"x": 5})
	if err != nil || seen != 1 {
		t.Fatalf("attach at seen: seen=%d err=%v", seen, err)
	}
	t.Logf("输入 Receive(B,0,1,{x:5}) seen=0 -> 输出 seen=%d (判定依据: a 恰等于 seen 可接上)", seen)

	// a == seen+1 is a causal gap: rejected, state untouched.
	seen, err = dst.Receive("B", 2, 3, map[string]uint64{"x": 9})
	if !errors.Is(err, ErrGap) {
		t.Fatalf("gap: seen=%d err=%v", seen, err)
	}
	if got := dst.Snapshot()["x"]; got != 5 {
		t.Fatalf("rejected receive mutated state, S[x]=%d", got)
	}
	if s, _ := dst.SeenOf("B"); s != 1 {
		t.Fatalf("rejected receive mutated seen, seen=%d", s)
	}
	t.Logf("输入 Receive(B,2,3,{x:9}) seen=1 -> 输出 err=%v (判定依据: a 比 seen 大 1 为缺口, 拒绝且不改状态)", err)

	// b == seen is a stale duplicate: no merge, seen unchanged, no error.
	seen, err = dst.Receive("B", 0, 1, map[string]uint64{"x": 5})
	if err != nil || seen != 1 {
		t.Fatalf("stale: seen=%d err=%v", seen, err)
	}
	t.Logf("输入 Receive(B,0,1,{x:5}) seen=1 -> 输出 seen=%d err=%v (判定依据: b 恰等于 seen 为陈旧重复)", seen, err)

	// Overlapping interval a < seen < b merges and advances seen.
	seen, err = dst.Receive("B", 0, 2, map[string]uint64{"x": 9})
	if err != nil || seen != 2 {
		t.Fatalf("overlap: seen=%d err=%v", seen, err)
	}
	if got := dst.Snapshot()["x"]; got != 9 {
		t.Fatalf("overlap merge failed, S[x]=%d", got)
	}
	if got := dst.Buffered(); got != 0 {
		t.Fatalf("receive produced buffer entries, |D|=%d", got)
	}
	t.Logf("输入 Receive(B,0,2,{x:9}) seen=1 -> 输出 seen=%d S[x]=%d |D|=%d (判定依据: a<=seen<b 合并且不产 D 条目)", seen, dst.Snapshot()["x"], dst.Buffered())
}

func TestReceiveErrorPriority(t *testing.T) {
	r := mustReplica(t, "A", []string{"B"}, 8)

	// Unknown peer wins over invalid interval and gap.
	if _, err := r.Receive("Z", 5, 5, nil); !errors.Is(err, ErrUnknownPeer) {
		t.Fatalf("priority 1: err=%v", err)
	}
	// Invalid interval wins over gap (a=3 > seen=0 would be a gap).
	if _, err := r.Receive("B", 3, 3, nil); !errors.Is(err, ErrInvalidInterval) {
		t.Fatalf("priority 2: err=%v", err)
	}
	if _, err := r.Receive("B", 4, 2, nil); !errors.Is(err, ErrInvalidInterval) {
		t.Fatalf("priority 2b: err=%v", err)
	}
	// Gap reported when peer known and interval valid.
	if _, err := r.Receive("B", 1, 2, nil); !errors.Is(err, ErrGap) {
		t.Fatalf("priority 3: err=%v", err)
	}
	t.Logf("输入 未知对端/非法区间/缺口 组合 -> 输出 依次报 ErrUnknownPeer/ErrInvalidInterval/ErrGap (判定依据: 只报第一个错误)")
}

func TestAckSemantics(t *testing.T) {
	r := mustReplica(t, "A", []string{"B", "C"}, 8)
	for _, v := range []uint64{1, 2, 3} {
		if _, err := r.Apply("x", v); err != nil {
			t.Fatal(err)
		}
	}

	// n == c is legal.
	if err := r.Ack("B", 3); err != nil {
		t.Fatalf("ack n==c: %v", err)
	}
	t.Logf("输入 Ack(B,3) c=3 -> 输出 err=nil (判定依据: n 恰等于 c 合法)")

	// n == c+1 is rejected, ack unchanged.
	if err := r.Ack("B", 4); !errors.Is(err, ErrAckBeyondCounter) {
		t.Fatalf("ack n==c+1: err=%v", err)
	}
	if got, _ := r.AckOf("B"); got != 3 {
		t.Fatalf("rejected ack mutated ack[B]=%d", got)
	}
	t.Logf("输入 Ack(B,4) c=3 -> 输出 err=%v ack[B]=3 (判定依据: n 大于 c 拒绝且不改状态)", ErrAckBeyondCounter)

	// Regressing ack is ignored without error.
	if err := r.Ack("B", 1); err != nil {
		t.Fatalf("regressing ack: %v", err)
	}
	if got, _ := r.AckOf("B"); got != 3 {
		t.Fatalf("regressing ack moved ack[B]=%d", got)
	}
	t.Logf("输入 Ack(B,1) ack[B]=3 -> 输出 err=nil ack[B]=3 (判定依据: 较小确认被忽略且不是错误)")

	if err := r.Ack("Z", 1); !errors.Is(err, ErrUnknownPeer) {
		t.Fatalf("unknown peer ack: %v", err)
	}
}

func TestReclaimKeepsMinAckEntry(t *testing.T) {
	r := mustReplica(t, "A", []string{"B", "C"}, 8)
	for _, v := range []uint64{1, 2, 3, 4} {
		if _, err := r.Apply("x", v); err != nil {
			t.Fatal(err)
		}
	}

	// B acks 3, C acks 2 -> min is 2 -> entries 0,1 reclaimed, entry 2 kept.
	if err := r.Ack("B", 3); err != nil {
		t.Fatal(err)
	}
	if got := r.Buffered(); got != 4 {
		t.Fatalf("after single ack: |D|=%d, want 4 (C still at 0)", got)
	}
	if err := r.Ack("C", 2); err != nil {
		t.Fatal(err)
	}
	if got := r.Buffered(); got != 2 {
		t.Fatalf("after both acks: |D|=%d, want 2", got)
	}
	a, b, g, err := r.DeltaTo("C")
	if err != nil || a != 2 || b != 4 {
		t.Fatalf("delta after reclaim: a=%d b=%d err=%v", a, b, err)
	}
	if g["x"] != 4 {
		t.Fatalf("entry at seq==minAck lost: group=%v", g)
	}
	t.Logf("输入 Ack(B,3),Ack(C,2) c=4 -> 输出 |D|=%d D 含序号 2,3 (判定依据: 序号恰等于 ack 最小值的条目保留, |D|==c-minAck)", r.Buffered())

	// Full ack empties the buffer.
	if err := r.Ack("B", 4); err != nil {
		t.Fatal(err)
	}
	if err := r.Ack("C", 4); err != nil {
		t.Fatal(err)
	}
	if got := r.Buffered(); got != 0 {
		t.Fatalf("after full ack: |D|=%d, want 0", got)
	}
}
