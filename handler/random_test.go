package handler

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

func TestRandomAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for iter := 0; iter < 200; iter++ {
		cap := int64(rng.Intn(30))
		k := 1 + rng.Intn(5)
		h := New(k)
		inst := fmt.Sprintf("inst%d", iter)
		if err := h.Create([]byte(inst), cap); err != nil {
			t.Fatal(err)
		}
		m := newNaive(cap, k)
		uids := []string{"a", "b", "c", "d", "e", "f", "g"}
		for op := 0; op < 60; op++ {
			uid := uids[rng.Intn(len(uids))]
			delta := int64(rng.Intn(21) - 10)
			switch rng.Intn(10) {
			case 0, 1, 2, 3, 4:
				got, gerr := h.Update([]byte(inst), []byte(uid), delta)
				want, werr := m.update(uid, delta)
				if !errEq(gerr, werr) || (gerr == nil && !got.equal(want)) {
					t.Fatalf("iter%d op%d Update(%s,%d) got=(%+v,%v) want=(%+v,%v)",
						iter, op, uid, delta, got, gerr, want, werr)
				}
			case 5, 6:
				gerr := h.Step([]byte(inst))
				werr := m.step()
				if !errEq(gerr, werr) {
					t.Fatalf("iter%d op%d Step got=%v want=%v", iter, op, gerr, werr)
				}
			case 7:
				if err := h.Close([]byte(inst)); err != nil {
					t.Fatal(err)
				}
				m.close()
			case 8:
				if err := h.Recover([]byte(inst)); err != nil {
					t.Fatal(err)
				}
				m = m.rebuild(k)
			default:
				got, gerr := h.Result([]byte(inst), []byte(uid))
				want, werr := m.lookup(uid)
				if gerr != nil || werr {
					if gerr != nil || !got.equal(want) {
						t.Fatalf("iter%d Result(%s) got=%+v,%v want=%+v", iter, uid, got, gerr, want)
					}
				} else if got.Kind != Unknown {
					t.Fatalf("iter%d Result(%s) got=%+v want Unknown", iter, uid, got)
				}
			}
			t.Logf("iter%d op%d uid=%s delta=%d -> s=%d p=%d closed=%v q=%d dedup=%d",
				iter, op, uid, delta, m.s, m.p, m.closed, len(m.queue), len(m.dedup))
		}
		// 历史与朴素模型逐事件一致。
		gotLog := h.store.Get([]byte(inst)).Events()
		if len(gotLog) != len(m.log) {
			t.Fatalf("iter%d history len got=%d want=%d", iter, len(gotLog), len(m.log))
		}
		for i := range gotLog {
			g, w := gotLog[i], m.log[i]
			if g.Type != w.Type || g.Index != w.Index || g.Delta != w.Delta ||
				string(g.UID) != string(w.UID) {
				t.Fatalf("iter%d event %d got=%+v want=%+v", iter, i, g, w)
			}
		}
	}
}

func errEq(a, b error) bool {
	return errors.Is(a, b) && errors.Is(b, a)
}
