package handler

import (
	"fmt"
	"math/rand"
	"testing"

	"ontology/history"
)

// FIFO 淘汰最旧 uid 后，该 uid 被当作新请求重新校验（即使新 delta 不同）。
func TestDedupeEviction(t *testing.T) {
	h := New(2)
	mustCreate(t, h, "i", 100)
	if r, _ := h.Update([]byte("i"), []byte("u1"), 1); r.Kind != Accepted {
		t.Fatalf("u1: %+v", r)
	}
	if r, _ := h.Update([]byte("i"), []byte("u2"), 1); r.Kind != Accepted {
		t.Fatalf("u2: %+v", r)
	}
	if r, _ := h.Update([]byte("i"), []byte("u3"), 99); r.Kind != Rejected {
		t.Fatalf("u3 should reject: %+v", r)
	}
	// 容量为2，u1 被淘汰（最旧）；u1 以相同请求再来：p=2，p+1=3 通过，
	// 被视为新请求 -> Accepted(3)（u3 的 Rejected 未耗序号）。
	if r, _ := h.Update([]byte("i"), []byte("u1"), 1); r != (Result{Kind: Accepted, Seq: 3}) {
		t.Fatalf("u1 re-validated: %+v", r)
	}
	t.Logf("u1 被 FIFO 淘汰后视为新请求，重新校验通过 -> Accepted(3)")
	// u3 仍在表中（登记为 Rejected）：即使本次 delta=-1 在 p=3 下本可通过，
	// 命中不刷新、不再校验，原样返回 Rejected。
	if r, _ := h.Update([]byte("i"), []byte("u3"), -1); r.Kind != Rejected {
		t.Fatalf("u3 hit should stay Rejected: %+v", r)
	}
	t.Logf("u3 命中不刷新，原样返回 Rejected，delta=-1 不再校验")
}

// 对历史的每个前缀做 Recover，并与朴素逐步模拟对照状态、结果与后续可复现性。
func TestRecoverEveryPrefix(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for iter := 0; iter < 40; iter++ {
		cap := int64(rng.Intn(20))
		h := New(5)
		inst := fmt.Sprintf("i%d", iter)
		mustCreate(t, h, inst, cap)

		type rec struct {
			uid   string
			delta int64
		}
		var accepted []rec // 入史顺序
		steps := rng.Intn(12)
		n := 3 + rng.Intn(10)
		for j := 0; j < n; j++ {
			uid := fmt.Sprintf("u%d", rng.Intn(6))
			delta := int64(rng.Intn(11) - 5)
			r, err := h.Update([]byte(inst), []byte(uid), delta)
			if err != nil {
				j-- // 非法参数不参与
				continue
			}
			if r.Kind == Accepted {
				accepted = append(accepted, rec{uid, delta})
			}
			if len(accepted) > 0 && rng.Intn(2) == 0 && steps > 0 {
				if err := h.Step([]byte(inst)); err == nil {
					steps--
				}
			}
		}
		for steps > 0 && len(accepted) > 0 {
			if err := h.Step([]byte(inst)); err != nil {
				break
			}
			steps--
		}
		if rng.Intn(2) == 0 {
			_ = h.Close([]byte(inst))
		}

		full := h.store.Get([]byte(inst)).Events()
		for prefix := 0; prefix <= len(full); prefix++ {
			rh := replayInto(t, "r", cap, full[:prefix])
			m := simulate(full[:prefix])
			for _, u := range accepted {
				got, _ := rh.Result([]byte("r"), []byte(u.uid))
				want, known := m[u.uid]
				if !known {
					continue // 被去重表淘汰
				}
				if !got.equal(want) {
					t.Fatalf("iter%d prefix%d uid=%s got=%+v want=%+v history=%v",
						iter, prefix, u.uid, got, want, full)
				}
			}
		}
	}
}

// replayInto 在新处理器上按给定历史前缀重建实例（模拟崩溃后重放）。
func replayInto(t *testing.T, inst string, cap int64, evs []history.Event) *Handler {
	t.Helper()
	h := New(5)
	if err := h.Create([]byte(inst), cap); err != nil {
		t.Fatalf("replay create: %v", err)
	}
	lg := h.store.Get([]byte(inst))
	for _, e := range evs {
		switch e.Type {
		case history.TypeUpdate:
			lg.AppendUpdate(e.UID, e.Delta)
		case history.TypeApplied:
			lg.AppendApplied(e.Seq)
		case history.TypeClosed:
			lg.AppendClosed()
		}
	}
	if err := h.Recover([]byte(inst)); err != nil {
		t.Fatalf("replay recover: %v", err)
	}
	return h
}

// simulate 按规则重放历史前缀，返回最近 K=5 个 U 的 uid 的结果。
func simulate(events []history.Event) map[string]Result {
	type u struct {
		uid   string
		delta int64
		seq   int64
	}
	var us []u
	applied := 0
	closed := false
	for _, e := range events {
		switch e.Type {
		case history.TypeUpdate:
			us = append(us, u{string(e.UID), e.Delta, e.Index})
		case history.TypeApplied:
			applied++
		case history.TypeClosed:
			closed = true
		}
	}
	if applied > len(us) {
		applied = len(us)
	}
	out := map[string]Result{}
	var s int64
	start := 0
	if len(us) > 5 {
		start = len(us) - 5
	}
	for i, x := range us {
		if i < applied {
			s += x.delta
			if i >= start {
				out[x.uid] = Result{Kind: Completed, Val: s}
			}
			continue
		}
		if i < start {
			continue
		}
		if closed {
			out[x.uid] = Result{Kind: Aborted}
		} else {
			out[x.uid] = Result{Kind: Accepted, Seq: x.seq}
		}
	}
	return out
}
