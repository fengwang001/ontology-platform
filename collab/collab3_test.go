package collab

import (
	"strings"
	"testing"
)

// 重放原样返回：整批重放与“重放前缀+新后缀”均不再次生效。
func TestReplayVerbatim(t *testing.T) {
	s := newTestServer(t, "d", basicSchema)
	ops := []Op{
		setOp(1, "title", 10, 0, "g1"),
		addOp(2, "count", 5, "g1"),
		setOp(3, "title", 11, 1, "g2"),
	}
	r1 := mustSync(t, s, "c", "d", ops, 1)
	r2 := mustSync(t, s, "c", "d", ops, 5)
	for i := range r1 {
		if r1[i] != r2[i] {
			t.Fatalf("replay mismatch at %d: %+v vs %+v", i, r1[i], r2[i])
		}
	}
	mixed := []Op{
		addOp(2, "count", 5, "g1"),     // replay
		setOp(3, "title", 11, 1, "g2"), // replay
		addOp(4, "count", 1, "g3"),     // new
	}
	r3 := mustSync(t, s, "c", "d", mixed, 6)
	if r3[0].Kind != r1[1].Kind || r3[1].Kind != r1[2].Kind {
		t.Fatalf("replayed prefix must be verbatim: %+v", r3[0])
	}
	if r3[2].Kind != ResApplied {
		t.Fatalf("new op = %v", r3[2].Kind)
	}
	if p := s.Pending("c", "d"); p != 4 {
		t.Fatalf("pending=%d", p)
	}
	snap, _ := s.Get("d")
	if snap.Fields["count"].Value != 6 {
		t.Fatalf("count=%d", snap.Fields["count"].Value)
	}
}

// 重放窗口边界：maxSeq=1001 时 seq 2 保留、seq 1 过期（差一）。
func TestReplayWindowBoundary(t *testing.T) {
	s := newTestServer(t, "d", basicSchema)
	batch := func(from, to int64) []Op {
		ops := make([]Op, 0, to-from+1)
		for seq := from; seq <= to; seq++ {
			ops = append(ops, addOp(seq, "count", 1, "g"))
		}
		return ops
	}
	mustSync(t, s, "c", "d", batch(1, 500), 1)
	mustSync(t, s, "c", "d", batch(501, 1000), 2)
	mustSync(t, s, "c", "d", batch(1001, 1001), 2)

	// seq 2 == maxSeq-999 -> still retained
	res := mustSync(t, s, "c", "d", batch(2, 3), 3)
	if res[0].Kind != ResApplied || res[1].Kind != ResApplied {
		t.Fatalf("boundary replay should succeed: %+v", res)
	}
	// seq 1 == maxSeq-1000 -> expired
	_, err := s.Sync("c", "d", batch(1, 1), 4)
	expectReject(t, err, ErrReplayExpired)

	// new batch exactly continuing after replay must work
	mustSync(t, s, "c", "d", batch(1002, 1003), 5)
	if p := s.Pending("c", "d"); p != 1003 {
		t.Fatalf("pending=%d", p)
	}
}

// 序号缺口：新批次必须从 maxSeq+1 起连续。
func TestSeqGap(t *testing.T) {
	s := newTestServer(t, "d", basicSchema)
	mustSync(t, s, "c", "d", []Op{addOp(1, "count", 1, "g")}, 1)
	_, err := s.Sync("c", "d", []Op{addOp(3, "count", 1, "g")}, 2)
	expectReject(t, err, ErrSeqGap)
	if p := s.Pending("c", "d"); p != 1 {
		t.Fatalf("rejected batch must not consume seqs, pending=%d", p)
	}
}

// 批级拒绝次序：对每一对相邻类别构造“两种问题同时存在”的输入。
func TestBatchRejectOrdering(t *testing.T) {
	// invalid-param vs clock: bad param AND stale clock -> invalid param
	s := newTestServer(t, "d", basicSchema)
	mustSync(t, s, "c", "d", []Op{addOp(1, "count", 1, "g")}, 10)
	_, err := s.Sync("c", "d", []Op{{Seq: 2, Group: "", Type: OpAdd, Field: "count"}}, 1)
	expectReject(t, err, ErrInvalidParam)

	// clock vs expired: stale clock AND seqs aged out -> clock wins
	// process 1002 ops then replay seq 1 with old now
	s2 := newTestServer(t, "d2", basicSchema)
	big := func(from, to int64) []Op {
		out := make([]Op, 0, to-from+1)
		for seq := from; seq <= to; seq++ {
			out = append(out, addOp(seq, "count", 1, "g"))
		}
		return out
	}
	mustSync(t, s2, "c", "d2", big(1, 500), 10)
	mustSync(t, s2, "c", "d2", big(501, 1000), 11)
	mustSync(t, s2, "c", "d2", big(1001, 1002), 11)
	_, err = s2.Sync("c", "d2", big(1, 1), 5) // old now + expired
	expectReject(t, err, ErrClockRollback)

	// expired vs gap cannot co-occur in a legal batch: seqs are consecutive
	// inside a <=500-op batch while the window is 1000, so a batch reaching
	// maxSeq+1 always continues without a gap. Exercise the expired side with
	// a consecutive aged-out prefix; the gap side is covered by TestSeqGap.
	s3 := newTestServer(t, "d3", basicSchema)
	mk := func(from, to int64) []Op {
		out := make([]Op, 0, to-from+1)
		for seq := from; seq <= to; seq++ {
			out = append(out, addOp(seq, "count", 1, "g"))
		}
		return out
	}
	mustSync(t, s3, "c", "d3", mk(1, 500), 1)
	mustSync(t, s3, "c", "d3", mk(501, 1000), 2)
	mustSync(t, s3, "c", "d3", mk(1001, 1001), 2)
	_, err = s3.Sync("c", "d3",
		[]Op{
			addOp(1, "count", 1, "g"),
			addOp(2, "count", 1, "g"),
			addOp(3, "count", 1, "g"),
		}, 3)
	expectReject(t, err, ErrReplayExpired)
}

// 整批拒绝零副作用：文档、时钟、序号均不变。
func TestRejectZeroSideEffects(t *testing.T) {
	s := newTestServer(t, "d", basicSchema)
	mustSync(t, s, "c", "d", []Op{setOp(1, "title", 10, 0, "g1")}, 5)
	before, _ := s.Get("d")

	// group adjacency violation -> reject
	_, err := s.Sync("c", "d", []Op{
		addOp(2, "count", 1, "g1"),
		addOp(3, "count", 1, "g2"),
		addOp(4, "count", 1, "g1"),
	}, 6)
	expectReject(t, err, ErrInvalidParam)

	// first group dependency -> reject
	_, err = s.Sync("c", "d",
		[]Op{func() Op { o := addOp(2, "count", 1, "g1"); o.Depends = true; return o }()}, 6)
	expectReject(t, err, ErrInvalidParam)

	// duplicate set in group -> reject
	_, err = s.Sync("c", "d", []Op{
		setOp(2, "title", 1, 1, "g1"),
		setOp(3, "title", 2, 1, "g1"),
	}, 6)
	expectReject(t, err, ErrInvalidParam)

	// unknown field -> reject
	_, err = s.Sync("c", "d", []Op{addOp(2, "nope", 1, "g1")}, 6)
	expectReject(t, err, ErrInvalidParam)

	// clock rollback -> reject; then same batch with valid now must proceed
	_, err = s.Sync("c", "d", []Op{addOp(2, "count", 1, "g1")}, 4)
	expectReject(t, err, ErrClockRollback)

	after, _ := s.Get("d")
	if after.Revision != before.Revision || after.Fields["title"].Value != before.Fields["title"].Value {
		t.Fatalf("state changed after rejections")
	}
	if p := s.Pending("c", "d"); p != 1 {
		t.Fatalf("pending=%d, want 1", p)
	}
	mustSync(t, s, "c", "d", []Op{addOp(2, "count", 1, "g1")}, 6)
}

func TestUnknownDocAndBadCreate(t *testing.T) {
	s := NewServer()
	_, err := s.Get("missing")
	expectReject(t, err, ErrUnknownDoc)
	_, err = s.Sync("c", "missing", []Op{addOp(1, "f", 1, "g")}, 1)
	expectReject(t, err, ErrUnknownDoc)
	if err := s.Create("", nil); !strings.Contains(err.Error(), ErrInvalidParam.Error()) {
		t.Fatalf("got %v", err)
	}
	if err := s.Create("d", map[string]Kind{"": KindSet}); err == nil {
		t.Fatalf("empty field name must be rejected")
	}
	if err := s.Create("d", map[string]Kind{"f": 99}); err == nil {
		t.Fatalf("bad kind must be rejected")
	}
	if err := s.Create("d", basicSchema); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.Create("d", basicSchema); err == nil {
		t.Fatalf("duplicate create must be rejected")
	}
}
