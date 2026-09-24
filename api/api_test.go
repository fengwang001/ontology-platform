package api

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

// 第三节的十步：判定结果、返回位点、最终日志都必须与推导表一致。
func TestTenStep(t *testing.T) {
	type exp struct {
		off int
		dup bool
		err error
	}
	reqs := [][3]int{{0, 0, 0}, {0, 1, 0}, {0, 0, 1}, {0, 0, 1}, {0, 0, 3},
		{0, 0, 2}, {0, 0, 0}, {1, 1, 0}, {0, 0, 3}, {1, 0, 0}}
	exps := []exp{{0, false, nil}, {0, false, nil}, {1, false, nil}, {1, true, nil},
		{0, false, ErrOutOfOrder}, {2, false, nil}, {0, false, ErrDuplicateExpired},
		{1, false, nil}, {0, false, ErrFenced}, {3, false, nil}}
	s := New(2, 2)
	for i, r := range reqs {
		off, dup, err := s.Produce(7, r[0], r[1], r[2], 0)
		if off != exps[i].off || dup != exps[i].dup || !errors.Is(err, exps[i].err) || (err == nil) != (exps[i].err == nil) {
			t.Fatalf("step %d: got (%d,%v,%v), want %+v", i+1, off, dup, err, exps[i])
		}
	}
	want0 := []Record{{Pid: 7, Epoch: 0, Seq: 0}, {Pid: 7, Epoch: 0, Seq: 1}, {Pid: 7, Epoch: 0, Seq: 2}, {Pid: 7, Epoch: 1, Seq: 0}}
	want1 := []Record{{Pid: 7, Epoch: 0, Seq: 0}, {Pid: 7, Epoch: 1, Seq: 0}}
	if !reflect.DeepEqual(s.Log(0), want0) || !reflect.DeepEqual(s.Log(1), want1) {
		t.Fatalf("logs: %v %v", s.Log(0), s.Log(1))
	}
}

// 不变量 1：多档规模下与朴素参照逐条一致（含不变量 2、3 的日志侧核验）。
func TestNaiveEquivalence(t *testing.T) {
	if err := New(4, 3).SelfCheck(); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ n, w, pids, epochs, count int }{{2, 2, 1, 1, 500}, {2, 2, 3, 2, 2000}, {4, 3, 5, 3, 4000}, {8, 1, 4, 4, 4000}, {3, 5, 2, 2, 3000}}
	for _, c := range cases {
		if err := replayAndCheck(New(c.n, c.w), lcgReqs(c.count, c.pids, c.epochs, c.n)); err != nil {
			t.Fatalf("%+v: %v", c, err)
		}
	}
}

// 不变量 3：epoch 5 被接受后，任何 epoch<5 的请求都不落盘。
func TestFencing(t *testing.T) {
	s := New(3, 2)
	if _, _, err := s.Produce(1, 5, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	for _, r := range [][3]int{{4, 0, 0}, {4, 1, 0}, {4, 2, 7}, {0, 0, 0}} {
		if _, _, err := s.Produce(1, r[0], r[1], r[2], 0); !errors.Is(err, ErrFenced) {
			t.Fatalf("%v: want fenced, got %v", r, err)
		}
	}
	total := len(s.Log(0)) + len(s.Log(1)) + len(s.Log(2))
	if total != 1 {
		t.Fatalf("fenced records appended, total=%d", total)
	}
}

// 不变量 4：四类拒绝都不留痕（日志不变、epoch 未被失败升级改动），且服务仍可用。
func TestRejectNoTrace(t *testing.T) {
	s := New(2, 2)
	s.Produce(5, 0, 0, 0, 0)
	s.Produce(5, 1, 1, 0, 0)
	s.Produce(5, 1, 1, 1, 0)
	s.Produce(5, 1, 1, 2, 0) // P1 窗口变为 {1,2}
	b0, b1 := s.Log(0), s.Log(1)
	bads := []struct {
		pid, epoch, part, seq int
		err                   error
	}{
		{-1, 0, 0, 0, ErrInvalid}, {0, -1, 0, 0, ErrInvalid},
		{0, 0, -1, 0, ErrInvalid}, {0, 0, 2, 0, ErrInvalid},
		{5, 0, 0, 0, ErrFenced}, {5, 1, 0, 7, ErrOutOfOrder},
		{5, 9, 0, 3, ErrOutOfOrder}, // 升级但 seq≠0：不得升级 epoch
		{5, 1, 1, 0, ErrDuplicateExpired},
	}
	for _, c := range bads {
		if _, _, err := s.Produce(c.pid, c.epoch, c.part, c.seq, 0); !errors.Is(err, c.err) {
			t.Fatalf("%+v: got %v", c, err)
		}
	}
	if !reflect.DeepEqual(s.Log(0), b0) || !reflect.DeepEqual(s.Log(1), b1) {
		t.Fatal("rejected requests changed the log")
	}
	if _, _, err := s.Produce(5, 2, 0, 0, 0); err != nil { // epoch 仍为 1，可正常升级
		t.Fatalf("epoch modified by rejected upgrade: %v", err)
	}
	if _, _, err := s.Produce(5, 2, 0, 1, 0); err != nil {
		t.Fatalf("server unusable after rejections: %v", err)
	}
}

// 四类哨兵错误必须互不相同。
func TestErrorsDistinct(t *testing.T) {
	es := []error{ErrInvalid, ErrFenced, ErrOutOfOrder, ErrDuplicateExpired}
	for i := range es {
		for j := i + 1; j < len(es); j++ {
			if es[i] == es[j] || errors.Is(es[i], es[j]) {
				t.Fatalf("errors %d and %d not distinct", i, j)
			}
		}
	}
}

// 并发：K 个 goroutine 发同一组 seq=0..S-1，恰好一次落盘，位点一致。
func TestConcurrentDuplicate(t *testing.T) {
	cases := []struct{ k, s, w int }{{4, 16, 16}, {8, 32, 64}, {16, 50, 50}}
	for _, c := range cases {
		sv, offs := New(1, c.w), make([][]int, c.k)
		var wg sync.WaitGroup
		for g := 0; g < c.k; g++ {
			wg.Add(1)
			go func(g int) {
				defer wg.Done()
				offs[g] = make([]int, c.s)
				for seq := 0; seq < c.s; seq++ {
					off, _, err := sv.Produce(9, 0, 0, seq, seq)
					for err != nil {
						off, _, err = sv.Produce(9, 0, 0, seq, seq)
					}
					offs[g][seq] = off
				}
			}(g)
		}
		wg.Wait()
		lg := sv.Log(0)
		if len(lg) != c.s {
			t.Fatalf("%+v: log len %d", c, len(lg))
		}
		for i, rec := range lg {
			if rec.Seq != i {
				t.Fatalf("%+v: log[%d].Seq=%d", c, i, rec.Seq)
			}
		}
		for g := 0; g < c.k; g++ {
			for seq := 0; seq < c.s; seq++ {
				if offs[g][seq] != seq {
					t.Fatalf("%+v: g=%d seq=%d off=%d", c, g, seq, offs[g][seq])
				}
			}
		}
	}
}
