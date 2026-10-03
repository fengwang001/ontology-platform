package handler

import (
	"errors"
	"testing"
)

func mustCreate(t *testing.T, h *Handler, inst string, cap int64) {
	t.Helper()
	if err := h.Create([]byte(inst), cap); err != nil {
		t.Fatalf("Create(%s): %v", inst, err)
	}
}

func wantResult(t *testing.T, got Result, want Result, ctx string) {
	t.Helper()
	if !got.equal(want) {
		t.Fatalf("%s: got %+v want %+v", ctx, got, want)
	}
}

// 题目例 1（cap=10）：接受/拒绝/投影值/序号连续/去重命中/Recover 后拒绝项丢失。
func TestExampleCap10(t *testing.T) {
	h := New(1000)
	mustCreate(t, h, "i", 10)

	r, err := h.Update([]byte("i"), []byte("u1"), 6)
	if err != nil || r != (Result{Kind: Accepted, Seq: 1}) {
		t.Fatalf("u1: r=%+v err=%v", r, err)
	}
	t.Logf("IN Update(u1,+6) | p=0, p+delta=6 in [0,10] -> OUT %+v", r)

	// u2 投影值为 6：6+6=12>10，Rejected，不写历史、不耗序号。
	r, err = h.Update([]byte("i"), []byte("u2"), 6)
	if err != nil || r.Kind != Rejected {
		t.Fatalf("u2: r=%+v err=%v", r, err)
	}
	t.Logf("IN Update(u2,+6) | p=6, 12>10 -> OUT Rejected，历史不变")

	if err := h.Step([]byte("i")); err != nil {
		t.Fatalf("Step: %v", err)
	}
	if s := h.store.Get([]byte("i")).Len(); s != 2 {
		t.Fatalf("after Step 历史长度=%d want 2", s)
	}
	wantResult(t, mustResult(t, h, "i", "u1"), Result{Kind: Completed, Val: 6}, "u1 after step")
	t.Logf("IN Step | s=6, 追加 A(1) 序号2 -> u1=Completed(6)")

	r, err = h.Update([]byte("i"), []byte("u3"), -4)
	if err != nil || r != (Result{Kind: Accepted, Seq: 3}) {
		t.Fatalf("u3: r=%+v err=%v", r, err)
	}
	t.Logf("IN Update(u3,-4) | p=6, 2 in range -> OUT Accepted(3)")
	if err := h.Step([]byte("i")); err != nil {
		t.Fatalf("Step2: %v", err)
	}
	wantResult(t, mustResult(t, h, "i", "u3"), Result{Kind: Completed, Val: 2}, "u3")
	if s := h.store.Get([]byte("i")).Len(); s != 4 {
		t.Fatalf("历史长度=%d want 4", s)
	}

	// 命中去重：即使 2+6=8 本可通过，仍原样返回 Rejected。
	r, _ = h.Update([]byte("i"), []byte("u2"), 6)
	if r.Kind != Rejected {
		t.Fatalf("u2 dedupe hit: %+v", r)
	}
	t.Logf("IN Update(u2,+6) | 去重命中 -> OUT Rejected（不再校验）")

	// Recover 后拒绝项 u2 丢失，u1/u3 重建为 Completed。
	if err := h.Recover([]byte("i")); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	wantResult(t, mustResult(t, h, "i", "u1"), Result{Kind: Completed, Val: 6}, "recover u1")
	wantResult(t, mustResult(t, h, "i", "u3"), Result{Kind: Completed, Val: 2}, "recover u3")
	wantResult(t, mustResult(t, h, "i", "u2"), Result{Kind: Unknown}, "recover u2")
	r, err = h.Update([]byte("i"), []byte("u2"), 6)
	if err != nil || r != (Result{Kind: Accepted, Seq: 5}) {
		t.Fatalf("u2 after recover: r=%+v err=%v", r, err)
	}
	t.Logf("Recover 后 u2 重新校验：2+6=8 -> Accepted(5)")
}

func mustResult(t *testing.T, h *Handler, inst, uid string) Result {
	t.Helper()
	r, err := h.Result([]byte(inst), []byte(uid))
	if err != nil {
		t.Fatalf("Result(%s): %v", uid, err)
	}
	return r
}

// 题目例 2：排队投影 0->6->10 都接受，第 11 被拒（尽管 s 仍为 0）；
// Close 后未应用者 Aborted，且去重命中先于一切地返回 Aborted。
func TestProjectionAndClose(t *testing.T) {
	h := New(1000)
	mustCreate(t, h, "i", 10)
	for _, c := range []struct {
		uid   string
		delta int64
		seq   int64
	}{{"a", 6, 1}, {"b", 4, 2}} {
		r, err := h.Update([]byte("i"), []byte(c.uid), c.delta)
		if err != nil || r != (Result{Kind: Accepted, Seq: c.seq}) {
			t.Fatalf("%s: r=%+v err=%v", c.uid, r, err)
		}
	}
	r, err := h.Update([]byte("i"), []byte("c"), 1)
	if err != nil || r.Kind != Rejected {
		t.Fatalf("c: r=%+v err=%v", r, err)
	}
	t.Logf("IN Update(c,+1) | 投影 p=10（s 仍为0），11>10 -> Rejected")
	if err := h.Close([]byte("i")); err != nil {
		t.Fatalf("Close: %v", err)
	}
	wantResult(t, mustResult(t, h, "i", "a"), Result{Kind: Aborted}, "a aborted")
	wantResult(t, mustResult(t, h, "i", "b"), Result{Kind: Aborted}, "b aborted")
	if err := h.Step([]byte("i")); !errors.Is(err, ErrEmpty) {
		t.Fatalf("Step after close: err=%v want ErrEmpty", err)
	}
	// 去重命中先于 ErrClosed：b 返回 Aborted 而非 ErrClosed。
	r, err = h.Update([]byte("i"), []byte("b"), 4)
	if err != nil || r.Kind != Aborted {
		t.Fatalf("b after close: r=%+v err=%v", r, err)
	}
	// 未登记的新 uid：关闭错误。
	if _, err = h.Update([]byte("i"), []byte("d"), 1); !errors.Is(err, ErrClosed) {
		t.Fatalf("d after close: err=%v want ErrClosed", err)
	}
	// 幂等 Close，且 Recover 后仍为已关闭、未应用者仍 Aborted。
	if err := h.Close([]byte("i")); err != nil {
		t.Fatalf("Close idempotent: %v", err)
	}
	if err := h.Recover([]byte("i")); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	wantResult(t, mustResult(t, h, "i", "a"), Result{Kind: Aborted}, "recover a")
	wantResult(t, mustResult(t, h, "i", "b"), Result{Kind: Aborted}, "recover b")
	if _, err = h.Update([]byte("i"), []byte("z"), 1); !errors.Is(err, ErrClosed) {
		t.Fatalf("after recover err=%v want ErrClosed", err)
	}
}

func TestErrorPriorityAndParams(t *testing.T) {
	h := New(2)
	if err := h.Create(nil, 1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("empty inst: %v", err)
	}
	mustCreate(t, h, "i", 5)
	if err := h.Create([]byte("i"), 5); !errors.Is(err, ErrExists) {
		t.Fatalf("dup create: %v", err)
	}
	if _, err := h.Update([]byte("nope"), []byte("u"), 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("not found: %v", err)
	}
	if _, err := h.Update([]byte("i"), nil, 1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("empty uid: %v", err)
	}
	if _, err := h.Update([]byte("i"), []byte("u"), MaxDelta+1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("delta high: %v", err)
	}
	if _, err := h.Update([]byte("i"), []byte("u"), MinDelta-1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("delta low: %v", err)
	}
	if err := h.Create([]byte("j"), MaxCap+1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("cap high: %v", err)
	}
	if err := h.Step([]byte("i")); !errors.Is(err, ErrEmpty) {
		t.Fatalf("empty step: %v", err)
	}
	if err := h.Step([]byte("nope")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("step missing: %v", err)
	}
	// 参数非法时即使实例不存在也优先报参数错误。
	if _, err := h.Result(nil, []byte("u")); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("result param: %v", err)
	}
}
