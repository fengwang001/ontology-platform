package store

import (
	"testing"

	"ontology/core"
)

var k1 = core.Key{Type: "Customer", ID: "C-1"}

func put(key core.Key, biz int64, cred string) core.WriteRequest {
	return core.WriteRequest{Key: key, BizStart: biz, Payload: "p", Credential: cred}
}

func mustCommit(t *testing.T, s *Store, req core.WriteRequest) core.Version {
	t.Helper()
	v, err := s.Commit(req)
	if err != nil {
		t.Fatalf("Commit(%+v) 意外被拒: %v", req, err)
	}
	return v
}

func mustReject(t *testing.T, s *Store, req core.WriteRequest, code core.ErrorCode) *core.Error {
	t.Helper()
	_, err := s.Commit(req)
	if err == nil {
		t.Fatalf("Commit(%+v) 意外成功，期望 %s", req, code)
	}
	if err.Code != code {
		t.Fatalf("Commit(%+v) 错误码 = %s，期望 %s", req, err.Code, code)
	}
	return err
}

func TestCommitAllocatesMonotonicSeq(t *testing.T) {
	s := New()
	for i, cred := range []string{"v0", "v1", "v2"} {
		v := mustCommit(t, s, put(k1, int64(10+i*10), cred))
		if want := uint64(i + 1); v.Seq != want {
			t.Fatalf("第 %d 次写入 Seq = %d，期望 %d（系统时间序号不得回退）", i, v.Seq, want)
		}
	}
	if got := s.LatestSeq(k1); got != 3 {
		t.Fatalf("LatestSeq = %d，期望 3", got)
	}
}

func TestDeleteOccupiesSeqAndResurrect(t *testing.T) {
	s := New()
	mustCommit(t, s, put(k1, 10, "v0"))
	d := mustCommit(t, s, core.WriteRequest{Key: k1, BizStart: 20, Delete: true, Credential: "v1"})
	if !d.Deleted || d.Seq != 2 || d.Payload != "" {
		t.Fatalf("删除版本应为占序号的空内容墓碑，得到 %+v", d)
	}
	r := mustCommit(t, s, put(k1, 30, "v2")) // 复活
	if r.Seq != 3 || r.Deleted {
		t.Fatalf("复活版本异常: %+v", r)
	}
	vs, boundary, ok := s.Snapshot(k1)
	if !ok || len(vs) != 3 || boundary != 10 {
		t.Fatalf("版本链快照异常: len=%d boundary=%d ok=%v", len(vs), boundary, ok)
	}
}

func TestRejectOrdering(t *testing.T) {
	s := New()
	mustCommit(t, s, put(k1, 100, "v0")) // 边界 = 100

	// 参数非法优先于凭证冲突与边界检查。
	bad := core.WriteRequest{Key: core.Key{Type: "", ID: ""}, BizStart: 1, Credential: "not-a-cred"}
	mustReject(t, s, bad, core.ErrInvalidArgument)

	// 凭证格式非法属于参数非法。
	mustReject(t, s, put(k1, 100, "vx"), core.ErrInvalidArgument)
	mustReject(t, s, put(k1, 100, ""), core.ErrInvalidArgument)

	// 业务时间起点为负属于参数非法。
	mustReject(t, s, put(k1, -5, "v1"), core.ErrInvalidArgument)

	// 凭证不匹配优先于边界检查（bizStart=1 同时早于边界 100）。
	mustReject(t, s, put(k1, 1, "v9"), core.ErrConcurrencyConflict)

	// 凭证正确但业务起点早于已提交边界。
	mustReject(t, s, put(k1, 50, "v1"), core.ErrBeforeBoundary)
}

func TestRejectedWriteHasNoSideEffect(t *testing.T) {
	s := New()
	mustCommit(t, s, put(k1, 100, "v0"))

	before, _, _ := s.Snapshot(k1)
	mustReject(t, s, put(k1, 100, "v7"), core.ErrConcurrencyConflict)
	mustReject(t, s, put(k1, 10, "v1"), core.ErrBeforeBoundary)
	mustReject(t, s, put(k1, 100, "bad-cred"), core.ErrInvalidArgument)
	mustReject(t, s, core.WriteRequest{Credential: "v1"}, core.ErrInvalidArgument)
	mustReject(t, s, put(core.Key{Type: "T", ID: "x"}, -1, "v0"), core.ErrInvalidArgument)

	after, boundary, _ := s.Snapshot(k1)
	if s.LatestSeq(k1) != 1 || len(after) != 1 || boundary != 100 {
		t.Fatalf("被拒绝的写入产生了副作用: latest=%d len=%d boundary=%d",
			s.LatestSeq(k1), len(after), boundary)
	}
	if before[0].Seq != after[0].Seq {
		t.Fatalf("版本链被篡改")
	}
	// 下一个合法写入仍应拿到序号 2（拒绝不占号）。
	v := mustCommit(t, s, put(k1, 200, "v1"))
	if v.Seq != 2 {
		t.Fatalf("拒绝占用了系统时间序号: 下一版本 Seq = %d，期望 2", v.Seq)
	}
}

func TestTraceableBoundary(t *testing.T) {
	s := New()
	mustCommit(t, s, put(k1, 100, "v0"))
	mustReject(t, s, put(k1, 99, "v1"), core.ErrBeforeBoundary)
	mustCommit(t, s, put(k1, 100, "v1")) // 等于边界允许（等起点覆盖场景）
	mustCommit(t, s, put(k1, 500, "v2"))
	// 边界仍为 100，不随后续写入移动。
	mustReject(t, s, put(k1, 50, "v3"), core.ErrBeforeBoundary)
	// 删除同样声明业务起点，也受边界约束。
	mustReject(t, s, core.WriteRequest{Key: k1, BizStart: 10, Delete: true, Credential: "v3"},
		core.ErrBeforeBoundary)
}
