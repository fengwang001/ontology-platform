package api_test

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology/api"
)

func fatalf(t *testing.T, cond bool, msg string, args ...any) {
	if cond {
		t.Fatalf(msg, args...)
	}
}

type model struct { // 朴素参照模型：按题目规则逐条手推，用于对拍
	half, est map[string][2]int64
	next      int64
}

func newModel() *model { return &model{half: map[string][2]int64{}, est: map[string][2]int64{}} }
func (m *model) syn(src string, seq int64) (int64, int64, error) {
	if seq < 0 {
		return 0, 0, api.ErrBadSeq
	}
	if c, ok := m.half[src]; ok && c[0] == seq {
		return c[1], c[0] + 1, nil
	}
	m.half[src] = [2]int64{seq, m.next}
	m.next++
	return m.next - 1, seq + 1, nil
}
func (m *model) ack(src string, ack int64) ([2]int64, error) {
	if c, ok := m.half[src]; ok {
		if ack != c[1]+1 {
			return [2]int64{}, api.ErrBadAck
		}
		delete(m.half, src)
		m.est[src] = c
		return c, nil
	}
	if c, ok := m.est[src]; ok {
		return c, nil
	}
	return [2]int64{}, api.ErrHalfOpen
}
func TestModelInterleave(t *testing.T) {
	rng := rand.New(rand.NewSource(603))
	s, m := api.New(), newModel()
	for i := 0; i < 4000; i++ {
		src := string(rune('a' + rng.Intn(4)))
		c, half := m.half[src]
		if rng.Intn(2) == 0 {
			seq := int64(rng.Intn(5) - 1) // 含负序号与重复
			if half && rng.Intn(2) == 0 {
				seq = c[0] // 触发重传分支
			}
			gi, ga, ge := s.RecvSYN(src, seq)
			wi, wa, we := m.syn(src, seq)
			fatalf(t, gi != wi || ga != wa || !errors.Is(ge, we), "SYN(%s,%d): (%d,%d,%v)!=模型(%d,%d,%v)", src, seq, gi, ga, ge, wi, wa, we)
		} else {
			ack := int64(rng.Intn(4))
			if half && rng.Intn(2) == 0 {
				ack = c[1] + 1 // 触发完成分支
			}
			gc, _, ge := s.RecvACK(src, ack)
			wc, we := m.ack(src, ack)
			fatalf(t, (ge == nil && gc != wc[0]) || !errors.Is(ge, we), "ACK(%s,%d): (%d,%v)!=模型(%v,%v)", src, ack, gc, ge, wc, we)
			_, inEst := m.est[src] // est 只会被 ACK 改动，只核当前 src 即可
			fatalf(t, s.Established(src) != inEst, "Established(%s) 与模型不一致", src)
		}
	}
}
func TestSeqNegotiation(t *testing.T) {
	s := api.New()
	for i, prev := 0, int64(-1); i < 50; i++ {
		src, cseq := fmt.Sprintf("s%d", i), int64(1000+i*7)
		isn, ack, err := s.RecvSYN(src, cseq)
		ci, si, err2 := s.RecvACK(src, isn+1)
		fatalf(t, err != nil || err2 != nil || ack != cseq+1 || isn <= prev || ci != cseq || si != isn, "序号协商错误: isn=%d ack=%d ci=%d si=%d", isn, ack, ci, si)
		prev = isn
	}
	fatalf(t, s.SelfCheck() != nil, "SelfCheck 未通过")
}
func TestDuplicateIdempotent(t *testing.T) {
	s := api.New()
	isn, ack, _ := s.RecvSYN("a", 42)
	for i := 0; i < 3; i++ {
		i2, a2, err := s.RecvSYN("a", 42)
		fatalf(t, err != nil || i2 != isn || a2 != ack, "重复 SYN 回复变了: (%d,%d,%v)", i2, a2, err)
	}
	iB, _, _ := s.RecvSYN("b", 7)
	fatalf(t, iB != isn+1, "重复 SYN 消耗了 nextISN: %d != %d", iB, isn+1)
	s.RecvACK("a", isn+1)
	ci, si, err := s.RecvACK("a", isn+1)
	fatalf(t, err != nil || ci != 42 || si != isn, "重复 ACK 不是幂等 no-op: (%d,%d,%v)", ci, si, err)
}
func TestFailureNoTrace(t *testing.T) {
	cases := []struct {
		name string
		op   func(*api.Server) error
		want error
	}{
		{"坏ACK", func(s *api.Server) error { _, _, e := s.RecvACK("h", 999); return e }, api.ErrBadAck},
		{"半开ACK", func(s *api.Server) error { _, _, e := s.RecvACK("ghost", 1); return e }, api.ErrHalfOpen},
		{"坏序号", func(s *api.Server) error { _, _, e := s.RecvSYN("neg", -1); return e }, api.ErrBadSeq},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := api.New()
			isn, _, _ := s.RecvSYN("h", 10)
			fatalf(t, !errors.Is(tc.op(s), tc.want), "错误不可判定, want %v", tc.want)
			_, _, err := s.RecvACK("h", isn+1)
			fatalf(t, err != nil, "被拒操作改变了状态: %v", err)
			i2, _, _ := s.RecvSYN("nxt", 1)
			fatalf(t, i2 != isn+1, "被拒操作消耗了 nextISN: %d != %d", i2, isn+1)
		})
	}
	fatalf(t, errors.Is(api.ErrBadAck, api.ErrHalfOpen) || errors.Is(api.ErrBadAck, api.ErrBadSeq) || errors.Is(api.ErrHalfOpen, api.ErrBadSeq), "三类错误必须互不相同")
}
func TestConcurrentHandshake(t *testing.T) {
	s := api.New()
	var wg sync.WaitGroup
	isns := make([]int64, 64)
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = s.SelfCheck()
			s.Established("g0")
			src := fmt.Sprintf("g%d", i)
			isn, ack, err := s.RecvSYN(src, int64(1000+i))
			ci, si, err2 := s.RecvACK(src, isn+1)
			if err != nil || err2 != nil || ack != int64(1001+i) || ci != int64(1000+i) {
				t.Errorf("g%d: isn=%d ack=%d ci=%d si=%d", i, isn, ack, ci, si)
				return
			}
			isns[i] = si
		}(i)
	}
	wg.Wait()
	seen := map[int64]bool{}
	for i, si := range isns {
		fatalf(t, seen[si] || !s.Established(fmt.Sprintf("g%d", i)), "g%d 串线或 serverISN 重复: %d", i, si)
		seen[si] = true
	}
}
