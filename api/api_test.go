package api_test

import (
	"errors"
	"slices"
	"sync"
	"testing"

	"ontology/api"
	"ontology/hist"
)

func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	// 四类哨兵错误互不相同。
	errs := []error{hist.ErrInvalidNode, hist.ErrMsgNotFound, hist.ErrMsgRecvTwice, hist.ErrEventLimit}
	for i, a := range errs {
		for j, b := range errs {
			if i != j && errors.Is(a, b) {
				t.Fatalf("sentinels %d and %d not distinct", i, j)
			}
		}
	}
	if _, err := api.New(0, 5); !errors.Is(err, hist.ErrInvalidNode) {
		t.Fatal("New(0,5) must fail with ErrInvalidNode")
	}
	if _, err := api.New(3, 0); !errors.Is(err, hist.ErrInvalidNode) {
		t.Fatal("New(3,0) must fail with ErrInvalidNode")
	}
	s, err := api.New(2, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Local(1); err != nil { // 事件 1：节点 1 时钟 → 1
		t.Fatal(err)
	}
	m1, _, err := s.Send(1, 2) // 事件 2：m1
	if err != nil {
		t.Fatal(err)
	}
	before := s.Order()
	cases := []struct {
		name string
		try  func() error
		want error
	}{
		{"bad node local", func() error { return s.Local(3) }, hist.ErrInvalidNode},
		{"bad node send", func() error { _, _, e := s.Send(1, 0); return e }, hist.ErrInvalidNode},
		{"missing msg", func() error { _, e := s.Recv(999); return e }, hist.ErrMsgNotFound},
	}
	for _, c := range cases {
		if e := c.try(); !errors.Is(e, c.want) {
			t.Fatalf("%s: got %v want %v", c.name, e, c.want)
		}
		if !slices.Equal(s.Order(), before) {
			t.Fatalf("%s left a trace in the total order", c.name)
		}
	}
	// 拒绝后时钟/事件 ID/消息登记不变：m1 仍可正常接收，且事件 ID 连续。
	if _, err := s.Recv(m1); err != nil {
		t.Fatalf("valid recv broken after rejections: %v", err)
	}
	if _, err := s.Recv(m1); !errors.Is(err, hist.ErrMsgRecvTwice) {
		t.Fatal("second recv must fail with ErrMsgRecvTwice")
	}
	if err := s.Local(1); err != nil {
		t.Fatal(err)
	}
	var ev4 hist.Event
	for _, e := range s.Order() {
		if e.ID == 4 {
			ev4 = e
		}
	}
	if ev4.ID != 4 || ev4.TS != 3 { // 第 4 个事件；节点 1 时钟 1→2→3
		t.Fatalf("event after rejections: id=%d ts=%d, want id=4 ts=3", ev4.ID, ev4.TS)
	}
	// 事件数超限。
	g, _ := api.New(1, 1)
	if err := g.Local(1); err != nil {
		t.Fatal(err)
	}
	if err := g.Local(1); !errors.Is(err, hist.ErrEventLimit) {
		t.Fatal("overflow must fail with ErrEventLimit")
	}
	if len(g.Order()) != 1 {
		t.Fatal("overflow left a trace")
	}
}

func TestConcurrentWritesAndReads(t *testing.T) {
	for _, nk := range [][2]int{{4, 25}, {8, 50}} {
		n, k := nk[0], nk[1]
		s, _ := api.New(n, n*k)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for node := 1; node <= n; node++ {
			wg.Add(1)
			go func(nd int) {
				defer wg.Done()
				<-start
				for i := 0; i < k; i++ {
					if err := s.Local(nd); err != nil {
						t.Error(err)
					}
				}
			}(node)
		}
		close(start)
		wg.Wait()
		ord := s.Order()
		if len(ord) != n*k {
			t.Fatalf("n=%d k=%d: got %d events", n, k, len(ord))
		}
		per := map[int][]int{}
		for _, e := range ord {
			per[e.Node] = append(per[e.Node], e.TS)
		}
		for nd := 1; nd <= n; nd++ {
			for i, ts := range per[nd] {
				if ts != i+1 {
					t.Fatalf("node %d stamps %v, want 1..%d", nd, per[nd], k)
				}
			}
		}
		first := s.Order()
		ch := make(chan bool, 16)
		for i := 0; i < 16; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); ch <- slices.Equal(s.Order(), first) }()
		}
		wg.Wait()
		close(ch)
		for v := range ch {
			if !v {
				t.Fatal("concurrent readers observed different orders")
			}
		}
	}
}

func TestSelfCheck(t *testing.T) {
	s, err := api.New(3, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
