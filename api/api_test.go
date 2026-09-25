package api_test

import (
	"errors"
	"math/rand"
	"sync"
	"testing"

	"ontology/api"
)

// naive 是按题目规则逐步手推的朴素参照模型。
type naive struct {
	cw, ss, p, max int64
	st             api.State
}

func newNaive(ss, max int64) *naive { return &naive{cw: 1, ss: ss, max: max, st: api.SlowStart} }

func (n *naive) ack() error {
	nc, ns, np := n.cw, n.st, n.p
	switch {
	case ns == api.SlowStart && nc < n.ss:
		nc++
	case ns == api.SlowStart:
		ns, np = api.CongAvoid, 1
	default:
		if np++; np >= nc {
			nc, np = nc+1, 0
		}
	}
	if nc >= n.max {
		return api.ErrCwndLimit
	}
	n.cw, n.st, n.p = nc, ns, np
	return nil
}

func (n *naive) loss() error {
	if n.cw == 1 {
		return api.ErrAlreadyFloor
	}
	if n.ss = n.cw / 2; n.ss < 2 {
		n.ss = 2
	}
	n.cw, n.st, n.p = 1, api.SlowStart, 0
	return nil
}

// TestNaiveReference：多档 ssthresh/maxCwnd × 随机 ACK/丢包交错序列，逐步对拍。
func TestNaiveReference(t *testing.T) {
	cases := []struct{ ss, max, seed int64 }{
		{2, 1000, 1}, {4, 1 << 40, 2}, {7, 50, 3}, {16, 33, 4}, {2, 2, 5}, {100, 1 << 40, 6},
	}
	for _, c := range cases {
		s, err := api.New(c.ss, c.max)
		if err != nil {
			t.Fatal(err)
		}
		n, r := newNaive(c.ss, c.max), rand.New(rand.NewSource(c.seed))
		for i := 0; i < 400; i++ {
			var gErr, nErr error
			if r.Intn(4) == 0 {
				gErr, nErr = s.OnLoss(), n.loss()
			} else {
				gErr, nErr = s.OnAck(), n.ack()
			}
			if !errors.Is(gErr, nErr) {
				t.Fatalf("case%+v 步%d: err %v vs %v", c, i, gErr, nErr)
			}
			if s.Cwnd() != n.cw || s.Ssthresh() != n.ss || s.Partial() != n.p || s.State() != n.st {
				t.Fatalf("case%+v 步%d: got (%d,%d,%d,%s) want (%d,%d,%d,%s)",
					c, i, s.Cwnd(), s.Ssthresh(), s.Partial(), s.State(), n.cw, n.ss, n.p, n.st)
			}
		}
	}
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// TestErrors：三类哨兵错误互不相同；被拒后四字段不变且可继续使用。
func TestErrors(t *testing.T) {
	for _, arg := range [][2]int64{{1, 10}, {4, 0}} {
		if _, err := api.New(arg[0], arg[1]); !errors.Is(err, api.ErrBadParam) {
			t.Fatalf("New%v 应 ErrBadParam", arg)
		}
	}
	if errors.Is(api.ErrBadParam, api.ErrCwndLimit) || errors.Is(api.ErrCwndLimit, api.ErrAlreadyFloor) ||
		errors.Is(api.ErrBadParam, api.ErrAlreadyFloor) {
		t.Fatal("三类哨兵错误必须互不相同")
	}
	s, _ := api.New(2, 3)
	mustOK(t, s.OnAck()) // cwnd=2
	mustOK(t, s.OnAck()) // 切 CongAvoid, partial=1
	snap := func() [4]int64 { return [4]int64{s.Cwnd(), s.Ssthresh(), s.Partial(), int64(s.State())} }
	before := snap()
	if err := s.OnAck(); !errors.Is(err, api.ErrCwndLimit) {
		t.Fatalf("cwnd 触 maxCwnd 应 ErrCwndLimit, got %v", err)
	}
	if snap() != before {
		t.Fatal("ErrCwndLimit 留痕")
	}
	mustOK(t, s.OnLoss()) // 拒绝后仍可用
	if err := s.OnLoss(); !errors.Is(err, api.ErrAlreadyFloor) {
		t.Fatalf("cwnd=1 丢包应 ErrAlreadyFloor, got %v", err)
	}
	if snap() != [4]int64{1, 2, 0, int64(api.SlowStart)} {
		t.Fatal("ErrAlreadyFloor 留痕")
	}
	mustOK(t, s.OnAck())
}

// TestConcurrentRead：N 个 goroutine 并发只读同一实例，逐字段相同；不用 sleep。
func TestConcurrentRead(t *testing.T) {
	s, _ := api.New(4, 1<<40)
	for i := 0; i < 5; i++ {
		mustOK(t, s.OnAck())
	}
	cw, ss, st := s.Cwnd(), s.Ssthresh(), s.State()
	start := make(chan struct{})
	var wg sync.WaitGroup
	bad := make(chan struct{}, 64)
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 200; j++ {
				if s.Cwnd() != cw || s.Ssthresh() != ss || s.State() != st || api.SelfCheck() != nil {
					bad <- struct{}{}
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	if len(bad) != 0 {
		t.Fatal("并发只读结果不一致")
	}
}
