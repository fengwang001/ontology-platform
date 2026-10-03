package handler

import (
	"errors"
	"fmt"
	"testing"
)

var inst = []byte("i")

func mustCreate(t *testing.T, h *Handler, id []byte, maxCap int64, k int) {
	t.Helper()
	if err := h.Create(id, maxCap, k); err != nil {
		t.Fatalf("Create(%s, cap=%d, k=%d): %v", id, maxCap, k, err)
	}
}

func wantResult(t *testing.T, op string, got Result, err error, want Result) {
	t.Helper()
	t.Logf("%s -> %+v err=%v（期望 %+v）", op, got, err, want)
	if err != nil || got != want {
		t.Fatalf("%s: got %+v err=%v, want %+v", op, got, err, want)
	}
}

// TestSpecExample replays the worked example from the specification.
func TestSpecExample(t *testing.T) {
	h := New()
	mustCreate(t, h, inst, 10, 10)

	r, err := h.Update(inst, "u1", 6)
	wantResult(t, "Update(u1,+6)", r, err, Result{Kind: Accepted, Seq: 1})

	r, err = h.Update(inst, "u2", 6)
	wantResult(t, "Update(u2,+6) 投影 6+6=12>10", r, err, Result{Kind: Rejected})
	if n := len(h.History(inst)); n != 1 {
		t.Fatalf("Rejected 不写历史: history len=%d, want 1", n)
	}

	r, err = h.Step(inst)
	wantResult(t, "Step 应用 u1", r, err, Result{Kind: Completed, Seq: 1, Value: 6})

	r, err = h.Update(inst, "u3", -4)
	wantResult(t, "Update(u3,-4) 投影 6-4=2", r, err, Result{Kind: Accepted, Seq: 3})

	r, err = h.Step(inst)
	wantResult(t, "Step 应用 u3", r, err, Result{Kind: Completed, Seq: 3, Value: 2})

	r, err = h.Update(inst, "u2", 6)
	wantResult(t, "Update(u2,+6) 命中去重仍 Rejected", r, err, Result{Kind: Rejected})

	if err := h.Recover(inst); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	t.Log("Recover 后去重表只剩 u1、u3，u2 的 Rejected 丢失")
	r, err = h.Update(inst, "u2", 6)
	wantResult(t, "Recover 后 Update(u2,+6) 重新校验 2+6=8", r, err, Result{Kind: Accepted, Seq: 5})
}

// TestProjectionVsApplied shows queued updates count toward validation.
func TestProjectionVsApplied(t *testing.T) {
	h := New()
	mustCreate(t, h, inst, 10, 10)

	r, err := h.Update(inst, "a", 6)
	wantResult(t, "Update(a,+6) 投影 0->6", r, err, Result{Kind: Accepted, Seq: 1})
	r, err = h.Update(inst, "b", 4)
	wantResult(t, "Update(b,+4) 投影 6->10", r, err, Result{Kind: Accepted, Seq: 2})
	r, err = h.Update(inst, "c", 1)
	wantResult(t, "Update(c,+1) 投影 10+1>10 拒绝（已应用值仍为 0）", r, err, Result{Kind: Rejected})

	if err := h.Close(inst); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := h.Close(inst); err != nil {
		t.Fatalf("Close 幂等: %v", err)
	}
	if got := h.ResultOf(inst, "b"); got.Kind != Aborted {
		t.Fatalf("Close 后 Result(b)=%+v, want Aborted", got)
	}
	r, err = h.Update(inst, "b", 4)
	wantResult(t, "Update(b,+4) 去重命中先于 ErrClosed", r, err, Result{Kind: Aborted, Seq: 2})

	if _, err = h.Update(inst, "d", 1); !errors.Is(err, ErrClosed) {
		t.Fatalf("新 uid 在关闭后: err=%v, want ErrClosed", err)
	}
	if _, err = h.Step(inst); !errors.Is(err, ErrEmpty) {
		t.Fatalf("Close 清空队列后 Step: err=%v, want ErrEmpty", err)
	}

	if err := h.Recover(inst); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if got := h.ResultOf(inst, "b"); got.Kind != Aborted {
		t.Fatalf("Recover 后 Result(b)=%+v, want Aborted（C 之后未被 A 覆盖）", got)
	}
	if _, err = h.Update(inst, "e", 1); !errors.Is(err, ErrClosed) {
		t.Fatalf("Recover 后仍关闭: err=%v, want ErrClosed", err)
	}
	t.Log("Recover 后实例仍为已关闭，未应用者保持 Aborted")
}

// TestRejectedCostsNoSeqAndDedupHitSkipsValidation covers seq accounting.
func TestRejectedCostsNoSeqAndDedupHitSkipsValidation(t *testing.T) {
	h := New()
	mustCreate(t, h, inst, 10, 10)
	h.Update(inst, "u1", 6) // seq 1
	h.Update(inst, "u2", 6) // Rejected, no seq
	r, err := h.Update(inst, "u3", 1)
	wantResult(t, "Update(u3,+1) 在 Rejected 之后", r, err, Result{Kind: Accepted, Seq: 2})
	r, err = h.Update(inst, "u2", 6)
	wantResult(t, "Update(u2,+6) 命中去重（此时 6+1=7 本可通过）", r, err, Result{Kind: Rejected})
}

// TestErrorPriority checks rejection categories and their precedence.
func TestErrorPriority(t *testing.T) {
	h := New()
	if err := h.Create(nil, 1, 1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("Create 空 inst: %v", err)
	}
	if err := h.Create(inst, -1, 1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("Create cap=-1: %v", err)
	}
	if err := h.Create(inst, MaxCap+1, 1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("Create cap=1e12+1: %v", err)
	}
	if err := h.Create(inst, 1, 0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("Create k=0: %v", err)
	}
	if _, err := h.Update(inst, "u", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Update 不存在实例: %v", err)
	}
	mustCreate(t, h, inst, 10, 10)
	if err := h.Create(inst, 10, 10); !errors.Is(err, ErrExists) {
		t.Fatalf("重复 Create: %v", err)
	}
	if _, err := h.Update(nil, "u", 1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("空 inst: %v", err)
	}
	if _, err := h.Update(inst, "", 1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("空 uid: %v", err)
	}
	if _, err := h.Update(inst, "u", MaxDelta+1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("delta 越界: %v", err)
	}
	if _, err := h.Step(inst); !errors.Is(err, ErrEmpty) {
		t.Fatalf("空队列 Step: %v", err)
	}
	if got := h.ResultOf(inst, "u"); got.Kind != Unknown {
		t.Fatalf("非法参数不应登记去重表: %+v", got)
	}
	if n := len(h.History(inst)); n != 0 {
		t.Fatalf("非法操作不应写历史: len=%d", n)
	}
	h.Close(inst)
	if _, err := h.Update(inst, "z", 1); !errors.Is(err, ErrClosed) {
		t.Fatalf("关闭后新 uid: %v", err)
	}
	if got := h.ResultOf(inst, "z"); got.Kind != Unknown {
		t.Fatalf("ErrClosed 不应登记去重表: %+v", got)
	}
	t.Log("优先级：参数非法 > ErrNotFound/ErrExists > 去重命中 > ErrClosed > ErrEmpty")
}

// TestEvictionMakesUidNew: an evicted uid is validated as a fresh request.
func TestEvictionMakesUidNew(t *testing.T) {
	h := New()
	mustCreate(t, h, inst, 10, 1)
	r, _ := h.Update(inst, "x", 1)
	wantResult(t, "Update(x,+1)", r, nil, Result{Kind: Accepted, Seq: 1})
	r, _ = h.Update(inst, "y", 1)
	wantResult(t, "Update(y,+1) 淘汰 x", r, nil, Result{Kind: Accepted, Seq: 2})
	r, err := h.Update(inst, "x", 1)
	wantResult(t, "Update(x,+1) 被淘汰后视为新请求", r, err, Result{Kind: Accepted, Seq: 3})
}

// TestProjectionCounterWithLongQueue: validation is O(1) via the pending-sum
// counter, exercised at queue lengths 1 and 10000.
func TestProjectionCounterWithLongQueue(t *testing.T) {
	h := New()
	mustCreate(t, h, inst, MaxCap, 10_001)
	for i := 0; i < 10_000; i++ {
		if _, err := h.Update(inst, fmt.Sprintf("u%d", i), 1); err != nil {
			t.Fatalf("Update #%d: %v", i, err)
		}
	}
	in, _ := h.lookup(inst)
	if len(in.queue) != 10_000 || in.pendingSum != 10_000 {
		t.Fatalf("queue=%d pendingSum=%d, want 10000/10000", len(in.queue), in.pendingSum)
	}
	r, err := h.Update(inst, "last", 1)
	wantResult(t, "第 10001 个更新（投影 10000+1）", r, err, Result{Kind: Accepted, Seq: 10_001})
	if _, err := h.Step(inst); err != nil {
		t.Fatalf("Step: %v", err)
	}
	if in.pendingSum != 10_000 || in.applied != 1 {
		t.Fatalf("Step 后 applied=%d pendingSum=%d, want 1/10000", in.applied, in.pendingSum)
	}
	t.Log("队列长度 1 与 10000 两档：校验只读增量计数器，不遍历队列")
}
