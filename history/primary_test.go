package history_test

import (
	"errors"
	"testing"

	"ontology/history"
	"ontology/lease"
	"ontology/recovery"
)

func check(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("err=%v want %v", err, want)
	}
}

func seqOk(t *testing.T, want int) func(int, error) {
	return func(seq int, err error) {
		t.Helper()
		check(t, err, nil)
		if seq != want {
			t.Fatalf("seq=%d want %d", seq, want)
		}
	}
}

// 题目示例 1：E=100 全流程。
func TestExampleOne(t *testing.T) {
	p := history.NewPrimary(100, 10)
	seqOk(t, 1)(p.Index(0, "a"))
	seqOk(t, 2)(p.Index(0, "b"))
	seqOk(t, 3)(p.Delete(0, "a"))
	seqOk(t, 4)(p.Index(0, "b"))
	seqOk(t, 5)(p.Index(0, "c"))
	check(t, p.AddLease(0, "L1", 2), nil)
	check(t, p.SetGlobalCheckpoint(0, 4), nil)

	r, err := p.Merge(10)
	check(t, err, nil)
	if r.Purged != 1 || len(r.Removed) != 0 {
		t.Fatalf("Merge(10) = %+v", r)
	}
	t.Logf("Merge(10): floor=min(gcp+1=5,r=2)=2; [1,2) seq1 被 seq3 取代 => purged=%d H=%d", r.Purged, p.H())

	pl, err := p.Plan(1)
	check(t, err, nil)
	if pl.Mode != recovery.OpsBased || len(pl.Ops) != 4 || pl.Ops[0].Seq != 2 || pl.Ops[3].Seq != 5 {
		t.Fatalf("Plan(1) = %+v", pl)
	}
	pl0, err := p.Plan(0)
	check(t, err, nil)
	if pl0.Mode != recovery.FileBased || len(pl0.Docs) != 2 ||
		pl0.Docs[0].ID != "b" || pl0.Docs[0].Seq != 4 ||
		pl0.Docs[1].ID != "c" || pl0.Docs[1].Seq != 5 || pl0.MaxSeq != 5 {
		t.Fatalf("Plan(0) = %+v", pl0)
	}
	t.Logf("Plan(1): c+1=2>=H OpsBased[2..5]; Plan(0): c+1=1<H FileBased b@4 c@5 maxSeq=5")

	r, _ = p.Merge(100)
	if r.Purged != 0 || len(r.Removed) != 0 {
		t.Fatalf("Merge(100): 恰等 E 不过期, got %+v", r)
	}
	r, _ = p.Merge(101)
	if r.Purged != 2 || len(r.Removed) != 1 || r.Removed[0] != "L1" {
		t.Fatalf("Merge(101) = %+v", r)
	}
	t.Logf("Merge(101): 101-0>100 L1 剔除 floor=5; seq2 被取代 + seq3 Delete 清除, seq4 存活保留 => purged=%d H=%d", r.Purged, p.H())

	if pl, _ := p.Plan(3); pl.Mode != recovery.FileBased {
		t.Fatalf("Plan(3) want FileBased, got %v", pl.Mode)
	}
	pl, _ = p.Plan(4)
	if pl.Mode != recovery.OpsBased || len(pl.Ops) != 1 || pl.Ops[0].Seq != 5 {
		t.Fatalf("Plan(4) = %+v", pl)
	}
}

// 题目示例 2：过期未剔除可续活；续活后 floor=3；r<H 历史不可得。
func TestExampleTwoRenewExpired(t *testing.T) {
	p := history.NewPrimary(100, 10)
	seqOk(t, 1)(p.Index(0, "a"))
	seqOk(t, 2)(p.Index(0, "b"))
	seqOk(t, 3)(p.Delete(0, "a"))
	seqOk(t, 4)(p.Index(0, "b"))
	seqOk(t, 5)(p.Index(0, "c"))
	check(t, p.AddLease(0, "L1", 2), nil)
	check(t, p.SetGlobalCheckpoint(0, 4), nil)
	r, _ := p.Merge(10)
	if r.Purged != 1 {
		t.Fatalf("Merge(10) purged=%d", r.Purged)
	}
	r, _ = p.Merge(100)
	if len(r.Removed) != 0 {
		t.Fatalf("Merge(100) 不应剔除, got %+v", r)
	}

	if err := p.RenewLease(101, "L1", 3); err != nil {
		t.Fatalf("过期未剔除应可续活: %v", err)
	}
	r, _ = p.Merge(101)
	if r.Purged != 1 || len(r.Removed) != 0 || p.H() != 3 {
		t.Fatalf("Merge(101) = %+v H=%d", r, p.H())
	}
	t.Logf("续活后 floor=min(5,3)=3; [2,3) seq2 被 seq4 取代 => purged=%d H=%d", r.Purged, p.H())

	if err := p.AddLease(101, "L2", 2); !errors.Is(err, lease.ErrHistoryMissing) {
		t.Fatalf("r=2<H=3 应历史不可得, got %v", err)
	}
	check(t, p.AddLease(101, "L2", 3), nil)
	pl, _ := p.Plan(2)
	if pl.Mode != recovery.OpsBased || len(pl.Ops) != 3 ||
		pl.Ops[0].Seq != 3 || pl.Ops[0].Kind != recovery.KindDelete {
		t.Fatalf("Plan(2) = %+v", pl)
	}
	t.Logf("Plan(2)=OpsBased[3 Delete(a),4,5]; AddL2 r=2 拒(历史不可得), r=3 成功")
}

// 剔除后续活/删除报不存在；过期大 1 才剔除。
func TestRemovedLeaseNotFound(t *testing.T) {
	p := history.NewPrimary(1, 10)
	seqOk(t, 1)(p.Index(0, "a"))
	check(t, p.AddLease(0, "L", 1), nil)
	r, err := p.Merge(2)
	check(t, err, nil)
	if len(r.Removed) != 1 {
		t.Fatalf("2-0>1 应剔除: %+v", r)
	}
	if err := p.RenewLease(2, "L", 1); !errors.Is(err, lease.ErrLeaseNotFound) {
		t.Fatalf("剔除后续活 NotFound, got %v", err)
	}
	if err := p.RemoveLease(2, "L"); !errors.Is(err, lease.ErrLeaseNotFound) {
		t.Fatalf("剔除后删除 NotFound, got %v", err)
	}
}

// gcp 与租约谁小谁定 floor；Add 的 r 恰等 H；c+1 恰等 H 与小 1。
func TestFloorAndBoundary(t *testing.T) {
	p := history.NewPrimary(1000, 10)
	for i, id := range []string{"a", "b", "c", "d"} {
		seqOk(t, i+1)(p.Index(0, id))
	}
	check(t, p.AddLease(0, "L", 4), nil)
	check(t, p.SetGlobalCheckpoint(0, 2), nil)
	r, _ := p.Merge(1)
	if p.H() != 3 || r.Purged != 0 {
		t.Fatalf("gcp+1=3<r=4 应由 gcp 定 floor: H=%d purged=%d", p.H(), r.Purged)
	}
	t.Logf("gcp+1=3<r=4 => floor=3; 区间内存活 Index 不清除, H=%d", p.H())

	check(t, p.AddLease(1, "Eq", 3), nil)
	if err := p.AddLease(1, "Low", 2); !errors.Is(err, lease.ErrHistoryMissing) {
		t.Fatalf("r=H-1 历史不可得, got %v", err)
	}
	if pl, _ := p.Plan(3); pl.Mode != recovery.OpsBased {
		t.Fatalf("c+1==H 应 OpsBased, got %v", pl.Mode)
	}
	if pl, _ := p.Plan(2); pl.Mode != recovery.OpsBased {
		t.Fatalf("c+1==H 应 OpsBased(恰等), got %v", pl.Mode)
	}
	if pl, _ := p.Plan(1); pl.Mode != recovery.FileBased {
		t.Fatalf("c+1==H-1 应 FileBased, got %v", pl.Mode)
	}
	if _, err := p.Plan(5); !errors.Is(err, recovery.ErrInvalidArgument) {
		t.Fatalf("c>maxSeq 非法, got %v", err)
	}
	if _, err := p.Plan(-1); !errors.Is(err, recovery.ErrInvalidArgument) {
		t.Fatalf("c<0 非法, got %v", err)
	}
}

// 时钟回退优先于 Delete 文档不存在与 gcp 回退；同 now 合法。
func TestClockOrdering(t *testing.T) {
	p := history.NewPrimary(100, 10)
	seqOk(t, 1)(p.Index(5, "a"))
	if _, err := p.Delete(4, "ghost"); !errors.Is(err, history.ErrClockBacktrack) {
		t.Fatalf("Delete 时钟回退优先于文档不存在, got %v", err)
	}
	check(t, p.SetGlobalCheckpoint(5, 1), nil)
	if err := p.SetGlobalCheckpoint(4, 0); !errors.Is(err, history.ErrClockBacktrack) {
		t.Fatalf("SetGCP 时钟回退优先于检查点回退, got %v", err)
	}
	if err := p.SetGlobalCheckpoint(5, 0); !errors.Is(err, lease.ErrGCPBacktrack) {
		t.Fatalf("SetGCP 检查点回退, got %v", err)
	}
	if _, err := p.Delete(5, "ghost"); !errors.Is(err, history.ErrDocNotFound) {
		t.Fatalf("Delete 文档不存在, got %v", err)
	}
	seqOk(t, 2)(p.Index(5, "z"))
}

// 拒绝次序：参数非法 > 时钟 > 存在性 > 租约回退 > 历史不可得 > 超限。
func TestRejectOrder(t *testing.T) {
	p := history.NewPrimary(100, 1)
	seqOk(t, 1)(p.Index(0, "a"))
	check(t, p.AddLease(0, "Occ", 2), nil)
	check(t, p.SetGlobalCheckpoint(0, 1), nil)
	r, _ := p.Merge(150)
	if p.H() != 2 || len(r.Removed) != 1 {
		t.Fatalf("前置: %+v H=%d", r, p.H())
	}
	// 历史不可得优先于超限（名额已满）。
	if err := p.AddLease(150, "New", 1); !errors.Is(err, lease.ErrHistoryMissing) {
		t.Fatalf("历史不可得优先于超限, got %v", err)
	}

	p2 := history.NewPrimary(100, 1)
	seqOk(t, 1)(p2.Index(0, "a"))
	check(t, p2.AddLease(0, "Dup", 2), nil)
	check(t, p2.SetGlobalCheckpoint(0, 1), nil)
	// 同名"已存在"优先于历史不可得与超限。
	if err := p2.AddLease(0, "Dup", 1); !errors.Is(err, lease.ErrLeaseExists) {
		t.Fatalf("同名报已存在, got %v", err)
	}
	// 参数非法优先于时钟与存在性。
	if err := p2.AddLease(-1, "Dup", 1); !errors.Is(err, lease.ErrInvalidArgument) {
		t.Fatalf("now 越界, got %v", err)
	}
	if err := p2.AddLease(150, "X", 3); !errors.Is(err, lease.ErrInvalidArgument) {
		t.Fatalf("r>maxSeq+1 参数非法(优先时钟), got %v", err)
	}
	// Renew：不存在优先（即使 r 也回退）。
	if err := p2.RenewLease(200, "Missing", 0); !errors.Is(err, lease.ErrLeaseNotFound) {
		t.Fatalf("Renew 不存在, got %v", err)
	}
	// Renew 租约回退。
	if err := p2.RenewLease(200, "Dup", 1); !errors.Is(err, lease.ErrLeaseBacktrack) {
		t.Fatalf("Renew r 回退, got %v", err)
	}
}

// 被拒操作不剔除租约：时钟回退后，过期租约仍占名额且可续活。
func TestRejectedOpDoesNotExpire(t *testing.T) {
	p := history.NewPrimary(10, 1)
	seqOk(t, 1)(p.Index(0, "a"))
	check(t, p.AddLease(0, "L", 1), nil)
	// 先用一个合法 now 推进时钟，再制造一次被时钟回退拒绝的操作。
	seqOk(t, 2)(p.Index(50, "b"))
	if _, err := p.Index(49, "c"); !errors.Is(err, history.ErrClockBacktrack) {
		t.Fatalf("应时钟回退拒绝, got %v", err)
	}
	// 名额仍被 L 占用：合法 Add 新名应超限（说明 L 未被剔除）。
	if err := p.AddLease(50, "X", 2); !errors.Is(err, lease.ErrLeaseLimit) {
		t.Fatalf("过期未剔除仍占名额, got %v", err)
	}
	// L 仍可续活（过期只在 Merge 剔除）。
	check(t, p.RenewLease(50, "L", 2), nil)
	r, _ := p.Merge(50)
	if len(r.Removed) != 0 {
		t.Fatalf("续活后不过期, got %+v", r)
	}
	t.Logf("被拒不剔除: 过期 L 仍占名额(Add 超限)且可续活; Merge(50) 无剔除 %+v", r)
}

// id 长度与 g 参数非法；seq 被拒操作不占号。
func TestInvalidArgsAndSeq(t *testing.T) {
	p := history.NewPrimary(100, 10)
	if _, err := p.Index(0, ""); !errors.Is(err, history.ErrInvalidArgument) {
		t.Fatalf("空 id, got %v", err)
	}
	long := make([]byte, 257)
	if _, err := p.Index(0, string(long)); !errors.Is(err, history.ErrInvalidArgument) {
		t.Fatalf("257 字节 id, got %v", err)
	}
	if _, err := p.Index(1_000_000_000_001, "a"); !errors.Is(err, history.ErrInvalidArgument) {
		t.Fatalf("now 超上限, got %v", err)
	}
	seqOk(t, 1)(p.Index(0, "a"))
	if _, err := p.Delete(0, "ghost"); !errors.Is(err, history.ErrDocNotFound) {
		t.Fatal(err)
	}
	// Delete 被拒不占 seq。
	seqOk(t, 2)(p.Index(1, "b"))
	if err := p.SetGlobalCheckpoint(0, 3); !errors.Is(err, lease.ErrInvalidArgument) {
		t.Fatalf("g>maxSeq 参数非法, got %v", err)
	}
	if err := p.AddLease(0, "Z", 0); !errors.Is(err, lease.ErrInvalidArgument) {
		t.Fatalf("r<1, got %v", err)
	}
	if err := p.AddLease(1, "Z", 4); !errors.Is(err, lease.ErrInvalidArgument) {
		t.Fatalf("r>maxSeq+1, got %v", err)
	}
}
