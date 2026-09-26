package causal

import (
	"errors"
	"testing"

	"ontology/vc"
)

func eqVC(t *testing.T, s *System, p int, want ...int) {
	t.Helper()
	v, err := s.VC(p)
	if err != nil || !v.Equal(want) {
		t.Fatalf("VC[%d]=%v err=%v, want %v", p, v, err, want)
	}
}

// 钉住复杂度约束：Deliver 按消息身份直接索引待投递缓冲，
// 为定位/校验消息检查过的待投递消息个数不随缓冲规模 m 线性增长。
func TestDeliverCheckedCountConstant(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		s := New(2)
		msgs := make([]Msg, m)
		for i := range msgs {
			m, err := s.Broadcast(0)
			if err != nil {
				t.Fatal(err)
			}
			msgs[i] = m
		}
		if len(s.pending) != m {
			t.Fatalf("m=%d: pending=%d", m, len(s.pending))
		}
		d, err := s.Deliver(1, msgs[m/2]) // 阻塞：前序消息未投递
		if err != nil || d {
			t.Fatalf("m=%d: 应阻塞且无错误, d=%v err=%v", m, d, err)
		}
		if s.lastChecked > 2 { // 与 m 无关的小常数
			t.Fatalf("m=%d: 检查了 %d 条待投递消息，随 m 增长", m, s.lastChecked)
		}
	}
}

// 不变量 2：因果序——跨节点依赖未投递时后继消息必须阻塞。
func TestInvariantCausalOrder(t *testing.T) {
	s := New(3)
	a1, _ := s.Broadcast(0)
	if d, _ := s.Deliver(1, a1); !d {
		t.Fatal("a1 应可投递到 1")
	}
	b1, _ := s.Broadcast(1) // b1 因果依赖 a1
	if d, _ := s.Deliver(2, b1); d {
		t.Fatal("节点 2 未投递 a1，b1 必须阻塞")
	}
	eqVC(t, s, 2, 0, 0, 0)
	if d, _ := s.Deliver(2, a1); !d {
		t.Fatal("a1 应可投递到 2")
	}
	if d, _ := s.Deliver(2, b1); !d {
		t.Fatal("a1 投递后 b1 应可投递")
	}
	eqVC(t, s, 2, 1, 1, 0)
}

// 不变量 3：同源不跳号、不重复，分量逐条递增。
func TestInvariantNoSkip(t *testing.T) {
	s := New(2)
	m1, _ := s.Broadcast(0)
	m2, _ := s.Broadcast(0)
	if d, _ := s.Deliver(1, m2); d {
		t.Fatal("跳过第 1 条投递第 2 条应阻塞")
	}
	for i, m := range []Msg{m1, m2} {
		if d, _ := s.Deliver(1, m); !d {
			t.Fatalf("第 %d 条应可投递", i+1)
		}
		eqVC(t, s, 1, i+1, 0)
	}
	if d, _ := s.Deliver(1, m1); d {
		t.Fatal("重复投递第 1 条应阻塞")
	}
	eqVC(t, s, 1, 2, 0)
}

// 不变量 4：三类故障各有可判定且互不相同的错误，被拒后状态不变、可继续用。
func TestInvariantFailureNoTrace(t *testing.T) {
	s := New(2)
	m1, _ := s.Broadcast(0)
	cases := []struct {
		op   func() error
		want error
	}{
		{func() error { _, e := s.Broadcast(2); return e }, ErrNodeOutOfRange},
		{func() error { _, e := s.Deliver(9, m1); return e }, ErrNodeOutOfRange},
		{func() error { _, e := s.Deliver(1, Msg{From: 0, TS: vc.Vector{5, 0}}); return e }, ErrUnknownMessage},
		{func() error { _, e := s.Deliver(0, m1); return e }, ErrSelfDelivery},
	}
	for i, c := range cases {
		if err := c.op(); !errors.Is(err, c.want) {
			t.Fatalf("用例 %d: got %v, want %v", i, err, c.want)
		}
	}
	if ErrNodeOutOfRange == ErrUnknownMessage || ErrUnknownMessage == ErrSelfDelivery || ErrNodeOutOfRange == ErrSelfDelivery {
		t.Fatal("三类错误必须互不相同")
	}
	eqVC(t, s, 0, 1, 0)
	eqVC(t, s, 1, 0, 0)
	if d, err := s.Deliver(1, m1); err != nil || !d {
		t.Fatal("被拒后系统应可继续正常使用")
	}
}
