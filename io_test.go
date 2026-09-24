package ontology_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"ontology/attrib"
	"ontology/dump"
	"ontology/sampler"
	"ontology/stack"
	"ontology/tree"
)

type ftick struct{ ch chan time.Time }

func newFtick(n int) *ftick {
	t := &ftick{ch: make(chan time.Time, 4096)}
	for i := 0; i < n; i++ {
		t.ch <- time.Now()
	}
	return t
}

func (t *ftick) Chan() <-chan time.Time { return t.ch }
func (t *ftick) Stop()                  { close(t.ch) }

type src struct {
	busy map[int]bool
	bad  bool
	i    int
}

func (s *src) Stack() (stack.Stack, error) {
	i := s.i
	s.i++
	if s.busy[i] {
		return stack.Stack{}, sampler.ErrBusy
	}
	if s.bad {
		return stack.Stack{}, stack.ErrEmpty
	}
	return stack.Normalize([]string{"m", "A"}, 0)
}

func drive(sm *sampler.Sampler, tk *ftick, sent uint64) sampler.Stats {
	go sm.Run()
	for i := 0; i < 2000; i++ {
		st := sm.Stats()
		if st.Ticks+st.BadIntervals >= sent {
			break
		}
		time.Sleep(time.Millisecond)
	}
	sm.Stop()
	sm.Stop()
	return sm.Stats()
}

func TestSamplerDropsRollbackInvalidRace(t *testing.T) {
	tk := newFtick(1000)
	busy := map[int]bool{}
	for i := 1; i <= 137; i++ {
		busy[i] = true
	}
	sm := sampler.New(&src{busy: busy}, tk, time.Now, time.Millisecond)
	st := drive(sm, tk, 1000)
	tr := sm.Snapshot()
	if st.Dropped != 137 || tr.SumSelf()+st.Dropped != 1000 {
		t.Fatalf("dropped=%d identity=%d", st.Dropped, tr.SumSelf()+st.Dropped)
	}

	tk2 := newFtick(10)
	var n int64
	var rollbackOnce bool
	clk := func() time.Time {
		n++
		if n == 3 && !rollbackOnce {
			n = 0
			rollbackOnce = true
		}
		return time.Unix(0, n*int64(time.Millisecond))
	}
	sm2 := sampler.New(&src{}, tk2, clk, time.Millisecond)
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				attrib.ByTotal(sm2.Snapshot())
			}
		}()
	}
	st2 := drive(sm2, tk2, 10)
	wg.Wait()
	if st2.BadIntervals != 1 || sm2.Snapshot().SumSelf() != st2.Ticks {
		t.Fatalf("bad=%d self=%d ticks=%d", st2.BadIntervals, sm2.Snapshot().SumSelf(), st2.Ticks)
	}

	tk3 := newFtick(5)
	sm3 := sampler.New(&src{bad: true}, tk3, time.Now, time.Millisecond)
	st3 := drive(sm3, tk3, 5)
	if st3.Invalid != 5 || sm3.Snapshot().Samples != 0 {
		t.Fatalf("invalid=%d samples=%d", st3.Invalid, sm3.Snapshot().Samples)
	}
}

func TestDumpTruncationByteByByte(t *testing.T) {
	tr := tree.New()
	for _, f := range [][]string{{"m", "A", "BB"}, {"m", "A", "C"}, {"m", "A", "BB"}, {"m", "D"}} {
		s, _ := stack.Normalize(f, 0)
		tr.Insert(s)
	}
	full := dump.Encode(&dump.Profile{Tree: tr, Dropped: 137})
	p, err := dump.Decode(full)
	if err != nil || p.Tree.SumSelf() != 4 || p.Dropped != 137 {
		t.Fatalf("roundtrip err=%v", err)
	}
	// 记录边界（字节偏移）：头 36；记录 23/23/24/23/23；CRC 4。总 156。
	bounds := []int{36, 59, 82, 106, 129, 152}
	for cut := 1; cut < len(full); cut++ {
		_, err := dump.Decode(full[:cut])
		switch {
		case cut < 36:
			if !errors.Is(err, dump.ErrHeader) {
				t.Fatalf("cut=%d want ErrHeader got %v", cut, err)
			}
		case cut < 152:
			if !errors.Is(err, dump.ErrRecord) {
				t.Fatalf("cut=%d want ErrRecord got %v", cut, err)
			}
		default:
			if !errors.Is(err, dump.ErrCRC) {
				t.Fatalf("cut=%d want ErrCRC got %v", cut, err)
			}
		}
		rec, _ := dump.Recover(full[:cut])
		if rec.Tree.SumSelf() != rec.Tree.Samples {
			t.Fatalf("cut=%d recovered self=%d != samples=%d", cut, rec.Tree.SumSelf(), rec.Tree.Samples)
		}
		wantNodes := 0
		for _, b := range bounds[1:] {
			if cut >= b {
				wantNodes++
			}
		}
		gotNodes, prev := 0, 0
		rec.Tree.Walk(func(_ *tree.Node, d int) {
			if d == 0 {
				return
			}
			gotNodes++
			if d > prev+1 {
				t.Fatalf("cut=%d orphan node at depth %d", cut, d)
			}
			prev = d
		})
		if gotNodes != wantNodes {
			t.Fatalf("cut=%d recovered nodes=%d want %d", cut, gotNodes, wantNodes)
		}
	}
}
