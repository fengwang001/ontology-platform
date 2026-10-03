package handler

import (
	"errors"
	"math/rand"
	"testing"

	"ontology/history"
)

// model is a naive step-by-step simulation written directly from the spec.
type model struct {
	maxCap, applied, pendingSum int64
	k                           int
	queue                       []mEntry
	res                         map[string]Result
	order                       []string
	closed                      bool
	hist                        []history.Event
}

type mEntry struct {
	uid         string
	delta, uSeq int64
}

func newModel(maxCap int64, k int) *model {
	return &model{maxCap: maxCap, k: k, res: make(map[string]Result)}
}

func (m *model) register(uid string, r Result) {
	if _, ok := m.res[uid]; ok {
		m.res[uid] = r
		return
	}
	if len(m.res) >= m.k {
		delete(m.res, m.order[0])
		m.order = m.order[1:]
	}
	m.res[uid] = r
	m.order = append(m.order, uid)
}

func (m *model) append(e history.Event) int64 {
	e.Seq = int64(len(m.hist)) + 1
	m.hist = append(m.hist, e)
	return e.Seq
}

func (m *model) update(uid string, delta int64) (Result, error) {
	if r, ok := m.res[uid]; ok {
		return r, nil
	}
	if m.closed {
		return Result{}, ErrClosed
	}
	if v := m.applied + m.pendingSum + delta; v < 0 || v > m.maxCap {
		r := Result{Kind: Rejected}
		m.register(uid, r)
		return r, nil
	}
	seq := m.append(history.Event{Kind: history.UpdateAccepted, UID: uid, Delta: delta})
	r := Result{Kind: Accepted, Seq: seq}
	m.queue = append(m.queue, mEntry{uid: uid, delta: delta, uSeq: seq})
	m.pendingSum += delta
	m.register(uid, r)
	return r, nil
}

func (m *model) step() (Result, error) {
	if len(m.queue) == 0 {
		return Result{}, ErrEmpty
	}
	head := m.queue[0]
	m.queue = m.queue[1:]
	m.applied += head.delta
	m.pendingSum -= head.delta
	m.append(history.Event{Kind: history.UpdateApplied, Ref: head.uSeq})
	r := Result{Kind: Completed, Seq: head.uSeq, Value: m.applied}
	m.res[head.uid] = r
	return r, nil
}

func (m *model) close() {
	if m.closed {
		return
	}
	m.append(history.Event{Kind: history.Closed})
	m.closed = true
	for _, q := range m.queue {
		m.res[q.uid] = Result{Kind: Aborted, Seq: q.uSeq}
	}
	m.queue = nil
	m.pendingSum = 0
}

// recoverFrom recomputes what Recover must produce from a history prefix.
func (m *model) recoverFrom(events []history.Event) {
	fresh := newModel(m.maxCap, m.k)
	var us []history.Event
	appliedCount := 0
	for _, e := range events {
		switch e.Kind {
		case history.UpdateAccepted:
			us = append(us, e)
		case history.UpdateApplied:
			appliedCount++
		case history.Closed:
			fresh.closed = true
		}
		fresh.hist = append(fresh.hist, e)
	}
	for i, u := range us {
		if i < appliedCount {
			fresh.applied += u.Delta
			fresh.register(u.UID, Result{Kind: Completed, Seq: u.Seq, Value: fresh.applied})
		} else {
			fresh.queue = append(fresh.queue, mEntry{uid: u.UID, delta: u.Delta, uSeq: u.Seq})
			fresh.pendingSum += u.Delta
			fresh.register(u.UID, Result{Kind: Accepted, Seq: u.Seq})
		}
	}
	if fresh.closed {
		for _, q := range fresh.queue {
			fresh.res[q.uid] = Result{Kind: Aborted, Seq: q.uSeq}
		}
		fresh.queue = nil
		fresh.pendingSum = 0
	}
	*m = *fresh
}

// checkState compares the handler's observable state against the model.
func checkState(t *testing.T, h *Handler, id []byte, m *model, uids []string) {
	t.Helper()
	for _, uid := range append(append([]string{}, uids...), "never-seen") {
		want := m.res[uid]
		if got := h.ResultOf(id, uid); got != want {
			t.Fatalf("ResultOf(%s)=%+v, want %+v", uid, got, want)
		}
	}
	in, err := h.lookup(id)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if in.applied != m.applied || in.pendingSum != m.pendingSum ||
		len(in.queue) != len(m.queue) || in.closed != m.closed {
		t.Fatalf("state mismatch: got applied=%d pending=%d queue=%d closed=%v, "+
			"want %d/%d/%d/%v", in.applied, in.pendingSum, len(in.queue),
			in.closed, m.applied, m.pendingSum, len(m.queue), m.closed)
	}
	if n := len(h.History(id)); n != len(m.hist) {
		t.Fatalf("history len=%d, want %d", n, len(m.hist))
	}
}

// TestRandomAgainstModel drives random op sequences on both implementations.
func TestRandomAgainstModel(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	h := New()
	m := newModel(10, 4)
	mustCreate(t, h, inst, m.maxCap, m.k)
	uids := []string{"a", "b", "c", "d", "e", "f"}

	for step := 0; step < 3000; step++ {
		switch rng.Intn(10) {
		case 0, 1, 2, 3, 4, 5: // Update
			uid := uids[rng.Intn(len(uids))]
			delta := int64(rng.Intn(31) - 15)
			got, gErr := h.Update(inst, uid, delta)
			want, wErr := m.update(uid, delta)
			if got != want || !errors.Is(gErr, wErr) {
				t.Fatalf("step %d Update(%s,%d): got %+v/%v, want %+v/%v",
					step, uid, delta, got, gErr, want, wErr)
			}
		case 6, 7: // Step
			got, gErr := h.Step(inst)
			want, wErr := m.step()
			if got != want || !errors.Is(gErr, wErr) {
				t.Fatalf("step %d Step: got %+v/%v, want %+v/%v", step, got, gErr, want, wErr)
			}
		case 8: // Close
			if err := h.Close(inst); err != nil {
				t.Fatalf("step %d Close: %v", step, err)
			}
			m.close()
		case 9: // Recover
			if err := h.Recover(inst); err != nil {
				t.Fatalf("step %d Recover: %v", step, err)
			}
			m.recoverFrom(m.hist)
		}
		checkState(t, h, inst, m, uids)
		if in, _ := h.lookup(inst); in.applied < 0 || in.applied > m.maxCap ||
			in.applied+in.pendingSum < 0 || in.applied+in.pendingSum > m.maxCap {
			t.Fatalf("step %d: 不变量违反 applied=%d pending=%d", step, in.applied, in.pendingSum)
		}
	}
	t.Logf("3000 步随机操作（cap=10, K=4）与朴素模拟完全一致，最终历史 %d 条", len(m.hist))
}
