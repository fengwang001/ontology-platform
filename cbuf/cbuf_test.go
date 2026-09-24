package cbuf

import (
	"testing"

	"ontology/vc"
)

// TestCheckedCounterBound 钉住第四节：checked 不随缓冲规模 m 线性增长，
// 级联时不超过 (本次交付条数+1)*n 加常数。白盒直读非导出字段。
func TestCheckedCounterBound(t *testing.T) {
	const idle, slack = int64(3), int64(6)
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		b := New(2, m+8)
		for s := 2; s <= m+1; s++ { // 发送方1序号2..m+1，序号1缺失，全部不可交付
			if _, err := b.Receive(vc.Msg{From: 1, V: []int64{1, int64(s)}}); err != nil {
				t.Fatalf("m=%d fill: %v", m, err)
			}
		}
		if _, err := b.Receive(vc.Msg{From: 0, V: []int64{1, 0}}); err != nil { // 无关到达
			t.Fatalf("m=%d idle: %v", m, err)
		}
		if b.checked > idle { // 不随 m 线性增长
			t.Fatalf("m=%d idle arrival checked %d > %d", m, b.checked, idle)
		}
		before := len(b.Delivered())
		if _, err := b.Receive(vc.Msg{From: 1, V: []int64{1, 1}}); err != nil { // 触发全量级联
			t.Fatalf("m=%d cascade: %v", m, err)
		}
		given := int64(len(b.Delivered()) - before)
		if b.checked < int64(m)-1 || b.checked > (given+1)*2+slack {
			t.Fatalf("m=%d cascade checked %d, given %d, bound %d", m, b.checked, given, (given+1)*2+slack)
		}
		if len(b.Buffered()) != 0 || len(b.Delivered()) != m+2 {
			t.Fatalf("m=%d final buf=%d del=%d", m, len(b.Buffered()), len(b.Delivered()))
		}
	}
}

// TestBufferBasics 表驱动核验到达消息计入返回、缓冲重复判重、序号连续与级联。
func TestBufferBasics(t *testing.T) {
	b := New(2, 4)
	cases := []struct {
		name      string
		m         vc.Msg
		wantOut   int
		wantBuf   int
		wantDups  int64
		wantLocal []int64
	}{
		{"e early buffered", vc.Msg{From: 1, V: []int64{2, 2}}, 0, 1, 0, []int64{0, 0}},
		{"e dup in buffer", vc.Msg{From: 1, V: []int64{2, 2}}, 0, 1, 1, []int64{0, 0}},
		{"a delivered", vc.Msg{From: 0, V: []int64{1, 0}}, 1, 1, 1, []int64{1, 0}},
		{"b delivered, e still waits", vc.Msg{From: 1, V: []int64{1, 1}}, 1, 1, 1, []int64{1, 1}},
		{"a2 delivered cascades e", vc.Msg{From: 0, V: []int64{2, 1}}, 2, 0, 1, []int64{2, 2}},
	}
	for _, c := range cases {
		out, err := b.Receive(c.m)
		if err != nil || len(out) != c.wantOut || len(b.Buffered()) != c.wantBuf ||
			b.Dups() != c.wantDups || !eqInt64(b.Local(), c.wantLocal) {
			t.Fatalf("%s: out=%d buf=%d dups=%d local=%v err=%v",
				c.name, len(out), len(b.Buffered()), b.Dups(), b.Local(), err)
		}
	}
}

// TestBufferFullAndReject 表驱动核验满员拒绝不留痕、非法消息拒绝不留痕。
func TestBufferFullAndReject(t *testing.T) {
	cases := []struct {
		name string
		n, b int
		m    vc.Msg
		err  error
	}{
		{"full", 2, 0, vc.Msg{From: 0, V: []int64{2, 0}}, ErrFull},
		{"bad sender", 2, 0, vc.Msg{From: 3, V: []int64{1, 0}}, vc.ErrSender},
		{"bad length", 2, 0, vc.Msg{From: 0, V: []int64{1}}, vc.ErrVector},
		{"negative", 2, 0, vc.Msg{From: 1, V: []int64{1, -1}}, vc.ErrVector},
		{"self seq zero", 2, 0, vc.Msg{From: 0, V: []int64{0, 0}}, vc.ErrVector},
	}
	for _, c := range cases {
		b := New(c.n, c.b)
		if _, err := b.Receive(c.m); err != c.err {
			t.Fatalf("%s: want %v got %v", c.name, c.err, err)
		}
		if len(b.Delivered()) != 0 || len(b.Buffered()) != 0 || b.Dups() != 0 ||
			!eqInt64(b.Local(), make([]int64, c.n)) {
			t.Fatalf("%s: rejection left a trace: %v %v %d", c.name, b.Local(), b.Buffered(), b.Dups())
		}
	}
}

func eqInt64(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
